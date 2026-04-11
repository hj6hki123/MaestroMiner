package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	ocr "github.com/getcharzp/go-ocr"
	"github.com/kvarenzn/ssm/adb"
	xdraw "golang.org/x/image/draw"
)

type detectSongNameRequest struct {
	DeviceSerial string `json:"deviceSerial"`
	Mode         string `json:"mode"`
}

type SongDetectCandidate struct {
	SongID int    `json:"songId"`
	Title  string `json:"title"`
	Score  int    `json:"score"`
}

type detectSongNameResponse struct {
	Matched    bool                  `json:"matched"`
	SongID     int                   `json:"songId,omitempty"`
	Title      string                `json:"title,omitempty"`
	SourceText string                `json:"sourceText,omitempty"`
	Score      int                   `json:"score,omitempty"`
	Texts      []string              `json:"texts,omitempty"`
	Candidates []SongDetectCandidate `json:"candidates,omitempty"`
}

type songNameCandidate struct {
	id     int
	titles []string
}

type songDetectOCRClient struct {
	mu     sync.Mutex
	engine ocr.Engine
}

type songDetectOCRPaths struct {
	lib  string
	det  string
	rec  string
	dict string
}

var (
	nonWordRegex       = regexp.MustCompile(`[\s\p{P}\p{S}]+`)
	songOCRGlobal      *songDetectOCRClient
	songOCRGlobalMutex sync.Mutex
)

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if pathExists(p) {
			return p
		}
	}
	return ""
}

func defaultLibNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"onnxruntime.dll"}
	}
	if runtime.GOOS == "darwin" {
		return []string{"onnxruntime_" + runtime.GOARCH + ".dylib", "onnxruntime.dylib"}
	}
	return []string{"onnxruntime_" + runtime.GOARCH + ".so", "onnxruntime.so"}
}

func resolveSongDetectOCRPaths() (songDetectOCRPaths, error) {
	fromEnv := songDetectOCRPaths{
		lib:  strings.TrimSpace(os.Getenv("SSM_GO_OCR_LIB")),
		det:  strings.TrimSpace(os.Getenv("SSM_GO_OCR_DET")),
		rec:  strings.TrimSpace(os.Getenv("SSM_GO_OCR_REC")),
		dict: strings.TrimSpace(os.Getenv("SSM_GO_OCR_DICT")),
	}
	if fromEnv.lib == "" {
		fromEnv.lib = strings.TrimSpace(os.Getenv("SSM_OCR_ONNXRUNTIME_LIB"))
	}
	if fromEnv.det == "" {
		fromEnv.det = strings.TrimSpace(os.Getenv("SSM_OCR_DET_MODEL"))
	}
	if fromEnv.rec == "" {
		fromEnv.rec = strings.TrimSpace(os.Getenv("SSM_OCR_REC_MODEL"))
	}
	if fromEnv.dict == "" {
		fromEnv.dict = strings.TrimSpace(os.Getenv("SSM_OCR_DICT"))
	}
	if pathExists(fromEnv.lib) && pathExists(fromEnv.det) && pathExists(fromEnv.rec) && pathExists(fromEnv.dict) {
		return fromEnv, nil
	}

	roots := []string{".", "./go-ocr", "./models/go-ocr", "./ocr"}
	var libCandidates []string
	for _, root := range roots {
		for _, name := range defaultLibNames() {
			libCandidates = append(libCandidates, filepath.Join(root, "lib", name))
		}
	}

	paths := songDetectOCRPaths{
		lib: firstExisting(libCandidates...),
		det: firstExisting(
			"./paddle_weights/det.onnx",
			"./go-ocr/paddle_weights/det.onnx",
			"./models/go-ocr/paddle_weights/det.onnx",
			"./ocr/paddle_weights/det.onnx",
		),
		rec: firstExisting(
			"./paddle_weights/rec.onnx",
			"./go-ocr/paddle_weights/rec.onnx",
			"./models/go-ocr/paddle_weights/rec.onnx",
			"./ocr/paddle_weights/rec.onnx",
		),
		dict: firstExisting(
			"./paddle_weights/dict.txt",
			"./go-ocr/paddle_weights/dict.txt",
			"./models/go-ocr/paddle_weights/dict.txt",
			"./ocr/paddle_weights/dict.txt",
		),
	}

	if pathExists(paths.lib) && pathExists(paths.det) && pathExists(paths.rec) && pathExists(paths.dict) {
		return paths, nil
	}

	return songDetectOCRPaths{}, fmt.Errorf("go-ocr models not found; set env SSM_GO_OCR_LIB/DET/REC/DICT or place files under go-ocr/paddle_weights and go-ocr/lib")
}

func getSongDetectOCRClient() (*songDetectOCRClient, error) {
	songOCRGlobalMutex.Lock()
	defer songOCRGlobalMutex.Unlock()

	if songOCRGlobal != nil {
		return songOCRGlobal, nil
	}

	paths, err := resolveSongDetectOCRPaths()
	if err != nil {
		return nil, err
	}
	eng, err := ocr.NewPaddleOcrEngine(ocr.Config{
		OnnxRuntimeLibPath: paths.lib,
		DetModelPath:       paths.det,
		RecModelPath:       paths.rec,
		DictPath:           paths.dict,
	})
	if err != nil {
		return nil, fmt.Errorf("init go-ocr engine: %w", err)
	}

	songOCRGlobal = &songDetectOCRClient{engine: eng}
	return songOCRGlobal, nil
}

func defaultSongNameROI(mode string) [4]float64 {
	if strings.EqualFold(mode, "pjsk") {
		return [4]float64{0.59, 0.46, 0.85, 0.52}
	}
	return [4]float64{0.23, 0.46, 0.47, 0.50}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func ensureMinimumROISize(roi [4]float64, minW, minH float64) [4]float64 {
	x1 := clamp01(roi[0])
	y1 := clamp01(roi[1])
	x2 := clamp01(roi[2])
	y2 := clamp01(roi[3])
	if x2 <= x1 {
		x1, x2 = 0, 1
	}
	if y2 <= y1 {
		y1, y2 = 0, 1
	}

	w := x2 - x1
	h := y2 - y1
	cx := (x1 + x2) * 0.5
	cy := (y1 + y2) * 0.5

	if w < minW {
		half := minW * 0.5
		x1 = cx - half
		x2 = cx + half
	}
	if h < minH {
		half := minH * 0.5
		y1 = cy - half
		y2 = cy + half
	}

	if x1 < 0 {
		x2 -= x1
		x1 = 0
	}
	if y1 < 0 {
		y2 -= y1
		y1 = 0
	}
	if x2 > 1 {
		x1 -= x2 - 1
		x2 = 1
	}
	if y2 > 1 {
		y1 -= y2 - 1
		y2 = 1
	}

	x1 = clamp01(x1)
	y1 = clamp01(y1)
	x2 = clamp01(x2)
	y2 = clamp01(y2)

	if x2 <= x1 {
		x1, x2 = 0, 1
	}
	if y2 <= y1 {
		y1, y2 = 0, 1
	}

	return [4]float64{x1, y1, x2, y2}
}

func isCropEmptyError(err error) bool {
	if err == nil {
		return false
	}
	es := strings.ToLower(err.Error())
	return strings.Contains(es, "crop rectangle is empty") || strings.Contains(es, "裁切框失败")
}

func ensureOCRImageSize(src image.Image, minW, minH int) image.Image {
	if src == nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}

	scale := 1.0
	if w < minW {
		scale = math.Max(scale, float64(minW)/float64(w))
	}
	if h < minH {
		scale = math.Max(scale, float64(minH)/float64(h))
	}
	if scale <= 1.01 {
		return src
	}
	if scale > 4.0 {
		scale = 4.0
	}

	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw <= 0 || nh <= 0 {
		return src
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

func padImageWhite(src image.Image, minW, minH, border int) image.Image {
	if src == nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	if border < 0 {
		border = 0
	}

	tw := w + border*2
	th := h + border*2
	if tw < minW {
		tw = minW
	}
	if th < minH {
		th = minH
	}
	if tw <= w && th <= h {
		return src
	}

	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	imagedraw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, imagedraw.Src)
	offX := (tw - w) / 2
	offY := (th - h) / 2
	drawRect := image.Rect(offX, offY, offX+w, offY+h)
	imagedraw.Draw(dst, drawRect, src, b.Min, imagedraw.Src)
	return dst
}

func roiRetryCandidates(base [4]float64) [][4]float64 {
	// Keep strictly to user ROI; retries should pad white instead of widening capture area.
	base = ensureMinimumROISize(base, 0.01, 0.01)
	return [][4]float64{base}
}

func expandROI(roi [4]float64, marginX, marginY float64) [4]float64 {
	out := [4]float64{
		clamp01(roi[0] - marginX),
		clamp01(roi[1] - marginY),
		clamp01(roi[2] + marginX),
		clamp01(roi[3] + marginY),
	}
	if out[2] <= out[0] {
		out[0], out[2] = 0, 1
	}
	if out[3] <= out[1] {
		out[1], out[3] = 0, 1
	}
	return out
}

func cropByROI(src image.Image, roi *[4]float64) image.Image {
	if src == nil || roi == nil {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 1 || h <= 1 {
		return src
	}
	x1 := int(clamp01(roi[0]) * float64(w))
	y1 := int(clamp01(roi[1]) * float64(h))
	x2 := int(clamp01(roi[2]) * float64(w))
	y2 := int(clamp01(roi[3]) * float64(h))
	if x2 <= x1 || y2 <= y1 {
		return src
	}
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > w {
		x2 = w
	}
	if y2 > h {
		y2 = h
	}
	if x2 <= x1 || y2 <= y1 {
		return src
	}
	rect := image.Rect(b.Min.X+x1, b.Min.Y+y1, b.Min.X+x2, b.Min.Y+y2)
	if sub, ok := src.(interface {
		SubImage(r image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(rect)
	}
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	imagedraw.Draw(out, out.Bounds(), src, rect.Min, imagedraw.Src)
	return out
}

func (c *songDetectOCRClient) OCR(pngBytes []byte, roi *[4]float64) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.engine == nil {
		return nil, fmt.Errorf("go-ocr engine not initialized")
	}
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}

	runOCRForROI := func(rr *[4]float64) ([]string, error) {
		baseRegion := cropByROI(img, rr)
		attempts := []struct {
			minW   int
			minH   int
			border int
		}{
			{minW: 0, minH: 0, border: 0},
			{minW: 160, minH: 64, border: 8},
			{minW: 224, minH: 96, border: 12},
			{minW: 288, minH: 120, border: 18},
		}

		var lastErr error
		for i, attempt := range attempts {
			region := padImageWhite(baseRegion, attempt.minW, attempt.minH, attempt.border)
			region = ensureOCRImageSize(region, attempt.minW, attempt.minH)
			rawResults, runErr := c.engine.RunOCR(region)
			if runErr != nil {
				lastErr = runErr
				if !isCropEmptyError(runErr) || i == len(attempts)-1 {
					return nil, runErr
				}
				continue
			}

			seen := make(map[string]struct{}, len(rawResults))
			texts := make([]string, 0, len(rawResults))
			for _, r := range rawResults {
				t := strings.TrimSpace(r.Text)
				if t == "" {
					continue
				}
				if _, ok := seen[t]; ok {
					continue
				}
				seen[t] = struct{}{}
				texts = append(texts, t)
			}
			return texts, nil
		}
		return nil, lastErr
	}

	var texts []string
	if roi == nil {
		texts, err = runOCRForROI(nil)
	} else {
		candidates := roiRetryCandidates(*roi)
		for i, cand := range candidates {
			candCopy := cand
			texts, err = runOCRForROI(&candCopy)
			if err == nil {
				break
			}
			if !isCropEmptyError(err) {
				break
			}
			if i == len(candidates)-1 {
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("go-ocr run: %w", err)
	}
	return texts, nil
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

func rankSongCandidatesByTexts(texts []string, mode string) (bestSongID int, bestTitle string, bestScore int, bestSource string, top []SongDetectCandidate, err error) {
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

	topBySong := make(map[int]SongDetectCandidate)

	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		for _, song := range candidates {
			maxScore := 0
			titleHit := ""
			for _, title := range song.titles {
				sc := scoreTextMatch(text, title)
				if sc > maxScore {
					maxScore = sc
					titleHit = title
				}
			}

			if maxScore == 0 {
				continue
			}

			prev, exists := topBySong[song.id]
			if !exists || maxScore > prev.Score {
				topBySong[song.id] = SongDetectCandidate{SongID: song.id, Title: titleHit, Score: maxScore}
			}

			if maxScore > bestScore {
				bestScore = maxScore
				bestSongID = song.id
				bestTitle = titleHit
				bestSource = text
			}
		}
	}

	list := make([]SongDetectCandidate, 0, len(topBySong))
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

	return bestSongID, bestTitle, bestScore, bestSource, list, nil
}

// DetectSongByText matches a raw text string (e.g. from OCR) against the song
// database for the given mode. Returns (songID, title, true) on success.
func DetectSongByText(raw, mode string) (int, string, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, "", false
	}
	id, title, score, _, _, ok := DetectSongByTextsDetailed([]string{raw}, mode)
	if !ok || score < 68 {
		return 0, "", false
	}
	return id, title, true
}

// DetectSongByTextsDetailed matches multiple raw OCR texts and also returns top
// scoring candidates for diagnostics.
func DetectSongByTextsDetailed(texts []string, mode string) (int, string, int, string, []SongDetectCandidate, bool) {
	bestID, bestTitle, bestScore, bestSource, top, err := rankSongCandidatesByTexts(texts, mode)
	if err != nil {
		return 0, "", 0, "", nil, false
	}
	if bestScore >= 68 && bestID > 0 {
		return bestID, bestTitle, bestScore, bestSource, top, true
	}
	return 0, "", bestScore, bestSource, top, false
}

// DetectSongByTexts matches multiple raw OCR texts against the song database,
// returning the best scoring hit across all candidate texts.
// Returns (songID, title, score, sourceText, true) on success.
func DetectSongByTexts(texts []string, mode string) (int, string, int, string, bool) {
	id, title, score, source, _, ok := DetectSongByTextsDetailed(texts, mode)
	return id, title, score, source, ok
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

	pngBytes, err := device.ScreencapPNGBytes()
	if err != nil {
		http.Error(w, "screencap failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	ocrClient, err := getSongDetectOCRClient()
	if err != nil {
		http.Error(w, "go-ocr unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	roi := defaultSongNameROI(req.Mode)
	texts, err := ocrClient.OCR(pngBytes, &roi)
	if err != nil {
		http.Error(w, "go-ocr failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if len(texts) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(detectSongNameResponse{Matched: false})
		return
	}

	bestID, bestTitle, bestScore, bestSource, top, err := rankSongCandidatesByTexts(texts, req.Mode)
	if err != nil {
		http.Error(w, "load song db failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	best := detectSongNameResponse{
		Matched:    bestScore >= 80 && bestID > 0,
		SongID:     bestID,
		Title:      bestTitle,
		SourceText: bestSource,
		Score:      bestScore,
		Texts:      firstN(texts, 8),
		Candidates: top,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(best)
}
