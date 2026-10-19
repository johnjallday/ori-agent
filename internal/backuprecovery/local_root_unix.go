//go:build darwin || linux

package backuprecovery

import (
	"os"
	"syscall"
)

func localRootFileIdentity(info os.FileInfo) (uint64, uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Dev <= 0 || stat.Ino == 0 {
		return 0, 0, false
	}
	return uint64(stat.Dev), stat.Ino, true
}
