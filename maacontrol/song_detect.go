// Copyright (C) 2026 kvarenzn
// SPDX-License-Identifier: GPL-3.0-or-later

package maacontrol

import (
	"fmt"

	ssmadb "github.com/kvarenzn/ssm/adb"
)

// DetectSongForRun is the single entry used by run flow:
// 1) use navigator (if available), otherwise
// 2) capture one screenshot from ADB and detect directly.
func DetectSongForRun(nav *Navigator, mode string, device *ssmadb.Device, titleROI, songROI ROI) (SongDetectResult, error) {
	if nav != nil {
		return nav.DetectSong(mode)
	}
	if device == nil {
		return SongDetectResult{}, fmt.Errorf("auto song detection requires ADB backend")
	}
	pngData, err := device.ScreencapPNGBytes()
	if err != nil {
		return SongDetectResult{}, fmt.Errorf("screencap failed: %w", err)
	}
	return DetectSongFromPNG(mode, pngData, titleROI, songROI)
}
