//go:build darwin || linux || freebsd

package projectlibrary

import (
	"fmt"
	"os"
	"syscall"
)

// directoryIdentity is a persistent, volume-scoped identity. Refuse platforms
// where it is unavailable rather than falling back to a path or modification
// time (which can be reused by a replacement directory).
//
// It is the device and inode number, so it is only as unique as the filesystem
// makes inode numbers. APFS never reuses one. ext4 and tmpfs can give a
// directory made straight after another was deleted the same number, and such
// a replacement is then not told apart from the directory it replaced. The
// directory's birth time would close that; it is not read here, and stored
// identities would have to stay comparable if it were added.
func directoryIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrUnavailable
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
