package workspacecontinuity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

// openDirectory walks one component at a time using confined handles. Lstat and
// handle identity checks reject static symlinks and substitution during opening;
// os.Root prevents an attacker from escaping confinement even during a race.
func openDirectory(root *os.Root, path string) (*os.Root, error) {
	if !safeRelativePath(path) {
		return nil, ErrUnsafe
	}
	current := root
	owned := false
	for _, part := range strings.Split(path, "/") {
		before, err := current.Lstat(part)
		if err != nil {
			if owned {
				_ = current.Close()
			}
			return nil, err
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			if owned {
				_ = current.Close()
			}
			return nil, ErrUnsafe
		}
		next, err := current.OpenRoot(part)
		if err == nil {
			opened, statErr := next.Stat(".")
			after, pathErr := current.Lstat(part)
			if statErr != nil || pathErr != nil || !os.SameFile(before, opened) || !os.SameFile(before, after) || after.Mode()&os.ModeSymlink != 0 {
				_ = next.Close()
				err = ErrChanged
			}
		}
		if owned {
			_ = current.Close()
		}
		if err != nil {
			return nil, err
		}
		current, owned = next, true
	}
	return current, nil
}

func openRegular(root *os.Root, path string) (*os.File, error) {
	if !safeRelativePath(path) {
		return nil, ErrUnsafe
	}
	parent := root
	name := path
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		var err error
		parent, err = openDirectory(root, path[:index])
		if err != nil {
			return nil, err
		}
		defer func() { _ = parent.Close() }()
		name = path[index+1:]
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, ErrUnsafe
	}
	file, err := openReadFile(parent, name)
	if err != nil {
		return nil, ErrUnsafe
	}
	opened, statErr := file.Stat()
	after, pathErr := parent.Lstat(name)
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, ErrChanged
	}
	return file, nil
}

// copyVerified streams private bytes. A caller writing a destination must use a
// private staging file and publish it only after success; a digest is known only
// at EOF. No linked file or arbitrary external path is opened.
func copyVerified(ctx context.Context, root *os.Root, path, digest string, size, limit int64, destination io.Writer) error {
	if !validDigest(digest) || size < 0 || size > limit {
		return ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := openRegular(root, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrIncomplete
		}
		return ErrUnsafe
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil || before.Size() != size {
		return ErrChanged
	}
	hash := sha256.New()
	writer := io.MultiWriter(destination, hash)
	remaining := size
	buffer := make([]byte, 64<<10)
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := min(int64(len(buffer)), remaining)
		n, err := file.Read(buffer[:length])
		if n > 0 {
			if _, writeErr := writer.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			remaining -= int64(n)
		}
		if err != nil && (err != io.EOF || remaining != 0) {
			return ErrIncomplete
		}
	}
	var extra [1]byte
	n, readErr := file.Read(extra[:])
	after, statErr := file.Stat()
	if n != 0 || readErr != io.EOF || statErr != nil || after.Size() != size || !before.ModTime().Equal(after.ModTime()) {
		return ErrChanged
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return ErrDigest
	}
	return ctx.Err()
}
