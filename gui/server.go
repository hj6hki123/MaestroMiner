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
	TimingJitter       int64   `json:"timingJitter"`       // Time jitter (ms), 0 = disabled
	PositionJitter     float64 `json:"positionJitter"`     // Position jitter (track units), 0 = disabled
	TapDurJitter       int64   `json:"tapDurJitter"`       // Tap duration jitter (ms), 0 = disabled
	GreatOffsetMs      int64   `json:"greatOffsetMs"`      // Absolute offset in ms
	GreatCount         int64   `json:"greatCount"`         // Exact number of tap notes to force as Great, 0 = probability mode
	AutoTriggerVision  bool    `json:"autoTriggerVision"`  // Auto start by visual detection on decoded scrcpy frames
	AutoTriggerPollMs  int64   `json:"autoTriggerPollMs"`  // Frame polling interval in ms for visual trigger
	AutoTriggerROIBang ROI     `json:"autoTriggerRoiBang"` // ROI (% space) for Bang mode
	AutoTriggerROIPjsk ROI     `json:"autoTriggerRoiPjsk"` // ROI (% space) for PJSK mode
	AutoNavigation     bool    `json:"autoNavigation"`     // Auto navigate through pre-game screens (ADB only)
	AutoDetectSong     bool    `json:"autoDetectSong"`     // Auto detect current selected song via OCR on 楽曲選択 screen
	NavSongROIBang     ROI     `json:"navSongRoiBang"`     // OCR ROI (% space) for song-name panel in Bang mode
	NavSongROIPjsk     ROI     `json:"navSongRoiPjsk"`     // OCR ROI (% space) for song-name panel in PJSK mode

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

type AutoTriggerDebug struct {
	Enabled     bool    `json:"enabled"`
	Mode        string  `json:"mode"`
	Armed       bool    `json:"armed"`
	Fired       bool    `json:"fired"`
	PollMs      int64   `json:"pollMs"`
	Luma        float64 `json:"luma"`
	Delta       float64 `json:"delta"`
	StableCount int     `json:"stableCount"`
	ROI         ROI     `json:"roi"`
	Message     string  `json:"message"`
	StripeVar   float64 `json:"stripeVar"`   // column-luma std-dev across lane area; high = track visible
	InGame      bool    `json:"inGame"`      // true when stripe variance confirms HUD is on screen
	ScreenLuma  float64 `json:"screenLuma"`  // whole-frame average luma; album loading screen is very dark (~40)
	HasSeenDark bool    `json:"hasSeenDark"` // true once a dark loading screen has been observed (primary arm gate)
	NavStage    string  `json:"navStage"`    // current pipeline stage (SCREEN_CHECK, SONG_DETECT, ...)
	NavScene    string  `json:"navScene"`    // current scene detected by navigation pipeline
}

type Server struct {
	port int
	conf *config.Config

	mu         sync.Mutex
	state      PlayState
	offset     int
	errMsg     string
	nowPlaying NowPlaying
	lastRunReq RunRequest
	greatReq   int64
	greatApply int64
	autoDebug  AutoTriggerDebug

	startCh  chan struct{}
	offsetCh chan int
	stopCh   chan struct{}

	controller controllers.Controller
	events     []common.ViscousEventItem

	clientsMu sync.Mutex
	clients   map[chan string]struct{}

	OnRunRequest     func(req RunRequest)
	OnExtractRequest func(path string) error
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
	data := map[string]interface{}{
		"state":            int(s.state),
		"offset":           s.offset,
		"error":            s.errMsg,
		"nowPlaying":       s.nowPlaying,
		"greatReq":         s.greatReq,
		"greatApply":       s.greatApply,
		"autoTriggerDebug": s.autoDebug,
	}
	s.mu.Unlock()
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
	data := map[string]interface{}{
		"state":            int(s.state),
		"offset":           s.offset,
		"error":            s.errMsg,
		"nowPlaying":       s.nowPlaying,
		"greatReq":         s.greatReq,
		"greatApply":       s.greatApply,
		"autoTriggerDebug": s.autoDebug,
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

	if st != StateReady && st != StatePlaying && st != StateDone {
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

	if s.OnRunRequest != nil && (st == StatePlaying || st == StateDone) {
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
	s.autoDebug = AutoTriggerDebug{}
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

func (s *Server) SetAutoTriggerDebug(v AutoTriggerDebug) {
	s.mu.Lock()
	s.autoDebug = v
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
	//ctrl := s.controller
	s.mu.Unlock()

	// if sc, ok := ctrl.(*controllers.ScrcpyController); ok {
	// 	sc.ResetTouch()
	// 	time.Sleep(50 * time.Millisecond) // 等待 50ms 讓設備反應
	// }

	n := len(events)
	current := 0

	for current < n {
		select {
		case <-stopCh:
			goto done
		case <-ctx.Done():
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

			if s.OnRunRequest != nil {
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

func normalizeAutoTriggerROI(mode string, bang ROI, pjsk ROI) ROI {
	roi := bang
	if mode == "pjsk" {
		roi = pjsk
	}

	if roi.X1 == 0 && roi.Y1 == 0 && roi.X2 == 0 && roi.Y2 == 0 {
		if mode == "pjsk" {
			return ROI{X1: 0, Y1: 35, X2: 100, Y2: 60}
		}
		return ROI{X1: 14, Y1: 73, X2: 87, Y2: 80}
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

func normalizeNavSongROI(mode string, bang ROI, pjsk ROI) ROI {
	roi := bang
	if mode == "pjsk" {
		roi = pjsk
	}

	if roi.X1 == 0 && roi.Y1 == 0 && roi.X2 == 0 && roi.Y2 == 0 {
		if mode == "pjsk" {
			return ROI{X1: 59, Y1: 46, X2: 85, Y2: 52}
		}
		return ROI{X1: 23, Y1: 46, X2: 47, Y2: 50}
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

func (s *Server) handleVisionROI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	ctrl := s.controller
	req := s.lastRunReq
	s.mu.Unlock()

	sc, ok := ctrl.(*controllers.ScrcpyController)
	if !ok || sc == nil {
		http.Error(w, "vision preview unavailable", http.StatusServiceUnavailable)
		return
	}

	frame, ok := sc.LatestFrame()
	if !ok || frame.Width <= 0 || frame.Height <= 0 {
		http.Error(w, "no decoded frame", http.StatusServiceUnavailable)
		return
	}

	need := frame.Width * frame.Height
	if len(frame.Plane0) < need {
		http.Error(w, "invalid frame", http.StatusServiceUnavailable)
		return
	}

	roi := normalizeAutoTriggerROI(req.Mode, req.AutoTriggerROIBang, req.AutoTriggerROIPjsk)
	x1 := frame.Width * roi.X1 / 100
	x2 := frame.Width * roi.X2 / 100
	y1 := frame.Height * roi.Y1 / 100
	y2 := frame.Height * roi.Y2 / 100
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > frame.Width {
		x2 = frame.Width
	}
	if y2 > frame.Height {
		y2 = frame.Height
	}
	if x2 <= x1 || y2 <= y1 {
		http.Error(w, "invalid roi", http.StatusBadRequest)
		return
	}

	wid := x2 - x1
	hgt := y2 - y1
	img := image.NewGray(image.Rect(0, 0, wid, hgt))
	for y := 0; y < hgt; y++ {
		srcOff := (y1+y)*frame.Width + x1
		dstOff := y * img.Stride
		copy(img.Pix[dstOff:dstOff+wid], frame.Plane0[srcOff:srcOff+wid])
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if err := png.Encode(w, img); err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleNavROI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	ctrl := s.controller
	req := s.lastRunReq
	s.mu.Unlock()

	sc, ok := ctrl.(*controllers.ScrcpyController)
	if !ok || sc == nil {
		http.Error(w, "nav preview unavailable", http.StatusServiceUnavailable)
		return
	}

	frame, ok := sc.LatestFrame()
	if !ok || frame.Width <= 0 || frame.Height <= 0 {
		http.Error(w, "no decoded frame", http.StatusServiceUnavailable)
		return
	}

	need := frame.Width * frame.Height
	if len(frame.Plane0) < need {
		http.Error(w, "invalid frame", http.StatusServiceUnavailable)
		return
	}

	roi := normalizeNavSongROI(req.Mode, req.NavSongROIBang, req.NavSongROIPjsk)
	x1 := frame.Width * roi.X1 / 100
	x2 := frame.Width * roi.X2 / 100
	y1 := frame.Height * roi.Y1 / 100
	y2 := frame.Height * roi.Y2 / 100
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > frame.Width {
		x2 = frame.Width
	}
	if y2 > frame.Height {
		y2 = frame.Height
	}
	if x2 <= x1 || y2 <= y1 {
		http.Error(w, "invalid nav roi", http.StatusBadRequest)
		return
	}

	wid := x2 - x1
	hgt := y2 - y1
	img := image.NewGray(image.Rect(0, 0, wid, hgt))
	for y := 0; y < hgt; y++ {
		srcOff := (y1+y)*frame.Width + x1
		dstOff := y * img.Stride
		copy(img.Pix[dstOff:dstOff+wid], frame.Plane0[srcOff:srcOff+wid])
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if err := png.Encode(w, img); err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
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
	mux.HandleFunc("/api/vision-roi.png", s.handleVisionROI)
	mux.HandleFunc("/api/nav-roi.png", s.handleNavROI)
	mux.HandleFunc("/api/frame.png", s.handleFrame)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return "", err
	}

	addr := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	go http.Serve(ln, mux)
	return addr, nil
}
