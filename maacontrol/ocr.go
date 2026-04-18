// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package maacontrol

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	maa "github.com/MaaXYZ/maa-framework-go/v4"

	"github.com/kvarenzn/ssm/core/log"
)

// ─── Public OCR facade ────────────────────────────────────────────────────────

// OCRTextBox stores one OCR text with its box in absolute image pixels.
// Box format is [x1, y1, x2, y2].
type OCRTextBox struct {
	Text  string
	Box   [4]int
	Score float64
}

// OCRClient is the public OCR facade used by app flow.
// Backend details are hidden behind an internal recognizer interface.
type OCRClient struct {
	recognizer ocrBoxRecognizer
}

type ocrBoxRecognizer interface {
	OCRWithBoxes(pngBytes []byte, roi *[4]float64) ([]OCRTextBox, error)
}

var (
	ocrClientOnce sync.Once
	ocrClientInst *OCRClient
	ocrClientErr  error
)

// GetOCRClient returns the singleton OCR client powered by MAA OCR.
func GetOCRClient() (*OCRClient, error) {
	ocrClientOnce.Do(func() {
		backend, err := newMAAOCRBackend()
		if err != nil {
			ocrClientErr = fmt.Errorf("MAA OCR unavailable: %w", err)
			return
		}
		ocrClientInst = &OCRClient{recognizer: backend}
	})

	if ocrClientErr != nil {
		return nil, ocrClientErr
	}
	return ocrClientInst, nil
}

// OCR recognizes text from image bytes and optional normalized ROI.
func (c *OCRClient) OCR(pngBytes []byte, roi *[4]float64) ([]string, error) {
	if c == nil || c.recognizer == nil {
		return nil, fmt.Errorf("MAA OCR client not initialized")
	}

	boxes, err := c.recognizer.OCRWithBoxes(pngBytes, roi)
	if err != nil {
		return nil, err
	}
	return dedupTextsFromBoxes(boxes), nil
}

// OCRWithBoxes recognizes text and returns text boxes in image coordinates.
func (c *OCRClient) OCRWithBoxes(pngBytes []byte, roi *[4]float64) ([]OCRTextBox, error) {
	if c == nil || c.recognizer == nil {
		return nil, fmt.Errorf("MAA OCR client not initialized")
	}
	return c.recognizer.OCRWithBoxes(pngBytes, roi)
}

func dedupTextsFromBoxes(items []OCRTextBox) []string {
	seen := make(map[string]struct{}, len(items))
	texts := make([]string, 0, len(items))
	for _, it := range items {
		t := strings.TrimSpace(it.Text)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		texts = append(texts, t)
	}
	return texts
}

// ─── MAA OCR backend ─────────────────────────────────────────────────────────

// maaOCRBackend is the concrete MAA OCR runtime implementation.
type maaOCRBackend struct {
	mu        sync.Mutex
	res       *maa.Resource
	tasker    *maa.Tasker
	modelName string
	cleanup   func()
}

func newMAAOCRBackend() (*maaOCRBackend, error) {
	log.Infof("[newMAAOCRBackend] step 1: initMAARuntimeForOCR")
	if err := initMAARuntimeForOCR(); err != nil {
		return nil, err
	}

	log.Infof("[newMAAOCRBackend] step 2: resolveMAAOCRModel")
	modelDir, modelName, cleanup, err := resolveMAAOCRModel()
	if err != nil {
		return nil, err
	}
	log.Infof("[newMAAOCRBackend] step 3: NewResource")

	res, err := maa.NewResource()
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("maa resource: %w", err)
	}

	log.Infof("[newMAAOCRBackend] step 4: PostOcrModel %q", modelDir)
	if !res.PostOcrModel(modelDir).Wait().Success() {
		res.Destroy()
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("maa load OCR model from %q failed", modelDir)
	}

	log.Infof("[newMAAOCRBackend] step 5: NewTasker")
	tasker, err := maa.NewTasker()
	if err != nil {
		res.Destroy()
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("maa tasker: %w", err)
	}
	log.Infof("[newMAAOCRBackend] step 6: BindResource")
	if err := tasker.BindResource(res); err != nil {
		tasker.Destroy()
		res.Destroy()
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("maa bind resource: %w", err)
	}

	log.Infof("[newMAAOCRBackend] done")
	return &maaOCRBackend{
		res:       res,
		tasker:    tasker,
		modelName: modelName,
		cleanup:   cleanup,
	}, nil
}

func (c *maaOCRBackend) OCRWithBoxes(pngBytes []byte, roi *[4]float64) ([]OCRTextBox, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}

	param := maa.OCRParam{
		Threshold: 0,
		OrderBy:   maa.OCROrderByHorizontal,
		Model:     c.modelName,
	}

	if roi != nil {
		rect, ok := normalizeROIToMAARect(*roi, img.Bounds().Dx(), img.Bounds().Dy())
		if ok {
			param.ROI = maa.NewTargetRect(rect)
		}
	}

	job := c.tasker.PostRecognition(maa.RecognitionTypeOCR, &param, img).Wait()
	if err := job.Error(); err != nil {
		return nil, fmt.Errorf("maa post recognition: %w", err)
	}
	if !job.Success() {
		return nil, fmt.Errorf("maa OCR task failed with status %v", job.Status())
	}

	taskDetail, err := job.GetDetail()
	if err != nil {
		return nil, fmt.Errorf("maa OCR task detail: %w", err)
	}
	recDetail, err := findRecognitionDetail(taskDetail)
	if err != nil {
		return nil, err
	}

	boxes, err := collectOCRBoxesFromDetail(recDetail)
	if err != nil {
		return nil, err
	}
	return boxes, nil
}

func findRecognitionDetail(task *maa.TaskDetail) (*maa.RecognitionDetail, error) {
	if task == nil {
		return nil, fmt.Errorf("nil maa task detail")
	}
	for i := len(task.Nodes) - 1; i >= 0; i-- {
		nodeDetail, err := task.Nodes[i].GetDetail()
		if err != nil || nodeDetail == nil {
			continue
		}
		if nodeDetail.Recognition != nil {
			return nodeDetail.Recognition, nil
		}
	}
	return nil, fmt.Errorf("maa OCR recognition detail not found")
}

func collectOCRBoxesFromDetail(detail *maa.RecognitionDetail) ([]OCRTextBox, error) {
	if detail == nil {
		return nil, fmt.Errorf("maa OCR detail is nil")
	}
	if detail.Results == nil {
		return nil, fmt.Errorf("maa OCR result set is empty")
	}

	out := make([]OCRTextBox, 0, len(detail.Results.All))
	appendOCR := func(item *maa.RecognitionResult) {
		if item == nil {
			return
		}
		r, ok := item.AsOCR()
		if !ok || r == nil {
			return
		}
		t := strings.TrimSpace(r.Text)
		if t == "" {
			return
		}

		x := r.Box[0]
		y := r.Box[1]
		w := r.Box[2]
		h := r.Box[3]
		if w <= 0 || h <= 0 {
			return
		}

		out = append(out, OCRTextBox{
			Text:  t,
			Box:   [4]int{x, y, x + w, y + h},
			Score: r.Score,
		})
	}

	for _, item := range detail.Results.All {
		appendOCR(item)
	}
	if len(out) == 0 {
		appendOCR(detail.Results.Best)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("maa OCR produced no text")
	}
	return out, nil
}

func normalizeROIToMAARect(roi [4]float64, w, h int) (maa.Rect, bool) {
	if w <= 1 || h <= 1 {
		return maa.Rect{}, false
	}

	x1 := int(clamp01(roi[0]) * float64(w))
	y1 := int(clamp01(roi[1]) * float64(h))
	x2 := int(clamp01(roi[2]) * float64(w))
	y2 := int(clamp01(roi[3]) * float64(h))

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
		return maa.Rect{}, false
	}

	return maa.Rect{x1, y1, x2 - x1, y2 - y1}, true
}

func initMAARuntimeForOCR() error {
	libDir := strings.TrimSpace(os.Getenv("SSM_MAA_LIB_DIR"))
	return ensureMaaInit(libDir)
}

// CheckOCRModels returns true if the MAA OCR model files can be found without
// initialising the backend.  Use this to decide whether to download models.
func CheckOCRModels() bool {
	_, _, cl, err := resolveMAAOCRModel()
	if cl != nil {
		cl()
	}
	return err == nil
}

// DefaultOCRModelDir is the preferred local directory for MAA OCR model files.
const DefaultOCRModelDir = "./maacontrol/resource/model/ocr"

func resolveMAAOCRModel() (modelDir string, modelName string, cleanup func(), err error) {
	envDir := strings.TrimSpace(os.Getenv("SSM_MAA_OCR_MODEL_DIR"))
	envModel := strings.TrimSpace(os.Getenv("SSM_MAA_OCR_MODEL_NAME"))

	if envDir != "" {
		if envModel != "" {
			if !isDir(envDir) {
				return "", "", nil, fmt.Errorf("SSM_MAA_OCR_MODEL_DIR is not a directory: %q", envDir)
			}
			return envDir, envModel, nil, nil
		}

		dir, cl, err := normalizeMAAModelRootDir(envDir)
		if err != nil {
			return "", "", nil, err
		}
		return dir, "", cl, nil
	}

	candidates := []string{
		"./maacontrol/resource/model/ocr",
		"./resource/model/ocr",
		"./model/ocr",
		"./paddle_weights",
		"./go-ocr/paddle_weights",
		"./models/go-ocr/paddle_weights",
		"./ocr/paddle_weights",
	}
	for _, candidate := range candidates {
		dir, cl, err := normalizeMAAModelRootDir(candidate)
		if err == nil {
			return dir, "", cl, nil
		}
	}

	return "", "", nil, fmt.Errorf("MAA OCR model not found; set SSM_MAA_OCR_MODEL_DIR (and optional SSM_MAA_OCR_MODEL_NAME), or place det.onnx/rec.onnx/keys.txt under model/ocr")
}

func normalizeMAAModelRootDir(dir string) (modelDir string, cleanup func(), err error) {
	if !isDir(dir) {
		return "", nil, fmt.Errorf("OCR model directory not found: %q", dir)
	}

	det := filepath.Join(dir, "det.onnx")
	rec := filepath.Join(dir, "rec.onnx")
	keys := filepath.Join(dir, "keys.txt")
	if fileExists(det) && fileExists(rec) && fileExists(keys) {
		return dir, nil, nil
	}

	dict := filepath.Join(dir, "dict.txt")
	if fileExists(det) && fileExists(rec) && fileExists(dict) {
		tmp, err := os.MkdirTemp("", "ssm-maa-ocr-dir-*")
		if err != nil {
			return "", nil, err
		}

		if err := copyFile(det, filepath.Join(tmp, "det.onnx")); err != nil {
			_ = os.RemoveAll(tmp)
			return "", nil, err
		}
		if err := copyFile(rec, filepath.Join(tmp, "rec.onnx")); err != nil {
			_ = os.RemoveAll(tmp)
			return "", nil, err
		}
		if err := copyFile(dict, filepath.Join(tmp, "keys.txt")); err != nil {
			_ = os.RemoveAll(tmp)
			return "", nil, err
		}

		cleanup = func() {
			_ = os.RemoveAll(tmp)
		}
		return tmp, cleanup, nil
	}

	return "", nil, fmt.Errorf("OCR model files not found in %q: need det.onnx + rec.onnx + keys.txt (or dict.txt)", dir)
}

func isDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
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
