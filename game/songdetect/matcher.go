package songdetect

import (
	"regexp"
	"sort"
	"strings"
)

const DefaultScoreThreshold = 68

var nonWordRegex = regexp.MustCompile(`[\s\p{P}\p{S}]+`)

// Candidate is a mode-specific song candidate with one or more titles.
type Candidate struct {
	SongID int
	Titles []string
}

// MatchCandidate is the best hit per song for debug display.
type MatchCandidate struct {
	SongID int    `json:"songId"`
	Title  string `json:"title"`
	Score  int    `json:"score"`
}

func normalizeSongText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = nonWordRegex.ReplaceAllString(s, "")
	return s
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

// RankByTexts computes the best song hit and top candidates across OCR texts.
func RankByTexts(texts []string, candidates []Candidate) (bestSongID int, bestTitle string, bestScore int, bestSource string, top []MatchCandidate) {
	if len(texts) == 0 || len(candidates) == 0 {
		return 0, "", 0, "", nil
	}

	topBySong := make(map[int]MatchCandidate)

	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, song := range candidates {
			maxScore := 0
			titleHit := ""
			for _, title := range song.Titles {
				sc := scoreTextMatch(text, title)
				if sc > maxScore {
					maxScore = sc
					titleHit = title
				}
			}

			if maxScore == 0 {
				continue
			}

			prev, exists := topBySong[song.SongID]
			if !exists || maxScore > prev.Score {
				topBySong[song.SongID] = MatchCandidate{SongID: song.SongID, Title: titleHit, Score: maxScore}
			}

			if maxScore > bestScore {
				bestScore = maxScore
				bestSongID = song.SongID
				bestTitle = titleHit
				bestSource = text
			}
		}
	}

	list := make([]MatchCandidate, 0, len(topBySong))
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

	return bestSongID, bestTitle, bestScore, bestSource, list
}

// DetectByTextsDetailed returns best match and top candidates using threshold.
func DetectByTextsDetailed(texts []string, candidates []Candidate, threshold int) (int, string, int, string, []MatchCandidate, bool) {
	if threshold <= 0 {
		threshold = DefaultScoreThreshold
	}
	bestID, bestTitle, bestScore, bestSource, top := RankByTexts(texts, candidates)
	if bestScore >= threshold && bestID > 0 {
		return bestID, bestTitle, bestScore, bestSource, top, true
	}
	return 0, "", bestScore, bestSource, top, false
}

// DetectByText is a convenience wrapper for a single OCR string.
func DetectByText(raw string, candidates []Candidate, threshold int) (int, string, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, "", false
	}
	id, title, _, _, _, ok := DetectByTextsDetailed([]string{raw}, candidates, threshold)
	if !ok {
		return 0, "", false
	}
	return id, title, true
}
