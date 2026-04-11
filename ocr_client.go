// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ocr "github.com/getcharzp/go-ocr"
	xdraw "golang.org/x/image/draw"
)

// goOCRClient manages a singleton native Go OCR engine (go-ocr).
type goOCRClient struct {
	mu     sync.Mutex
	engine ocr.Engine
}

var (
	globalOCR   *goOCRClient
	globalOCRMu sync.Mutex
)

// getOCRClient returns the singleton go-ocr client.
func getOCRClient() (*goOCRClient, error) {
	globalOCRMu.Lock()
	defer globalOCRMu.Unlock()

	if globalOCR != nil {
		return globalOCR, nil
	}

	c := &goOCRClient{}
	if err := c.start(); err != nil {
		return nil, err
	}
	globalOCR = c
	return c, nil
}

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
		return []string{
			"onnxruntime_" + runtime.GOARCH + ".dylib",
			"onnxruntime.dylib",
		}
	}
	return []string{
		"onnxruntime_" + runtime.GOARCH + ".so",
		"onnxruntime.so",
	}
}

type goOCRPaths struct {
	lib  string
	det  string
	rec  string
	dict string
}

func resolveGoOCRPaths() (goOCRPaths, error) {
	fromEnv := goOCRPaths{
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
	libPath := firstExisting(libCandidates...)
	detPath := firstExisting(
		"./paddle_weights/det.onnx",
		"./go-ocr/paddle_weights/det.onnx",
		"./models/go-ocr/paddle_weights/det.onnx",
		"./ocr/paddle_weights/det.onnx",
	)
	recPath := firstExisting(
		"./paddle_weights/rec.onnx",
		"./go-ocr/paddle_weights/rec.onnx",
		"./models/go-ocr/paddle_weights/rec.onnx",
		"./ocr/paddle_weights/rec.onnx",
	)
	dictPath := firstExisting(
		"./paddle_weights/dict.txt",
		"./go-ocr/paddle_weights/dict.txt",
		"./models/go-ocr/paddle_weights/dict.txt",
		"./ocr/paddle_weights/dict.txt",
	)

	if pathExists(libPath) && pathExists(detPath) && pathExists(recPath) && pathExists(dictPath) {
		return goOCRPaths{lib: libPath, det: detPath, rec: recPath, dict: dictPath}, nil
	}

	return goOCRPaths{}, fmt.Errorf(
		"go-ocr models not found; set env SSM_GO_OCR_LIB/DET/REC/DICT or place files under go-ocr/paddle_weights and go-ocr/lib",
	)
}

func (c *goOCRClient) start() error {
	paths, err := resolveGoOCRPaths()
	if err != nil {
		return err
	}

	eng, err := ocr.NewPaddleOcrEngine(ocr.Config{
		OnnxRuntimeLibPath: paths.lib,
		DetModelPath:       paths.det,
		RecModelPath:       paths.rec,
		DictPath:           paths.dict,
	})
	if err != nil {
		return fmt.Errorf("init go-ocr engine: %w", err)
	}
	c.engine = eng
	return nil
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
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

// OCR sends a PNG image (with optional normalised ROI) to the go-ocr engine
// and returns the recognised text strings.
func (c *goOCRClient) OCR(pngBytes []byte, roi *[4]float64) ([]string, error) {
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
