// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package maacontrol

import "os"

// tryLinkDir creates a symlink from dst pointing at src.
func tryLinkDir(src, dst string) error {
	return os.Symlink(src, dst)
}
