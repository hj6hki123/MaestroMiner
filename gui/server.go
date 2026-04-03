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
	StateReady                    // 1 就緒等待開始
	StatePlaying                  // 2 播放中
	StateDone                     // 3 播放完畢
	StateError                    // 4 錯誤
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
}

type Server struct {
	port int
	conf *config.Config

	mu         sync.Mutex
	state      PlayState
	offset     int
	errMsg     string
	nowPlaying NowPlaying
	lastRunReq RunRequest // 儲存最後一次的請求，供中斷重打使用

	startCh  chan struct{}
	offsetCh chan int
	stopCh   chan struct{} // close() 此 channel 來停止播放

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
	// 儲存這次請求，供中斷後重打使用
	s.mu.Lock()
	s.lastRunReq = req
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
	s.mu.Lock()
	st := s.state
	ch := s.startCh
	s.mu.Unlock()

	if st != StateReady {
		http.Error(w, "not ready", http.StatusConflict)
		return
	}
	select {
	case ch <- struct{}{}:
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

	if st != StatePlaying {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 1. 關閉 stopCh，讓 autoplayInner 立刻退出（sleep 也會中斷）
	s.mu.Lock()
	oldStop := s.stopCh
	s.mu.Unlock()
	select {
	case <-oldStop:
		// 已關閉，不重複
	default:
		close(oldStop)
	}

	// 2. 先把狀態設成 Idle，讓前端知道正在重新初始化
	s.mu.Lock()
	s.state = StateIdle
	s.mu.Unlock()
	s.broadcastState()

	// 3. 重新觸發 OnRunRequest（用相同的歌曲資料 + 重新建連線）
	//    main.go 的 goroutine 會跑完整的連線流程，最後呼叫 SetReady 回到 Ready 狀態
	if s.OnRunRequest != nil {
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

func (s *Server) SetReady(ctrl controllers.Controller, events []common.ViscousEventItem, np NowPlaying) {
	s.mu.Lock()
	// 關閉舊 stopCh，中斷任何殘留的等待
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
	s.broadcastState()
}

func (s *Server) SetError(msg string) {
	s.mu.Lock()
	s.state = StateError
	s.errMsg = msg
	s.mu.Unlock()
	s.broadcastState()
}

// WaitForStart 阻塞直到前端按「開始」或被取消
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

// Autoplay 播放主迴圈
func (s *Server) Autoplay(ctx context.Context, start time.Time) {
	s.mu.Lock()
	stopCh := s.stopCh
	events := s.events
	offsetCh := s.offsetCh
	s.mu.Unlock()

	n := len(events)
	current := 0

	for current < n {
		// 停止或換歌
		select {
		case <-stopCh:
			goto done
		case <-ctx.Done():
			goto done
		default:
		}

		// offset 調整：只移動播放時間基準點
		// s.offset 已由 handleOffset 更新，這裡不重複加
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

		// sleep 期間也能響應 stop/ctx
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
	// 正常播完才設 Done；被 stop/ctx 中斷時狀態已由外部設好，不覆蓋
	if s.state == StatePlaying {
		s.state = StateDone
		s.mu.Unlock()
		s.broadcastState()
	} else {
		s.mu.Unlock()
	}
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
