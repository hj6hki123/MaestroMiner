// Copyright (C) 2026 hj6hki123
// SPDX-License-Identifier: GPL-3.0-or-later

package maacontrol

import (
	"os/exec"
	"path/filepath"

	"github.com/kvarenzn/ssm/log"
)

// tryLinkDir attempts to create a directory junction (no privileges required on
// Windows) from dst pointing to src. Returns nil on success. Falls back to
// nil-with-warning so the caller can proceed to copyDir.
func tryLinkDir(src, dst string) error {
	// os.Symlink for directories requires SeCreateSymbolicLinkPrivilege on
	// Windows. Junction points ("mklink /J") need no special rights at all.
	src = filepath.FromSlash(src)
	dst = filepath.FromSlash(dst)
	out, err := exec.Command("cmd", "/c", "mklink", "/J", dst, src).CombinedOutput()
	if err != nil {
		log.Debugf("[buildMergedBundle] junction failed (%v: %s) – copying", err, out)
	}
	return err
}
