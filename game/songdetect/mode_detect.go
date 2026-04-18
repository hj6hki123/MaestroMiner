package songdetect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type modeSongCandidate struct {
	id     int
	titles []string
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

func loadBangSongCandidates() ([]modeSongCandidate, error) {
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
	out := make([]modeSongCandidate, 0, len(songs))
	for sid, s := range songs {
		id, err := strconv.Atoi(sid)
		if err != nil {
			continue
		}
		titles := uniqueTitles(s.MusicTitle)
		if len(titles) == 0 {
			continue
		}
		out = append(out, modeSongCandidate{id: id, titles: titles})
	}
	return out, nil
}

func loadPJSKSongCandidates() ([]modeSongCandidate, error) {
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
	out := make([]modeSongCandidate, 0, len(songs))
	for _, s := range songs {
		if s.ID <= 0 {
			continue
		}
		titles := uniqueTitles([]string{s.Title, s.Pronunciation})
		if len(titles) == 0 {
			continue
		}
		out = append(out, modeSongCandidate{id: s.ID, titles: titles})
	}
	return out, nil
}

func loadSongCandidates(mode string) ([]modeSongCandidate, error) {
	if strings.EqualFold(mode, "pjsk") {
		return loadPJSKSongCandidates()
	}
	return loadBangSongCandidates()
}

func rankByModeTexts(texts []string, mode string) (bestSongID int, bestTitle string, bestScore int, bestSource string, top []MatchCandidate, err error) {
	if len(texts) == 0 {
		return 0, "", 0, "", nil, nil
	}

	candidates, err := loadSongCandidates(mode)
	if err != nil {
		return 0, "", 0, "", nil, err
	}
	if len(candidates) == 0 {
		return 0, "", 0, "", nil, nil
	}

	coreCandidates := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		coreCandidates = append(coreCandidates, Candidate{SongID: c.id, Titles: c.titles})
	}

	bestSongID, bestTitle, bestScore, bestSource, top = RankByTexts(texts, coreCandidates)
	return bestSongID, bestTitle, bestScore, bestSource, top, nil
}

// DetectByModeText matches a raw OCR text against mode-specific song data.
func DetectByModeText(raw, mode string) (int, string, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, "", false
	}
	id, title, score, _, _, ok := DetectByModeTextsDetailed([]string{raw}, mode)
	if !ok || score < DefaultScoreThreshold {
		return 0, "", false
	}
	return id, title, true
}

// DetectByModeTextsDetailed matches multiple OCR texts and returns ranked candidates.
func DetectByModeTextsDetailed(texts []string, mode string) (int, string, int, string, []MatchCandidate, bool) {
	bestID, bestTitle, bestScore, bestSource, top, err := rankByModeTexts(texts, mode)
	if err != nil {
		return 0, "", 0, "", nil, false
	}
	if bestScore >= DefaultScoreThreshold && bestID > 0 {
		return bestID, bestTitle, bestScore, bestSource, top, true
	}
	return 0, "", bestScore, bestSource, top, false
}

// DetectByModeTexts matches multiple OCR texts and returns the best hit.
func DetectByModeTexts(texts []string, mode string) (int, string, int, string, bool) {
	id, title, score, source, _, ok := DetectByModeTextsDetailed(texts, mode)
	return id, title, score, source, ok
}
