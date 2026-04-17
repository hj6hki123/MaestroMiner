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
	"io"
	"net/http"
	"os"
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

// ─────────────────────────────────────────────
// GUI mode main flow
// ─────────────────────────────────────────────
func runGUI(conf *config.Config) {
	srv := gui.NewServer(guiPort, conf)
	prepareGUIPrerequisites()

	// Ensure only one playback goroutine runs at a time.
	var (
		runMu         sync.Mutex
		currentCancel context.CancelFunc
		doneCh        chan struct{}
	)

	// Persistent scrcpy connection — reused across Stop/Re-run on the same device.
	// Only closed when the device serial changes or the app shuts down.
	var (
		scrcpyPersist       *controllers.ScrcpyController
		scrcpyPersistSerial string
		scrcpyPersistMu     sync.Mutex
	)
	closePersistedScrcpy := func() {
		scrcpyPersistMu.Lock()
		defer scrcpyPersistMu.Unlock()
		if scrcpyPersist != nil {
			scrcpyPersist.Close()
			scrcpyPersist = nil
			scrcpyPersistSerial = ""
		}
	}

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
		titleROI := maacontrol.SongTitleROI
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
				deviceCfg = conf.Get(adbDevice.Serial())
				if deviceCfg == nil {
					srv.SetError(fmt.Sprintf("Device [%s] not configured. Please add it in Settings first.", adbDevice.Serial()))
					return
				}

				// Reuse existing scrcpy connection if same device; reconnect only if device changed.
				scrcpyPersistMu.Lock()
				if scrcpyPersist != nil && scrcpyPersistSerial == adbDevice.Serial() {
					scrcpyCtrl = scrcpyPersist
					scrcpyPersistMu.Unlock()
				} else {
					if scrcpyPersist != nil {
						scrcpyPersist.Close()
						scrcpyPersist = nil
					}
					scrcpyPersistMu.Unlock()
					newCtrl := controllers.NewScrcpyController(adbDevice)
					if err := newCtrl.Open("./"+SERVER_FILE, SERVER_FILE_VERSION); err != nil {
						srv.SetError("Failed to connect to device: " + err.Error())
						return
					}
					scrcpyPersistMu.Lock()
					scrcpyPersist = newCtrl
					scrcpyPersistSerial = adbDevice.Serial()
					scrcpyPersistMu.Unlock()
					scrcpyCtrl = newCtrl
				}

				w, h := deviceCfg.Width, deviceCfg.Height
				if direction == "right" || direction == "left" {
					w, h = deviceCfg.Height, deviceCfg.Width
				}
				scrcpyCtrl.SetDeviceSize(w, h)
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

			// buildPlaybackPlan loads/parses the chart for resolvedSongID, generates
			// touch events, preprocesses them for the active controller, and returns
			// them ready for srv.SetReady.  Called from both PlaySong (auto path) and
			// the non-auto code path below.
			buildPlaybackPlan := func(resolvedSongID int) ([]common.ViscousEventItem, gui.NowPlaying, error) {
				var chartText []byte
				var loadErr error
				if chartPath == "" {
					var pathResults []string
					if pjskMode {
						pathResults, loadErr = filepath.Glob(filepath.Join("./assets/sekai/assetbundle/resources/startapp/music/music_score/",
							fmt.Sprintf("%04d_01/%s.txt", resolvedSongID, difficulty)))
					} else {
						pathResults, loadErr = filepath.Glob(filepath.Join("./assets/star/forassetbundle/startapp/musicscore/",
							fmt.Sprintf("musicscore*/%03d/*_%s.txt", resolvedSongID, difficulty)))
					}
					if loadErr != nil || len(pathResults) < 1 {
						msg := "Musicscore not found. Please extract assets first or use a custom chart path."
						srv.SetError(msg)
						return nil, gui.NowPlaying{}, fmt.Errorf("%s", msg)
					}
					chartText, loadErr = os.ReadFile(pathResults[0])
				} else {
					chartText, loadErr = os.ReadFile(chartPath)
				}
				if loadErr != nil {
					srv.SetError("Failed to read musicscore: " + loadErr.Error())
					return nil, gui.NowPlaying{}, loadErr
				}

				var parsedChart scores.Chart
				var parseErr error
				if pjskMode {
					parsedChart, parseErr = scores.ParseSUS(string(chartText))
					if parseErr != nil {
						srv.SetError("Failed to parse SUS: " + parseErr.Error())
						return nil, gui.NowPlaying{}, parseErr
					}
				} else {
					parsedChart = scores.ParseBMS(string(chartText))
				}

				gc := &scores.VTEGenerateConfig{
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
					gc.FlickFactor = 1.0 / 6
					gc.FlickDuration = 20
				}
				if req.TapDuration > 0 {
					gc.TapDuration = req.TapDuration
				}
				if req.FlickDuration > 0 {
					gc.FlickDuration = req.FlickDuration
				}
				if req.FlickReportInterval > 0 {
					gc.FlickReportInterval = req.FlickReportInterval
				}
				if req.SlideReportInterval > 0 {
					gc.SlideReportInterval = req.SlideReportInterval
				}
				if req.FlickFactor > 0 {
					gc.FlickFactor = req.FlickFactor
				}
				if req.FlickPow > 0 {
					gc.FlickPow = req.FlickPow
				}
				rawEvts, greatApplied := scores.GenerateTouchEvent(gc, parsedChart)
				srv.SetGreatStats(req.GreatCount, int64(greatApplied))

				var evts []common.ViscousEventItem
				if scrcpyCtrl != nil {
					evts = scrcpyCtrl.Preprocess(rawEvts, direction == "right", deviceCfg, getJudgeLineCalculator())
				} else if hidCtrl != nil {
					evts = hidCtrl.Preprocess(rawEvts, direction == "right", getJudgeLineCalculator())
				}
				np := req.NowPlaying
				np.SongID = resolvedSongID
				np.Diff = difficulty
				np.Mode = req.Mode
				return evts, np, nil
			}

			if req.AutoNavigation {
				if adbDevice == nil {
					log.Warn("AutoNavigation requires ADB backend")
					srv.SetError("AutoNavigation requires ADB backend")
					return
				}

				navCfg := maacontrol.NavConfig{
					Mode:       req.Mode,
					Difficulty: difficulty,
					AdbSerial:  adbDevice.Serial(),
					GameServer: req.GameServer,

					OnProgress: func(stage, scene, msg string) {
						log.Infof("[MAA] %s / %s: %s", stage, scene, msg)
					},

					OnSongDetected: func(detectedID int, detectedTitle string) {
						np := req.NowPlaying
						np.SongID = detectedID
						np.Mode = req.Mode
						np.Title = detectedTitle
						srv.SetSongPreview(np)
					},
				}

				// PlaySong: called from MAA's Play custom action when wait_live_start
				// fires (pause button visible). Drives scrcpy/HID playback until ctx
				// is cancelled or the chart finishes. live_failed polling is handled
				// by playAction.Run() via ctx.RunTask().
				navCfg.PlaySong = func(playCtx context.Context) error {
					// 1. Resolve song from navigation's SongRecognition pipeline.
					// Always re-read the latest detection in auto-navigation mode so that
					// each loop iteration picks up the newly selected song, not the first one.
					if chartPath == "" {
						if detect := nav.GetLastSongDetect(); detect.SongID > 0 {
							songID = detect.SongID
							req.SongID = detect.SongID
							req.NowPlaying.Title = detect.SongTitle
							log.Infof("[PlaySong] detected songID=%d title=%q score=%d", detect.SongID, detect.SongTitle, detect.SongScore)
						}
					}

					// 2. Load chart + generate events
					evts, np, buildErr := buildPlaybackPlan(songID)
					if buildErr != nil {
						return buildErr
					}

					// 3. If auto trigger, start autoTrigger now that we're confirmed on the
					// live screen (wait_live_start fired). Otherwise trigger immediately.
					if req.AutoTrigger {
						srv.StartAutoTrigger(req.AutoTriggerY, req.AutoTriggerX, req.AutoTriggerGap, req.AutoTriggerSens, req.AutoTriggerDelay)
					}
					srv.SetReady(ctrl, evts, np)
					if !req.AutoTrigger {
						srv.TriggerStart()
					}

					// 4. Wait for start acknowledgement
					if !srv.WaitForStart(playCtx) {
						if req.AutoTrigger {
							srv.StopAutoTrigger()
						}
						return nil // ctx cancelled externally
					}

					// 5. Blocking playback.
					start := time.Now().Add(-time.Duration(evts[0].Timestamp) * time.Millisecond)
					srv.Autoplay(playCtx, start)
					if req.AutoTrigger {
						srv.StopAutoTrigger()
					}
					return nil
				}

				var navErr error
				nav, navErr = maacontrol.NewNavigator(navCfg)
				if navErr != nil {
					srv.SetError("Failed to initialize MAA navigator: " + navErr.Error())
					return
				}
				defer nav.Destroy()
				nav.Run(ctx, req.Mode, difficulty)
				return
			}

			// AutoDetectSong via screencap: only when AutoNavigation is not active
			// (navigation already provides song detection via its internal pipeline).
			if req.AutoDetectSong && !req.AutoNavigation && chartPath == "" && songID <= 0 {
				if adbDevice == nil {
					srv.SetError("Auto song detection requires ADB backend.")
					return
				}
				songNameROI := maacontrol.SongNameROIBang
				if req.Mode == "pjsk" {
					songNameROI = maacontrol.SongNameROIPjsk
				}
				detectRes, detectErr := maacontrol.DetectSongForRun(
					nav,
					req.Mode,
					adbDevice,
					maacontrol.SongTitleROI,
					songNameROI,
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

			// Non-auto path: load chart, generate events, set ready.
			events, np, buildErr := buildPlaybackPlan(songID)
			if buildErr != nil {
				return
			}
			srv.SetReady(ctrl, events, np)

			// Non-auto path: wait for user to press Start in the UI (or autoTrigger detection).
			if !srv.WaitForStart(ctx) {
				return
			}
			start := time.Now().Add(-time.Duration(events[0].Timestamp) * time.Millisecond)
			srv.Autoplay(ctx, start)

			time.Sleep(300 * time.Millisecond)
		}()
	}

	srv.OnRunRequest = func(req gui.RunRequest) {
		runOnce(req)
	}

	srv.OnStop = func() {
		runMu.Lock()
		cancel := currentCancel
		runMu.Unlock()
		if cancel != nil {
			cancel()
		}
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
	closePersistedScrcpy()
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
	if dc != nil {
		controller.SetDeviceSize(dc.Height, dc.Width)
	}
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
