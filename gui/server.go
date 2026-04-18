// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/maacontrol"
)

//go:embed frontend/dist
var staticFiles embed.FS

type PlayState int

const (
	StateIdle    PlayState = iota // 0 Idle
	StateReady                    // 1 Ready (waiting to start)
	StatePlaying                  // 2 Playing
	StateDone                     // 3 Finished
	StateError                    // 4 Error
)

type NowPlaying struct {
	SongID    int    `json:"songId"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Diff      string `json:"diff"`
	DiffLevel int    `json:"diffLevel"`
	JacketURL string `json:"jacketUrl"`
	Mode      string `json:"mode"`
}

type RunRequest struct {
	Mode         string     `json:"mode"`
	Backend      string     `json:"backend"`
	Diff         string     `json:"diff"`
	Orient       string     `json:"orient"`
	SongID       int        `json:"songId"`
	ChartPath    string     `json:"chartPath"`
	DeviceSerial string     `json:"deviceSerial"`
	NowPlaying   NowPlaying `json:"nowPlaying"`

	// Jitter settings
	TimingJitter   int64   `json:"timingJitter"`   // Time jitter (ms), 0 = disabled
	PositionJitter float64 `json:"positionJitter"` // Position jitter (track units), 0 = disabled
	TapDurJitter   int64   `json:"tapDurJitter"`   // Tap duration jitter (ms), 0 = disabled
	GreatOffsetMs  int64   `json:"greatOffsetMs"`  // Absolute offset in ms
	GreatCount     int64   `json:"greatCount"`     // Exact number of tap notes to force as Great, 0 = probability mode
	AutoNavigation bool    `json:"autoNavigation"` // Auto navigate through pre-game screens (ADB only)
	AutoDetectSong bool    `json:"autoDetectSong"` // Auto detect current selected song via OCR on 楽曲選択 screen
	GameServer     string  `json:"gameServer"`     // "jp", "tw", "en", "cn", "kr"

	// Auto Trigger (autoTrigger): wait for note pattern detection before starting
	AutoTrigger      bool    `json:"autoTrigger"`
	AutoTriggerY     float64 `json:"autoTriggerY"`
	AutoTriggerX     float64 `json:"autoTriggerX"`
	AutoTriggerGap   float64 `json:"autoTriggerGap"`
	AutoTriggerSens  float64 `json:"autoTriggerSens"`
	AutoTriggerDelay int     `json:"autoTriggerDelay"`

	// Advanced VTE parameters (0 = use mode default)
	TapDuration         int64   `json:"tapDuration"`
	FlickDuration       int64   `json:"flickDuration"`
	FlickReportInterval int64   `json:"flickReportInterval"`
	SlideReportInterval int64   `json:"slideReportInterval"`
	FlickFactor         float64 `json:"flickFactor"`
	FlickPow            float64 `json:"flickPow"`
}

type ROI struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

type Server struct {
	port int
	conf *config.Config

	// MaaLibDir is forwarded to maacontrol functions that need the MAA native
	// library directory (same value used by the Navigator).
	MaaLibDir string

	mu         sync.Mutex
	state      PlayState
	offset     int
	errMsg     string
	nowPlaying NowPlaying
	lastRunReq RunRequest
	greatReq   int64
	greatApply int64

	startCh  chan struct{}
	offsetCh chan int
	stopCh   chan struct{}

	controller controllers.Controller
	events     []common.ViscousEventItem

	clientsMu sync.Mutex
	clients   map[chan string]struct{}

	buyMusicMu   sync.Mutex
	buyMusicStop chan struct{}

	atMu      sync.Mutex
	atRunning bool
	atCancel  context.CancelFunc
	atLevels  [7]float64

	OnRunRequest     func(req RunRequest)
	OnExtractRequest func(path string) error
	// OnStop is called whenever the frontend stop button is pressed and there
	// is an active run (StateReady / StatePlaying / StateDone).  It is called
	// after stopCh is closed, so Autoplay is already stopping.  Use this to
	// cancel the run context and stop the MAA navigator.
	OnStop func()
	// OCRProbe takes an ADB screencap and runs OCR on the given percent ROI.
	// x1,y1,x2,y2 are 0-100 percent values. Returns OCR texts and song match info as JSON bytes.
	OCRProbe func(mode string, x1, y1, x2, y2 int) ([]byte, error)

	// OnPreviewRequest is called by POST /api/screen/start to open a lightweight
	// scrcpy preview connection (calibration mode). The serial may be empty to
	// use the first available device. Implementations should call SetPreviewController.
	OnPreviewRequest func(serial string) error

	// previewCtrl is used exclusively by /api/screen when s.controller has no
	// scrcpy connection (i.e. before any run has been submitted).
	previewCtrl   *controllers.ScrcpyController
	previewCtrlMu sync.Mutex
}

func NewServer(port int, conf *config.Config) *Server {
	s := &Server{
		port:    port,
		conf:    conf,
		state:   StateIdle,
		clients: make(map[chan string]struct{}),
	}
	s.startCh = make(chan struct{}, 1)
	s.stopCh = make(chan struct{})
	s.offsetCh = make(chan int, 32)
	return s
}

// ─── SSE ───────────────────────────────────────

func (s *Server) addClient(ch chan string) {
	s.clientsMu.Lock()
	s.clients[ch] = struct{}{}
	s.clientsMu.Unlock()
}

func (s *Server) removeClient(ch chan string) {
	s.clientsMu.Lock()
	delete(s.clients, ch)
	s.clientsMu.Unlock()
}

func (s *Server) broadcast(msg string) {
	s.clientsMu.Lock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	s.clientsMu.Unlock()
}

func (s *Server) broadcastState() {
	s.mu.Lock()
	data := map[string]any{
		"state":      int(s.state),
		"offset":     s.offset,
		"error":      s.errMsg,
		"nowPlaying": s.nowPlaying,
		"greatReq":   s.greatReq,
		"greatApply": s.greatApply,
	}
	s.mu.Unlock()
	s.atMu.Lock()
	data["atRunning"] = s.atRunning
	lvl := s.atLevels
	s.atMu.Unlock()
	levels := make([]float64, 7)
	copy(levels, lvl[:])
	data["atLevels"] = levels
	b, _ := json.Marshal(data)
	s.broadcast("data: " + string(b) + "\n\n")
}

// ─── HTTP handlers ─────────────────────────────

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	ch := make(chan string, 16)
	s.addClient(ch)
	defer s.removeClient(ch)
	s.broadcastState()
	for {
		select {
		case msg := <-ch:
			fmt.Fprint(w, msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	data := map[string]any{
		"state":      int(s.state),
		"offset":     s.offset,
		"error":      s.errMsg,
		"nowPlaying": s.nowPlaying,
		"greatReq":   s.greatReq,
		"greatApply": s.greatApply,
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.lastRunReq = req
	s.greatReq = req.GreatCount
	s.greatApply = 0
	s.mu.Unlock()

	if s.OnRunRequest != nil {
		s.OnRunRequest(req)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.TriggerStart() {
		http.Error(w, "not ready", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) TriggerStart() bool {
	s.mu.Lock()
	st := s.state
	ch := s.startCh
	s.mu.Unlock()

	if st != StateReady {
		return false
	}

	select {
	case ch <- struct{}{}:
	default:
	}

	return true
}

func (s *Server) handleOffset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Delta int `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.offset += body.Delta
	ch := s.offsetCh
	s.mu.Unlock()
	select {
	case ch <- body.Delta:
	default:
	}
	s.broadcastState()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	st := s.state
	req := s.lastRunReq
	s.mu.Unlock()

	if st != StateReady && st != StatePlaying && st != StateDone && st != StateIdle {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.mu.Lock()
	oldStop := s.stopCh
	s.mu.Unlock()
	select {
	case <-oldStop:
	default:
		close(oldStop)
	}

	s.mu.Lock()
	s.state = StateIdle
	s.mu.Unlock()
	s.broadcastState()

	if s.OnStop != nil {
		s.OnStop()
	}

	if s.OnRunRequest != nil && !req.AutoNavigation && (st == StatePlaying || st == StateDone || st == StateReady) {
		go s.OnRunRequest(req)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		devices := s.conf.Devices
		if devices == nil {
			devices = map[string]*config.DeviceConfig{}
		}
		json.NewEncoder(w).Encode(devices)
	case http.MethodPost:
		var body struct {
			Serial string `json:"serial"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if s.conf.Devices == nil {
			s.conf.Devices = map[string]*config.DeviceConfig{}
		}
		s.conf.Devices[body.Serial] = &config.DeviceConfig{
			Serial: body.Serial,
			Width:  body.Width,
			Height: body.Height,
		}
		s.conf.Save()
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		var body struct {
			Serial string `json:"serial"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if s.conf.Devices != nil {
			delete(s.conf.Devices, body.Serial)
			s.conf.Save()
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSongDB(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != "pjsk" {
		mode = "bang"
	}
	w.Header().Set("Content-Type", "application/json")

	if mode == "bang" {
		songs, err := fetchOrLoad("./all.5.json", "https://bestdori.com/api/songs/all.5.json")
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		bands, err := fetchOrLoad("./all.1.json", "https://bestdori.com/api/bands/all.1.json")
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"songs":%s,"bands":%s}`, songs, bands)
	} else {
		const sekaiMusicsURL = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musics.json"
		const sekaiMusicDifficultiesURL = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musicDifficulties.json"
		const sekaiMusicArtistsURL = "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musicArtists.json"

		songs, err := fetchOrLoad("./sekai_master_db_diff_musics.json", sekaiMusicsURL)
		if err != nil {
			http.Error(w, `{"error":"songs EN fetch failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		difficulties, err := fetchOrLoad("./sekai_master_db_diff_music_difficulties.json", sekaiMusicDifficultiesURL)
		if err != nil {
			http.Error(w, `{"error":"difficulties fetch failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		artists, err := fetchOrLoad("./sekai_master_db_diff_music_artists.json", sekaiMusicArtistsURL)
		if err != nil {
			http.Error(w, `{"error":"artists fetch failed: `+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"songs":%s,"songsJp":%s,"bands":{},"artists":%s,"musicDifficulties":%s}`, songs, songs, artists, difficulties)
	}
}

func fetchOrLoad(localPath, url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			data, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				if localData, localErr := os.ReadFile(localPath); localErr != nil || !bytes.Equal(localData, data) {
					go os.WriteFile(localPath, data, 0o644)
				}
				return data, nil
			}
		}
	}
	if data, readErr := os.ReadFile(localPath); readErr == nil {
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("failed to fetch %s and local cache missing", url)
}

func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, "path field is required", http.StatusBadRequest)
		return
	}
	if s.OnExtractRequest == nil {
		http.Error(w, "extract not configured", http.StatusInternalServerError)
		return
	}
	if err := s.OnExtractRequest(body.Path); err != nil {
		http.Error(w, "Extraction failed:"+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Playback state control ───────────────────────────────

func (s *Server) SetReady(ctrl controllers.Controller, events []common.ViscousEventItem, np NowPlaying) {
	s.mu.Lock()
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	s.controller = ctrl
	s.events = events
	s.state = StateReady
	s.offset = 0
	s.errMsg = ""
	s.nowPlaying = np
	s.startCh = make(chan struct{}, 1)
	s.stopCh = make(chan struct{})
	s.offsetCh = make(chan int, 32)
	s.mu.Unlock()

	// Perform a reset when ready instead of at playback start
	if sc, ok := ctrl.(*controllers.ScrcpyController); ok {
		sc.ResetTouch()
	}

	s.broadcastState()
}

func (s *Server) SetError(msg string) {
	s.mu.Lock()
	s.state = StateError
	s.errMsg = msg
	s.mu.Unlock()
	s.broadcastState()
}

// SetSongPreview updates nowPlaying without changing playback state.
// Called during auto-navigation when a song is detected on the select screen,
// so the Play Control pane shows the matched song before SetReady is invoked.
func (s *Server) SetSongPreview(np NowPlaying) {
	s.mu.Lock()
	s.nowPlaying = np
	s.mu.Unlock()
	s.broadcastState()
}

func (s *Server) SetGreatStats(requested, applied int64) {
	s.mu.Lock()
	if requested < 0 {
		requested = 0
	}
	if applied < 0 {
		applied = 0
	}
	s.greatReq = requested
	s.greatApply = applied
	s.mu.Unlock()
	s.broadcastState()
}

func (s *Server) WaitForStart(ctx context.Context) bool {
	s.mu.Lock()
	startCh := s.startCh
	stopCh := s.stopCh
	s.mu.Unlock()

	select {
	case <-startCh:
		if ctx.Err() != nil {
			return false
		}
		s.mu.Lock()
		s.state = StatePlaying
		s.mu.Unlock()
		go s.broadcastState()
		return true
	case <-ctx.Done():
		return false
	case <-stopCh:
		return false
	}
}

func (s *Server) Autoplay(ctx context.Context, start time.Time) {
	s.mu.Lock()
	stopCh := s.stopCh
	events := s.events
	offsetCh := s.offsetCh
	s.mu.Unlock()

	n := len(events)
	log.Debugf("[Autoplay] start: n=%d start=%v ctx.Err=%v", n, start, ctx.Err())
	if n > 0 {
		log.Debugf("[Autoplay] events[0].Timestamp=%d events[n-1].Timestamp=%d", events[0].Timestamp, events[n-1].Timestamp)
	}
	current := 0

	for current < n {
		select {
		case <-stopCh:
			log.Debugf("[Autoplay] stopped via stopCh at event %d/%d", current, n)
			goto done
		case <-ctx.Done():
			log.Debugf("[Autoplay] stopped via ctx at event %d/%d", current, n)
			goto done
		default:
		}

		select {
		case delta := <-offsetCh:
			start = start.Add(time.Duration(-delta) * time.Millisecond)
		default:
		}

		now := time.Since(start).Milliseconds()
		event := events[current]
		remaining := event.Timestamp - now

		if remaining <= 0 {
			s.controller.Send(event.Data)
			current++
			continue
		}

		if remaining > 10 {
			select {
			case <-stopCh:
				goto done
			case <-ctx.Done():
				goto done
			case <-time.After(time.Duration(remaining-5) * time.Millisecond):
			}
		} else if remaining > 4 {
			time.Sleep(1 * time.Millisecond)
		}
	}

done:
	log.Debugf("[Autoplay] done: sent %d/%d events", current, n)
	s.mu.Lock()
	doneCtrl := s.controller
	s.mu.Unlock()
	if sc, ok := doneCtrl.(*controllers.ScrcpyController); ok {
		sc.ResetTouch()
	}
	s.mu.Lock()
	if s.state == StatePlaying {
		s.state = StateDone
		req := s.lastRunReq
		s.mu.Unlock()
		s.broadcastState()

		go func() {
			time.Sleep(1000 * time.Millisecond)

			s.mu.Lock()
			if s.state != StateDone || s.lastRunReq != req {
				s.mu.Unlock()
				return
			}
			s.state = StateIdle
			s.mu.Unlock()
			s.broadcastState()

			if s.OnRunRequest != nil && !req.AutoNavigation {
				s.OnRunRequest(req)
			}
		}()
	} else {
		s.mu.Unlock()
	}
}

// ─── ADB Utils ──────────────────────────────────────

func (s *Server) handleKillAdb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cmd := exec.Command("adb", "kill-server")
	_ = cmd.Run()

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleBuyMusic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Action string `json:"action"`
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.buyMusicMu.Lock()
	defer s.buyMusicMu.Unlock()

	switch body.Action {
	case "stop":
		if s.buyMusicStop != nil {
			select {
			case <-s.buyMusicStop:
			default:
				close(s.buyMusicStop)
			}
			s.buyMusicStop = nil
		}
		w.WriteHeader(http.StatusOK)

	case "start":
		if s.buyMusicStop != nil {
			// already running
			w.WriteHeader(http.StatusOK)
			return
		}
		stopCh := make(chan struct{})
		s.buyMusicStop = stopCh
		serial := strings.TrimSpace(body.Serial)
		maaLibDir := s.MaaLibDir
		go func() {
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				<-stopCh
				cancel()
			}()
			cfg := maacontrol.BuyMusicConfig{
				AdbSerial:   serial,
				MaaLibDir:   maaLibDir,
				ResourceDir: "./maacontrol/resource",
			}
			if err := maacontrol.RunBuyMusicLoop(ctx, cfg); err != nil {
				fmt.Printf("[BuyMusic] error: %v\n", err)
			}
		}()
		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

func (s *Server) handleDetectAdb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	client := adb.NewDefaultClient()
	devices, err := client.Devices()

	if err != nil || len(devices) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"serial": ""})
		return
	}

	device := adb.FirstAuthorizedDevice(devices)
	if device != nil {
		json.NewEncoder(w).Encode(map[string]string{"serial": device.Serial()})
	} else {
		json.NewEncoder(w).Encode(map[string]string{"serial": ""})
	}
}

func (s *Server) handleOCRProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	probe := s.OCRProbe
	req := s.lastRunReq
	s.mu.Unlock()

	if probe == nil {
		http.Error(w, "OCR probe unavailable (no ADB device connected)", http.StatusServiceUnavailable)
		return
	}

	q := r.URL.Query()
	parseIntParam := func(key string, def int) int {
		v := q.Get(key)
		if v == "" {
			return def
		}
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}

	mode := q.Get("mode")
	if mode == "" {
		mode = req.Mode
	}
	if mode == "" {
		mode = "bang"
	}

	x1 := parseIntParam("x1", 0)
	y1 := parseIntParam("y1", 0)
	x2 := parseIntParam("x2", 100)
	y2 := parseIntParam("y2", 100)

	data, err := probe(mode, x1, y1, x2, y2)
	if err != nil {
		http.Error(w, "OCR probe failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(data)
}

func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	ctrl := s.controller
	req := s.lastRunReq
	s.mu.Unlock()

	sc, ok := ctrl.(*controllers.ScrcpyController)
	if ok && sc != nil {
		frame, hasFrame := sc.LatestFrame()
		if hasFrame && frame.Width > 0 && frame.Height > 0 {
			need := frame.Width * frame.Height
			if len(frame.Plane0) >= need {
				img := image.NewGray(image.Rect(0, 0, frame.Width, frame.Height))
				for y := 0; y < frame.Height; y++ {
					srcOff := y * frame.Width
					dstOff := y * img.Stride
					copy(img.Pix[dstOff:dstOff+frame.Width], frame.Plane0[srcOff:srcOff+frame.Width])
				}
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				if err := png.Encode(w, img); err != nil {
					http.Error(w, "encode failed", http.StatusInternalServerError)
					return
				}
				return
			}
		}
	}

	// Fallback path: if scrcpy frame isn't ready yet, fetch a fresh screenshot from ADB
	// so ROI editor can still be used before pressing Load/Start.
	if err := adb.StartADBServer("localhost", 5037); err != nil && err != adb.ErrADBServerRunning {
		http.Error(w, "adb server unavailable", http.StatusServiceUnavailable)
		return
	}

	serial := strings.TrimSpace(r.URL.Query().Get("serial"))
	if serial == "" {
		serial = strings.TrimSpace(req.DeviceSerial)
	}

	device, err := pickDevice(serial)
	if err != nil {
		http.Error(w, "frame preview unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	pngBytes, err := device.ScreencapPNGBytes()
	if err != nil {
		http.Error(w, "screencap failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		http.Error(w, "decode screencap failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if err := png.Encode(w, decoded); err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
}

// ─── Screen MJPEG stream ──────────────────────────
// GET /api/screen          — MJPEG stream (multipart/x-mixed-replace)
// GET /api/screen?once=1   — single JPEG frame
func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	getFrame := func() (*image.Gray, bool) {
		// prefer active game controller
		s.mu.Lock()
		ctrl := s.controller
		s.mu.Unlock()
		sc, ok := ctrl.(*controllers.ScrcpyController)
		if !ok || sc == nil {
			// fall back to preview controller (calibration mode)
			s.previewCtrlMu.Lock()
			sc = s.previewCtrl
			s.previewCtrlMu.Unlock()
			if sc == nil {
				return nil, false
			}
		}
		frame, ok := sc.LatestFrame()
		if !ok || frame.Width <= 0 || frame.Height <= 0 || len(frame.Plane0) < frame.Width*frame.Height {
			return nil, false
		}
		img := image.NewGray(image.Rect(0, 0, frame.Width, frame.Height))
		for y := 0; y < frame.Height; y++ {
			srcOff := y * frame.Width
			dstOff := y * img.Stride
			copy(img.Pix[dstOff:dstOff+frame.Width], frame.Plane0[srcOff:srcOff+frame.Width])
		}
		return img, true
	}

	once := r.URL.Query().Get("once") == "1"
	if once {
		img, ok := getFrame()
		if !ok {
			http.Error(w, "no frame available", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		jpeg.Encode(w, img, &jpeg.Options{Quality: 75})
		return
	}

	// MJPEG stream
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			img, ok := getFrame()
			if !ok {
				continue
			}
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 75}); err != nil {
				continue
			}
			fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", buf.Len())
			w.Write(buf.Bytes())
			fmt.Fprintf(w, "\r\n")
			flusher.Flush()
		}
	}
}

// SetPreviewController registers a scrcpy controller used exclusively by
// /api/screen when no active game run is in progress (calibration preview).
func (s *Server) SetPreviewController(sc *controllers.ScrcpyController) {
	s.previewCtrlMu.Lock()
	s.previewCtrl = sc
	s.previewCtrlMu.Unlock()
}

// POST /api/screen/start — opens a lightweight scrcpy preview connection so
// that /api/screen works before any song run has been submitted.
func (s *Server) handleScreenStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		DeviceSerial string `json:"deviceSerial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if s.OnPreviewRequest == nil {
		http.Error(w, "preview not configured", http.StatusNotImplemented)
		return
	}
	if err := s.OnPreviewRequest(body.DeviceSerial); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── 7-Lane Vision Detection ─────────────────────

// StartAutoTrigger starts the autoTrigger detection goroutine with the given parameters.
// Safe to call when already running; it will restart with new params.
func (s *Server) StartAutoTrigger(y, x, gap, sens float64, delay int) {
	s.atMu.Lock()
	if s.atCancel != nil {
		s.atCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.atCancel = cancel
	s.atRunning = true
	s.atMu.Unlock()
	s.broadcastState()

	type atParams struct {
		Y, X, Gap, Sens float64
		Delay           int
	}
	params := atParams{Y: y, X: x, Gap: gap, Sens: sens, Delay: delay}
	go func() {
		defer func() {
			s.atMu.Lock()
			s.atRunning = false
			var zero [7]float64
			s.atLevels = zero
			s.atMu.Unlock()
			s.broadcastState()
		}()

		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		lastBroadcast := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			// Only detect when in Ready state (waiting for song start).
			s.mu.Lock()
			curState := s.state
			ctrl := s.controller
			s.mu.Unlock()
			if curState != StateReady {
				// Song already started via another path (manual click, autoMode pipeline, etc.)
				// — our job is done, exit so defer can zero atLevels/atRunning.
				if curState == StatePlaying || curState == StateDone || curState == StateError {
					return
				}
				continue
			}
			sc, ok := ctrl.(*controllers.ScrcpyController)
			if !ok || sc == nil {
				continue
			}
			frame, ok := sc.LatestFrame()
			if !ok || frame.Width <= 0 || frame.Height <= 0 {
				continue
			}
			if time.Since(frame.CapturedAt) > 300*time.Millisecond {
				continue
			}

			cy := int(params.Y * float64(frame.Height) / 100.0)
			cx := int(params.X * float64(frame.Width) / 100.0)
			gap := max(1, int(params.Gap*float64(frame.Width)/100.0))

			var levels [7]float64
			triggered := false
			for lane := range 7 {
				lx := cx + (lane-3)*gap
				bright, total := 0, 0
				for dy := -7; dy < 7; dy++ {
					for dx := -7; dx < 7; dx++ {
						px, py := lx+dx, cy+dy
						if px < 0 || px >= frame.Width || py < 0 || py >= frame.Height {
							continue
						}
						total++
						if frame.Plane0[py*frame.Width+px] > 220 {
							bright++
						}
					}
				}
				if total > 0 {
					levels[lane] = float64(bright) / float64(total)
				}
				if levels[lane] >= params.Sens {
					triggered = true
				}
			}

			s.atMu.Lock()
			s.atLevels = levels
			s.atMu.Unlock()
			if time.Since(lastBroadcast) >= 100*time.Millisecond {
				lastBroadcast = time.Now()
				s.broadcastState()
			}

			if triggered {
				delayMs := max(0, params.Delay)
				if delayMs > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Duration(delayMs) * time.Millisecond):
					}
				}
				log.Debugf("[autoTrigger] triggered! calling TriggerStart()")
				ok := s.TriggerStart()
				log.Debugf("[autoTrigger] TriggerStart() returned %v", ok)
				if ok {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
	}()
}

// StopAutoTrigger stops the autoTrigger detection goroutine if running.
func (s *Server) StopAutoTrigger() {
	s.atMu.Lock()
	if s.atCancel != nil {
		s.atCancel()
		s.atCancel = nil
	}
	s.atRunning = false
	s.atMu.Unlock()
	s.broadcastState()
}

func (s *Server) handleAutoTriggerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var params struct {
		Y     float64 `json:"y"`
		X     float64 `json:"x"`
		Gap   float64 `json:"gap"`
		Sens  float64 `json:"sens"`
		Delay int     `json:"delay"`
	}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.StartAutoTrigger(params.Y, params.X, params.Gap, params.Sens, params.Delay)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAutoTriggerStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.StopAutoTrigger()
	w.WriteHeader(http.StatusOK)
}

// ─── Startup ──────────────────────────────────────

func (s *Server) Start() (string, error) {
	staticFS, err := fs.Sub(staticFiles, "frontend/dist")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	staticHandler := http.FileServer(http.FS(staticFS))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Avoid stale frontend assets after rebuilding embedded dist files.
		if strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		staticHandler.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/offset", s.handleOffset)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/device", s.handleDevice)
	mux.HandleFunc("/api/extract", s.handleExtract)
	mux.HandleFunc("/api/songdb", s.handleSongDB)
	mux.HandleFunc("/api/kill-adb", s.handleKillAdb)
	mux.HandleFunc("/api/detect-adb", s.handleDetectAdb)
	mux.HandleFunc("/api/buy-music", s.handleBuyMusic)
	mux.HandleFunc("/api/ocr-probe", s.handleOCRProbe)
	mux.HandleFunc("/api/frame.png", s.handleFrame)
	mux.HandleFunc("/api/screen", s.handleScreen)
	mux.HandleFunc("/api/screen/start", s.handleScreenStart)
	mux.HandleFunc("/api/autoTrigger/start", s.handleAutoTriggerStart)
	mux.HandleFunc("/api/autoTrigger/stop", s.handleAutoTriggerStop)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return "", err
	}

	addr := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	go http.Serve(ln, mux)
	return addr, nil
}
