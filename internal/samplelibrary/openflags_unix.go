//go:build unix

package samplelibrary

import (
	"os"
	"syscall"
)

func openWriteNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openReadNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0) // #nosec G304 -- path is component-revalidated beneath an exact reviewed root.
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
