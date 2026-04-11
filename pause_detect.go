// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sync"

	"github.com/kvarenzn/ssm/controllers"
	xdraw "golang.org/x/image/draw"
)

const pauseTemplatePath = "assets/live/button/pause.png"

// scaleFactors are the template resize ratios tried during multi-scale matching.
var scaleFactors = []float64{0.5, 0.75, 1.0, 1.25, 1.5, 2.0}

type scaledTmpl struct {
	zm     []float64 // zero-mean pixel values
	w, h   int
	stddev float64
}

var pauseTemplateState struct {
	once   sync.Once
	mu     sync.RWMutex
	scales []scaledTmpl // one entry per scaleFactors element
}

// LoadPauseTemplate loads and precomputes all scaled variants of the reference image.
func LoadPauseTemplate() error {
	var retErr error
	pauseTemplateState.once.Do(func() {
		f, err := os.Open(pauseTemplatePath)
		if err != nil {
			retErr = fmt.Errorf("pause template: %w", err)
			return
		}
		defer f.Close()

		img, _, err := image.Decode(f)
		if err != nil {
			retErr = fmt.Errorf("pause template decode: %w", err)
			return
		}

		b := img.Bounds()
		origW, origH := b.Dx(), b.Dy()
		if origW <= 0 || origH <= 0 {
			retErr = fmt.Errorf("pause template: empty image")
			return
		}

		// Convert to grayscale image once.
		srcGray := image.NewGray(image.Rect(0, 0, origW, origH))
		for y := 0; y < origH; y++ {
			for x := 0; x < origW; x++ {
				c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
				srcGray.SetGray(x, y, c)
			}
		}

		scales := make([]scaledTmpl, 0, len(scaleFactors))
		for _, sf := range scaleFactors {
			st, ok := buildScaledTmpl(srcGray, origW, origH, sf)
			if ok {
				scales = append(scales, st)
			}
		}
		if len(scales) == 0 {
			retErr = fmt.Errorf("pause template: all scales produced uniform images")
			return
		}

		pauseTemplateState.mu.Lock()
		pauseTemplateState.scales = scales
		pauseTemplateState.mu.Unlock()
	})
	return retErr
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
		return scaledTmpl{}, false // uniform image at this scale, skip
	}
	return scaledTmpl{zm: zm, w: w, h: h, stddev: std}, true
}

// MatchPauseButton slides each scaled template across the frame ROI and returns
// the peak NCC score across all scales. loaded is false when no template exists.
func MatchPauseButton(f controllers.ScrcpyFrame, roi navROI) (ncc float64, loaded bool) {
	pauseTemplateState.mu.RLock()
	scales := pauseTemplateState.scales
	pauseTemplateState.mu.RUnlock()

	if len(scales) == 0 {
		return 0, false
	}

	if f.Width <= 0 || f.Height <= 0 || len(f.Plane0) < f.Width*f.Height {
		return 0, true
	}

	x1 := iclamp(int(roi.x1*float64(f.Width)), 0, f.Width)
	x2 := iclamp(int(roi.x2*float64(f.Width)), 0, f.Width)
	y1 := iclamp(int(roi.y1*float64(f.Height)), 0, f.Height)
	y2 := iclamp(int(roi.y2*float64(f.Height)), 0, f.Height)

	best := -1.0
	for i := range scales {
		s := scales[i].w
		t := scales[i].h
		if x2-x1 < s || y2-y1 < t {
			continue
		}
		score := slideNCC(f.Plane0, f.Width, x1, y1, x2, y2, &scales[i])
		if score > best {
			best = score
		}
	}
	return best, true
}

// slideNCC runs sliding-window NCC of tmpl over the frame ROI and returns the peak score.
func slideNCC(plane []byte, frameW, x1, y1, x2, y2 int, tmpl *scaledTmpl) float64 {
	tw, th := tmpl.w, tmpl.h
	fn := float64(tw * th)
	strideX := max(1, tw/4)
	strideY := max(1, th/4)
	best := -1.0

	for py := y1; py+th <= y2; py += strideY {
		for px := x1; px+tw <= x2; px += strideX {
			// Compute patch mean.
			var patchSum float64
			for ty := 0; ty < th; ty++ {
				row := (py+ty)*frameW + px
				for tx := 0; tx < tw; tx++ {
					patchSum += float64(plane[row+tx])
				}
			}
			patchMean := patchSum / fn

			// Compute cross-correlation and patch variance.
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
