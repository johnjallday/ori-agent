//go:build darwin

package wakeinstall

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openNoFollow(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open staged wake artifact without following links: %w", err)
	}
	return os.NewFile(uintptr(descriptor), path), nil
}
