// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package maacontrol

import maa "github.com/MaaXYZ/maa-framework-go/v4"

// ─── Absolute-pixel OCR boxes (x, y, w, h) ───────────────────────────────────
// Calibrated for the default 1544×720 game layout.
// Adjust these if your device uses a different resolution.
var (
	songNameOCRBox       = maa.Rect{386, 327, 316, 33}
	liveBoostValueOCRBox = maa.Rect{1210, 24, 63, 23}
	playResultFieldBoxes = map[string]maa.Rect{
		"score":     {1100, 199, 165, 35},
		"max_combo": {1118, 365, 96, 37},
		"perfect":   {950, 281, 90, 31},
		"great":     {952, 320, 82, 32},
		"good":      {951, 361, 85, 28},
		"bad":       {950, 398, 84, 28},
		"miss":      {950, 432, 88, 27},
		"fast":      {1190, 284, 83, 38},
		"slow":      {1220, 312, 60, 39},
	}
)

// ─── Normalised ROIs (x1, y1, x2, y2 in [0, 1]) ──────────────────────────────
var (
	// SongTitleROI is the page-title area used to confirm the 楽曲選択 screen.
	// The bounding box is identical for both Bang Dream and PJSK.
	SongTitleROI = ROI{0.06, 0.04, 0.28, 0.12}

	// SongNameROIBang / SongNameROIPjsk cover the song-name panel on the
	// song-select screen for each game mode.
	SongNameROIBang = ROI{0.23, 0.46, 0.47, 0.51}
	SongNameROIPjsk = ROI{0.59, 0.46, 0.85, 0.52}

	// livePlayToggleROI is the liveplay-mode toggle button area (~309,630 on 1544×720).
	livePlayToggleROI = ROI{0.160, 0.840, 0.250, 0.910}
)
