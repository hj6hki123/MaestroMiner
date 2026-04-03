// Copyright (C) 2024, 2025 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package gui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/kvarenzn/ssm/common"
	"github.com/kvarenzn/ssm/config"
	"github.com/kvarenzn/ssm/controllers"
)

//go:embed static
var staticFiles embed.FS

type PlayState int

const (
	StateIdle    PlayState = iota // 0 閒置
	StateReady                   // 1 就緒等待開始
	StatePlaying                 // 2 播放中
	StateDone                    // 3 播放完畢（保留資料，可重播）
	StateError                   // 4 錯誤
)

// NowPlaying 儲存目前載入的歌曲資訊，供前端顯示
type NowPlaying struct {
	SongID    int    `json:"songId"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Diff      string `json:"diff"`      // "expert"
	DiffLevel int    `json:"diffLevel"` // 數字難度，例如 29
	JacketURL string `json:"jacketUrl"` // CDN URL
	Mode      string `json:"mode"`      // "bang" | "pjsk"
}

// RunRequest 是前端 POST /api/run 送過來的 JSON
type RunRequest struct {
	Mode         string     `json:"mode"`
	Backend      string     `json:"backend"`
	Diff         string     `json:"diff"`
	Orient       string     `json:"orient"`
	SongID       int        `json:"songId"`
	ChartPath    string     `json:"chartPath"`
	DeviceSerial string     `json:"deviceSerial"`
	NowPlaying   NowPlaying `json:"nowPlaying"` // 前端帶過來的顯示資訊
}

type Server struct {
	port int
	conf *config.Config

	mu         sync.Mutex
	state      PlayState
	offset     int
	errMsg     string
	nowPlaying NowPlaying // 持久保存，中斷後依然可見

	// 播放控制（重播時會重新建立）
	startCh  chan struct{}
	offsetCh chan int
	stopCh   chan struct{}

	controller controllers.Controller
	events     []common.ViscousEventItem

	// SSE clients
	clientsMu sync.Mutex
	clients   map[chan string]struct{}

	// 注入的 callback
	OnRunRequest     func(req RunRequest)
	OnExtractRequest func(path string) error
}

func NewServer(port int, conf *config.Config) *Server {
	return &Server{
		port:    port,
		conf:    conf,
		state:   StateIdle,
		startCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}, 1),
		clients: make(map[chan string]struct{}),
	}
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
		"state":      int(s.state),
		"offset":     s.offset,
		"error":      s.errMsg,
		"nowPlaying": s.nowPlaying,
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
		"state":      int(s.state),
		"offset":     s.offset,
		"error":      s.errMsg,
		"nowPlaying": s.nowPlaying,
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
	if s.OnRunRequest != nil {
		s.OnRunRequest(req)
	}
	w.WriteHeader(http.StatusOK)
}

// handleStart — 按下「開始」
// 在 StateReady 或 StateDone 時都允許觸發（StateDone 表示可重播）
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()

	if st != StateReady && st != StateDone {
		http.Error(w, "not ready", http.StatusConflict)
		return
	}

	// StateDone → 重置為 Ready 再觸發（讓 Autoplay goroutine 重新跑）
	if st == StateDone {
		s.mu.Lock()
		s.state = StateReady
		// 清空舊 channel，建新的
		s.startCh = make(chan struct{}, 1)
		s.stopCh = make(chan struct{}, 1)
		s.offsetCh = make(chan int, 32)
		s.mu.Unlock()
		s.broadcastState()

		// 通知 main.go 重新跑一次 autoplay
		if s.OnRunRequest != nil {
			s.mu.Lock()
			// 用 nowPlaying 重建 RunRequest（不需要重新載入譜面，直接重播）
			// 這裡只是觸發信號，實際重播邏輯由 WaitForStart 控制
			s.mu.Unlock()
		}
	}

	select {
	case s.startCh <- struct{}{}:
	default:
	}
	w.WriteHeader(http.StatusOK)
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
	if s.offsetCh != nil {
		select {
		case s.offsetCh <- body.Delta:
		default:
		}
	}
	// 閒置時也更新 offset（讓下次播放生效）
	s.offset += body.Delta
	s.mu.Unlock()
	s.broadcastState()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	select {
	case s.stopCh <- struct{}{}:
	default:
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
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSongDB — proxy Bestdori / Sekai World，快取到本地
// GET /api/songdb?mode=bang|pjsk
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
		songs, err := fetchOrLoad("./sekai_songs.json", "https://sekai-world.github.io/sekai-master-db-en-diff/musics.json")
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, `{"songs":%s,"bands":{}}`, songs)
	}
}

func fetchOrLoad(localPath, url string) ([]byte, error) {
	if data, err := os.ReadFile(localPath); err == nil {
		return data, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	go os.WriteFile(localPath, data, 0o644)
	return data, nil
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
		http.Error(w, "需要提供 path 欄位", http.StatusBadRequest)
		return
	}
	if s.OnExtractRequest == nil {
		http.Error(w, "extract not configured", http.StatusInternalServerError)
		return
	}
	if err := s.OnExtractRequest(body.Path); err != nil {
		http.Error(w, "解包失敗："+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── 播放狀態控制 ───────────────────────────────

// SetReady 載入完成，設定為就緒狀態，並記錄 NowPlaying 資訊
func (s *Server) SetReady(ctrl controllers.Controller, events []common.ViscousEventItem, np NowPlaying) {
	s.mu.Lock()
	s.controller = ctrl
	s.events = events
	s.state = StateReady
	s.offset = 0
	s.errMsg = ""
	s.nowPlaying = np
	s.offsetCh = make(chan int, 32)
	s.startCh = make(chan struct{}, 1)
	s.stopCh = make(chan struct{}, 1)
	s.mu.Unlock()
	s.broadcastState()
}

func (s *Server) SetError(msg string) {
	s.mu.Lock()
	s.state = StateError
	s.errMsg = msg
	s.mu.Unlock()
	s.broadcastState()
}

// WaitForStart 阻塞直到前端按「開始」
func (s *Server) WaitForStart(ctx context.Context) bool {
	select {
	case <-s.startCh:
		s.mu.Lock()
		s.state = StatePlaying
		s.mu.Unlock()
		s.broadcastState()
		return true
	case <-ctx.Done():
		return false
	}
}

// Autoplay 播放主迴圈
func (s *Server) Autoplay(ctx context.Context, start time.Time) {
	s.mu.Lock()
	events := s.events
	offsetCh := s.offsetCh
	stopCh := s.stopCh
	s.mu.Unlock()

	n := len(events)
	current := 0

	for current < n {
		select {
		case <-ctx.Done():
			goto done
		case <-stopCh:
			goto done
		case delta := <-offsetCh:
			s.mu.Lock()
			s.offset += delta
			start = start.Add(time.Duration(-delta) * time.Millisecond)
			s.mu.Unlock()
			s.broadcastState()
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
			time.Sleep(time.Duration(remaining-5) * time.Millisecond)
		} else if remaining > 4 {
			time.Sleep(1 * time.Millisecond)
		}
	}

done:
	// StateDone：保留 nowPlaying / events / controller，可重播
	s.mu.Lock()
	s.state = StateDone
	s.mu.Unlock()
	s.broadcastState()
}

// ─── 啟動 ──────────────────────────────────────

func (s *Server) Start() (string, error) {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/offset", s.handleOffset)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/device", s.handleDevice)
	mux.HandleFunc("/api/extract", s.handleExtract)
	mux.HandleFunc("/api/songdb", s.handleSongDB)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return "", err
	}

	addr := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	go http.Serve(ln, mux)
	return addr, nil
}