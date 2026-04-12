// Copyright (C) 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

// Package maacontrol implements pre-game navigation using MaaFramework.
//
// The navigation pipeline is defined declaratively in
// maacontrol/resource/pipeline/*.json (main/start/live/common). Complex steps
// (song OCR, live-mode checks) are wired as custom recognitions/actions so the
// JSON remains a clean, editable state-machine skeleton.
//
// OCR switching: pass a different ResourceDir that points at a resource
// bundle containing different model/ocr/* assets.
package maacontrol

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/adb"

	"github.com/kvarenzn/ssm/log"
	"github.com/kvarenzn/ssm/songdetect"
)

// ─────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────

// ROI is a normalised region of interest [x1, y1, x2, y2] in [0, 1].
type ROI [4]float64

// SongDetectCandidate is one ranked song match candidate.
type SongDetectCandidate struct {
	SongID int
	Title  string
	Score  int
}

// SongDetectResult is the unified output structure for song detection.
// Both pipeline SongRecognition and GUI AutoDetectSong share this format.
type SongDetectResult struct {
	Mode                string
	TitleTexts          []string
	TitleNormalizedText []string
	SongTexts           []string
	TitleScore          float64
	OnSongSelectScreen  bool
	SongID              int
	SongTitle           string
	SongScore           int
	SourceText          string
	TopCandidates       []SongDetectCandidate
}

func copyStrings(items []string) []string {
	return append([]string(nil), items...)
}

func copySongCandidates(items []SongDetectCandidate) []SongDetectCandidate {
	return append([]SongDetectCandidate(nil), items...)
}

func copySongDetectResult(in SongDetectResult) SongDetectResult {
	in.TitleTexts = copyStrings(in.TitleTexts)
	in.TitleNormalizedText = copyStrings(in.TitleNormalizedText)
	in.SongTexts = copyStrings(in.SongTexts)
	in.TopCandidates = copySongCandidates(in.TopCandidates)
	return in
}

func (r SongDetectResult) SongTextsPreview(n int) []string {
	if n <= 0 || len(r.SongTexts) == 0 {
		return nil
	}
	if len(r.SongTexts) <= n {
		return copyStrings(r.SongTexts)
	}
	return copyStrings(r.SongTexts[:n])
}

func (r SongDetectResult) TopSummary(n int) string {
	if n <= 0 || len(r.TopCandidates) == 0 {
		return ""
	}
	if len(r.TopCandidates) > n {
		r.TopCandidates = r.TopCandidates[:n]
	}
	parts := make([]string, 0, len(r.TopCandidates))
	for _, c := range r.TopCandidates {
		parts = append(parts, fmt.Sprintf("#%d %s(%d)", c.SongID, c.Title, c.Score))
	}
	return strings.Join(parts, " | ")
}

// ResolveSong validates scene/match status and returns the final song id/title.
func (r SongDetectResult) ResolveSong() (int, string, error) {
	if !r.OnSongSelectScreen {
		return 0, "", fmt.Errorf("not on 楽曲選択 screen (score=%.2f, title OCR=%v). If needed, adjust SCREEN_CHECK title ROI in ROI Box Tool.", r.TitleScore, r.TitleTexts)
	}
	if r.SongID > 0 {
		return r.SongID, r.SongTitle, nil
	}
	errMsg := fmt.Sprintf("failed (OCR texts: %v", r.SongTexts)
	if r.SongScore > 0 {
		errMsg = fmt.Sprintf("%s, bestScore=%d", errMsg, r.SongScore)
	}
	if top := r.TopSummary(3); top != "" {
		errMsg = fmt.Sprintf("%s, top=%s", errMsg, top)
	}
	errMsg = fmt.Sprintf("%s). Keep the song selected on 楽曲選択 and retry, or set Song ID manually.", errMsg)
	return 0, "", fmt.Errorf("%s", errMsg)
}

// NavConfig is the complete configuration for a single navigation run.
// Callers fill this in from the global ROI variables and pass it to NewNavigator.
type NavConfig struct {
	// Game mode: "bang" or "pjsk".
	Mode string
	// Target difficulty: "easy" | "normal" | "hard" | "expert" |
	//   "special" | "master" | "append".  Empty = skip difficulty tap.
	Difficulty string
	// Minimum acceptable liveboost before starting a live.
	// Values <= 0 are treated as 1.
	MinLiveBoost int

	// ADB connection
	AdbPath   string // path to the adb binary; empty = search PATH
	AdbSerial string // device serial (e.g. "127.0.0.1:16384")

	// ResourceDir is the root of the MAA resource bundle.
	// Defaults to "./maacontrol/resource" when empty.
	ResourceDir string

	// MaaLibDir is the directory that contains the MaaFramework native
	// libraries (.dll / .so / .dylib).  Empty = use PATH or CWD.
	MaaLibDir string

	// Normalised ROIs for each UI element (caller supplies mode-correct values).
	KetteiROI      ROI
	LiveStartROI   ROI
	BandConfirmROI ROI // PJSK-specific band-confirm tap
	DialogOKROI    ROI
	DialogTitleROI ROI // used for luma-based dialog detection
	SongTitleROI   ROI // used for SCREEN_CHECK title OCR
	SongNameROI    ROI // used for SONG_DETECT name OCR

	// DifficultyTapFn returns the device-pixel tap point for difficulty
	// selection given the screenshot dimensions.
	// Returns ok=false when the difficulty does not need tapping.
	DifficultyTapFn func(mode, diff string, w, h int) (x, y int, ok bool)

	// PageArrowFn returns the pixel tap point for the PJSK difficulty
	// page-flip arrow (used when difficulty == "append").
	PageArrowFn func(w, h int) (x, y int)

	// NodeROIs maps pipeline node names to normalised [x, y, w, h] ROIs
	// (each value in [0.0, 1.0]).  At run time these are converted to absolute
	// pixel coordinates using the actual screencap dimensions and injected as
	// a pipeline override so the JSON files stay resolution-independent.
	NodeROIs map[string][4]float64

	// OnProgress is called on every significant navigation stage change.
	// May be nil.
	OnProgress func(stage, scene, msg string)
}

// ─────────────────────────────────────────────
// Navigator
// ─────────────────────────────────────────────

// Navigator drives pre-game navigation via MaaFramework.
type Navigator struct {
	cfg    NavConfig
	ctrl   *maa.Controller
	res    *maa.Resource
	tasker *maa.Tasker

	// mutable state read by custom recognitions / actions
	mu             sync.RWMutex
	mode           string
	diff           string
	lastSongDetect SongDetectResult
	lastLiveBoost  int
}

// NewNavigator creates a Navigator, connects the MAA ADB controller and loads
// the pipeline resource bundle.  Call Destroy() when done.
func NewNavigator(cfg NavConfig) (*Navigator, error) {
	log.Infof("[NewNavigator] step 1: ensureMaaInit libDir=%q", cfg.MaaLibDir)
	if err := ensureMaaInit(cfg.MaaLibDir); err != nil {
		return nil, fmt.Errorf("maa init: %w", err)
	}
	// Init toolkit so MAA reads config/maa_option.json (enables save_draw etc.)
	if err := maa.ConfigInitOption("./", "{}"); err != nil {
		log.Warnf("[NewNavigator] ConfigInitOption: %v", err)
	}

	if cfg.ResourceDir == "" {
		cfg.ResourceDir = "./maacontrol/resource"
	}
	if cfg.MinLiveBoost <= 0 {
		cfg.MinLiveBoost = 1
	}

	adbPath := cfg.AdbPath
	if adbPath == "" {
		adbPath = "adb"
	}

	log.Infof("[NewNavigator] step 2: NewAdbController serial=%q", cfg.AdbSerial)
	ctrl, err := maa.NewAdbController(
		adbPath,
		cfg.AdbSerial,
		adb.ScreencapDefault,
		adb.InputAdbShell, // simple, no extra binary required
		"{}", "",
	)
	if err != nil {
		return nil, fmt.Errorf("maa adb controller: %w", err)
	}
	log.Infof("[NewNavigator] step 3: PostConnect")
	if !ctrl.PostConnect().Wait().Success() {
		ctrl.Destroy()
		return nil, fmt.Errorf("maa: connect to %q failed", cfg.AdbSerial)
	}

	log.Infof("[NewNavigator] step 4: NewResource")
	res, err := maa.NewResource()
	if err != nil {
		ctrl.Destroy()
		return nil, fmt.Errorf("maa resource: %w", err)
	}
	log.Infof("[NewNavigator] step 5: PostBundle %q", cfg.ResourceDir)
	if !res.PostBundle(cfg.ResourceDir).Wait().Success() {
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa resource bundle load from %q failed", cfg.ResourceDir)
	}
	log.Infof("[NewNavigator] step 6: register custom recs/actions")

	n := &Navigator{cfg: cfg, ctrl: ctrl, res: res}

	// Register custom recognitions
	for name, rec := range map[string]maa.CustomRecognitionRunner{
		"ROICenterRec":               &roiCenterRec{nav: n},
		"DialogDetectRec":            &dialogDetectRec{nav: n},
		"DifficultyRec":              &difficultyRec{nav: n},
		"SongSelectRec":              &songSelectRec{nav: n},
		"SongNameRec":                &songNameRec{nav: n},
		"SongRecognition":            &songNameRec{nav: n},
		"LivePlayOffRec":             &livePlayOffRec{nav: n},
		"LivePlayOnRec":              &livePlayOnRec{nav: n},
		"LiveBoostEnoughRecognition": &liveBoostEnoughRec{nav: n},
	} {
		if err := res.RegisterCustomRecognition(name, rec); err != nil {
			res.Destroy()
			ctrl.Destroy()
			return nil, fmt.Errorf("register recognition %q: %w", name, err)
		}
		log.Infof("[NewNavigator] registered recognition %q", name)
	}

	// Register custom actions
	for name, act := range map[string]maa.CustomActionRunner{
		"ClickDifficultyAction":    &clickDifficultyAction{nav: n},
		"ClickKetteiAction":        &clickKetteiAction{nav: n},
		"ClickLiveOrBandAction":    &clickLiveOrBandAction{nav: n},
		"ClickDialogOKAction":      &clickDialogOKAction{nav: n},
		"SaveSongAction":           &saveSongAction{nav: n},
		"SaveSong":                 &saveSongAction{nav: n},
		"HandleLiveBoost":          &handleLiveBoostAction{nav: n},
		"SwitchLivePlayModeAction": &switchLivePlayModeAction{nav: n},
		"ExitRehearsalModeAction":  &exitRehearsalModeAction{nav: n},
	} {
		if err := res.RegisterCustomAction(name, act); err != nil {
			res.Destroy()
			ctrl.Destroy()
			return nil, fmt.Errorf("register action %q: %w", name, err)
		}
	}

	tasker, err := maa.NewTasker()
	if err != nil {
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa tasker: %w", err)
	}
	log.Infof("[NewNavigator] step 7: BindController")
	if err := tasker.BindController(ctrl); err != nil {
		tasker.Destroy()
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa bind controller: %w", err)
	}
	log.Infof("[NewNavigator] step 8: BindResource")
	if err := tasker.BindResource(res); err != nil {
		tasker.Destroy()
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa bind resource: %w", err)
	}
	log.Infof("[NewNavigator] done")

	n.tasker = tasker

	return n, nil
}

// Run executes the navigation pipeline for the given mode and difficulty.
// It blocks until navigation succeeds, fails, or ctx is cancelled.
func (n *Navigator) Run(ctx context.Context, mode, diff string) bool {
	n.mu.Lock()
	n.mode = mode
	n.diff = diff
	n.lastSongDetect = SongDetectResult{}
	n.lastLiveBoost = -1
	n.mu.Unlock()

	n.emit("Nav", "楽曲選択", "MAA 導航開始", true)
	n.emit("Start", "start-layer", "→ 進入 Start 層", true)
	n.emit("Main", "main-layer", "→ 進入 Main 層", true)

	// Build pipeline override to inject resolution-correct absolute ROIs.
	var roiOverride string
	if len(n.cfg.NodeROIs) > 0 {
		if w, h, err := screencapDims(n.ctrl); err == nil && w > 0 && h > 0 {
			parts := make([]string, 0, len(n.cfg.NodeROIs))
			for node, nr := range n.cfg.NodeROIs {
				ax := int(nr[0] * float64(w))
				ay := int(nr[1] * float64(h))
				aw := int(nr[2] * float64(w))
				ah := int(nr[3] * float64(h))
				parts = append(parts, fmt.Sprintf("%q:{\"roi\":[%d,%d,%d,%d]}", node, ax, ay, aw, ah))
			}
			roiOverride = "{" + strings.Join(parts, ",") + "}"
			log.Infof("[Navigator.Run] ROI override (%dx%d): %s", w, h, roiOverride)
		}
	}

	job := n.tasker.PostTask("Nav", roiOverride)

	done := make(chan bool, 1)
	go func() {
		done <- job.Wait().Success()
	}()

	select {
	case <-ctx.Done():
		n.tasker.PostStop()
		n.emit("Nav", "", "已取消", true)
		return false
	case ok := <-done:
		if ok {
			n.emit("NavSuccess", "done", "MAA 導航完成", true)
		} else {
			n.emit("Nav", "", "MAA 導航失敗", true)
		}
		return ok
	}
}

// Destroy releases all MAA resources.
func (n *Navigator) Destroy() {
	if n.tasker != nil {
		n.tasker.Destroy()
		n.tasker = nil
	}
	if n.res != nil {
		n.res.Destroy()
		n.res = nil
	}
	if n.ctrl != nil {
		n.ctrl.Destroy()
		n.ctrl = nil
	}
}

// ─────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────

func (n *Navigator) getModeDiff() (mode, diff string) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.mode, n.diff
}

func (n *Navigator) setLastSongDetect(res SongDetectResult) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastSongDetect = copySongDetectResult(res)
}

func (n *Navigator) getLastSongDetect() SongDetectResult {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return copySongDetectResult(n.lastSongDetect)
}

// GetLastSongDetect returns a copy of the song detection result stored by the
// pipeline's SongRecognition / SaveSong steps. Valid after a successful Run().
func (n *Navigator) GetLastSongDetect() SongDetectResult {
	return n.getLastSongDetect()
}

func (n *Navigator) setLastLiveBoost(v int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastLiveBoost = v
}

func (n *Navigator) getLastLiveBoost() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.lastLiveBoost
}

// roiByKey returns the normalised ROI [x1,y1,x2,y2] for a named UI element.
// The key matches the custom_recognition_param values in pipeline/*.json.
func (n *Navigator) roiByKey(key string) ROI {
	switch key {
	case "kettei":
		return n.cfg.KetteiROI
	case "live_start":
		return n.cfg.LiveStartROI
	case "band_confirm":
		return n.cfg.BandConfirmROI
	case "dialog_ok":
		return n.cfg.DialogOKROI
	case "dialog_title":
		return n.cfg.DialogTitleROI
	}
	return ROI{0, 0, 1, 1}
}

func (n *Navigator) emit(stage, scene, msg string, force bool) {
	if n.cfg.OnProgress != nil {
		n.cfg.OnProgress(stage, scene, msg)
	}
	if force {
		log.Infof("[MAA_NAV] [%s] %s", stage, msg)
	} else {
		log.Debugf("[MAA_NAV] [%s] %s", stage, msg)
	}
}

func toSongDetectCandidates(items []songdetect.MatchCandidate) []SongDetectCandidate {
	out := make([]SongDetectCandidate, 0, len(items))
	for _, it := range items {
		out = append(out, SongDetectCandidate{SongID: it.SongID, Title: it.Title, Score: it.Score})
	}
	return out
}

func buildSongDetectResult(mode string, titleTexts, songTexts []string) SongDetectResult {
	titleTexts = copyStrings(titleTexts)
	songTexts = copyStrings(songTexts)
	titleNorm := songdetect.NormalizeSceneTexts(titleTexts)
	titleScore := songdetect.SongSelectTitleScore(titleTexts)
	onSongSelect := songdetect.IsSongSelectTitle(titleTexts)

	id, title, score, source, top, ok := songdetect.DetectByModeTextsDetailed(songTexts, mode)
	res := SongDetectResult{
		Mode:                mode,
		TitleTexts:          titleTexts,
		TitleNormalizedText: titleNorm,
		SongTexts:           songTexts,
		TitleScore:          titleScore,
		OnSongSelectScreen:  onSongSelect,
		SongScore:           score,
		SourceText:          source,
		TopCandidates:       toSongDetectCandidates(top),
	}
	if ok {
		res.SongID = id
		res.SongTitle = title
	}
	return res
}

func detectSongFromImage(img image.Image, mode string, titleROI, songROI ROI) (SongDetectResult, error) {
	titleTexts, err := ocrImageTexts(img, titleROI)
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("title ocr failed: %w", err)
	}
	songTexts, err := ocrImageTexts(img, songROI)
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("song-name ocr failed: %w", err)
	}
	return buildSongDetectResult(mode, titleTexts, songTexts), nil
}

// DetectSongFromPNG performs unified OCR + matching from a screenshot PNG.
func DetectSongFromPNG(mode string, pngBytes []byte, titleROI, songROI ROI) (SongDetectResult, error) {
	ocrC, err := GetOCRClient()
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("ocr unavailable: %w", err)
	}
	titleArr := [4]float64(titleROI)
	titleTexts, err := ocrC.OCR(pngBytes, &titleArr)
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("title ocr failed: %w", err)
	}
	songArr := [4]float64(songROI)
	songTexts, err := ocrC.OCR(pngBytes, &songArr)
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("song-name ocr failed: %w", err)
	}
	return buildSongDetectResult(mode, titleTexts, songTexts), nil
}

func (n *Navigator) detectModeForSong(modeHint string) string {
	if modeHint != "" {
		return modeHint
	}
	mode, _ := n.getModeDiff()
	if mode != "" {
		return mode
	}
	if n.cfg.Mode != "" {
		return n.cfg.Mode
	}
	return "bang"
}

// DetectSong captures current screen and returns unified song-detect output.
func (n *Navigator) DetectSong(modeHint string) (SongDetectResult, error) {
	if !n.ctrl.PostScreencap().Wait().Success() {
		return SongDetectResult{}, fmt.Errorf("PostScreencap failed")
	}
	img, err := n.ctrl.CacheImage()
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("CacheImage: %w", err)
	}
	mode := n.detectModeForSong(modeHint)
	return detectSongFromImage(img, mode, n.cfg.SongTitleROI, n.cfg.SongNameROI)
}

// DetectSongTexts is kept for compatibility; prefer DetectSong.
func (n *Navigator) DetectSongTexts() (titleTexts, songTexts []string, err error) {
	res, err := n.DetectSong("")
	if err != nil {
		return nil, nil, err
	}
	return copyStrings(res.TitleTexts), copyStrings(res.SongTexts), nil
}

func ocrImageTexts(img image.Image, roi ROI) ([]string, error) {

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode screenshot png: %w", err)
	}

	ocrC, err := GetOCRClient()
	if err != nil {
		return nil, fmt.Errorf("ocr unavailable: %w", err)
	}

	roiArr := [4]float64(roi)
	texts, err := ocrC.OCR(buf.Bytes(), &roiArr)
	if err != nil {
		return nil, err
	}
	return texts, nil
}

func templateMatchBest(ctx *maa.Context, template string) (score float64, box maa.Rect, ok bool) {
	img, err := ctx.GetTasker().GetController().CacheImage()
	if err != nil || img == nil {
		return 0, maa.Rect{}, false
	}
	detail, err := ctx.RunRecognition("random_choice_song", img, map[string]any{
		"random_choice_song": map[string]any{
			"template":  []string{template},
			"threshold": []float64{0.0},
		},
	})
	if err != nil || detail == nil || detail.Results == nil || detail.Results.Best == nil {
		return 0, maa.Rect{}, false
	}
	tmr, ok := detail.Results.Best.AsTemplateMatch()
	if !ok {
		return 0, maa.Rect{}, false
	}
	return tmr.Score, tmr.Box, true
}

func roiCenterFromBox(box maa.Rect) (x, y int) {
	return box[0] + box[2]/2, box[1] + box[3]/2
}

var liveBoostRE = regexp.MustCompile(`(\d+)\s*/`)

func parseLiveBoostValue(texts []string) (int, bool) {
	for _, raw := range texts {
		text := strings.ReplaceAll(raw, " ", "")
		m := liveBoostRE.FindStringSubmatch(text)
		if len(m) < 2 {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		return v, true
	}
	return 0, false
}

// screencapDims takes a fresh screencap and returns the actual image dimensions.
// Use this instead of ctrl.GetResolution() when computing PostClick coordinates,
// because GetResolution() returns Android logical dimensions (may be portrait even
// when the game runs in landscape), while PostClick expects screencap-space coords.
func screencapDims(ctrl *maa.Controller) (w, h int, err error) {
	if !ctrl.PostScreencap().Wait().Success() {
		return 0, 0, fmt.Errorf("PostScreencap failed")
	}
	img, err := ctrl.CacheImage()
	if err != nil {
		return 0, 0, fmt.Errorf("CacheImage: %w", err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy(), nil
}

// roiCenterPx converts a normalised ROI to its centre pixel coordinates.
func roiCenterPx(roi ROI, w, h int) (cx, cy int) {
	x1 := clampI(int(roi[0]*float64(w)), 0, w-1)
	x2 := clampI(int(roi[2]*float64(w)), 0, w)
	y1 := clampI(int(roi[1]*float64(h)), 0, h-1)
	y2 := clampI(int(roi[3]*float64(h)), 0, h)
	return (x1 + x2) / 2, (y1 + y2) / 2
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ─────────────────────────────────────────────
// Luma sampling (dialog detection)
// ─────────────────────────────────────────────

func sampleROILuma(img image.Image, roi ROI) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return 0
	}
	x1 := clampI(int(roi[0]*float64(w)), 0, w-1)
	x2 := clampI(int(roi[2]*float64(w)), 0, w)
	y1 := clampI(int(roi[1]*float64(h)), 0, h-1)
	y2 := clampI(int(roi[3]*float64(h)), 0, h)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	stepX := max(1, (x2-x1)/48)
	stepY := max(1, (y2-y1)/48)
	var sum int64
	count := 0
	for y := y1; y < y2; y += stepY {
		for x := x1; x < x2; x += stepX {
			c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
			sum += int64(c.Y)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}

func sampleFullScreenLuma(img image.Image) float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	n := w * h
	if n <= 0 {
		return 0
	}
	step := max(1, n/1024)
	var sum int64
	count := 0
	idx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if idx%step == 0 {
				c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
				sum += int64(c.Y)
				count++
			}
			idx++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}

// detectDialogByLuma mirrors the ScrcpyFrame-based logic but works on image.Image.
func detectDialogByLuma(img image.Image, dialogTitleROI ROI) bool {
	dialogLuma := sampleROILuma(img, dialogTitleROI)
	screenLuma := sampleFullScreenLuma(img)
	return dialogLuma > 120 && dialogLuma-screenLuma > 35
}

// ─────────────────────────────────────────────
// Custom recognition: ROICenterRec
// ─────────────────────────────────────────────

// roiCenterRec always succeeds and returns the centre pixel of the ROI
// identified by CustomRecognitionParam.  ClickSelf then taps that pixel.
type roiCenterRec struct{ nav *Navigator }

func (r *roiCenterRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	roi := r.nav.roiByKey(arg.CustomRecognitionParam)
	w := arg.Img.Bounds().Dx()
	h := arg.Img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return nil, false
	}
	cx, cy := roiCenterPx(roi, w, h)
	log.Infof("[MAA_NAV] ROICenterRec[%s]: img=%dx%d → tap=(%d,%d)", arg.CustomRecognitionParam, w, h, cx, cy)
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: fmt.Sprintf("%s@(%d,%d)", arg.CustomRecognitionParam, cx, cy),
	}, true
}

// ─────────────────────────────────────────────
// Custom recognition: DifficultyRec
// ─────────────────────────────────────────────

// difficultyRec wraps TemplateMatch so the best match score is logged.
// This is a diagnostic aid: adjust the threshold constant below if needed.
const difficultyMatchThreshold = 0.80
const songSelectFallbackThreshold = 0.80
const livePlayOffMatchThreshold = 0.80

var (
	defaultSwitchLivePlayROI = ROI{0.320, 0.748, 0.405, 0.790}
	defaultExitRehearsalROI  = ROI{0.074, 0.595, 0.205, 0.652}
	defaultLiveBoostROI      = ROI{0.765, 0.042, 0.813, 0.069}
	// liveplay toggle button, centred at approx (309, 630) on 1544×720
	defaultLivePlayToggleROI = ROI{0.160, 0.840, 0.250, 0.910}
)

type difficultyRec struct{ nav *Navigator }

func (r *difficultyRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	n := r.nav
	_, diff := n.getModeDiff()
	log.Infof("[DifficultyRec] ── entered diff=%q arg.Img=%dx%d", diff, arg.Img.Bounds().Dx(), arg.Img.Bounds().Dy())
	if diff == "" {
		log.Infof("[DifficultyRec] diff is empty → skip")
		return &maa.CustomRecognitionResult{Box: maa.Rect{0, 0, 1, 1}, Detail: "skip"}, true
	}

	img, err := ctx.GetTasker().GetController().CacheImage()
	if err != nil || img == nil {
		log.Infof("[DifficultyRec] CacheImage failed: %v", err)
		return nil, false
	}
	log.Infof("[DifficultyRec] CacheImage OK %dx%d", img.Bounds().Dx(), img.Bounds().Dy())

	bestScore := 0.0
	bestBox := maa.Rect{}
	bestTmpl := ""
	for _, variant := range []string{"active", "inactive"} {
		tmpl := fmt.Sprintf("live/difficulty/%s_%s.png", diff, variant)
		log.Infof("[DifficultyRec] trying template=%s", tmpl)
		detail, err := ctx.RunRecognition("random_choice_song", img, map[string]any{
			"random_choice_song": map[string]any{
				"template":  []string{tmpl},
				"threshold": []float64{0.0},
				"order_by":  "Score",
			},
		})
		if err != nil {
			log.Infof("[DifficultyRec] template=%s RunRecognition error: %v", tmpl, err)
			continue
		}
		if detail == nil {
			log.Infof("[DifficultyRec] template=%s detail==nil", tmpl)
			continue
		}
		if detail.Results == nil {
			log.Infof("[DifficultyRec] template=%s Results==nil", tmpl)
			continue
		}
		if detail.Results.Best == nil {
			log.Infof("[DifficultyRec] template=%s Best==nil (all count=%d)", tmpl, len(detail.Results.All))
			for i, r := range detail.Results.All {
				if tm, ok := r.AsTemplateMatch(); ok {
					log.Infof("[DifficultyRec]   all[%d] score=%.3f box=%v", i, tm.Score, tm.Box)
				}
			}
			continue
		}
		tmr, ok := detail.Results.Best.AsTemplateMatch()
		if !ok {
			log.Infof("[DifficultyRec] template=%s Best.AsTemplateMatch() failed", tmpl)
			continue
		}
		log.Infof("[DifficultyRec] template=%s best_score=%.3f box=%v threshold=%.2f accepted=%v",
			tmpl, tmr.Score, tmr.Box, difficultyMatchThreshold, tmr.Score >= difficultyMatchThreshold)
		for i, r := range detail.Results.All {
			if tm, ok2 := r.AsTemplateMatch(); ok2 {
				log.Infof("[DifficultyRec]   all[%d] score=%.3f box=%v", i, tm.Score, tm.Box)
			}
		}
		if tmr.Score >= difficultyMatchThreshold && tmr.Score > bestScore {
			bestScore = tmr.Score
			bestBox = tmr.Box
			bestTmpl = tmpl
		}
	}
	if bestScore > 0 {
		cx := bestBox[0] + bestBox[2]/2
		cy := bestBox[1] + bestBox[3]/2
		log.Infof("[DifficultyRec] ── result: tmpl=%s score=%.3f box=%v tap=(%d,%d)",
			bestTmpl, bestScore, bestBox, cx, cy)
		return &maa.CustomRecognitionResult{
			Box:    maa.Rect{cx, cy, 1, 1},
			Detail: fmt.Sprintf("tmpl=%s score=%.3f", bestTmpl, bestScore),
		}, true
	}
	log.Infof("[DifficultyRec] ── result: NO MATCH (bestScore=%.3f threshold=%.2f)", bestScore, difficultyMatchThreshold)
	return nil, false
}

// ─────────────────────────────────────────────
// Custom recognition: SongSelectRec
// ─────────────────────────────────────────────

// songSelectRec validates we are on the song-select scene by random-choice button template match.
type songSelectRec struct{ nav *Navigator }

func (r *songSelectRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	score, box, ok := templateMatchBest(ctx, "live/button/random_choice_song.png")
	if ok {
		log.Infof("[MAA_NAV] SongSelectRec fallback template score=%.3f threshold=%.2f", score, songSelectFallbackThreshold)
		if score >= songSelectFallbackThreshold {
			cx, cy := roiCenterFromBox(box)
			return &maa.CustomRecognitionResult{
				Box:    maa.Rect{cx, cy, 1, 1},
				Detail: fmt.Sprintf("fallback_score=%.3f", score),
			}, true
		}
	}

	return nil, false
}

// ─────────────────────────────────────────────
// Custom recognition: SongNameRec
// ─────────────────────────────────────────────

// songNameRec OCRs the song-name ROI and stores preview texts for SaveSongAction.
type songNameRec struct{ nav *Navigator }

func (r *songNameRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	n := r.nav
	mode, _ := n.getModeDiff()
	if mode == "" {
		mode = n.cfg.Mode
	}
	if mode == "" {
		mode = "bang"
	}
	w := arg.Img.Bounds().Dx()
	h := arg.Img.Bounds().Dy()

	roi := n.cfg.SongNameROI
	roiPxX1 := int(roi[0] * float64(w))
	roiPxY1 := int(roi[1] * float64(h))
	roiPxX2 := int(roi[2] * float64(w))
	roiPxY2 := int(roi[3] * float64(h))
	log.Infof("[MAA_NAV] SongNameRec img=%dx%d songNameROI=[%.4f,%.4f,%.4f,%.4f] → px[%d,%d,%d,%d] wh=[%d,%d]",
		w, h, roi[0], roi[1], roi[2], roi[3],
		roiPxX1, roiPxY1, roiPxX2, roiPxY2, roiPxX2-roiPxX1, roiPxY2-roiPxY1)

	res, err := detectSongFromImage(arg.Img, mode, n.cfg.SongTitleROI, n.cfg.SongNameROI)
	if err != nil {
		log.Warnf("[MAA_NAV] SongNameRec OCR failed: %v", err)
		return nil, false
	}
	n.setLastSongDetect(res)

	preview := res.SongTextsPreview(6)
	log.Infof("[MAA_NAV] SongNameRec texts=%v", preview)
	if !res.OnSongSelectScreen {
		log.Infof("[MAA_NAV] SongNameRec screen score=%.2f (not confidently on song-select)", res.TitleScore)
	}
	if len(res.SongTexts) == 0 {
		return nil, false
	}
	// Reject clearly-incomplete OCR: single-character result with no punctuation
	// indicates the screen is still transitioning (e.g. only "R" visible before
	// the full title renders).  Accept if total rune count >= 2 or there are
	// multiple OCR fragments (MAA may split "R·I·O·T" into ["R","I","O","T"]).
	totalOCRLen := 0
	for _, t := range res.SongTexts {
		totalOCRLen += len([]rune(t))
	}
	if len(res.SongTexts) == 1 && totalOCRLen < 2 {
		log.Infof("[MAA_NAV] SongNameRec OCR too short (%d runes in 1 fragment), retrying", totalOCRLen)
		return nil, false
	}
	if res.SongID <= 0 {
		log.Infof("[MAA_NAV] SongNameRec no confident match score=%d top=%s", res.SongScore, res.TopSummary(3))
		return nil, false
	}
	log.Infof("[MAA_NAV] SongNameRec matched id=%d title=%q score=%d source=%q",
		res.SongID, res.SongTitle, res.SongScore, res.SourceText)

	cx, cy := roiCenterPx(n.cfg.SongNameROI, w, h)
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: fmt.Sprintf("song_id=%d title=%s score=%d", res.SongID, res.SongTitle, res.SongScore),
	}, true
}

// ─────────────────────────────────────────────
// Custom recognition: LivePlayOffRec
// ─────────────────────────────────────────────

// livePlayOffRec checks whether liveplay/mv/3d effects are already disabled.
type livePlayOffRec struct{ nav *Navigator }

func (r *livePlayOffRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	offScore, offBox, ok := templateMatchBest(ctx, "live/button/liveplay_off.png")
	if !ok {
		log.Infof("[MAA_NAV] LivePlayOffRec no result")
		return nil, false
	}
	log.Infof("[MAA_NAV] LivePlayOffRec score=%.3f threshold=%.2f", offScore, livePlayOffMatchThreshold)
	if offScore < livePlayOffMatchThreshold {
		return nil, false
	}
	cx, cy := roiCenterFromBox(offBox)
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: fmt.Sprintf("off_score=%.3f", offScore),
	}, true
}

// ─────────────────────────────────────────────
// Custom recognition: LivePlayOnRec
// ─────────────────────────────────────────────

// livePlayOnRec detects liveplay_3d.png or liveplay_mv.png (MV/3D is currently ON).
// Returns the button box so the pipeline can Click it to toggle OFF.
type livePlayOnRec struct{ nav *Navigator }

func (r *livePlayOnRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	for _, tmpl := range []string{"live/button/liveplay_3d.png", "live/button/liveplay_mv.png"} {
		score, _, ok := templateMatchBest(ctx, tmpl)
		if !ok {
			continue
		}
		if score < livePlayOffMatchThreshold {
			continue
		}
		log.Infof("[MAA_NAV] LivePlayOnRec matched %s score=%.3f", tmpl, score)

		ctrl := ctx.GetTasker().GetController()
		w, h, err := screencapDims(ctrl)
		if err != nil {
			return nil, false
		}
		cx, cy := roiCenterPx(defaultLivePlayToggleROI, w, h)
		log.Infof("[MAA_NAV] LivePlayOnRec tap=(%d,%d)", cx, cy)
		return &maa.CustomRecognitionResult{
			Box:    maa.Rect{cx, cy, 1, 1},
			Detail: fmt.Sprintf("on_tmpl=%s score=%.3f tap=(%d,%d)", tmpl, score, cx, cy),
		}, true
	}
	return nil, false
}

// ─────────────────────────────────────────────
// Custom recognition: LiveBoostEnoughRecognition
// ─────────────────────────────────────────────

// liveBoostEnoughRec marks the pre-live confirm region as ready for the
// follow-up HandleLiveBoost action.
type liveBoostEnoughRec struct{ nav *Navigator }

func (r *liveBoostEnoughRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	n := r.nav
	w := arg.Img.Bounds().Dx()
	h := arg.Img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return nil, false
	}
	texts, err := ocrImageTexts(arg.Img, defaultLiveBoostROI)
	if err != nil {
		log.Warnf("[MAA_NAV] LiveBoostEnoughRecognition OCR failed: %v", err)
		return nil, false
	}
	boost, ok := parseLiveBoostValue(texts)
	if !ok {
		log.Infof("[MAA_NAV] LiveBoostEnoughRecognition parse failed texts=%v", songdetect.FirstNStrings(texts, 6))
		return nil, false
	}
	n.setLastLiveBoost(boost)

	cx, cy := roiCenterPx(defaultLiveBoostROI, w, h)
	log.Infof("[MAA_NAV] LiveBoostEnoughRecognition boost=%d", boost)
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: fmt.Sprintf("%d", boost),
	}, true
}

// ─────────────────────────────────────────────
// Custom action: ClickDifficultyAction
// ─────────────────────────────────────────────

// clickDifficultyAction handles difficulty selection including the PJSK
// append page-flip.  It runs immediately after the initial 1.5 s preDelay.
type clickDifficultyAction struct{ nav *Navigator }

func (a *clickDifficultyAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	mode, diff := n.getModeDiff()

	if diff == "" {
		n.emit("ClickDifficulty", "楽曲選択", "→ 難度未指定，跳過", false)
		return true
	}

	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] ClickDifficulty screencapDims: %v", err)
		return true // non-fatal
	}
	log.Infof("[MAA_NAV] ClickDifficulty: screencap=%dx%d landscape=%v", w, h, w > h)

	// PJSK + append requires flipping to the second difficulty page first.
	if mode == "pjsk" && diff == "append" {
		if fn := n.cfg.PageArrowFn; fn != nil {
			ax, ay := fn(int(w), int(h))
			n.emit("ClickDifficulty", "楽曲選択", "→ PJSK APPEND: 翻至第二頁", true)
			ctrl.PostClick(int32(ax), int32(ay)).Wait()
			time.Sleep(2 * time.Second)
		}
	}

	if fn := n.cfg.DifficultyTapFn; fn != nil {
		x, y, ok := fn(mode, diff, int(w), int(h))
		if ok {
			n.emit("ClickDifficulty", "楽曲選択", fmt.Sprintf("→ 點擊難度 %s @ (%d,%d)", diff, x, y), true)
			ctrl.PostClick(int32(x), int32(y)).Wait()
		}
	}
	return true
}

// ─────────────────────────────────────────────
// Custom recognition: DialogDetectRec
// ─────────────────────────────────────────────

// dialogDetectRec succeeds when a dialog overlay is visible (luma heuristic).
// The pipeline uses this to branch: next → dialog path, timeout_next → no-dialog path.
type dialogDetectRec struct{ nav *Navigator }

func (r *dialogDetectRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	w := arg.Img.Bounds().Dx()
	h := arg.Img.Bounds().Dy()
	visible := detectDialogByLuma(arg.Img, r.nav.cfg.DialogTitleROI)
	log.Infof("[MAA_NAV] DialogDetectRec: img=%dx%d visible=%v", w, h, visible)
	if !visible {
		return nil, false
	}
	cx, cy := roiCenterPx(r.nav.cfg.DialogTitleROI, w, h)
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: "dialog_visible",
	}, true
}

// ─────────────────────────────────────────────
// Custom action: ClickKetteiAction
// ─────────────────────────────────────────────

// clickKetteiAction taps the 決定 (confirm/select) button using portrait
// device coordinates via ctrl.PostClick, bypassing MAA's landscape screenshot
// coordinate system entirely.
type clickKetteiAction struct{ nav *Navigator }

func (a *clickKetteiAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] ClickKettei screencapDims: %v", err)
		return true
	}
	log.Infof("[MAA_NAV] ClickKettei: screencap=%dx%d landscape=%v", w, h, w > h)
	cx, cy := roiCenterPx(n.cfg.KetteiROI, w, h)
	n.emit("ClickKettei", "楽曲選択", fmt.Sprintf("→ 點擊決定 @ (%d,%d)", cx, cy), true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Custom action: ClickLiveOrBandAction
// ─────────────────────────────────────────────

// clickLiveOrBandAction taps ライブスタート (BanG Dream) or バンド確認 (PJSK).
// Called only when no ライブ設定 dialog appeared after ClickKettei.
type clickLiveOrBandAction struct{ nav *Navigator }

func (a *clickLiveOrBandAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	mode, _ := n.getModeDiff()
	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] ClickLiveOrBand screencapDims: %v", err)
		return true
	}
	log.Infof("[MAA_NAV] ClickLiveOrBand: screencap=%dx%d landscape=%v", w, h, w > h)
	roi := n.cfg.LiveStartROI
	if mode == "pjsk" {
		roi = n.cfg.BandConfirmROI
	}
	cx, cy := roiCenterPx(roi, w, h)
	n.emit("ClickLiveOrBand", "バンド確認", "→ 點擊確認/開始", true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Custom action: HandleLiveBoost
// ─────────────────────────────────────────────

// handleLiveBoostAction confirms pre-live entry (BanG Dream start / PJSK band).
type handleLiveBoostAction struct{ nav *Navigator }

func (a *handleLiveBoostAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	minBoost := n.cfg.MinLiveBoost
	if minBoost <= 0 {
		minBoost = 1
	}
	boost := n.getLastLiveBoost()
	if boost >= 0 && boost < minBoost {
		n.emit("ensure_liveboost", "liveboost", fmt.Sprintf("→ LiveBoost %d < %d，停止導航", boost, minBoost), true)
		ctx.GetTasker().PostStop()
		return true
	}

	mode, _ := n.getModeDiff()
	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] HandleLiveBoost screencapDims: %v", err)
		return true
	}
	roi := n.cfg.LiveStartROI
	if mode == "pjsk" {
		roi = n.cfg.BandConfirmROI
	}
	cx, cy := roiCenterPx(roi, w, h)
	n.emit("ensure_liveboost", "comfirm_song", "→ 點擊確認/開始", true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Custom action: ClickDialogOKAction
// ─────────────────────────────────────────────

// clickDialogOKAction taps the OK button of the ライブ設定 dialog.
// Called only when DialogDetectRec succeeds on the HandleLiveSetting node.
type clickDialogOKAction struct{ nav *Navigator }

func (a *clickDialogOKAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] ClickDialogOK screencapDims: %v", err)
		return true
	}
	log.Infof("[MAA_NAV] ClickDialogOK: screencap=%dx%d landscape=%v", w, h, w > h)
	cx, cy := roiCenterPx(n.cfg.DialogOKROI, w, h)
	n.emit("ClickDialogOK", "ライブ設定", "→ 點擊 OK", true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Custom action: SaveSongAction
// ─────────────────────────────────────────────

// saveSongAction stores/logs the song-name OCR captured by SongNameRec.
type saveSongAction struct{ nav *Navigator }

func (a *saveSongAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	res := n.getLastSongDetect()
	if len(res.SongTexts) == 0 {
		n.emit("SONG_DETECT", "song-name", "→ 歌名 OCR 為空", false)
		return true
	}
	preview := res.SongTextsPreview(5)
	topSummary := res.TopSummary(3)
	msg := fmt.Sprintf("→ 歌名 OCR: %v", preview)
	if !res.OnSongSelectScreen {
		msg = fmt.Sprintf("%s\n  → SCREEN_CHECK score=%.2f (未確認在楽曲選択)", msg, res.TitleScore)
	}
	if res.SongID > 0 {
		msg = fmt.Sprintf("%s\n  → 命中: #%d %s (score=%d)", msg, res.SongID, res.SongTitle, res.SongScore)
		if res.SourceText != "" {
			msg = fmt.Sprintf("%s\n  → source: %q", msg, res.SourceText)
		}
	} else {
		msg = fmt.Sprintf("%s\n  → 尚未命中曲名 (best=%d)", msg, res.SongScore)
	}
	if topSummary != "" {
		msg = fmt.Sprintf("%s\n  → top: %s", msg, topSummary)
	}
	n.emit("SONG_DETECT", "song-name", msg, true)
	return true
}

// ─────────────────────────────────────────────
// Custom action: SwitchLivePlayModeAction
// ─────────────────────────────────────────────

// switchLivePlayModeAction toggles the liveplay/mv/3d option toward OFF.
type switchLivePlayModeAction struct{ nav *Navigator }

func (a *switchLivePlayModeAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] SwitchLivePlayMode screencapDims: %v", err)
		return true
	}
	cx, cy := roiCenterPx(defaultSwitchLivePlayROI, w, h)
	n.emit("ensure_live_mode", "switch_liveplay_mode", fmt.Sprintf("→ 切換 LivePlay 模式 @ (%d,%d)", cx, cy), true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Custom action: ExitRehearsalModeAction
// ─────────────────────────────────────────────

// exitRehearsalModeAction exits rehearsal/training overlay when present.
type exitRehearsalModeAction struct{ nav *Navigator }

func (a *exitRehearsalModeAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	n := a.nav
	ctrl := ctx.GetTasker().GetController()
	w, h, err := screencapDims(ctrl)
	if err != nil {
		log.Warnf("[MAA_NAV] ExitRehearsalMode screencapDims: %v", err)
		return true
	}
	cx, cy := roiCenterPx(defaultExitRehearsalROI, w, h)
	n.emit("ensure_live_mode", "exit_rehearsal_mode", fmt.Sprintf("→ 退出排練模式 @ (%d,%d)", cx, cy), true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Global MAA init (once per process)
// ─────────────────────────────────────────────

var (
	maaInitOnce sync.Once
	maaInitErr  error
)

func ensureMaaInit(libDir string) error {
	maaInitOnce.Do(func() {
		var opts []maa.InitOption
		if libDir != "" {
			opts = append(opts, maa.WithLibDir(libDir))
		}
		err := maa.Init(opts...)
		if err != nil && err != maa.ErrAlreadyInitialized {
			maaInitErr = err
		}
	})
	return maaInitErr
}
