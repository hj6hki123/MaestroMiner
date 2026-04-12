// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"crypto"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/db"
	"github.com/kvarenzn/ssm/gui"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/maacontrol"
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
	// MAA OCR model files — downloaded individually (no git/git-lfs required).
	MAA_OCR_MODEL_BASE_URL = "https://huggingface.co/getcharzp/go-ocr/resolve/main/paddle_weights"
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
// MAA ROI diagnostic draw helpers
// ─────────────────────────────────────────────

// maaDrawROIBox draws a 3-pixel-wide coloured border for the given normalised ROI.
func maaDrawROIBox(img *image.RGBA, roi navROI, imgW, imgH int, c color.RGBA) {
	x1 := iclamp(int(roi.x1*float64(imgW)), 0, imgW-1)
	x2 := iclamp(int(roi.x2*float64(imgW)), 0, imgW-1)
	y1 := iclamp(int(roi.y1*float64(imgH)), 0, imgH-1)
	y2 := iclamp(int(roi.y2*float64(imgH)), 0, imgH-1)
	const thick = 3
	b := img.Bounds()
	setpx := func(x, y int) {
		if x >= b.Min.X && x < b.Max.X && y >= b.Min.Y && y < b.Max.Y {
			img.SetRGBA(x, y, c)
		}
	}
	for t := 0; t < thick; t++ {
		for x := x1; x <= x2; x++ {
			setpx(x, y1+t)
			setpx(x, y2-t)
		}
		for y := y1; y <= y2; y++ {
			setpx(x1+t, y)
			setpx(x2-t, y)
		}
	}
}

// maaDrawCross draws a ±12-pixel cross (3 px thick) to mark a tap point.
func maaDrawCross(img *image.RGBA, cx, cy int, c color.RGBA) {
	const arm, thick = 12, 3
	b := img.Bounds()
	setpx := func(x, y int) {
		if x >= b.Min.X && x < b.Max.X && y >= b.Min.Y && y < b.Max.Y {
			img.SetRGBA(x, y, c)
		}
	}
	for t := -thick / 2; t <= thick/2; t++ {
		for d := -arm; d <= arm; d++ {
			setpx(cx+d, cy+t) // horizontal bar
			setpx(cx+t, cy+d) // vertical bar
		}
	}
}

// ─────────────────────────────────────────────
// GUI mode main flow
// ─────────────────────────────────────────────
func runGUI(conf *config.Config) {
	srv := gui.NewServer(guiPort, conf)
	prepareGUIPrerequisites()

	normalizeROI := func(mode string, bang gui.ROI, pjsk gui.ROI) gui.ROI {
		roi := bang
		if mode == "pjsk" {
			roi = pjsk
		}

		if roi.X1 == 0 && roi.Y1 == 0 && roi.X2 == 0 && roi.Y2 == 0 {
			if mode == "pjsk" {
				return gui.ROI{X1: 14, Y1: 73, X2: 87, Y2: 80}
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

	autoTriggerByVision := func(ctx context.Context, sc *controllers.ScrcpyController, mode string, roi gui.ROI, pollMs int64, startHasSeenDark bool) bool {
		if pollMs < 30 {
			pollMs = 30
		}
		if pollMs > 1000 {
			pollMs = 1000
		}

		srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{
			Enabled:  true,
			Mode:     mode,
			Armed:    true,
			Fired:    false,
			PollMs:   pollMs,
			ROI:      roi,
			NavStage: "VISION_TRIGGER",
			Message:  "armed; waiting first-note trigger",
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

		// sampleStripeVariance computes the std-dev of per-column luma averages inside the
		// game's lane area (derived from the judge-line geometry).  A high value means
		// the lane track structure is visible → we are inside the gameplay HUD.
		// A low value means we are on a loading screen, album popup, or song-select screen.
		sampleStripeVariance := func(f controllers.ScrcpyFrame) (float64, bool) {
			if f.Width <= 0 || f.Height <= 0 || len(f.Plane0) < f.Width*f.Height {
				return 0, false
			}
			var lx, rx, jy float64
			if mode == "pjsk" {
				lx, rx, jy = stage.PJSKJudgeLinePos(float64(f.Width), float64(f.Height))
			} else {
				lx, rx, jy = stage.BanGJudgeLinePos(float64(f.Width), float64(f.Height))
			}
			// Sample the middle portion of the track area (above the judge line).
			x1, x2 := int(lx), int(rx)
			y1, y2 := int(jy*0.30), int(jy*0.60)
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
				return 0, false
			}
			const numCols = 16
			colWidth := float64(x2-x1) / numCols
			if colWidth < 1 {
				return 0, false
			}
			yStep := max(1, (y2-y1)/32)
			var colSum [numCols]float64
			var colCnt [numCols]int
			for y := y1; y < y2; y += yStep {
				row := y * f.Width
				for i := 0; i < numCols; i++ {
					cx1 := x1 + int(float64(i)*colWidth)
					cx2 := x1 + int(float64(i+1)*colWidth)
					if cx2 > x2 {
						cx2 = x2
					}
					xStep := max(1, (cx2-cx1)/4)
					for x := cx1; x < cx2; x += xStep {
						colSum[i] += float64(f.Plane0[row+x])
						colCnt[i]++
					}
				}
			}
			var mean float64
			var colAvg [numCols]float64
			for i := range colAvg {
				if colCnt[i] > 0 {
					colAvg[i] = colSum[i] / float64(colCnt[i])
				}
				mean += colAvg[i]
			}
			mean /= numCols
			var variance float64
			for _, v := range colAvg {
				d := v - mean
				variance += d * d
			}
			return math.Sqrt(variance / numCols), true
		}

		// sampleFrameLuma samples the whole frame at low resolution for overall brightness.
		// The album loading screen (dark background + centred album art) has very low luma (~35-55),
		// distinctly darker than the song-select / band-confirm UI screens (~100-150).
		sampleFrameLuma := func(f controllers.ScrcpyFrame) (float64, bool) {
			if f.Width <= 0 || f.Height <= 0 || len(f.Plane0) < f.Width*f.Height {
				return 0, false
			}
			step := max(1, f.Width*f.Height/1024)
			var sum int64
			count := 0
			for i := 0; i < len(f.Plane0); i += step {
				sum += int64(f.Plane0[i])
				count++
			}
			if count == 0 {
				return 0, false
			}
			return float64(sum) / float64(count), true
		}

		ticker := time.NewTicker(time.Duration(pollMs) * time.Millisecond)
		defer ticker.Stop()

		const stableNeed = 4
		const stableDiffBase = 1.0
		const triggerDiffBase = 1.4
		const noiseAlpha = 0.18
		const seqWindow = 550 * time.Millisecond
		const stripeThreshold = 5.0
		// Frames with whole-screen luma below this are treated as the dark album loading screen.
		// Song-select / band-confirm UIs are typically luma 100-150; the loading screen is ~35-55.
		const albumDarkThreshold = 60.0

		var last, lastTop, lastMid, lastBottom float64
		hasLast := false
		stableCount := 0
		noiseEMA := 0.6
		var riseTopAt, riseMidAt time.Time
		lastDebugAt := time.Time{}
		lastVisionStage := ""
		var stripeVar float64
		var screenLuma float64
		// Primary gate: only arm after the dark album-loading screen has been seen once.
		// This prevents false triggers on any pre-game UI screen (song select, band confirm, etc.).
		hasSeenDark := startHasSeenDark
		inGame := startHasSeenDark // if nav pipeline already passed dark screen, start armed

		publishDebug := func(v gui.AutoTriggerDebug, force bool) {
			if !force && !lastDebugAt.IsZero() && time.Since(lastDebugAt) < 120*time.Millisecond {
				return
			}
			lastDebugAt = time.Now()
			srv.SetAutoTriggerDebug(v)
		}
		logVisionStage := func(stageName, action string) {
			if stageName == "" || stageName == lastVisionStage {
				return
			}
			lastVisionStage = stageName
			switch stageName {
			case "WAIT_ALBUM_DARK":
				action = "→ 監控 frame luma < threshold，等待黑畫面與封面切換"
			case "WAIT_STAGE":
				action = "→ WAIT_STAGE (3~4 sec): stripe variance 偵測音軌出現"
			case "VISION_TRIGGER":
				action = "→ VISION_TRIGGER: 開始打歌偵測"
			}
			log.Infof("[%s] %s", stageName, action)
		}

		for {
			select {
			case <-ctx.Done():
				srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: true, Mode: mode, Armed: false, Fired: false, PollMs: pollMs, ROI: roi, NavStage: "VISION_TRIGGER", Message: "stopped"})
				return false
			case <-ticker.C:
			}

			stageName := "VISION_TRIGGER"
			switch {
			case !hasSeenDark:
				stageName = "WAIT_ALBUM_DARK"
			case !inGame:
				stageName = "WAIT_STAGE"
			}
			logVisionStage(stageName, "Vision loop active")

			frame, ok := sc.LatestFrame()
			if !ok {
				publishDebug(gui.AutoTriggerDebug{
					Enabled:  true,
					Mode:     mode,
					Armed:    true,
					Fired:    false,
					PollMs:   pollMs,
					ROI:      roi,
					NavStage: stageName,
					Message:  "VISION_TRIGGER -> no decoded frame",
				}, false)
				continue
			}

			// Sample whole-frame luma first — updates the dark-screen gate.
			if fl, flOk := sampleFrameLuma(frame); flOk {
				screenLuma = fl
				if screenLuma < albumDarkThreshold {
					hasSeenDark = true
				}
			}

			cur, curTop, curMid, curBottom, ok := sampleLumaBand(frame)
			if !ok {
				publishDebug(gui.AutoTriggerDebug{
					Enabled:     true,
					Mode:        mode,
					Armed:       false,
					Fired:       false,
					PollMs:      pollMs,
					ROI:         roi,
					ScreenLuma:  screenLuma,
					HasSeenDark: hasSeenDark,
					NavStage:    stageName,
					Message:     "VISION_TRIGGER -> invalid roi on frame",
				}, false)
				continue
			}

			// inGame = hasSeenDark (primary gate: album loading screen seen)
			//        AND stripeVar >= threshold (secondary: lane structure visible in HUD).
			// hasSeenDark alone prevents the song-select / band-confirm screens from arming
			// even when the album art on those screens produces a high stripe variance.
			if sv, svOk := sampleStripeVariance(frame); svOk {
				stripeVar = sv
				inGame = hasSeenDark && stripeVar >= stripeThreshold
			}
			stageName = "VISION_TRIGGER"
			switch {
			case !hasSeenDark:
				stageName = "WAIT_ALBUM_DARK"
			case !inGame:
				stageName = "WAIT_STAGE"
			}
			logVisionStage(stageName, "Gate status updated")

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

				if !inGame {
					// Gates not met: hard-reset stability counters.
					stableCount = 0
					riseTopAt = time.Time{}
					riseMidAt = time.Time{}
				} else {
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
								StripeVar:   stripeVar,
								InGame:      inGame,
								ScreenLuma:  screenLuma,
								HasSeenDark: hasSeenDark,
								NavStage:    "VISION_TRIGGER",
								Message:     fmt.Sprintf("VISION_TRIGGER -> triggered[%s] thr=%.2f rise=%.2f noise=%.2f stripe=%.2f luma=%.1f", reason, triggerDiff, riseDiff, noiseEMA, stripeVar, screenLuma),
							}, true)
							log.Debugf("Vision trigger fired: reason=%s stable=%d delta=%.2f stripe=%.2f screenLuma=%.1f dark=%v", reason, stableCount, delta, stripeVar, screenLuma, hasSeenDark)
							return true
						}
						// Soften reset on noisy devices.
						if stableCount > 0 {
							stableCount--
						}
					}
				}
			}

			armed := inGame && stableCount >= stableNeed
			var stateMsg string
			switch {
			case !hasSeenDark:
				stateMsg = fmt.Sprintf("WAIT_ALBUM_DARK -> monitor frame luma (%.1f > %.0f)", screenLuma, albumDarkThreshold)
			case !inGame:
				stateMsg = fmt.Sprintf("WAIT_STAGE (3~4 sec) -> waiting stripe variance (%.2f < %.2f), luma=%.1f", stripeVar, stripeThreshold, screenLuma)
			default:
				stateMsg = fmt.Sprintf("VISION_TRIGGER -> HUD ready stripe=%.2f noise=%.2f d=%.2f/%.2f/%.2f", stripeVar, noiseEMA, dTop, dMid, dBottom)
			}
			publishDebug(gui.AutoTriggerDebug{
				Enabled:     true,
				Mode:        mode,
				Armed:       armed,
				Fired:       false,
				PollMs:      pollMs,
				Luma:        cur,
				Delta:       delta,
				StableCount: stableCount,
				ROI:         roi,
				StripeVar:   stripeVar,
				InGame:      inGame,
				ScreenLuma:  screenLuma,
				HasSeenDark: hasSeenDark,
				NavStage:    stageName,
				Message:     stateMsg,
			}, false)

			last = cur
			lastTop = curTop
			lastMid = curMid
			lastBottom = curBottom
			hasLast = true
		}
	}

	// Ensure only one playback goroutine runs at a time — placeholder to satisfy linter.
	_ = func(ctx context.Context, device *adb.Device, sc *controllers.ScrcpyController, _ /*expectedSongTitle*/, targetDiff string) bool {
		_ = ctx
		_ = device
		_ = sc
		_ = targetDiff
		return false
	}

	// Ensure only one playback goroutine runs at a time.
	var (
		runMu         sync.Mutex
		currentCancel context.CancelFunc
		doneCh        chan struct{}
	)

	// currentAdbDevice holds the ADB device while a run is active.
	// Used by the OCR probe API so it uses the same screencap source as MAA.
	var currentAdbDevice atomic.Pointer[adb.Device]

	srv.OCRProbe = func(mode string, x1, y1, x2, y2 int) ([]byte, error) {
		// Use the active run device if available, otherwise connect on-demand.
		dev := currentAdbDevice.Load()
		if dev == nil {
			serial := deviceSerial
			if serial == "" {
				// Pick the first authorized device.
				client := adb.NewDefaultClient()
				devices, err := client.Devices()
				if err != nil || len(devices) == 0 {
					return nil, fmt.Errorf("no ADB devices found")
				}
				dev = adb.FirstAuthorizedDevice(devices)
				if dev == nil {
					return nil, fmt.Errorf("no authorized ADB device found")
				}
			} else {
				client := adb.NewDefaultClient()
				devices, err := client.Devices()
				if err != nil {
					return nil, fmt.Errorf("adb devices: %w", err)
				}
				for _, d := range devices {
					if d.Serial() == serial {
						dev = d
						break
					}
				}
				if dev == nil {
					return nil, fmt.Errorf("ADB device %q not found", serial)
				}
			}
		}
		pngBytes, err := dev.ScreencapPNGBytes()
		if err != nil {
			return nil, fmt.Errorf("screencap: %w", err)
		}
		clamp := func(v int) float64 {
			if v < 0 {
				return 0
			}
			if v > 100 {
				return 1
			}
			return float64(v) / 100.0
		}
		titleROI := maacontrol.ROI{roiPageTitle.x1, roiPageTitle.y1, roiPageTitle.x2, roiPageTitle.y2}
		songROI := maacontrol.ROI{clamp(x1), clamp(y1), clamp(x2), clamp(y2)}
		res, err := maacontrol.DetectSongFromPNG(mode, pngBytes, titleROI, songROI)
		if err != nil {
			return nil, err
		}
		type probeResult struct {
			Mode       string   `json:"mode"`
			ROI        [4]int   `json:"roi"`
			SongTexts  []string `json:"songTexts"`
			TitleTexts []string `json:"titleTexts"`
			SongID     int      `json:"songId"`
			SongTitle  string   `json:"songTitle"`
			SongScore  int      `json:"songScore"`
			SourceText string   `json:"sourceText"`
			Top        string   `json:"top"`
		}
		out := probeResult{
			Mode:       mode,
			ROI:        [4]int{x1, y1, x2, y2},
			SongTexts:  res.SongTexts,
			TitleTexts: res.TitleTexts,
			SongID:     res.SongID,
			SongTitle:  res.SongTitle,
			SongScore:  res.SongScore,
			SourceText: res.SourceText,
			Top:        res.TopSummary(5),
		}
		return json.Marshal(out)
	}

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
				currentAdbDevice.Store(nil)
				close(thisDone)
			}()

			backend = req.Backend
			songID = req.SongID
			difficulty = req.Diff
			direction = req.Orient
			chartPath = req.ChartPath
			deviceSerial = req.DeviceSerial
			pjskMode = req.Mode == "pjsk"
			applyNavSongNameROI(req.Mode, req.NavSongROIBang, req.NavSongROIPjsk)

			var ctrl controllers.Controller
			var events []common.ViscousEventItem
			var adbDevice *adb.Device
			var scrcpyCtrl *controllers.ScrcpyController
			var hidCtrl *controllers.HIDController
			var deviceCfg *config.DeviceConfig
			var nav *maacontrol.Navigator

			switch backend {
			case "adb":
				ok, err := hasValidScrcpyServer()
				if err != nil {
					srv.SetError("Failed to verify scrcpy-server: " + err.Error())
					return
				}
				if !ok {
					srv.SetError("scrcpy-server is not ready. Restart SSM GUI and approve the download prompt, or place the server file manually.")
					return
				}
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
				if deviceSerial == "" {
					adbDevice = adb.FirstAuthorizedDevice(devices)
				} else {
					for _, d := range devices {
						if d.Serial() == deviceSerial {
							adbDevice = d
							break
						}
					}
				}
				if adbDevice == nil {
					srv.SetError("No authorized ADB device found.")
					return
				}
				currentAdbDevice.Store(adbDevice)
				scrcpyCtrl = controllers.NewScrcpyController(adbDevice)
				if err := scrcpyCtrl.Open("./"+SERVER_FILE, SERVER_FILE_VERSION); err != nil {
					srv.SetError("Failed to connect to device: " + err.Error())
					return
				}
				defer scrcpyCtrl.Close()
				deviceCfg = conf.Get(adbDevice.Serial())
				if deviceCfg == nil {
					srv.SetError(fmt.Sprintf("Device [%s] not configured. Please add it in Settings first.", adbDevice.Serial()))
					return
				}
				ctrl = scrcpyCtrl

			default: // hid
				if deviceSerial == "" {
					serials := controllers.FindHIDDevices()
					if len(serials) == 0 {
						srv.SetError("No HID devices found. Please make sure USB is connected.")
						return
					}
					deviceSerial = serials[0]
				}
				deviceCfg = conf.Get(deviceSerial)
				hidCtrl = controllers.NewHIDController(deviceCfg)
				if err := hidCtrl.Open(); err != nil {
					srv.SetError("Failed to initialize HID: " + err.Error())
					return
				}
				defer hidCtrl.Close()
				ctrl = hidCtrl
			}

			if req.AutoNavigation {
				if adbDevice == nil {
					srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: true, Mode: req.Mode, Message: "auto-nav requires ADB backend"})
					log.Warn("AutoNavigation requires ADB backend")
					return
				}

				navCfg := maacontrol.NavConfig{
					Mode:       req.Mode,
					Difficulty: difficulty,
					AdbSerial:  adbDevice.Serial(),

					KetteiROI:       maacontrol.ROI{roiKettei.x1, roiKettei.y1, roiKettei.x2, roiKettei.y2},
					LiveStartROI:    maacontrol.ROI{roiLiveStart.x1, roiLiveStart.y1, roiLiveStart.x2, roiLiveStart.y2},
					BandConfirmROI:  maacontrol.ROI{roiBandConfirmTap.x1, roiBandConfirmTap.y1, roiBandConfirmTap.x2, roiBandConfirmTap.y2},
					DialogOKROI:     maacontrol.ROI{roiDialogOK.x1, roiDialogOK.y1, roiDialogOK.x2, roiDialogOK.y2},
					DialogTitleROI:  maacontrol.ROI{roiDialogTitle.x1, roiDialogTitle.y1, roiDialogTitle.x2, roiDialogTitle.y2},
					SongTitleROI:    maacontrol.ROI{roiPageTitle.x1, roiPageTitle.y1, roiPageTitle.x2, roiPageTitle.y2},
					SongNameROI:     maacontrol.ROI{roiSongName.x1, roiSongName.y1, roiSongName.x2, roiSongName.y2},
					DifficultyTapFn: difficultyTapCoords,
					PageArrowFn:     pjskDifficultyPageArrowCoords,
					NodeROIs: map[string][4]float64{
						// tap_to_next: TAPTONEXT text area (normalised from 1280x720 design)
						"tap_to_next": {0.400, 0.889, 0.183, 0.071},
					},

					OnProgress: func(stage, scene, msg string) {
						srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{
							Enabled:  true,
							Mode:     req.Mode,
							NavStage: stage,
							NavScene: scene,
							Message:  fmt.Sprintf("%s\n  %s", stage, msg),
						})
					},
				}

				var navErr error
				nav, navErr = maacontrol.NewNavigator(navCfg)
				if navErr != nil {
					srv.SetError("AutoNavigation init failed: " + navErr.Error())
					return
				}
				defer nav.Destroy()

				// Run navigation immediately — nav's SongRecognition pipeline
				// detects the song internally (SaveSong stores it in lastSongDetect).
				// Chart loading and event generation happen after nav completes,
				// so SetReady can provide the correct NowPlaying to the frontend.
				if !nav.Run(ctx, req.Mode, difficulty) {
					return
				}

				if chartPath == "" && songID <= 0 {
					if navDetect := nav.GetLastSongDetect(); navDetect.SongID > 0 {
						songID = navDetect.SongID
						req.SongID = navDetect.SongID
						if strings.TrimSpace(req.NowPlaying.Title) == "" && navDetect.SongTitle != "" {
							req.NowPlaying.Title = navDetect.SongTitle
						}
						log.Infof("AutoNavigation: detected songID=%d title=%q via=NAV score=%d", navDetect.SongID, navDetect.SongTitle, navDetect.SongScore)
					}
				}
			}

			// AutoDetectSong via screencap: only when AutoNavigation is not active
			// (navigation already provides song detection via its internal pipeline).
			if req.AutoDetectSong && !req.AutoNavigation && chartPath == "" && songID <= 0 {
				if adbDevice == nil {
					srv.SetError("Auto song detection requires ADB backend.")
					return
				}
				detectRes, detectErr := maacontrol.DetectSongForRun(
					nav,
					req.Mode,
					adbDevice,
					maacontrol.ROI{roiPageTitle.x1, roiPageTitle.y1, roiPageTitle.x2, roiPageTitle.y2},
					maacontrol.ROI{roiSongName.x1, roiSongName.y1, roiSongName.x2, roiSongName.y2},
				)
				if detectErr != nil {
					log.Warnf("Auto song detect failed: %v", detectErr)
					srv.SetError("Auto song detect failed: " + detectErr.Error())
					return
				}

				detectedID, detectedTitle, detectErr := detectRes.ResolveSong()
				if detectErr != nil {
					srv.SetError("Auto song detect failed: " + detectErr.Error())
					return
				}

				songID = detectedID
				req.SongID = detectedID
				if strings.TrimSpace(req.NowPlaying.Title) == "" && detectedTitle != "" {
					req.NowPlaying.Title = detectedTitle
				}
				log.Infof("AutoDetectSong: detected songID=%d title=%q via=OCR score=%d", detectedID, detectedTitle, detectRes.SongScore)
			}

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

			if scrcpyCtrl != nil {
				events = scrcpyCtrl.Preprocess(rawEvents, direction == "right", deviceCfg, getJudgeLineCalculator())
			} else if hidCtrl != nil {
				events = hidCtrl.Preprocess(rawEvents, direction == "right", getJudgeLineCalculator())
			}

			np := req.NowPlaying
			np.SongID = songID
			np.Diff = difficulty
			np.Mode = req.Mode

			srv.SetReady(ctrl, events, np)

			srv.SetAutoTriggerDebug(gui.AutoTriggerDebug{Enabled: req.AutoTriggerVision, Mode: req.Mode, Message: "idle"})

			// Auto mode should continue into navigation immediately after Load.
			// This keeps the frontend flow aligned with autodori-style one-click run.
			if req.AutoNavigation {
				srv.TriggerStart()
			}

			if !srv.WaitForStart(ctx) {
				return
			}

			// navHasSeenDark: AutoNavigation already ran the full pipeline which
			// includes the live-loading dark screen, so the vision trigger can
			// skip waiting for it again.
			navHasSeenDark := req.AutoNavigation

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
				if !autoTriggerByVision(ctx, sc, req.Mode, roi, req.AutoTriggerPollMs, navHasSeenDark) {
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
	if err := downloadServerBinary(); err != nil {
		log.Dieln("Failed to download `scrcpy-server`.", fmt.Sprintf("Error: %s", err))
	}
}

func downloadServerBinary() error {
	res, err := http.Get(SERVER_FILE_DOWNLOAD_URL)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected HTTP status: %s", res.Status)
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	h := crypto.SHA256.New()
	h.Write(data)
	if fmt.Sprintf("%x", h.Sum(nil)) != SERVER_FILE_SHA256 {
		return fmt.Errorf("checksum mismatch")
	}
	if err := os.WriteFile(SERVER_FILE, data, 0o644); err != nil {
		return err
	}
	return nil
}

func hasValidScrcpyServer() (bool, error) {
	if _, err := os.Stat(SERVER_FILE); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	data, err := os.ReadFile(SERVER_FILE)
	if err != nil {
		return false, err
	}
	h := crypto.SHA256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil)) == SERVER_FILE_SHA256, nil
}

func promptYesNo(question string, defaultYes bool) bool {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Printf("%s %s: ", question, suffix)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		line = strings.TrimSpace(strings.ToLower(line))
		switch line {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		case "":
			return defaultYes
		default:
			return defaultYes
		}
	}

	line = strings.TrimSpace(strings.ToLower(line))
	switch line {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	case "":
		return defaultYes
	default:
		return defaultYes
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func maybePrepareScrcpyServerForGUI() {
	ok, err := hasValidScrcpyServer()
	if err != nil {
		log.Warnf("Unable to verify scrcpy-server: %v", err)
		return
	}
	if ok {
		return
	}

	log.Infoln("ADB backend requires scrcpy-server.")
	log.Infoln("SSM GUI can download it now from the official release URL.")
	if !promptYesNo("Download scrcpy-server now?", false) {
		log.Infoln("Skipped scrcpy-server download. ADB backend will not work until the file is available.")
		return
	}

	if err := downloadServerBinary(); err != nil {
		log.Warnf("Failed to download scrcpy-server: %v", err)
		return
	}
	log.Infoln("scrcpy-server is ready.")
}

func maybePrepareGoOCRAssetsForGUI() {
	if maacontrol.CheckOCRModels() {
		log.Infoln("MAA OCR models found.")
		return
	}

	log.Infoln("MAA OCR model files (det.onnx / rec.onnx / keys.txt) are required for OCR features.")
	log.Infof("They will be downloaded to: %s", maacontrol.DefaultOCRModelDir)
	if !promptYesNo("Download MAA OCR models now?", true) {
		log.Infoln("Skipped OCR model download. OCR features will be unavailable.")
		return
	}

	if err := downloadMAAOCRModels(); err != nil {
		log.Warnf("Failed to download MAA OCR models: %v", err)
	} else {
		log.Infoln("MAA OCR models downloaded successfully.")
	}
}

func downloadMAAOCRModels() error {
	dst := maacontrol.DefaultOCRModelDir
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}

	files := []struct{ remote, local string }{
		{MAA_OCR_MODEL_BASE_URL + "/det.onnx", filepath.Join(dst, "det.onnx")},
		{MAA_OCR_MODEL_BASE_URL + "/rec.onnx", filepath.Join(dst, "rec.onnx")},
		{MAA_OCR_MODEL_BASE_URL + "/dict.txt", filepath.Join(dst, "keys.txt")},
	}

	for _, f := range files {
		log.Infof("  downloading %s ...", filepath.Base(f.local))
		if err := downloadFileTo(f.remote, f.local); err != nil {
			return fmt.Errorf("download %s: %w", filepath.Base(f.local), err)
		}
	}
	return nil
}

func downloadFileTo(url, dst string) error {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func prepareGUIPrerequisites() {
	maybePrepareScrcpyServerForGUI()
	maybePrepareGoOCRAssetsForGUI()
}

func checkOrDownload() {
	ok, err := hasValidScrcpyServer()
	if err != nil {
		log.Die("Failed to verify server file:", err)
	}
	if !ok {
		if _, statErr := os.Stat(SERVER_FILE); statErr == nil {
			log.Warn("Checksum mismatch.")
		}
		downloadServer()
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
