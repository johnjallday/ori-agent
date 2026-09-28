package workspace

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// CheckContinuityFilesCoverage compares the physical workspace-owned files/
// tree with the reviewed manifest without opening any file content or following
// links. It does not claim that listed assets have been installed: the modern
// importer still needs a bounded streaming, receipt-owned file publisher.
// Missing, extra and empty unrepresented directories are incomplete, not an
// invitation to fall back to legacy registration or look outside the tree.
func CheckContinuityFilesCoverage(ctx context.Context, folder string, files []workspacecontinuity.Fingerprint) error {
	if len(files) > workspacecontinuity.MaxFiles {
		return workspacecontinuity.ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	declared := make(map[string]bool)
	directories := make(map[string]bool)
	for _, file := range files {
		if file.Path == FilesDir {
			return workspacecontinuity.ErrInvalid
		}
		if !strings.HasPrefix(file.Path, FilesDir+"/") {
			continue
		}
		if declared[file.Path] {
			return workspacecontinuity.ErrInvalid
		}
		declared[file.Path] = true
		parent := path.Dir(file.Path)
		for parent != FilesDir {
			if parent == "." || parent == "/" || len(parent) >= len(file.Path) {
				return workspacecontinuity.ErrInvalid
			}
			directories[parent] = true
			parent = path.Dir(parent)
		}
	}
	stack := []string{FilesDir}
	visited := 0
	for len(stack) != 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := workspacecontinuity.ListCanonicalDirectory(ctx, folder, name, workspacecontinuity.MaxFiles)
		if errors.Is(err, workspacecontinuity.ErrIncomplete) && name == FilesDir && len(declared) == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			visited++
			if visited > workspacecontinuity.MaxFiles {
				return workspacecontinuity.ErrLimit
			}
			location := name + "/" + entry.Name()
			if entry.Type()&os.ModeSymlink != 0 {
				return workspacecontinuity.ErrUnsafe
			}
			if entry.IsDir() {
				if !directories[location] {
					return workspacecontinuity.ErrIncomplete
				}
				delete(directories, location)
				stack = append(stack, location)
				continue
			}
			if entry.Type()&os.ModeType != 0 {
				return workspacecontinuity.ErrUnsafe
			}
			if !declared[location] {
				return workspacecontinuity.ErrIncomplete
			}
			delete(declared, location)
		}
	}
	if len(declared) != 0 || len(directories) != 0 {
		return workspacecontinuity.ErrIncomplete
	}
	return ctx.Err()
}
