//go:build unix

package downloadsjanitor

import (
	"fmt"
	"os"
	"syscall"
)

// platformFileID returns the device and inode numbers identifying this exact
// file. It is what distinguishes a file replaced in place — same name, same
// size, same timestamp — from the file the user actually approved.
//
// An empty result is valid: fingerprint matching then relies on name, size, and
// modification time alone.
func platformFileID(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", uint64(stat.Dev), stat.Ino) // #nosec G115 -- device numbers are platform-sized; the value is only ever compared as a string
}
