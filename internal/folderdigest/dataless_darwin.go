//go:build darwin

package folderdigest

import (
	"os"
	"syscall"
)

// sfDataless is the BSD file flag macOS sets on a file whose contents live
// only in the cloud (an evicted iCloud Drive item). Reading such a file would
// trigger a download; the scan never opens files, but counting one as a
// zero-byte file would still misdescribe the folder.
const sfDataless = 0x40000000

// isDataless reports whether the entry is a zero-size cloud placeholder.
func isDataless(info os.FileInfo) bool {
	if info == nil || info.Size() != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return false
	}
	return isDatalessSys(st)
}

func isDatalessSys(st *syscall.Stat_t) bool {
	return st.Flags&sfDataless != 0
}
