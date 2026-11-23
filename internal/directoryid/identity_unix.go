//go:build unix

package directoryid

import (
	"fmt"
	"os"
	"syscall"
)

func Witness(info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil || st.Ino == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
