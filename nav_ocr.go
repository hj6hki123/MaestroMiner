// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/kvarenzn/ssm/adb"
	"github.com/kvarenzn/ssm/controllers"
	"github.com/kvarenzn/ssm/gui"
	"github.com/kvarenzn/ssm/maacontrol"
)

// navROI defines a rectangular region of interest as normalized fractions [0,1] of frame dimensions.
type navROI struct{ x1, y1, x2, y2 float64 }

// Predefined ROIs derived from BanG Dream / PJSK screenshot analysis.
var (
	defaultRoiSongNameBang = navROI{0.23, 0.46, 0.47, 0.50}
	defaultRoiSongNamePjsk = navROI{0.59, 0.46, 0.85, 0.52}

	defaultRoiPageTitleBang = navROI{0.06, 0.04, 0.28, 0.12}
	defaultRoiPageTitlePjsk = navROI{0.06, 0.04, 0.28, 0.12}

	defaultRoiDifficultyRowBang = navROI{0.59, 0.68, 0.88, 0.80}
	defaultRoiDifficultyRowPjsk = navROI{0.60, 0.60, 0.90, 0.76}

	defaultRoiKetteiBang = navROI{0.76, 0.83, 0.92, 0.92}
	defaultRoiKetteiPjsk = navROI{0.68, 0.76, 0.80, 0.87}

	defaultRoiLiveStartBang = navROI{0.72, 0.72, 0.89, 0.94}
	defaultRoiLiveStartPjsk = navROI{0.72, 0.71, 0.76, 0.81}

	defaultRoiDialogTitleBang = navROI{0.33, 0.12, 0.47, 0.20}
	defaultRoiDialogTitlePjsk = navROI{0.26, 0.22, 0.43, 0.32}

	defaultRoiDialogOKBang = navROI{0.50, 0.76, 0.66, 0.87}
	defaultRoiDialogOKPjsk = navROI{0.50, 0.69, 0.66, 0.81}

	defaultRoiBandSongInfoBang = navROI{0.24, 0.60, 0.80, 0.88}
	defaultRoiBandSongInfoPjsk = navROI{0.24, 0.60, 0.80, 0.88}

	defaultRoiBandConfirmTapBang = navROI{0.72, 0.72, 0.89, 0.94}
	defaultRoiBandConfirmTapPjsk = navROI{0.72, 0.71, 0.76, 0.81}

	defaultRoiAlbumCoverBang = navROI{0.00, 0.00, 1.00, 1.00}
	defaultRoiAlbumCoverPjsk = navROI{0.05, 0.43, 0.53, 0.96}

	// roiPauseButton covers the in-game pause button area.
	defaultRoiPauseButtonBang = navROI{0.91, 0.00, 0.98, 0.14}
	defaultRoiPauseButtonPjsk = navROI{0.91, 0.00, 0.98, 0.14}

	roiFullScreen = navROI{0.00, 0.00, 1.00, 1.00}

	roiPageTitle      = defaultRoiPageTitleBang
	roiDifficultyRow  = defaultRoiDifficultyRowBang
	roiKettei         = defaultRoiKetteiBang
	roiLiveStart      = defaultRoiLiveStartBang
	roiDialogTitle    = defaultRoiDialogTitleBang
	roiDialogOK       = defaultRoiDialogOKBang
	roiSongName       = defaultRoiSongNameBang
	roiBandSongInfo   = defaultRoiBandSongInfoBang
	roiBandConfirmTap = defaultRoiBandConfirmTapBang
	roiAlbumCover     = defaultRoiAlbumCoverBang
	roiPauseButton    = defaultRoiPauseButtonBang
)

func applyNavModeProfile(mode string) {
	if mode == "pjsk" {
		roiPageTitle = defaultRoiPageTitlePjsk
		roiDifficultyRow = defaultRoiDifficultyRowPjsk
		roiKettei = defaultRoiKetteiPjsk
		roiLiveStart = defaultRoiLiveStartPjsk
		roiDialogTitle = defaultRoiDialogTitlePjsk
		roiDialogOK = defaultRoiDialogOKPjsk
		roiBandSongInfo = defaultRoiBandSongInfoPjsk
		roiBandConfirmTap = defaultRoiBandConfirmTapPjsk
		roiAlbumCover = defaultRoiAlbumCoverPjsk
		roiPauseButton = defaultRoiPauseButtonPjsk
		return
	}
	roiPageTitle = defaultRoiPageTitleBang
	roiDifficultyRow = defaultRoiDifficultyRowBang
	roiKettei = defaultRoiKetteiBang
	roiLiveStart = defaultRoiLiveStartBang
	roiDialogTitle = defaultRoiDialogTitleBang
	roiDialogOK = defaultRoiDialogOKBang
	roiBandSongInfo = defaultRoiBandSongInfoBang
	roiBandConfirmTap = defaultRoiBandConfirmTapBang
	roiAlbumCover = defaultRoiAlbumCoverBang
	roiPauseButton = defaultRoiPauseButtonBang
}

func normalizePercentROI(roi gui.ROI, fallback navROI) navROI {
	if roi.X1 == 0 && roi.Y1 == 0 && roi.X2 == 0 && roi.Y2 == 0 {
		return fallback
	}
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > 100 {
			return 100
		}
		return v
	}
	x1, y1, x2, y2 := clamp(roi.X1), clamp(roi.Y1), clamp(roi.X2), clamp(roi.Y2)
	if x2 <= x1 {
		if x1 >= 99 {
			x1 = 98
		}
		x2 = x1 + 1
	}
	if y2 <= y1 {
		if y1 >= 99 {
			y1 = 98
		}
		y2 = y1 + 1
	}
	return navROI{
		x1: float64(x1) / 100.0,
		y1: float64(y1) / 100.0,
		x2: float64(x2) / 100.0,
		y2: float64(y2) / 100.0,
	}
}

func applyNavSongNameROI(mode string, bang, pjsk gui.ROI) {
	applyNavModeProfile(mode)
	if mode == "pjsk" {
		roiSongName = normalizePercentROI(pjsk, defaultRoiSongNamePjsk)
		return
	}
	roiSongName = normalizePercentROI(bang, defaultRoiSongNameBang)
}

// roiCenterPx returns the center pixel coordinates of the given ROI in frame space.
func roiCenterPx(f controllers.ScrcpyFrame, roi navROI) (int, int, bool) {
	if f.Width <= 0 || f.Height <= 0 {
		return 0, 0, false
	}
	x1 := iclamp(int(roi.x1*float64(f.Width)), 0, f.Width-1)
	x2 := iclamp(int(roi.x2*float64(f.Width)), 0, f.Width)
	y1 := iclamp(int(roi.y1*float64(f.Height)), 0, f.Height-1)
	y2 := iclamp(int(roi.y2*float64(f.Height)), 0, f.Height)
	if x2 <= x1 || y2 <= y1 {
		return 0, 0, false
	}
	return (x1 + x2) / 2, (y1 + y2) / 2, true
}

// roiPointPx returns a pixel coordinate at (xFrac, yFrac) within the given ROI.
// xFrac/yFrac are fractions [0,1] within the ROI's own extent.
func roiPointPx(f controllers.ScrcpyFrame, roi navROI, xFrac, yFrac float64) (int, int, bool) {
	if f.Width <= 0 || f.Height <= 0 {
		return 0, 0, false
	}
	x1 := roi.x1 * float64(f.Width)
	x2 := roi.x2 * float64(f.Width)
	y1 := roi.y1 * float64(f.Height)
	y2 := roi.y2 * float64(f.Height)
	x := iclamp(int(x1+xFrac*(x2-x1)), 0, f.Width-1)
	y := iclamp(int(y1+yFrac*(y2-y1)), 0, f.Height-1)
	return x, y, true
}

// sampleROILuma returns the average luma of pixels within the given ROI.
func sampleROILuma(f controllers.ScrcpyFrame, roi navROI) (float64, bool) {
	if f.Width <= 0 || f.Height <= 0 || len(f.Plane0) < f.Width*f.Height {
		return 0, false
	}
	x1 := iclamp(int(roi.x1*float64(f.Width)), 0, f.Width-1)
	x2 := iclamp(int(roi.x2*float64(f.Width)), 0, f.Width)
	y1 := iclamp(int(roi.y1*float64(f.Height)), 0, f.Height-1)
	y2 := iclamp(int(roi.y2*float64(f.Height)), 0, f.Height)
	if x2 <= x1 || y2 <= y1 {
		return 0, false
	}
	stepY := max(1, (y2-y1)/48)
	stepX := max(1, (x2-x1)/48)
	var sum int64
	count := 0
	for y := y1; y < y2; y += stepY {
		row := y * f.Width
		for x := x1; x < x2; x += stepX {
			sum += int64(f.Plane0[row+x])
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return float64(sum) / float64(count), true
}

func iclamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// detectDialogByLuma returns true if a dialog overlay is likely visible.
// Dialogs show a bright panel over a darkened game screen.
func detectDialogByLuma(f controllers.ScrcpyFrame) bool {
	overallLuma, ok1 := sampleROILuma(f, roiFullScreen)
	dialogLuma, ok2 := sampleROILuma(f, roiDialogTitle)
	if !ok1 || !ok2 {
		return false
	}
	return dialogLuma > 120 && dialogLuma-overallLuma > 35
}

// difficultyTapCoords returns the device-pixel tap point for difficulty selection
// given explicit image dimensions.  Used by both the scrcpy-based pipeline and
// the MAA navigator custom action.
func difficultyTapCoords(mode, diff string, w, h int) (x, y int, ok bool) {
	xNorm, yNorm := 0.0, 0.0
	if mode == "pjsk" {
		switch diff {
		case "easy":
			xNorm, yNorm = 0.64, 0.65
		case "normal":
			xNorm, yNorm = 0.69, 0.66
		case "hard":
			xNorm, yNorm = 0.74, 0.67
		case "expert":
			xNorm, yNorm = 0.79, 0.67
		case "special", "master":
			xNorm, yNorm = 0.84, 0.68
		case "append":
			// append 在第二頁，tap 位置等同 easy 的位置；翻頁箭頭由呼叫方處理
			xNorm, yNorm = 0.74, 0.67
		default:
			return 0, 0, false
		}
	} else {
		yNorm = 0.72
		switch diff {
		case "easy":
			xNorm = 0.62
		case "normal":
			xNorm = 0.69
		case "hard":
			xNorm = 0.76
		case "expert":
			xNorm = 0.82
		case "special", "master", "append":
			xNorm = 0.90
		default:
			return 0, 0, false
		}
	}
	x = iclamp(int(xNorm*float64(w)), 0, w-1)
	y = iclamp(int(yNorm*float64(h)), 0, h-1)
	return x, y, true
}

// difficultyTapPointPx returns the tap pixel for difficulty selection using
// full-screen normalized anchors (mode-specific), without relying on ROI.
func difficultyTapPointPx(f controllers.ScrcpyFrame, mode, diff string) (int, int, bool) {
	if f.Width <= 0 || f.Height <= 0 {
		return 0, 0, false
	}
	return difficultyTapCoords(mode, diff, f.Width, f.Height)
}

// pjskDifficultyPageArrowCoords returns the tap coordinate of the PJSK difficulty
// page-flip arrow given explicit image dimensions.
func pjskDifficultyPageArrowCoords(w, h int) (int, int) {
	x := iclamp(int(0.88*float64(w)), 0, w-1)
	y := iclamp(int(0.68*float64(h)), 0, h-1)
	return x, y
}

// pjskDifficultyPageArrowPx returns the tap coordinate of the PJSK difficulty
// page-flip arrow (→ next page button at ~88%,68%).
func pjskDifficultyPageArrowPx(f controllers.ScrcpyFrame) (int, int, bool) {
	if f.Width <= 0 || f.Height <= 0 {
		return 0, 0, false
	}
	x, y := pjskDifficultyPageArrowCoords(f.Width, f.Height)
	return x, y, true
}

// pjskIsOnDifficultyPage1ByOCR returns true when easy/normal difficulty labels
// are visible, i.e. we are on page 1 of the PJSK difficulty selector.
// Falls back to true (assume page 1) if the OCR engine is unavailable.
func pjskIsOnDifficultyPage1ByOCR(device *adb.Device) bool {
	_, _, found, _, err := difficultyTapPointByOCR(device, "pjsk", "easy")
	if err != nil {
		// OCR unavailable — assume page 1 to be safe
		return true
	}
	return found
}

func difficultyKeywords(mode, diff string) []string {
	switch diff {
	case "easy":
		return []string{"easy", "イージー", "簡単", "简单"}
	case "normal":
		return []string{"normal", "ノーマル", "普通"}
	case "hard":
		return []string{"hard", "ハード", "困難", "困难"}
	case "expert":
		return []string{"expert", "エキスパート", "專家", "专家"}
	case "special":
		if mode == "pjsk" {
			return []string{"master", "マスター"}
		}
		return []string{"special", "スペシャル"}
	case "master":
		if mode == "pjsk" {
			return []string{"master", "マスター"}
		}
		return []string{"special", "スペシャル", "master", "マスター"}
	case "append":
		return []string{"append", "アペンド"}
	default:
		return nil
	}
}

// difficultyTapPointByOCR finds the difficulty button location from OCR text boxes.
// It does not rely on roiDifficultyRow and taps the center of the best matched text box.
func difficultyTapPointByOCR(device *adb.Device, mode, diff string) (int, int, bool, string, error) {
	if device == nil {
		return 0, 0, false, "", fmt.Errorf("nil device")
	}
	keywords := difficultyKeywords(mode, diff)
	if len(keywords) == 0 {
		return 0, 0, false, "", fmt.Errorf("unsupported difficulty: %s", diff)
	}

	pngBytes, err := device.ScreencapPNGBytes()
	if err != nil {
		return 0, 0, false, "", err
	}
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return 0, 0, false, "", err
	}

	oc, err := maacontrol.GetOCRClient()
	if err != nil {
		return 0, 0, false, "", err
	}

	rawResults, err := oc.OCRWithBoxes(pngBytes, nil)
	if err != nil {
		return 0, 0, false, "", err
	}

	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return 0, 0, false, "", fmt.Errorf("invalid screenshot size")
	}

	bestScore := 0.0
	bestX, bestY := 0, 0
	bestText := ""
	for _, r := range rawResults {
		t := strings.TrimSpace(r.Text)
		if t == "" {
			continue
		}
		score := 0.0
		for _, kw := range keywords {
			if sc := fuzzyKeywordScore(t, kw); sc > score {
				score = sc
			}
		}
		if score <= 0 {
			continue
		}

		bx := r.Box
		if bx[2] <= bx[0] || bx[3] <= bx[1] {
			continue
		}
		cx := (bx[0] + bx[2]) / 2
		cy := (bx[1] + bx[3]) / 2

		xNorm := float64(cx) / float64(w)
		yNorm := float64(cy) / float64(h)
		if xNorm < 0.45 {
			score *= 0.80
		}
		if yNorm < 0.45 || yNorm > 0.90 {
			score *= 0.75
		}

		if score > bestScore {
			bestScore = score
			bestX = iclamp(cx, 0, w-1)
			bestY = iclamp(cy, 0, h-1)
			bestText = t
		}
	}

	if bestScore < 0.62 {
		return 0, 0, false, "", nil
	}
	return bestX, bestY, true, fmt.Sprintf("%s(%.2f)", bestText, bestScore), nil
}
