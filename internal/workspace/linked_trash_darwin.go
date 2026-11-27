package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Trash exactly one entry relative to its held parent. Never traverse that
// path again, overwrite a name in Trash, copy across volumes or unlink.
func trashLinkedItem(parent *os.Root, name string, version SourceVersion, folder bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trashPath := filepath.Join(home, ".Trash")
	if err := os.Mkdir(trashPath, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		if errors.Is(err, os.ErrPermission) {
			return errTrashDirectoryDenied
		}
		return err
	}
	trash, err := os.OpenFile(trashPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0) // #nosec G304 G703 -- fixed Trash under the current user's home
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return errTrashDirectoryDenied
		}
		return err
	}
	defer func() { _ = trash.Close() }()
	return renameLinkedItem(parent, name, trash, version, folder, true)
}

func moveLinkedItem(parent *os.Root, name string, destination *os.Root, version SourceVersion, folder bool) error {
	to, err := destination.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	return renameLinkedItem(parent, name, to, version, folder, false)
}

func renameLinkedItem(parent *os.Root, name string, destination *os.File, version SourceVersion, folder, disambiguate bool) error {
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	srcfd, destfd := int(dir.Fd()), int(destination.Fd()) // #nosec G115 -- open Darwin file descriptors fit int
	// Hold the selected entry across the rename so its inode cannot be reused.
	matches := func(info os.FileInfo) bool {
		return info != nil && (folder && info.IsDir() || !folder && info.Mode().IsRegular()) && sourceVersion(info) == version
	}
	file, err := parent.OpenFile(name, unix.O_EVTONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return err
		}
		return ErrSourceChanged
	}
	defer func() { _ = file.Close() }()
	seen, err := file.Stat()
	if err != nil || !matches(seen) {
		return ErrSourceChanged
	}
	var held unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &held); err != nil { // #nosec G115 -- open Darwin descriptor
		return err
	}
	ext := filepath.Ext(name)
	if folder {
		ext = ""
	}
	for i := 0; i < 1000; i++ {
		target := name
		if i > 0 {
			target = fmt.Sprintf("%s (%d)%s", name[:len(name)-len(ext)], i, ext)
		}
		current, err := parent.Lstat(name)
		if err != nil || !matches(current) {
			return ErrSourceChanged
		}
		err = unix.RenameatxNp(srcfd, name, destfd, target, unix.RENAME_EXCL)
		if disambiguate && errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return err
		}
		var moved unix.Stat_t
		if err := unix.Fstatat(destfd, target, &moved, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if moved.Dev != held.Dev || moved.Ino != held.Ino {
			// An external writer replaced the entry during rename. Put it back
			// only if the original name is still vacant; never destroy either.
			if err := unix.RenameatxNp(destfd, target, srcfd, name, unix.RENAME_EXCL); err != nil {
				return err
			}
			return ErrSourceChanged
		}
		return nil
	}
	return os.ErrExist
}
