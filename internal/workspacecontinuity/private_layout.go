package workspacecontinuity

import (
	"io"
	"os"
	"runtime"
)

// Writers never reuse a publicly readable object and advertise it as private.
// Readers do not impose POSIX permission bits on copied Windows/removable media.
func privateFile(root *os.Root, path string) error {
	file, err := openRegular(root, path)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return ErrUnsafe
	}
	return nil
}

// AdoptManagedPermissions makes a copied checkpoint subtree private again
// (directories 0750, files 0600) after a reviewed import has attached its
// folder here. Plain copies and removable media commonly widen these modes,
// which would otherwise stop this installation from ever preparing the folder.
// Links and unexpected entry types are refused, never followed.
func AdoptManagedPermissions(workspaceDirectory string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	workspace, err := os.OpenRoot(workspaceDirectory)
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = workspace.Close() }()
	managed, err := openDirectory(workspace, Directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return ErrUnsafe
	}
	defer func() { _ = managed.Close() }()
	var walk func(dir *os.Root, depth int) error
	walk = func(dir *os.Root, depth int) error {
		if depth > 2 {
			return ErrUnsafe
		}
		if err := dir.Chmod(".", 0o750); err != nil {
			return err
		}
		file, err := dir.Open(".")
		if err != nil {
			return ErrUnsafe
		}
		entries, readErr := file.ReadDir(-1)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return ErrUnsafe
		}
		for _, entry := range entries {
			switch {
			case entry.Type()&os.ModeSymlink != 0:
				return ErrUnsafe
			case entry.IsDir():
				child, err := openDirectory(dir, entry.Name())
				if err != nil {
					return ErrUnsafe
				}
				err = walk(child, depth+1)
				_ = child.Close()
				if err != nil {
					return err
				}
			case entry.Type().IsRegular():
				if err := dir.Chmod(entry.Name(), 0o600); err != nil {
					return err
				}
			default:
				return ErrUnsafe
			}
		}
		return nil
	}
	return walk(managed, 0)
}

func verifyManagedLayout(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o007 != 0 {
		return ErrUnsafe
	}
	file, err := root.Open(".")
	if err != nil {
		return ErrUnsafe
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(6) // at most the five reserved names are valid
	if err != nil && err != io.EOF {
		return ErrUnsafe
	}
	if len(entries) > 5 {
		return ErrCollision
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "format.json", "current.json":
			if err := privateFile(root, entry.Name()); err != nil {
				return err
			}
		case "generations", "objects", "staging":
			info, err := root.Lstat(entry.Name())
			if err != nil || !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0o007 != 0 {
				return ErrUnsafe
			}
		default:
			return ErrCollision
		}
	}
	return nil
}
