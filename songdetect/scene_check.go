package songdetect

import (
	"fmt"
	"strings"
	"unicode"
)

func normalizeSceneText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			continue
		}
		switch r {
		case '樂':
			r = '楽'
		case '擇':
			r = '択'
		case '选':
			r = '選'
		}
		b.WriteRune(r)
	}

	return b.String()
}

// NormalizeSceneTexts normalizes OCR outputs for scene/title checks.
func NormalizeSceneTexts(texts []string) []string {
	normalized := make([]string, 0, len(texts))
	for _, raw := range texts {
		t := normalizeSceneText(raw)
		if t != "" {
			normalized = append(normalized, t)
		}
	}
	return normalized
}

func runeCount(s string) int {
	return len([]rune(s))
}

func levenshteinDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	la := len(ar)
	lb := len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			ins := curr[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			v := ins
			if del < v {
				v = del
			}
			if sub < v {
				v = sub
			}
			curr[j] = v
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

func similarityScore(a, b string) float64 {
	a = normalizeSceneText(a)
	b = normalizeSceneText(b)
	if a == "" || b == "" {
		return 0
	}
	maxLen := runeCount(a)
	if lb := runeCount(b); lb > maxLen {
		maxLen = lb
	}
	if maxLen == 0 {
		return 0
	}
	d := levenshteinDistance(a, b)
	score := 1 - float64(d)/float64(maxLen)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func fuzzyKeywordScore(text, keyword string) float64 {
	text = normalizeSceneText(text)
	keyword = normalizeSceneText(keyword)
	if text == "" || keyword == "" {
		return 0
	}
	if strings.Contains(text, keyword) {
		return 1
	}

	best := similarityScore(text, keyword)

	if strings.Contains(keyword, text) && runeCount(text) >= 2 {
		ratio := float64(runeCount(text)) / float64(runeCount(keyword))
		if ratio > best {
			best = ratio
		}
	}

	textRunes := []rune(text)
	kwLen := runeCount(keyword)
	if len(textRunes) > 0 && kwLen > 0 {
		windowMin := kwLen - 2
		if windowMin < 2 {
			windowMin = 2
		}
		windowMax := kwLen + 2
		if windowMax > len(textRunes) {
			windowMax = len(textRunes)
		}
		for w := windowMin; w <= windowMax; w++ {
			for i := 0; i+w <= len(textRunes); i++ {
				s := string(textRunes[i : i+w])
				if sc := similarityScore(s, keyword); sc > best {
					best = sc
				}
			}
		}
	}

	if best < 0 {
		return 0
	}
	if best > 1 {
		return 1
	}
	return best
}

// FuzzyKeywordScore exposes OCR keyword fuzzy scoring for callers outside songdetect.
func FuzzyKeywordScore(text, keyword string) float64 {
	return fuzzyKeywordScore(text, keyword)
}

func bestKeywordScore(texts []string, keywords []string) float64 {
	best := 0.0
	for _, t := range texts {
		for _, kw := range keywords {
			if sc := fuzzyKeywordScore(t, kw); sc > best {
				best = sc
			}
		}
	}
	return best
}

// SongSelectTitleScore scores whether OCR texts look like the song-select screen.
func SongSelectTitleScore(texts []string) float64 {
	normalized := NormalizeSceneTexts(texts)
	if len(normalized) == 0 {
		return 0
	}

	combined := strings.Join(normalized, "")
	candidates := make([]string, 0, len(normalized)+1)
	candidates = append(candidates, normalized...)
	if combined != "" {
		candidates = append(candidates, combined)
	}

	fullTitleScore := bestKeywordScore(candidates, []string{"楽曲選択", "songselect", "selectsong"})
	songConceptScore := bestKeywordScore(candidates, []string{"楽曲", "song", "music"})
	selectConceptScore := bestKeywordScore(candidates, []string{"選択", "select", "choice"})

	conceptScore := songConceptScore
	if selectConceptScore < conceptScore {
		conceptScore = selectConceptScore
	}
	if fullTitleScore > conceptScore {
		return fullTitleScore
	}
	return conceptScore
}

// IsSongSelectTitle checks if OCR texts pass the scene-title threshold.
func IsSongSelectTitle(texts []string) bool {
	return SongSelectTitleScore(texts) >= 0.50
}

// FirstNStrings returns up to n elements from the provided string slice.
func FirstNStrings(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

// FormatMatchCandidates renders top candidates for concise logging/debug output.
func FormatMatchCandidates(cands []MatchCandidate, n int) string {
	if len(cands) == 0 || n <= 0 {
		return ""
	}
	if len(cands) > n {
		cands = cands[:n]
	}
	parts := make([]string, 0, len(cands))
	for _, c := range cands {
		parts = append(parts, fmt.Sprintf("#%d %s(%d)", c.SongID, c.Title, c.Score))
	}
	return strings.Join(parts, " | ")
}
