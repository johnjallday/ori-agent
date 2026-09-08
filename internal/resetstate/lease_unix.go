//go:build darwin || linux

package resetstate

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func Supported() bool   { return true }
func noFollowFlag() int { return unix.O_NOFOLLOW | unix.O_NONBLOCK }

func privateEntry(info os.FileInfo, directory bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0o700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && stat.Nlink == 1
}

func lockExclusive(file *os.File) error {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrInUse
		}
		return ErrUnsafeState
	}
	return nil
}
