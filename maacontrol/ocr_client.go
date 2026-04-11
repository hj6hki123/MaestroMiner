// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package maacontrol

import (
	"fmt"
	"strings"
	"sync"
)

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
