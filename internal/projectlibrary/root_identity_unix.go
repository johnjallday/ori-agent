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
func directoryIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrUnavailable
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
