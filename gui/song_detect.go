package gui

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kvarenzn/ssm/adb"
)

type detectSongNameRequest struct {
	DeviceSerial string `json:"deviceSerial"`
	Mode         string `json:"mode"`
}

type detectSongNameCandidate struct {
	SongID int    `json:"songId"`
	Title  string `json:"title"`
	Score  int    `json:"score"`
}

type detectSongNameResponse struct {
	Matched    bool                      `json:"matched"`
	SongID     int                       `json:"songId,omitempty"`
	Title      string                    `json:"title,omitempty"`
	SourceText string                    `json:"sourceText,omitempty"`
	Score      int                       `json:"score,omitempty"`
	Texts      []string                  `json:"texts,omitempty"`
	Candidates []detectSongNameCandidate `json:"candidates,omitempty"`
}

type songNameCandidate struct {
	id     int
	titles []string
}

var (
	uiTextRegex      = regexp.MustCompile(`\btext="([^"]+)"`)
	nonWordRegex     = regexp.MustCompile(`[\s\p{P}\p{S}]+`)
	nonAlnumHanRegex = regexp.MustCompile(`[^\p{L}\p{N}\p{Han}\p{Hiragana}\p{Katakana}]`)
)

func normalizeSongText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = nonWordRegex.ReplaceAllString(s, "")
	return s
}

func textLooksUseful(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || len([]rune(t)) < 2 {
		return false
	}
	cleaned := nonAlnumHanRegex.ReplaceAllString(t, "")
	if len([]rune(cleaned)) < 2 {
		return false
	}
	if strings.HasPrefix(t, "http") || strings.Contains(t, "com.") {
		return false
	}
	return true
}

func extractUITexts(xmlText string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 64)
	matches := uiTextRegex.FindAllStringSubmatch(xmlText, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		t := html.UnescapeString(strings.TrimSpace(m[1]))
		if !textLooksUseful(t) {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func scoreTextMatch(query, title string) int {
	q := normalizeSongText(query)
	t := normalizeSongText(title)
	if q == "" || t == "" {
		return 0
	}
	if q == t {
		return 100
	}
	if strings.Contains(q, t) || strings.Contains(t, q) {
		return 88
	}
	if strings.HasPrefix(q, t) || strings.HasPrefix(t, q) {
		return 80
	}
	minLen := len([]rune(q))
	if lt := len([]rune(t)); lt < minLen {
		minLen = lt
	}
	if minLen <= 2 {
		return 0
	}
	common := 0
	for _, r := range t {
		if strings.ContainsRune(q, r) {
			common++
		}
	}
	ratio := float64(common) / float64(minLen)
	if ratio >= 0.95 {
		return 75
	}
	if ratio >= 0.85 {
		return 68
	}
	return 0
}

func uniqueTitles(titles []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(titles))
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func loadBangSongCandidates() ([]songNameCandidate, error) {
	data, err := fetchOrLoad("./all.5.json", "https://bestdori.com/api/songs/all.5.json")
	if err != nil {
		return nil, err
	}
	type song struct {
		MusicTitle []string `json:"musicTitle"`
	}
	var songs map[string]song
	if err := json.Unmarshal(data, &songs); err != nil {
		return nil, err
	}
	out := make([]songNameCandidate, 0, len(songs))
	for sid, s := range songs {
		id, err := strconv.Atoi(sid)
		if err != nil {
			continue
		}
		titles := uniqueTitles(s.MusicTitle)
		if len(titles) == 0 {
			continue
		}
		out = append(out, songNameCandidate{id: id, titles: titles})
	}
	return out, nil
}

func loadPJSKSongCandidates() ([]songNameCandidate, error) {
	data, err := fetchOrLoad("./sekai_master_db_diff_musics.json", "https://raw.githubusercontent.com/Sekai-World/sekai-master-db-diff/main/musics.json")
	if err != nil {
		return nil, err
	}
	type song struct {
		ID            int    `json:"id"`
		Title         string `json:"title"`
		Pronunciation string `json:"pronunciation"`
	}
	var songs []song
	if err := json.Unmarshal(data, &songs); err != nil {
		return nil, err
	}
	out := make([]songNameCandidate, 0, len(songs))
	for _, s := range songs {
		if s.ID <= 0 {
			continue
		}
		titles := uniqueTitles([]string{s.Title, s.Pronunciation})
		if len(titles) == 0 {
			continue
		}
		out = append(out, songNameCandidate{id: s.ID, titles: titles})
	}
	return out, nil
}

func loadSongCandidates(mode string) ([]songNameCandidate, error) {
	if strings.EqualFold(mode, "pjsk") {
		return loadPJSKSongCandidates()
	}
	return loadBangSongCandidates()
}

func firstN(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func pickDevice(serial string) (*adb.Device, error) {
	client := adb.NewDefaultClient()
	devices, err := client.Devices()
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no adb devices")
	}

	serial = strings.TrimSpace(serial)
	if serial != "" {
		for _, d := range devices {
			if d.Serial() == serial && d.Authorized() {
				return d, nil
			}
		}
		return nil, fmt.Errorf("device %s not found or unauthorized", serial)
	}

	if d := adb.FirstAuthorizedDevice(devices); d != nil {
		return d, nil
	}
	return nil, fmt.Errorf("no authorized adb device")
}

func (s *Server) handleDetectSongName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req detectSongNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	device, err := pickDevice(req.DeviceSerial)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if _, err := device.RawSh("uiautomator", "dump", "/sdcard/ssm_ui.xml"); err != nil {
		http.Error(w, "uiautomator dump failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	xmlBytes, err := device.RawSh("cat", "/sdcard/ssm_ui.xml")
	if err != nil {
		http.Error(w, "read ui dump failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	texts := extractUITexts(string(xmlBytes))
	if len(texts) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(detectSongNameResponse{Matched: false})
		return
	}

	candidates, err := loadSongCandidates(req.Mode)
	if err != nil {
		http.Error(w, "load song db failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	topBySong := make(map[int]detectSongNameCandidate)
	best := detectSongNameResponse{Matched: false, Texts: firstN(texts, 8)}

	for _, text := range texts {
		for _, song := range candidates {
			maxScore := 0
			titleHit := ""
			for _, title := range song.titles {
				score := scoreTextMatch(text, title)
				if score > maxScore {
					maxScore = score
					titleHit = title
				}
			}

			if maxScore == 0 {
				continue
			}

			prev, exists := topBySong[song.id]
			if !exists || maxScore > prev.Score {
				topBySong[song.id] = detectSongNameCandidate{SongID: song.id, Title: titleHit, Score: maxScore}
			}

			if maxScore > best.Score {
				best.Score = maxScore
				best.SongID = song.id
				best.Title = titleHit
				best.SourceText = text
			}
		}
	}

	list := make([]detectSongNameCandidate, 0, len(topBySong))
	for _, c := range topBySong {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Score == list[j].Score {
			return list[i].SongID < list[j].SongID
		}
		return list[i].Score > list[j].Score
	})
	if len(list) > 5 {
		list = list[:5]
	}
	best.Candidates = list
	best.Matched = best.Score >= 80 && best.SongID > 0

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(best)
}
