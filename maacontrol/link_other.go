//go:build !windows

package maacontrol

import "os"

// tryLinkDir creates a symlink from dst pointing at src.
func tryLinkDir(src, dst string) error {
	return os.Symlink(src, dst)
}
