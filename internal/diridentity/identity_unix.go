//go:build darwin || linux || freebsd

// Package diridentity identifies an observed directory without opening it again.
package diridentity

import (
	"fmt"
	"os"
	"syscall"
)

// Of returns the same device/inode witness used by the library's pinned scans.
// It is only as unique as the filesystem's inode allocation policy.
func Of(info os.FileInfo) string {
	if info != nil && info.IsDir() {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
		}
	}
	return ""
}
