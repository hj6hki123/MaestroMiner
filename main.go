// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"crypto"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/db"
	"github.com/kvarenzn/ssm/gui" // newly added
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/scores"
	"github.com/kvarenzn/ssm/stage"
	"github.com/kvarenzn/ssm/term"
	"golang.org/x/image/draw"

	"github.com/kvarenzn/ssm/locale"
)

var SSM_VERSION = "(unknown)"

// original flags
var (
	backend      string
	songID       int
	difficulty   string
	extract      string
	direction    string
	chartPath    string
	deviceSerial string
	showDebugLog bool
	showVersion  bool
	pjskMode     bool
)

var (
	guiMode bool
	guiPort int
)

const (
	SERVER_FILE_VERSION      = "3.3.1"
	SERVER_FILE              = "scrcpy-server-v" + SERVER_FILE_VERSION
	SERVER_FILE_DOWNLOAD_URL = "https://github.com/Genymobile/scrcpy/releases/download/v" + SERVER_FILE_VERSION + "/" + SERVER_FILE
	SERVER_FILE_SHA256       = "a0f70b20aa4998fbf658c94118cd6c8dab6abbb0647a3bdab344d70bc1ebcbb8"
)

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

// ─────────────────────────────────────────────
// GUI mode main flow
// ─────────────────────────────────────────────
func runGUI(conf *config.Config) {
	srv := gui.NewServer(guiPort, conf)

	normalizeROI := func(mode string, bang gui.ROI, pjsk gui.ROI) gui.ROI {
		roi := bang
		if mode == "pjsk" {
			roi = pjsk
		}

		if roi.X1 == 0 && roi.Y1 == 0 && roi.X2 == 0 && roi.Y2 == 0 {
			if mode == "pjsk" {
				return gui.ROI{X1: 0, Y1: 35, X2: 100, Y2: 60}
			}
			return gui.ROI{X1: 14, Y1: 73, X2: 87, Y2: 80}
		}

		clamp := func(v int) int {
			if v < 0 {
				return 0
			}
			if v > 100 {
				return 100
			}
			return v
		}

		roi.X1 = clamp(roi.X1)
		roi.Y1 = clamp(roi.Y1)
		roi.X2 = clamp(roi.X2)
		roi.Y2 = clamp(roi.Y2)

		if roi.X2 <= roi.X1 {
			if roi.X1 >= 99 {
				roi.X1 = 98
			}
			roi.X2 = roi.X1 + 1
		}
		if roi.Y2 <= roi.Y1 {
			if roi.Y1 >= 99 {
				roi.Y1 = 98
			}
			roi.Y2 = roi.Y1 + 1
		}

		return roi
	}

	autoTriggerByVision := func(ctx context.Context, sc *controllers.ScrcpyController, mode string, roi gui.ROI, pollMs int64) bool {
		if pollMs < 30 {
			pollMs = 30
		}
		if pollMs > 1000 {
			pollMs = 1000
		}

		srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{
			Enabled: true,
			Mode:    mode,
			Armed:   true,
			Fired:   false,
			PollMs:  pollMs,
			ROI:     roi,
			Message: "armed; waiting first-note trigger",
		})

		sampleLumaBand := func(f controllers.ScrcpyFrame) (float64, float64, float64, float64, bool) {
			if f.Width <= 0 || f.Height <= 0 {
				return 0, 0, 0, 0, false
			}
			need := f.Width * f.Height
			if len(f.Plane0) < need {
				return 0, 0, 0, 0, false
			}

			x1 := f.Width * roi.X1 / 100
			x2 := f.Width * roi.X2 / 100
			y1 := f.Height * roi.Y1 / 100
			y2 := f.Height * roi.Y2 / 100
			if x1 < 0 {
				x1 = 0
			}
			if y1 < 0 {
				y1 = 0
			}
			if x2 > f.Width {
				x2 = f.Width
			}
			if y2 > f.Height {
				y2 = f.Height
			}
			if x2 <= x1 || y2 <= y1 {
				return 0, 0, 0, 0, false
			}

			xStep := max(1, (x2-x1)/96)
			yStep := max(1, (y2-y1)/24)
			var sum, sumTop, sumMid, sumBottom int64
			count := 0
			countTop, countMid, countBottom := 0, 0, 0
			ySpan := max(1, y2-y1)
			for y := y1; y < y2; y += yStep {
				row := y * f.Width
				for x := x1; x < x2; x += xStep {
					v := int64(f.Plane0[row+x])
					sum += v
					py := y - y1
					if py*3 < ySpan {
						sumTop += v
						countTop++
					} else if py*3 < ySpan*2 {
						sumMid += v
						countMid++
					} else {
						sumBottom += v
						countBottom++
					}
					count++
				}
			}
			if count == 0 {
				return 0, 0, 0, 0, false
			}
			avg := float64(sum) / float64(count)
			avgTop := avg
			avgMid := avg
			avgBottom := avg
			if countTop > 0 {
				avgTop = float64(sumTop) / float64(countTop)
			}
			if countMid > 0 {
				avgMid = float64(sumMid) / float64(countMid)
			}
			if countBottom > 0 {
				avgBottom = float64(sumBottom) / float64(countBottom)
			}
			return avg, avgTop, avgMid, avgBottom, true
		}

		ticker := time.NewTicker(time.Duration(pollMs) * time.Millisecond)
		defer ticker.Stop()

		const stableNeed = 4
		const stableDiffBase = 1.0
		const triggerDiffBase = 1.4
		const noiseAlpha = 0.18
		const seqWindow = 550 * time.Millisecond

		var last, lastTop, lastMid, lastBottom float64
		hasLast := false
		stableCount := 0
		noiseEMA := 0.6
		var riseTopAt, riseMidAt time.Time
		lastDebugAt := time.Time{}

		publishDebug := func(v gui.AutoTriggerDebug, force bool) {
			if !force && !lastDebugAt.IsZero() && time.Since(lastDebugAt) < 120*time.Millisecond {
				return
			}
			lastDebugAt = time.Now()
			srv.SetAutoTriggerDebug(v)
		}

		for {
			select {
			case <-ctx.Done():
				srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: true, Mode: mode, Armed: false, Fired: false, PollMs: pollMs, ROI: roi, Message: "stopped"})
				return false
			case <-ticker.C:
			}

			frame, ok := sc.LatestFrame()
			if !ok {
				publishDebug(gui.AutoTriggerDebug{
					Enabled: true,
					Mode:    mode,
					Armed:   true,
					Fired:   false,
					PollMs:  pollMs,
					ROI:     roi,
					Message: "no decoded frame",
				}, false)
				continue
			}

			cur, curTop, curMid, curBottom, ok := sampleLumaBand(frame)
			if !ok {
				publishDebug(gui.AutoTriggerDebug{
					Enabled: true,
					Mode:    mode,
					Armed:   true,
					Fired:   false,
					PollMs:  pollMs,
					ROI:     roi,
					Message: "invalid roi on frame",
				}, false)
				continue
			}

			delta := 0.0
			dTop, dMid, dBottom := 0.0, 0.0, 0.0
			if hasLast {
				delta = cur - last
				dTop = curTop - lastTop
				dMid = curMid - lastMid
				dBottom = curBottom - lastBottom
				absDelta := math.Abs(delta)
				stripeNoise := (math.Abs(dTop) + math.Abs(dMid) + math.Abs(dBottom)) / 3.0
				if stripeNoise > absDelta {
					absDelta = stripeNoise
				}

				// Adapt to device-specific decode noise: low-end phones often have unstable luma.
				noiseEMA = (1.0-noiseAlpha)*noiseEMA + noiseAlpha*absDelta
				stableBand := max(stableDiffBase, noiseEMA*1.8)
				triggerDiff := max(triggerDiffBase, noiseEMA*3.2)
				riseDiff := max(0.9, noiseEMA*2.2)

				nowT := time.Now()
				if dTop >= riseDiff {
					riseTopAt = nowT
				}
				if dMid >= riseDiff && !riseTopAt.IsZero() && nowT.Sub(riseTopAt) <= seqWindow {
					riseMidAt = nowT
				}
				flowTriggered := dBottom >= riseDiff && !riseMidAt.IsZero() && nowT.Sub(riseMidAt) <= seqWindow

				if absDelta <= stableBand {
					stableCount++
				} else {
					if flowTriggered || (stableCount >= stableNeed && delta >= triggerDiff) {
						reason := "luma"
						if flowTriggered {
							reason = "flow"
						}
						publishDebug(gui.AutoTriggerDebug{
							Enabled:     true,
							Mode:        mode,
							Armed:       true,
							Fired:       true,
							PollMs:      pollMs,
							Luma:        cur,
							Delta:       delta,
							StableCount: stableCount,
							ROI:         roi,
							Message:     fmt.Sprintf("triggered[%s] (thr=%.2f rise=%.2f noise=%.2f)", reason, triggerDiff, riseDiff, noiseEMA),
						}, true)
						log.Debugf("Vision auto trigger fired: reason=%s pollMs=%d stable=%d delta=%.2f dTop=%.2f dMid=%.2f dBottom=%.2f triggerThr=%.2f riseThr=%.2f noise=%.2f", reason, pollMs, stableCount, delta, dTop, dMid, dBottom, triggerDiff, riseDiff, noiseEMA)
						return true
					}
					// Soften reset on noisy devices.
					if stableCount > 0 {
						stableCount--
					}
				}
			}

			publishDebug(gui.AutoTriggerDebug{
				Enabled:     true,
				Mode:        mode,
				Armed:       stableCount >= stableNeed,
				Fired:       false,
				PollMs:      pollMs,
				Luma:        cur,
				Delta:       delta,
				StableCount: stableCount,
				ROI:         roi,
				Message:     fmt.Sprintf("watching (noise=%.2f d=%.2f/%.2f/%.2f)", noiseEMA, dTop, dMid, dBottom),
			}, false)

			last = cur
			lastTop = curTop
			lastMid = curMid
			lastBottom = curBottom
			hasLast = true
		}
	}

	// Ensure only one playback goroutine runs at a time.
	var (
		runMu         sync.Mutex
		currentCancel context.CancelFunc
		doneCh        chan struct{}
	)

	runOnce := func(req gui.RunRequest) {
		// Cancel the previous run and wait for it to finish (including scrcpy.Close).
		runMu.Lock()
		if currentCancel != nil {
			currentCancel()
			old := doneCh
			runMu.Unlock()
			<-old
			runMu.Lock()
		}

		ctx, cancel := context.WithCancel(context.Background())
		currentCancel = cancel
		thisDone := make(chan struct{})
		doneCh = thisDone
		runMu.Unlock()

		go func() {
			defer func() {
				cancel()
				close(thisDone)
			}()

			backend = req.Backend
			songID = req.SongID
			difficulty = req.Diff
			direction = req.Orient
			chartPath = req.ChartPath
			deviceSerial = req.DeviceSerial
			pjskMode = req.Mode == "pjsk"

			var chartText []byte
			var err error
			if chartPath == "" {
				var pathResults []string
				if pjskMode {
					pathResults, err = filepath.Glob(filepath.Join("./assets/sekai/assetbundle/resources/startapp/music/music_score/",
						fmt.Sprintf("%04d_01/%s.txt", songID, difficulty)))
				} else {
					pathResults, err = filepath.Glob(filepath.Join("./assets/star/forassetbundle/startapp/musicscore/",
						fmt.Sprintf("musicscore*/%03d/*_%s.txt", songID, difficulty)))
				}
				if err != nil || len(pathResults) < 1 {
					srv.SetError("Musicscore not found. Please extract assets first or use a custom chart path.")
					return
				}
				chartText, err = os.ReadFile(pathResults[0])
			} else {
				chartText, err = os.ReadFile(chartPath)
			}
			if err != nil {
				srv.SetError("Failed to read musicscore: " + err.Error())
				return
			}

			var chart scores.Chart
			if pjskMode {
				chart, err = scores.ParseSUS(string(chartText))
				if err != nil {
					srv.SetError("Failed to parse SUS: " + err.Error())
					return
				}
			} else {
				chart = scores.ParseBMS(string(chartText))
			}

			genConfig := &scores.VTEGenerateConfig{
				TapDuration:         10,
				FlickDuration:       60,
				FlickReportInterval: 5,
				FlickFactor:         1.0 / 5,
				FlickPow:            1,
				SlideReportInterval: 10,
				TimingJitter:        req.TimingJitter,
				PositionJitter:      req.PositionJitter,
				TapDurJitter:        req.TapDurJitter,
				GreatOffsetMs: func() int64 {
					v := req.GreatOffsetMs
					if v < 0 {
						v = -v
					}
					if v == 0 {
						v = 10
					}
					return v
				}(),
				GreatTargetCount: func() int64 {
					v := req.GreatCount
					if v < 0 {
						return 0
					}
					return v
				}(),
			}
			if pjskMode {
				genConfig.FlickFactor = 1.0 / 6
				genConfig.FlickDuration = 20
			}
			// Override defaults with user-supplied advanced params (0 = keep default)
			if req.TapDuration > 0 {
				genConfig.TapDuration = req.TapDuration
			}
			if req.FlickDuration > 0 {
				genConfig.FlickDuration = req.FlickDuration
			}
			if req.FlickReportInterval > 0 {
				genConfig.FlickReportInterval = req.FlickReportInterval
			}
			if req.SlideReportInterval > 0 {
				genConfig.SlideReportInterval = req.SlideReportInterval
			}
			if req.FlickFactor > 0 {
				genConfig.FlickFactor = req.FlickFactor
			}
			if req.FlickPow > 0 {
				genConfig.FlickPow = req.FlickPow
			}
			rawEvents, greatApplied := scores.GenerateTouchEvent(genConfig, chart)
			srv.SetGreatStats(req.GreatCount, int64(greatApplied))

			var ctrl controllers.Controller
			var events []common.ViscousEventItem

			switch backend {
			case "adb":
				checkOrDownload()
				if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
					srv.SetError("Failed to start ADB server: " + err.Error())
					return
				}
				client := adb.NewDefaultClient()
				devices, err := client.Devices()
				if err != nil || len(devices) == 0 {
					srv.SetError("No ADB devices found. Please make sure the device is connected.")
					return
				}
				var device *adb.Device
				if deviceSerial == "" {
					device = adb.FirstAuthorizedDevice(devices)
				} else {
					for _, d := range devices {
						if d.Serial() == deviceSerial {
							device = d
							break
						}
					}
				}
				if device == nil {
					srv.SetError("No authorized ADB device found.")
					return
				}
				scrcpy := controllers.NewScrcpyController(device)
				if err := scrcpy.Open("./"+SERVER_FILE, SERVER_FILE_VERSION); err != nil {
					srv.SetError("Failed to connect to device: " + err.Error())
					return
				}
				defer scrcpy.Close()
				dc := conf.Get(device.Serial())
				if dc == nil {
					srv.SetError(fmt.Sprintf("Device [%s] not configured. Please add it in Settings first.", device.Serial()))
					return
				}
				events = scrcpy.Preprocess(rawEvents, direction == "right", dc, getJudgeLineCalculator())
				ctrl = scrcpy

			default: // hid
				if deviceSerial == "" {
					serials := controllers.FindHIDDevices()
					if len(serials) == 0 {
						srv.SetError("No HID devices found. Please make sure USB is connected.")
						return
					}
					deviceSerial = serials[0]
				}
				dc := conf.Get(deviceSerial)
				hidCtrl := controllers.NewHIDController(dc)
				if err := hidCtrl.Open(); err != nil {
					srv.SetError("Failed to initialize HID: " + err.Error())
					return
				}
				defer hidCtrl.Close()
				events = hidCtrl.Preprocess(rawEvents, direction == "right", getJudgeLineCalculator())
				ctrl = hidCtrl
			}

			np := gui.NowPlaying{
				SongID:    req.SongID,
				Diff:      req.Diff,
				Mode:      req.Mode,
				Title:     req.NowPlaying.Title,
				Artist:    req.NowPlaying.Artist,
				DiffLevel: req.NowPlaying.DiffLevel,
				JacketURL: req.NowPlaying.JacketURL,
			}

			srv.SetReady(ctrl, events, np)

			srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: req.AutoTriggerVision, Mode: req.Mode, Message: "idle"})

			if !srv.WaitForStart(ctx) {
				return
			}

			if req.AutoTriggerVision {
				roi := normalizeROI(req.Mode, req.AutoTriggerROIBang, req.AutoTriggerROIPjsk)
				sc, ok := ctrl.(*controllers.ScrcpyController)
				if !ok {
					srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: true, Mode: req.Mode, PollMs: req.AutoTriggerPollMs, ROI: roi, Message: "backend not scrcpy"})
					log.Warn("Auto Trigger (Vision) is only available on scrcpy backend")
					return
				}
				if !isVideoDecodeEnabled() {
					srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: true, Mode: req.Mode, PollMs: req.AutoTriggerPollMs, ROI: roi, Message: "video decode disabled"})
					log.Warn("Auto Trigger (Vision) requested but video decode is disabled; set SSM_ENABLE_VIDEO_DECODE=0 to disable (default is enabled)")
					return
				}
				if !autoTriggerByVision(ctx, sc, req.Mode, roi, req.AutoTriggerPollMs) {
					return
				}
			}

			start := time.Now().Add(-time.Duration(events[0].Timestamp) * time.Millisecond)
			srv.Autoplay(ctx, start)

			time.Sleep(300 * time.Millisecond)
		}()
	}

	srv.OnRunRequest = func(req gui.RunRequest) {
		runOnce(req)
	}

	srv.OnExtractRequest = func(path string) error {
		_, err := Extract(path, func(p string) bool {
			if strings.HasSuffix(p, ".acb.bytes") || !strings.Contains(p, "startapp") {
				return false
			}
			return strings.Contains(p, "musicscore/") || strings.Contains(p, "music_score/") ||
				strings.Contains(p, "musicjacket/") || strings.Contains(p, "jacket/") ||
				strings.Contains(p, "ingameskin")
		})
		return err
	}

	addr, err := srv.Start()
	if err != nil {
		log.Die("Failed to start GUI server:", err)
	}

	fmt.Printf("\n  SSM GUI started\n")
	fmt.Printf("   Open this URL in your browser: %s\n\n", addr)

	openBrowser(addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nSSM GUI closed")
}

// ─────────────────────────────────────────────
// The following are original functions (unchanged).
// ─────────────────────────────────────────────

func downloadServer() {
	log.Infof("To use adb as the backend, the third-party component `scrcpy-server` (version %s) is required.", SERVER_FILE_VERSION)
	log.Infoln("This component is developed by Genymobile and licensed under Apache License 2.0.")
	log.Infoln()
	log.Infoln("Please download it from the official release page and place it in the same directory as `ssm.exe`.")
	log.Infoln("Download link:", SERVER_FILE_DOWNLOAD_URL)
	log.Infoln()
	log.Infoln("Alternatively, ssm can automatically handle this process for you.")
	log.Info("Proceed with automatic download? [Y/n]: ")
	var input string
	_, err := fmt.Scanln(&input)
	if err != nil {
		log.Die("Failed to get input:", err)
	}
	if input == "N" || input == "n" {
		log.Die("`scrcpy-server` is required.")
	}
	log.Infoln("Downloading... Please wait.")
	res, err := http.Get(SERVER_FILE_DOWNLOAD_URL)
	if err != nil {
		log.Dieln("Failed to download `scrcpy-server`.", fmt.Sprintf("Error: %s", err))
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		log.Dieln("Failed to download.", fmt.Sprintf("Error: %s", err))
	}
	h := crypto.SHA256.New()
	h.Write(data)
	if fmt.Sprintf("%x", h.Sum(nil)) != SERVER_FILE_SHA256 {
		log.Die("Checksum mismatch.")
	}
	if err := os.WriteFile(SERVER_FILE, data, 0o644); err != nil {
		log.Die("Failed to save:", err)
	}
}

func checkOrDownload() {
	if _, err := os.Stat(SERVER_FILE); err != nil {
		if !os.IsNotExist(err) {
			log.Die("Failed to locate server file:", err)
		}
		downloadServer()
	} else {
		data, err := os.ReadFile(SERVER_FILE)
		if err != nil {
			log.Die("Failed to read:", err)
		}
		h := crypto.SHA256.New()
		h.Write(data)
		if fmt.Sprintf("%x", h.Sum(nil)) != SERVER_FILE_SHA256 {
			log.Warn("Checksum mismatch.")
			downloadServer()
		}
	}
}

const errNoDevice = "Please connect your Android device to this computer."
const jacketHeight = 15

type tui struct {
	db             db.MusicDatabase
	size           *term.TermSize
	playing        bool
	start          time.Time
	offset         int
	controller     controllers.Controller
	events         []common.ViscousEventItem
	firstTick      int64
	loadFailed     bool
	orignal        image.Image
	scaled         image.Image
	graphicsMethod term.GraphicsMethod
	renderMutex    *sync.Mutex
	sigwinch       chan os.Signal
}

func newTui(database db.MusicDatabase) *tui {
	return &tui{db: database, renderMutex: &sync.Mutex{}, sigwinch: make(chan os.Signal, 1)}
}

func (t *tui) init(controller controllers.Controller, events []common.ViscousEventItem) error {
	if err := term.PrepareTerminal(); err != nil {
		return err
	}
	log.SetBeforeDie(func() { t.deinit() })
	if err := t.onResize(); err != nil {
		return err
	}
	t.controller = controller
	t.events = events
	t.startListenResize()
	term.SetWindowTitle(locale.P.Sprintf("ssm: READY"))
	return nil
}

func (t *tui) loadJacket() error {
	var err error
	if t.size == nil {
		t.size, err = term.GetTerminalSize()
		if err != nil {
			return err
		}
	}
	if chartPath != "" {
		return fmt.Errorf("No song ID provided")
	}
	thumb, jacket := t.db.Jacket(songID)
	if thumb == "" {
		return fmt.Errorf("Jacket not found")
	}
	t.graphicsMethod = term.GetGraphicsMethod()
	var path string
	switch t.graphicsMethod {
	case term.HALF_BLOCK, term.OVERSTRIKED_DOTS:
		path = thumb
	case term.SIXEL_PROTOCOL, term.ITERM2_GRAPHICS_PROTOCOL, term.KITTY_GRAPHICS_PROTOCOL:
		path = jacket
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	t.orignal, err = term.DecodeImage(data)
	if err != nil {
		return err
	}
	var length int
	switch t.graphicsMethod {
	case term.HALF_BLOCK:
		length = jacketHeight * 2
	case term.OVERSTRIKED_DOTS:
		length = jacketHeight * 4
	case term.SIXEL_PROTOCOL, term.KITTY_GRAPHICS_PROTOCOL:
		length = t.size.CellHeight * jacketHeight
	}
	if length > 0 {
		scaled := image.NewNRGBA(image.Rect(0, 0, length, length))
		draw.BiLinear.Scale(scaled, scaled.Rect, t.orignal, t.orignal.Bounds(), draw.Src, nil)
		t.scaled = scaled
	}
	return nil
}

func (t *tui) startListenResize() {
	term.StartWatchResize(t.sigwinch)
	go func() {
		for range t.sigwinch {
			t.onResize()
		}
	}()
}

func (t *tui) onResize() error {
	newSize, err := term.GetTerminalSize()
	if err != nil {
		return err
	}
	if t.orignal == nil && !t.loadFailed {
		if err := t.loadJacket(); err != nil {
			t.loadFailed = true
		}
	}
	if t.orignal != nil {
		var length int
		switch t.graphicsMethod {
		case term.HALF_BLOCK:
			if t.scaled != nil {
				length = jacketHeight * 2
			}
		case term.OVERSTRIKED_DOTS:
			if t.scaled != nil {
				length = jacketHeight * 4
			}
		case term.SIXEL_PROTOCOL, term.KITTY_GRAPHICS_PROTOCOL:
			if t.scaled == nil || t.size == nil || newSize.CellHeight != t.size.CellHeight {
				length = newSize.CellHeight * jacketHeight
			}
		}
		if length > 0 {
			s := image.NewNRGBA(image.Rect(0, 0, length, length))
			draw.BiLinear.Scale(s, s.Rect, t.orignal, t.orignal.Bounds(), draw.Src, nil)
			t.scaled = s
		}
	}
	t.size = newSize
	term.ClearScreen()
	t.render(true)
	return nil
}

func (t *tui) pcenterln(s string) {
	if t.size == nil {
		return
	}
	term.MoveHome()
	cols := t.size.Col
	width := term.WidthOf(s)
	fmt.Print(strings.Repeat(" ", max((cols-width)/2, 0)))
	fmt.Print(s)
	term.ClearToRight()
	fmt.Println()
}

func displayDifficulty() string {
	switch difficulty {
	case "easy":
		if pjskMode {
			return "\x1b[0;42m EASY \x1b[0m "
		}
		return "\x1b[0;44m EASY \x1b[0m "
	case "normal":
		if pjskMode {
			return "\x1b[0;44m NORMAL \x1b[0m "
		}
		return "\x1b[0;42m NORMAL \x1b[0m "
	case "hard":
		return "\x1b[0;43m HARD \x1b[0m "
	case "expert":
		return "\x1b[0;41m EXPERT \x1b[0m "
	case "special":
		return "\x1b[0;45m SPECIAL \x1b[0m "
	case "master":
		return "\x1b[0;45m MASTER \x1b[0m "
	case "append":
		return "\x1b[47m\x1b[35m APPEND \x1b[0m "
	default:
		return ""
	}
}

func (t *tui) emptyLine() {
	term.ClearCurrentLine()
	fmt.Println()
}

func (t *tui) render(full bool) {
	if t.size == nil {
		return
	}
	if ok := t.renderMutex.TryLock(); !ok {
		return
	}
	term.ResetCursor()
	t.emptyLine()
	if full && (t.scaled != nil || t.graphicsMethod == term.ITERM2_GRAPHICS_PROTOCOL && t.orignal != nil) {
		switch t.graphicsMethod {
		case term.HALF_BLOCK:
			term.DisplayImageUsingHalfBlock(t.scaled, false, (t.size.Col-jacketHeight*2)/2)
		case term.OVERSTRIKED_DOTS:
			term.DisplayImageUsingOverstrikedDots(t.scaled, 0, 0, (t.size.Col-jacketHeight*2)/2)
		case term.SIXEL_PROTOCOL:
			term.DisplayImageUsingSixelProtocol(t.scaled, t.size, jacketHeight)
		case term.ITERM2_GRAPHICS_PROTOCOL:
			term.DisplayImageUsingITerm2Protocol(t.orignal, t.size, jacketHeight)
		case term.KITTY_GRAPHICS_PROTOCOL:
			term.DisplayImageUsingKittyProtocol(t.scaled, t.size, jacketHeight)
		}
	} else {
		term.MoveDownAndReset(jacketHeight)
	}
	t.emptyLine()
	if chartPath == "" {
		t.pcenterln(fmt.Sprintf("%s%s", displayDifficulty(), t.db.Title(songID, "\x1b[1m${title}\x1b[0m")))
		t.pcenterln(t.db.Title(songID, "${artist}"))
	} else {
		t.pcenterln(chartPath)
	}
	t.emptyLine()
	if !t.playing {
		t.pcenterln(locale.Sprintf("ui line 0"))
		t.emptyLine()
		t.emptyLine()
	} else {
		t.pcenterln(locale.Sprintf("Offset: %d ms", t.offset))
		t.pcenterln(locale.Sprintf("ui line 1"))
		t.pcenterln(locale.Sprintf("ui line 2"))
	}
	t.renderMutex.Unlock()
}

func (t *tui) begin() {
	t.firstTick = t.events[0].Timestamp
	for {
		key, err := term.ReadKey(os.Stdin, 10*time.Millisecond)
		if err != nil {
			log.Dief("Failed to get key from stdin: %s", err)
		}
		if key == term.KEY_ENTER || key == term.KEY_SPACE {
			break
		}
	}
	t.playing = true
	t.start = time.Now().Add(-time.Duration(t.firstTick) * time.Millisecond)
	t.offset = 0
	if len(chartPath) == 0 {
		term.SetWindowTitle(locale.P.Sprintf("ssm: Autoplaying %s (%s)", t.db.Title(songID, "${title} :: ${artist}"), strings.ToUpper(difficulty)))
	} else {
		term.SetWindowTitle(locale.P.Sprintf("ssm: Autoplaying %s", chartPath))
	}
	t.render(false)
}

func (t *tui) addOffset(delta int) {
	t.offset += delta
	t.start = t.start.Add(time.Duration(-delta) * time.Millisecond)
	t.render(false)
}

func (t *tui) waitForKey() {
	for {
		key, err := term.ReadKey(os.Stdin, 10*time.Millisecond)
		if err != nil {
			log.Dief("Failed to get key from stdin: %s", err)
		}
		switch key {
		case term.KEY_LEFT:
			t.addOffset(-10)
		case term.KEY_SHIFT_LEFT:
			t.addOffset(-50)
		case term.KEY_CTRL_LEFT:
			t.addOffset(-100)
		case term.KEY_RIGHT:
			t.addOffset(10)
		case term.KEY_SHIFT_RIGHT:
			t.addOffset(50)
		case term.KEY_CTRL_RIGHT:
			t.addOffset(100)
		}
	}
}

func (t *tui) deinit() error {
	if err := term.RestoreTerminal(); err != nil {
		return err
	}
	term.Bye()
	return nil
}

func (t *tui) autoplay() {
	current := 0
	n := len(t.events)
	for current < n {
		now := time.Since(t.start).Milliseconds()
		event := t.events[current]
		remaining := event.Timestamp - now
		if remaining <= 0 {
			t.controller.Send(event.Data)
			current++
			continue
		}
		if remaining > 10 {
			time.Sleep(time.Duration(remaining-5) * time.Millisecond)
		} else if remaining > 4 {
			time.Sleep(1 * time.Millisecond)
		}
	}
}

func getJudgeLineCalculator() stage.JudgeLinePositionCalculator {
	if pjskMode {
		return stage.PJSKJudgeLinePos
	}
	return stage.BanGJudgeLinePos
}

func (t *tui) adbBackend(conf *config.Config, rawEvents common.RawVirtualEvents) {
	checkOrDownload()
	if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
		log.Fatal(err)
	}
	client := adb.NewDefaultClient()
	devices, err := client.Devices()
	if err != nil {
		log.Fatal(err)
	}
	if len(devices) == 0 {
		log.Die(errNoDevice)
	}
	log.Debugln("ADB devices:", devices)
	var device *adb.Device
	if deviceSerial == "" {
		device = adb.FirstAuthorizedDevice(devices)
		if device == nil {
			log.Die("No authorized devices.")
		}
	} else {
		for _, d := range devices {
			if d.Serial() == deviceSerial {
				device = d
				break
			}
		}
		if device == nil {
			log.Dief("No device has serial `%s`", deviceSerial)
		}
		if !device.Authorized() {
			log.Dief("Device `%s` is not authorized.", deviceSerial)
		}
	}
	log.Debugln("Selected device:", device)
	controller := controllers.NewScrcpyController(device)
	if err := controller.Open("./scrcpy-server-v3.3.1", "3.3.1"); err != nil {
		log.Die("Failed to connect to device:", err)
	}
	defer controller.Close()
	dc := conf.Get(device.Serial())
	events := controller.Preprocess(rawEvents, direction == "right", dc, getJudgeLineCalculator())
	t.init(controller, events)
	t.begin()
	go t.waitForKey()
	t.autoplay()
	time.Sleep(300 * time.Millisecond)
}

func (t *tui) hidBackend(conf *config.Config, rawEvents common.RawVirtualEvents) {
	if deviceSerial == "" {
		serials := controllers.FindHIDDevices()
		log.Debugln("Recognized devices:", serials)
		if len(serials) == 0 {
			log.Die(errNoDevice)
		}
		deviceSerial = serials[0]
	}
	dc := conf.Get(deviceSerial)
	controller := controllers.NewHIDController(dc)
	if err := controller.Open(); err != nil {
		log.Die("Failed to initialize HID:", err)
	}
	defer controller.Close()
	events := controller.Preprocess(rawEvents, direction == "right", getJudgeLineCalculator())
	t.init(controller, events)
	t.begin()
	go t.waitForKey()
	t.autoplay()
	time.Sleep(300 * time.Millisecond)
}

func main() {
	log.Debugf("LANG: %s", locale.LanguageString)
	p := locale.P

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), p.Sprintf("Usage of %s:", os.Args[0]))
		flag.PrintDefaults()
	}

	flag.StringVar(&backend, "b", "hid", p.Sprintf("usage.b"))
	flag.IntVar(&songID, "n", -1, p.Sprintf("usage.n"))
	flag.StringVar(&difficulty, "d", "", p.Sprintf("usage.d"))
	flag.StringVar(&extract, "e", "", p.Sprintf("usage.e"))
	flag.StringVar(&direction, "r", "left", p.Sprintf("usage.r"))
	flag.StringVar(&chartPath, "p", "", p.Sprintf("usage.p"))
	flag.StringVar(&deviceSerial, "s", "", p.Sprintf("usage.s"))
	flag.BoolVar(&pjskMode, "k", false, p.Sprintf("usage.k"))
	flag.BoolVar(&showDebugLog, "g", false, p.Sprintf("usage.g"))
	flag.BoolVar(&showVersion, "v", false, p.Sprintf("usage.v"))

	flag.BoolVar(&guiMode, "gui", false, "Start the graphical interface (browser GUI)")
	flag.IntVar(&guiPort, "port", 8765, "Port used by the GUI (default 8765)")

	flag.Parse()
	// If no arguments are provided, enable GUI mode by default.
	if len(os.Args) == 1 {
		guiMode = true
	}
	term.Hello()
	defer term.Bye()

	log.ShowDebug(showDebugLog)

	//  GUI mode has priority.
	if guiMode {
		config.DisablePrompt = true
		const CONFIG_PATH = "./config.json"
		conf, err := config.Load(CONFIG_PATH)
		if err != nil {
			log.Die(err)
		}
		runGUI(conf)
		return
	}

	// ─── Everything below is the same as the original version ───

	if extract != "" {
		db, err := Extract(extract, func(path string) bool {
			if strings.HasSuffix(path, ".acb.bytes") || !strings.Contains(path, "startapp") {
				return false
			}
			return strings.Contains(path, "musicscore/") || strings.Contains(path, "music_score/") ||
				strings.Contains(path, "musicjacket/") || strings.Contains(path, "jacket/") ||
				strings.Contains(path, "ingameskin")
		})
		if err != nil {
			log.Die(err)
		}
		data, err := json.MarshalIndent(db, "", "\t")
		if err != nil {
			log.Die(err)
		}
		if err := os.WriteFile("./extract.json", data, 0o644); err != nil {
			log.Die(err)
		}
		return
	}

	var database db.MusicDatabase
	var err error
	if pjskMode {
		database, err = db.NewSekaiDB()
	} else {
		database, err = db.NewBestdoriDB()
	}
	if err != nil {
		log.Warnf("Failed to load database: %s", err)
	}

	if showVersion {
		fmt.Println(p.Sprintf("ssm version: %s", p.Sprintf(SSM_VERSION)))
		fmt.Println(p.Sprintf("copyright info"))
		return
	}

	const CONFIG_PATH = "./config.json"
	conf, err := config.Load(CONFIG_PATH)
	if err != nil {
		log.Die(err)
	}

	if chartPath == "" && (songID == -1 || difficulty == "") {
		log.Die("Song id and difficulty are both required")
	}

	var chartText []byte
	if chartPath == "" {
		var pathResults []string
		if pjskMode {
			pathResults, err = filepath.Glob(filepath.Join("./assets/sekai/assetbundle/resources/startapp/music/music_score/",
				fmt.Sprintf("%04d_01/%s.txt", songID, difficulty)))
		} else {
			pathResults, err = filepath.Glob(filepath.Join("./assets/star/forassetbundle/startapp/musicscore/",
				fmt.Sprintf("musicscore*/%03d/*_%s.txt", songID, difficulty)))
		}
		if err != nil {
			log.Die("Failed to find musicscore file:", err)
		}
		if len(pathResults) < 1 {
			log.Die("Musicscore not found")
		}
		log.Debugln("Musicscore loaded:", pathResults[0])
		chartText, err = os.ReadFile(pathResults[0])
	} else {
		log.Debugln("Musicscore loaded:", chartPath)
		chartText, err = os.ReadFile(chartPath)
	}
	if err != nil {
		log.Die("Failed to load musicscore:", err)
	}

	var chart scores.Chart
	if pjskMode {
		chart, err = scores.ParseSUS(string(chartText))
		if err != nil {
			log.Die("Failed to parse musicscore:", err)
		}
	} else {
		chart = scores.ParseBMS(string(chartText))
	}

	genConfig := &scores.VTEGenerateConfig{
		TapDuration:         10,
		FlickDuration:       60,
		FlickReportInterval: 5,
		FlickFactor:         1.0 / 5,
		FlickPow:            1,
		SlideReportInterval: 10,
	}
	if pjskMode {
		genConfig.FlickFactor = 1.0 / 6
		genConfig.FlickDuration = 20
	}
	rawEvents, _ := scores.GenerateTouchEvent(genConfig, chart)

	t := newTui(database)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT)
	defer stop()

	go func() {
		switch backend {
		case "adb":
			t.adbBackend(conf, rawEvents)
		case "hid":
			t.hidBackend(conf, rawEvents)
		default:
			log.Dief("Unknown backend: %q", backend)
		}
		stop()
	}()

	<-ctx.Done()
	if err := t.deinit(); err != nil {
		log.Die(err)
	}
}
