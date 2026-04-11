// Copyright (C) 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

// Package maacontrol implements pre-game navigation using MaaFramework.
//
// The navigation pipeline is defined declaratively in
// maacontrol/resource/pipeline/pipeline.json.  Complex steps (difficulty
// selection, band-confirm handling, live-setting dialog, pause-button NCC
// detection) are wired as custom recognitions / actions so the JSON remains
// a clean, editable state-machine skeleton.
//
// Pause-button detection supports an arbitrary number of template images
// (multi-scale NCC).  Additional templates are added by listing more paths
// in NavConfig.PauseTemplates — no code change required.
//
// OCR switching: pass a different ResourceDir that points at a resource
// bundle containing different model/ocr/* assets.
package maacontrol

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sync"
	"time"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/MaaXYZ/maa-framework-go/v4/controller/adb"
	xdraw "golang.org/x/image/draw"

	"github.com/kvarenzn/ssm/log"
)

// ─────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────

// ROI is a normalised region of interest [x1, y1, x2, y2] in [0, 1].
type ROI [4]float64

// NavConfig is the complete configuration for a single navigation run.
// Callers fill this in from the global ROI variables and pass it to NewNavigator.
type NavConfig struct {
	// Game mode: "bang" or "pjsk".
	Mode string
	// Target difficulty: "easy" | "normal" | "hard" | "expert" |
	//   "special" | "master" | "append".  Empty = skip difficulty tap.
	Difficulty string

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
	PauseButtonROI ROI // search area for NCC pause-button detection

	// DifficultyTapFn returns the device-pixel tap point for difficulty
	// selection given the screenshot dimensions.
	// Returns ok=false when the difficulty does not need tapping.
	DifficultyTapFn func(mode, diff string, w, h int) (x, y int, ok bool)

	// PageArrowFn returns the pixel tap point for the PJSK difficulty
	// page-flip arrow (used when difficulty == "append").
	PageArrowFn func(w, h int) (x, y int)

	// PauseTemplates is a list of PNG file paths used for NCC-based pause-button
	// detection.  All images are tried at multiple scales; at least one path must
	// resolve to a readable file.
	PauseTemplates []string

	// PauseNccThreshold is the minimum NCC score to declare a pause-button hit.
	// Defaults to 0.40 when zero.
	PauseNccThreshold float64

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
	mu   sync.RWMutex
	mode string
	diff string

	// pre-computed NCC templates for pause-button detection
	pauseMu     sync.RWMutex
	pauseScales []scaledTmpl
}

// NewNavigator creates a Navigator, connects the MAA ADB controller and loads
// the pipeline resource bundle.  Call Destroy() when done.
func NewNavigator(cfg NavConfig) (*Navigator, error) {
	if err := ensureMaaInit(cfg.MaaLibDir); err != nil {
		return nil, fmt.Errorf("maa init: %w", err)
	}

	if cfg.ResourceDir == "" {
		cfg.ResourceDir = "./maacontrol/resource"
	}

	adbPath := cfg.AdbPath
	if adbPath == "" {
		adbPath = "adb"
	}

	ctrl, err := maa.NewAdbController(
		adbPath,
		cfg.AdbSerial,
		adb.ScreencapDefault,
		adb.InputAdbShell, // simple, no extra binary required
		"", "",
	)
	if err != nil {
		return nil, fmt.Errorf("maa adb controller: %w", err)
	}
	if !ctrl.PostConnect().Wait().Success() {
		ctrl.Destroy()
		return nil, fmt.Errorf("maa: connect to %q failed", cfg.AdbSerial)
	}

	res, err := maa.NewResource()
	if err != nil {
		ctrl.Destroy()
		return nil, fmt.Errorf("maa resource: %w", err)
	}
	if !res.PostBundle(cfg.ResourceDir).Wait().Success() {
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa resource bundle load from %q failed", cfg.ResourceDir)
	}

	n := &Navigator{cfg: cfg, ctrl: ctrl, res: res}

	// Register custom recognitions
	for name, rec := range map[string]maa.CustomRecognitionRunner{
		"ROICenterRec":   &roiCenterRec{nav: n},
		"PauseButtonRec": &pauseNccRec{nav: n},
		"DialogDetectRec": &dialogDetectRec{nav: n},
	} {
		if err := res.RegisterCustomRecognition(name, rec); err != nil {
			res.Destroy()
			ctrl.Destroy()
			return nil, fmt.Errorf("register recognition %q: %w", name, err)
		}
	}

	// Register custom actions
	for name, act := range map[string]maa.CustomActionRunner{
		"ClickDifficultyAction": &clickDifficultyAction{nav: n},
		"ClickLiveOrBandAction": &clickLiveOrBandAction{nav: n},
		"ClickDialogOKAction":   &clickDialogOKAction{nav: n},
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
	if err := tasker.BindController(ctrl); err != nil {
		tasker.Destroy()
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa bind controller: %w", err)
	}
	if err := tasker.BindResource(res); err != nil {
		tasker.Destroy()
		res.Destroy()
		ctrl.Destroy()
		return nil, fmt.Errorf("maa bind resource: %w", err)
	}

	n.tasker = tasker

	// Load pause-button NCC templates (non-fatal if missing).
	if err := n.loadPauseTemplates(); err != nil {
		log.Warnf("[maacontrol] pause templates: %v", err)
	}

	return n, nil
}

// Run executes the navigation pipeline for the given mode and difficulty.
// It blocks until navigation succeeds, fails, or ctx is cancelled.
func (n *Navigator) Run(ctx context.Context, mode, diff string) bool {
	n.mu.Lock()
	n.mode = mode
	n.diff = diff
	n.mu.Unlock()

	n.emit("Nav", "楽曲選択", "MAA 導航開始", true)

	job := n.tasker.PostTask("Nav")

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

// roiByKey returns the normalised ROI [x1,y1,x2,y2] for a named UI element.
// The key matches the custom_recognition_param values in pipeline.json.
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
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: fmt.Sprintf("%s@(%d,%d)", arg.CustomRecognitionParam, cx, cy),
	}, true
}

// ─────────────────────────────────────────────
// Custom recognition: PauseButtonRec
// ─────────────────────────────────────────────

// pauseNccRec detects the pause button using multi-scale NCC template matching.
// Returns a hit only when the best NCC score exceeds PauseNccThreshold.
type pauseNccRec struct{ nav *Navigator }

func (r *pauseNccRec) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	n := r.nav

	n.pauseMu.RLock()
	scales := n.pauseScales
	n.pauseMu.RUnlock()

	if len(scales) == 0 {
		return nil, false // no template loaded
	}

	img := arg.Img
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return nil, false
	}

	plane := extractLumaY(img)

	roi := n.cfg.PauseButtonROI
	x1 := clampI(int(roi[0]*float64(w)), 0, w)
	x2 := clampI(int(roi[2]*float64(w)), 0, w)
	y1 := clampI(int(roi[1]*float64(h)), 0, h)
	y2 := clampI(int(roi[3]*float64(h)), 0, h)

	best := -1.0
	for i := range scales {
		s := &scales[i]
		if x2-x1 < s.w || y2-y1 < s.h {
			continue
		}
		if sc := slideNCC(plane, w, x1, y1, x2, y2, s); sc > best {
			best = sc
		}
	}

	thresh := n.cfg.PauseNccThreshold
	if thresh <= 0 {
		thresh = 0.40
	}
	if best < thresh {
		return nil, false
	}

	cx := (x1 + x2) / 2
	cy := (y1 + y2) / 2
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: fmt.Sprintf("ncc=%.3f", best),
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
	w, h, err := ctrl.GetResolution()
	if err != nil {
		log.Warnf("[MAA_NAV] GetResolution: %v", err)
		return true // non-fatal: proceed without difficulty tap
	}

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
			n.emit("ClickDifficulty", "楽曲選択", fmt.Sprintf("→ 點擊難度 %s", diff), true)
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
	if !detectDialogByLuma(arg.Img, r.nav.cfg.DialogTitleROI) {
		return nil, false
	}
	w := arg.Img.Bounds().Dx()
	h := arg.Img.Bounds().Dy()
	cx, cy := roiCenterPx(r.nav.cfg.DialogTitleROI, w, h)
	return &maa.CustomRecognitionResult{
		Box:    maa.Rect{cx, cy, 1, 1},
		Detail: "dialog_visible",
	}, true
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
	w, h, err := ctrl.GetResolution()
	if err != nil {
		log.Warnf("[MAA_NAV] GetResolution: %v", err)
		return true
	}
	roi := n.cfg.LiveStartROI
	if mode == "pjsk" {
		roi = n.cfg.BandConfirmROI
	}
	cx, cy := roiCenterPx(roi, int(w), int(h))
	n.emit("ClickLiveOrBand", "バンド確認", "→ 點擊確認/開始", true)
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
	w, h, err := ctrl.GetResolution()
	if err != nil {
		log.Warnf("[MAA_NAV] GetResolution: %v", err)
		return true
	}
	cx, cy := roiCenterPx(n.cfg.DialogOKROI, int(w), int(h))
	n.emit("ClickDialogOK", "ライブ設定", "→ 點擊 OK", true)
	ctrl.PostClick(int32(cx), int32(cy)).Wait()
	return true
}

// ─────────────────────────────────────────────
// Pause-button NCC template loading
// ─────────────────────────────────────────────

// scaleFactors are the resize ratios applied to each template image.
// Adding additional ratios here extends multi-scale matching automatically.
var scaleFactors = []float64{0.5, 0.75, 1.0, 1.25, 1.5, 2.0}

type scaledTmpl struct {
	zm     []float64 // zero-mean pixel values
	w, h   int
	stddev float64
}

// loadPauseTemplates pre-computes all scaled variants from every template path
// listed in NavConfig.PauseTemplates.
func (n *Navigator) loadPauseTemplates() error {
	var scales []scaledTmpl

	for _, path := range n.cfg.PauseTemplates {
		f, err := os.Open(path)
		if err != nil {
			log.Warnf("[maacontrol] pause template %q: %v", path, err)
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			log.Warnf("[maacontrol] pause template decode %q: %v", path, err)
			continue
		}

		b := img.Bounds()
		origW, origH := b.Dx(), b.Dy()

		// Convert to grayscale once.
		srcGray := image.NewGray(image.Rect(0, 0, origW, origH))
		for y := 0; y < origH; y++ {
			for x := 0; x < origW; x++ {
				c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
				srcGray.SetGray(x, y, c)
			}
		}

		for _, sf := range scaleFactors {
			if st, ok := buildScaledTmpl(srcGray, origW, origH, sf); ok {
				scales = append(scales, st)
			}
		}
	}

	if len(scales) == 0 {
		return fmt.Errorf("no valid pause templates loaded from %v", n.cfg.PauseTemplates)
	}

	n.pauseMu.Lock()
	n.pauseScales = scales
	n.pauseMu.Unlock()
	return nil
}

func buildScaledTmpl(src *image.Gray, origW, origH int, scale float64) (scaledTmpl, bool) {
	w := max(1, int(math.Round(float64(origW)*scale)))
	h := max(1, int(math.Round(float64(origH)*scale)))

	dst := image.NewGray(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)

	n := w * h
	var sum float64
	for _, v := range dst.Pix[:n] {
		sum += float64(v)
	}
	mean := sum / float64(n)

	zm := make([]float64, n)
	var varSum float64
	for i, v := range dst.Pix[:n] {
		zm[i] = float64(v) - mean
		varSum += zm[i] * zm[i]
	}
	std := math.Sqrt(varSum / float64(n))
	if std < 1e-6 {
		return scaledTmpl{}, false
	}
	return scaledTmpl{zm: zm, w: w, h: h, stddev: std}, true
}

// slideNCC slides the template over the frame ROI and returns the peak NCC.
func slideNCC(plane []byte, frameW, x1, y1, x2, y2 int, tmpl *scaledTmpl) float64 {
	tw, th := tmpl.w, tmpl.h
	fn := float64(tw * th)
	strideX := max(1, tw/4)
	strideY := max(1, th/4)
	best := -1.0

	for py := y1; py+th <= y2; py += strideY {
		for px := x1; px+tw <= x2; px += strideX {
			var patchSum float64
			for ty := 0; ty < th; ty++ {
				row := (py+ty)*frameW + px
				for tx := 0; tx < tw; tx++ {
					patchSum += float64(plane[row+tx])
				}
			}
			patchMean := patchSum / fn

			var cross, patchVar float64
			for ty := 0; ty < th; ty++ {
				row := (py+ty)*frameW + px
				tmplRow := ty * tw
				for tx := 0; tx < tw; tx++ {
					d := float64(plane[row+tx]) - patchMean
					patchVar += d * d
					cross += d * tmpl.zm[tmplRow+tx]
				}
			}

			patchVar /= fn
			if patchVar < 1e-6 {
				continue
			}
			score := (cross / fn) / (math.Sqrt(patchVar) * tmpl.stddev)
			if score > best {
				best = score
			}
		}
	}
	return best
}

// extractLumaY converts an image to an 8-bit grayscale luma plane (BT.601)
// stored row-major with stride = image width.
func extractLumaY(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	plane := make([]byte, w*h)

	// Fast path for *image.RGBA (the common case from MAA screencap).
	if rgba, ok := img.(*image.RGBA); ok {
		for y := 0; y < h; y++ {
			srcOff := (b.Min.Y+y-rgba.Rect.Min.Y)*rgba.Stride + (b.Min.X-rgba.Rect.Min.X)*4
			dstOff := y * w
			for x := 0; x < w; x++ {
				r := uint32(rgba.Pix[srcOff+x*4+0])
				g := uint32(rgba.Pix[srcOff+x*4+1])
				bv := uint32(rgba.Pix[srcOff+x*4+2])
				plane[dstOff+x] = byte((299*r + 587*g + 114*bv) / 1000)
			}
		}
		return plane
	}

	// Fallback for any other image type.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
			plane[y*w+x] = c.Y
		}
	}
	return plane
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
		maaInitErr = maa.Init(opts...)
	})
	return maaInitErr
}
