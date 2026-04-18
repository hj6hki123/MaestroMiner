// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package controllers

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kvarenzn/ssm/device/adb"
	"github.com/kvarenzn/ssm/core/common"
	"github.com/kvarenzn/ssm/core/config"
	"github.com/kvarenzn/ssm/format/decoders/av"
	"github.com/kvarenzn/ssm/core/log"
	"github.com/kvarenzn/ssm/game/stage"
)

type ScrcpyController struct {
	device    *adb.Device
	sessionID string

	videoSocket   net.Conn
	controlSocket net.Conn

	width    int
	height   int
	codecID  string
	decoder  *av.AVDecoder
	cRunning bool
	vRunning bool

	deviceWidth  int
	deviceHeight int

	frameMu     sync.RWMutex
	latestFrame *ScrcpyFrame
	frameFn     func(ScrcpyFrame)
}

// ScrcpyFrame is a compact grayscale-friendly frame snapshot for analyzers.
// Plane0 typically represents Y/luma for the common YUV formats from scrcpy.
type ScrcpyFrame struct {
	PTS         int64
	Width       int
	Height      int
	PixelFormat int
	Plane0      []byte
	CapturedAt  time.Time
}

func isVideoDecodeEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("SSM_ENABLE_VIDEO_DECODE")))
	if v == "" {
		return true
	}
	switch v {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func NewScrcpyController(device *adb.Device) *ScrcpyController {
	return &ScrcpyController{
		device:    device,
		sessionID: fmt.Sprintf("%08x", rand.Int31()),
	}
}

func tryListen(host string, port int) (net.Listener, int) {
	for {
		addr := fmt.Sprintf("%s:%d", host, port)
		listen, err := net.Listen("tcp", addr)
		if err == nil {
			return listen, port
		}

		port++
	}
}

func readFull(conn net.Conn, buf []byte) error {
	_, err := io.ReadFull(conn, buf)
	return err
}

const testFromPort = 27188

func (c *ScrcpyController) Open(filepath string, version string) error {
	// Find a free local port (forward mode: PC dials out to device)
	ln, localPort := tryListen("localhost", testFromPort)
	ln.Close()
	log.Debugf("Using local port %d for ADB forward", localPort)

	localName := fmt.Sprintf("localabstract:scrcpy_%s", c.sessionID)
	if err := c.device.Forward(fmt.Sprintf("tcp:%d", localPort), localName, false, false); err != nil {
		return err
	}
	log.Debugf("ADB forward tcp:%d -> %s created.", localPort, localName)

	f, err := os.Open(filepath)
	if err != nil {
		return err
	}

	log.Debugln("`scrcpy-server` loaded.")

	if err := c.device.Push(f, "/data/local/tmp/scrcpy-server.jar"); err != nil {
		return err
	}

	log.Debugln("`scrcpy-server` pushed to gaming device.")

	go func() {
		result, err := c.device.Sh(
			"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
			"app_process",
			"/",
			"com.genymobile.scrcpy.Server",
			version,
			fmt.Sprintf("scid=%s", c.sessionID), // session id
			"log_level=warn",                    // log level
			"audio=false",                       // disable audio sync
			"clipboard_autosync=false",          // disable clipboard
			"tunnel_forward=true",               // forward mode: server listens on abstract socket
			"send_device_meta=false",            // skip 64-byte device name prefix
			"max_size=720",                      // downscale for lower bandwidth
			"video_bit_rate=2000000",            // 2 Mbps for smooth preview
		)
		if err != nil {
			log.Warnf("failed to start `scrcpy-server`: %v", err)
			return
		}

		log.Debugln(result)
	}()

	// Wait for scrcpy-server to start listening on the abstract socket
	time.Sleep(5 * time.Second)

	videoSocket, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		return fmt.Errorf("video socket dial: %w", err)
	}
	c.videoSocket = videoSocket

	log.Debugln("Video socket connected.")

	controlSocket, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		return fmt.Errorf("control socket dial: %w", err)
	}
	c.controlSocket = controlSocket

	log.Debugln("Control socket connected.")

	// With send_device_meta=false, scrcpy skips the 64-byte device name prefix.
	// Header format: [dummy:0-1][codec:4][width:4][height:4] = 13 bytes total.
	// The leading 0x00 dummy byte is present in some scrcpy 3.x builds; skip it if found.
	time.Sleep(500 * time.Millisecond)
	videoSocket.SetReadDeadline(time.Now().Add(10 * time.Second))
	dimBuf := make([]byte, 13)
	if err := readFull(videoSocket, dimBuf); err != nil {
		return fmt.Errorf("read stream header: %w", err)
	}
	videoSocket.SetReadDeadline(time.Time{})

	offset := 0
	if dimBuf[0] == 0x00 {
		offset = 1
	}
	c.codecID = string(dimBuf[offset : offset+4])
	c.width = int(binary.BigEndian.Uint32(dimBuf[offset+4 : offset+8]))
	c.height = int(binary.BigEndian.Uint32(dimBuf[offset+8 : offset+12]))
	log.Debugf("Scrcpy stream: codec=%s width=%d height=%d", c.codecID, c.width, c.height)

	var decErr error
	if isVideoDecodeEnabled() {
		c.decoder, decErr = av.NewAVDecoder(c.codecID)
		if decErr != nil {
			return decErr
		}
		c.decoder.SetFrameHandler(func(f av.DecodedFrame) {
			frame := ScrcpyFrame{
				PTS:         f.PTS,
				Width:       f.Width,
				Height:      f.Height,
				PixelFormat: f.PixelFormat,
				Plane0:      append([]byte(nil), f.Plane0...),
				CapturedAt:  time.Now(),
			}

			c.frameMu.Lock()
			c.latestFrame = &frame
			fn := c.frameFn
			c.frameMu.Unlock()

			if fn != nil {
				fn(frame)
			}
		})
	} else {
		log.Debugln("Video decode is disabled (set SSM_ENABLE_VIDEO_DECODE=0 to disable; default is enabled).")
	}

	c.cRunning = true
	c.vRunning = true

	go func() {
		msgTypeBuf := make([]byte, 1)
		sizeBuf := make([]byte, 4)
		for c.cRunning {
			if err := readFull(controlSocket, msgTypeBuf); err != nil {
				break
			}

			if err := readFull(controlSocket, sizeBuf); err != nil {
				break
			}

			size := binary.BigEndian.Uint32(sizeBuf)
			bodyBuf := make([]byte, size)
			if err := readFull(controlSocket, bodyBuf); err != nil {
				break
			}
		}

		c.cRunning = false
	}()

	go func() {
		ptsBuf := make([]byte, 8)
		sizeBuf := make([]byte, 4)
		for c.vRunning {
			if err := readFull(videoSocket, ptsBuf); err != nil {
				break
			}
			pts := binary.BigEndian.Uint64(ptsBuf)

			if err := readFull(videoSocket, sizeBuf); err != nil {
				break
			}
			size := binary.BigEndian.Uint32(sizeBuf)

			if c.decoder == nil {
				// No decoding needed, discard directly
				io.CopyN(io.Discard, videoSocket, int64(size))
				continue
			}

			data := make([]byte, size)
			if err := readFull(videoSocket, data); err != nil {
				break
			}
			c.decoder.Decode(pts, data)
		}
		c.vRunning = false
	}()

	return nil
}

func (c *ScrcpyController) Encode(action common.TouchAction, x, y int32, pointerID uint64) []byte {
	data := make([]byte, 32)
	data[0] = 2 // type: SC_CONTROL_MSG_TYPE_INJECT_TOUCH_EVENT
	data[1] = byte(action)
	binary.BigEndian.PutUint64(data[2:], pointerID)
	binary.BigEndian.PutUint32(data[10:], uint32(x))
	binary.BigEndian.PutUint32(data[14:], uint32(y))
	binary.BigEndian.PutUint16(data[18:], uint16(c.width))
	binary.BigEndian.PutUint16(data[20:], uint16(c.height))
	binary.BigEndian.PutUint16(data[22:], 0xffff)
	binary.BigEndian.PutUint32(data[24:], 1) // AMOTION_EVENT_BUTTON_PRIMARY
	binary.BigEndian.PutUint32(data[28:], 1) // AMOTION_EVENT_BUTTON_PRIMARY
	return data
}

func (c *ScrcpyController) touch(action common.TouchAction, x, y int32, pointerID uint64) {
	c.Send(c.Encode(action, x, y, pointerID))
}

func (c *ScrcpyController) Down(pointerID uint64, x, y int) {
	c.touch(common.TouchDown, int32(x), int32(y), pointerID)
}

func (c *ScrcpyController) Move(pointerID uint64, x, y int) {
	c.touch(common.TouchMove, int32(x), int32(y), pointerID)
}

func (c *ScrcpyController) Up(pointerID uint64, x, y int) {
	c.touch(common.TouchUp, int32(x), int32(y), pointerID)
}

func (c *ScrcpyController) Close() error {
	c.cRunning = false
	c.vRunning = false

	if err := c.videoSocket.Close(); err != nil {
		return err
	}

	if err := c.controlSocket.Close(); err != nil {
		return err
	}

	if c.decoder != nil {
		c.decoder.Drop()
		c.decoder = nil
	}

	return nil
}

// SetDeviceSize stores the physical device dimensions (portrait: width × height).
// Must be called after Open() so that Encode() uses the correct screen dimensions
// for the scrcpy touch protocol, regardless of stream downscaling (max_size).
func (c *ScrcpyController) SetDeviceSize(w, h int) {
	c.deviceWidth = w
	c.deviceHeight = h
}

func (c *ScrcpyController) SetFrameHandler(fn func(ScrcpyFrame)) {
	c.frameMu.Lock()
	c.frameFn = fn
	c.frameMu.Unlock()
}

func (c *ScrcpyController) LatestFrame() (ScrcpyFrame, bool) {
	c.frameMu.RLock()
	defer c.frameMu.RUnlock()
	if c.latestFrame == nil {
		return ScrcpyFrame{}, false
	}
	f := *c.latestFrame
	f.Plane0 = append([]byte(nil), c.latestFrame.Plane0...)
	return f, true
}

func (c *ScrcpyController) Preprocess(rawEvents common.RawVirtualEvents, turnRight bool, dc *config.DeviceConfig, calc stage.JudgeLinePositionCalculator) []common.ViscousEventItem {
	width, height := float64(dc.Height), float64(dc.Width)
	x1, x2, yy := calc(width, height)
	mapper := func(x, y float64) (int, int) {
		return int(math.Round(x1 + (x2-x1)*x)), int(math.Round(yy - (yy-height/2)*y))
	}

	result := []common.ViscousEventItem{}
	currentFingers := map[int]bool{}
	for _, events := range rawEvents {
		var data []byte
		for _, event := range events.Events {
			if event.PointerID < 0 {
				log.Fatalf("invalid pointer id: %d", event.PointerID)
			}
			x, y := mapper(event.X, event.Y)
			action, ok := common.NormalizeTouchAction(event.Action)
			if !ok {
				log.Fatalf("unknown touch action: %d\n", event.Action)
			}
			switch action {
			case common.TouchDown:
				if currentFingers[event.PointerID] {
					// Be tolerant to occasional duplicated down events to avoid hard crash.
					log.Warnf("pointer `%d` duplicated down; convert to move", event.PointerID)
					action = common.TouchMove
				} else {
					currentFingers[event.PointerID] = true
				}
			case common.TouchMove:
				if !currentFingers[event.PointerID] {
					// Recover by treating stray move as a down.
					log.Warnf("pointer `%d` move without down; convert to down", event.PointerID)
					action = common.TouchDown
					currentFingers[event.PointerID] = true
				}
			case common.TouchUp:
				if !currentFingers[event.PointerID] {
					// Ignore duplicated up events.
					log.Warnf("pointer `%d` duplicated up; ignore", event.PointerID)
					continue
				}
				delete(currentFingers, event.PointerID)
			}

			data = append(data, c.Encode(action, int32(x), int32(y), uint64(event.PointerID))...)
		}

		result = append(result, common.ViscousEventItem{
			Timestamp: events.Timestamp,
			Data:      data,
		})
	}

	return result
}

func (c *ScrcpyController) Send(data []byte) {
	out := data
	if c.deviceWidth > 0 && c.deviceHeight > 0 && c.width > 0 && c.height > 0 && len(data)%32 == 0 {
		buf := make([]byte, len(data))
		copy(buf, data)
		for i := 0; i+32 <= len(buf); i += 32 {
			if buf[i] != 2 {
				continue
			}
			x := int64(binary.BigEndian.Uint32(buf[i+10 : i+14]))
			y := int64(binary.BigEndian.Uint32(buf[i+14 : i+18]))
			sx := x * int64(c.width) / int64(c.deviceWidth)
			sy := y * int64(c.height) / int64(c.deviceHeight)
			if sx < 0 {
				sx = 0
			} else if sx >= int64(c.width) {
				sx = int64(c.width - 1)
			}
			if sy < 0 {
				sy = 0
			} else if sy >= int64(c.height) {
				sy = int64(c.height - 1)
			}
			binary.BigEndian.PutUint32(buf[i+10:i+14], uint32(sx))
			binary.BigEndian.PutUint32(buf[i+14:i+18], uint32(sy))
			binary.BigEndian.PutUint16(buf[i+18:i+20], uint16(c.width))
			binary.BigEndian.PutUint16(buf[i+20:i+22], uint16(c.height))
		}
		out = buf
	}
	n, err := c.controlSocket.Write(out)
	if err != nil {
		log.Warnf("failed to send control data through control socket: %v", err)
		return
	}
	if n != len(out) {
		log.Warnf("partial control data sent: expect %d bytes, sent %d bytes", len(out), n)
		return
	}
}

func (c *ScrcpyController) ResetTouch() {
	if c.controlSocket == nil {
		return
	}
	for i := 0; i < 10; i++ {
		data := c.Encode(common.TouchUp, 0, 0, uint64(i))
		c.controlSocket.Write(data)
	}
}

// IsAlive reports whether both the video and control goroutines are still running.
// A false return means the underlying sockets have been lost and the controller
// must be closed and replaced before reuse.
func (c *ScrcpyController) IsAlive() bool {
	return c.vRunning && c.cRunning
}
