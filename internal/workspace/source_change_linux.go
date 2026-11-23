package workspace

import (
	"os"
	"syscall"
)

func sourceChange(info os.FileInfo) (int64, uint64) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s == nil {
		return 0, 0
	}
	return s.Ctim.Nano(), uint64(s.Nlink)
}
