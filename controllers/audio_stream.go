// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package controllers

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/log"
)

// ScrcpyAudioStream opens a scrcpy audio-only side-session to receive raw PCM
// from the device. Requires Android 11+ for audio capture. The JAR is assumed
// already present on the device (pushed by the companion ScrcpyController.Open).
//
// Frame format from scrcpy (audio_codec=raw): PCM 16-bit LE, stereo, 48 kHz.
// Each frame on the socket: pts(8 bytes BE) | size(4 bytes BE) | data.
// Config/metadata frames have the high bit of pts set — these are discarded.
type ScrcpyAudioStream struct {
	device      *adb.Device
	sessionID   string
	listener    net.Listener
	socket      net.Conn
	socketOwned bool // true when Open() created the socket; false when AttachSocket() was used
	running     bool
	ch          chan []int16
}

// audioStreamBasePort is the starting port for audio-only sessions, kept
// separate from the main video session range (testFromPort = 27188).
const audioStreamBasePort = 27388

func NewScrcpyAudioStream(device *adb.Device) *ScrcpyAudioStream {
	return &ScrcpyAudioStream{
		device:    device,
		sessionID: fmt.Sprintf("%08x", rand.Int31()),
		ch:        make(chan []int16, 128),
	}
}

// Open starts an audio-only scrcpy side-session with video=false and control=false.
// Returns (true, nil) when the audio socket connects successfully.
// Returns (false, nil) when audio capture is unavailable (Android < 11 or permission denied).
// Returns (false, err) on a hard infrastructure failure.
func (a *ScrcpyAudioStream) Open(filepath, version string) (bool, error) {
	listener, port := tryListen("localhost", audioStreamBasePort)
	a.listener = listener

	localName := fmt.Sprintf("localabstract:scrcpy_%s", a.sessionID)
	if err := a.device.Forward(localName, fmt.Sprintf("tcp:%d", port), true, false); err != nil {
		listener.Close()
		return false, fmt.Errorf("audio stream forward: %w", err)
	}
	log.Debugf("[AudioStream] reverse tunnel %s → localhost:%d", localName, port)

	// Start scrcpy audio-only: video=false, control=false, audio=true, audio_codec=raw.
	// With control=false and video=false one socket connects if audio works, zero if not.
	go func() {
		_, err := a.device.Sh(
			"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
			"app_process",
			"/",
			"com.genymobile.scrcpy.Server",
			version,
			fmt.Sprintf("scid=%s", a.sessionID),
			"log_level=warn",
			"video=false",
			"audio=true",
			"audio_codec=raw",
			"control=false",
			"clipboard_autosync=false",
		)
		if err != nil {
			log.Debugf("[AudioStream] scrcpy-server exited: %v", err)
		}
	}()

	// Try to accept the audio socket with a 5 s deadline.
	// If audio is unavailable scrcpy won't connect at all and we time out gracefully.
	if tcpL, ok := a.listener.(*net.TCPListener); ok {
		tcpL.SetDeadline(time.Now().Add(5 * time.Second))
	}
	sock, err := a.listener.Accept()
	if tcpL, ok := a.listener.(*net.TCPListener); ok {
		tcpL.SetDeadline(time.Time{})
	}
	a.device.KillReverseForward(localName)

	if err != nil {
		// Timeout or I/O error == audio capture not available.
		log.Infof("[AudioStream] audio capture unavailable (Android < 11?): %v", err)
		a.listener.Close()
		return false, nil
	}

	a.socket = sock
	a.socketOwned = true
	a.running = true
	go a.readLoop()
	log.Infoln("[AudioStream] audio stream connected (PCM 48 kHz stereo raw)")
	return true, nil
}

// readLoop reads PCM frames from the audio socket and pushes []int16 slices to ch.
func (a *ScrcpyAudioStream) readLoop() {
	defer func() {
		a.running = false
		close(a.ch)
	}()

	ptsBuf := make([]byte, 8)
	sizeBuf := make([]byte, 4)

	for a.running {
		if err := readFull(a.socket, ptsBuf); err != nil {
			break
		}
		pts := binary.BigEndian.Uint64(ptsBuf)

		if err := readFull(a.socket, sizeBuf); err != nil {
			break
		}
		size := int(binary.BigEndian.Uint32(sizeBuf))

		// Config / metadata packets have the high bit of pts set — discard them.
		if pts>>63 != 0 || size == 0 {
			if size > 0 {
				io.CopyN(io.Discard, a.socket, int64(size))
			}
			continue
		}

		data := make([]byte, size)
		if err := readFull(a.socket, data); err != nil {
			break
		}

		// Convert PCM16LE bytes → int16 samples.
		samples := make([]int16, size/2)
		for i := range samples {
			samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
		}

		select {
		case a.ch <- samples:
		default:
			// Drop if consumer is slow — we only care about the most recent audio.
		}
	}
}

// Chan returns the channel of raw int16 PCM samples (stereo interleaved, 48 kHz).
// The channel is closed when the socket is lost or Close is called.
func (a *ScrcpyAudioStream) Chan() <-chan []int16 {
	return a.ch
}

// AttachSocket wires an already-accepted audio socket (from ScrcpyController when
// audio is enabled in the main session) into this stream and starts decoding.
// This is the preferred path on Android 12+ where a second concurrent scrcpy
// process cannot capture audio independently.
func (a *ScrcpyAudioStream) AttachSocket(conn net.Conn) {
	a.socket = conn
	a.socketOwned = false // socket lifetime managed by ScrcpyController
	a.running = true
	go a.readLoop()
	log.Infoln("[AudioStream] audio stream active (via main scrcpy session, PCM 48 kHz stereo)")
}

// Close shuts down the audio stream. It only closes the underlying socket when
// this stream opened it (Open path). On the AttachSocket path the socket is
// owned by ScrcpyController and will be closed there.
func (a *ScrcpyAudioStream) Close() {
	a.running = false
	if a.socketOwned && a.socket != nil {
		a.socket.Close()
	}
	if a.listener != nil {
		a.listener.Close()
	}
}
