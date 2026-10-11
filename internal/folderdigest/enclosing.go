package folderdigest

import (
	"os"
	"path/filepath"
)

// EnclosingMaxLevels is how many folders above a selection are checked for a
// repository marker. Six covers a package nested inside a monorepo without
// turning into a walk of the disk.
const EnclosingMaxLevels = 6

// Enclosing says a selection sits inside a larger repository.
type Enclosing struct {
	// Levels is how many folders up the repository's root is (1 is the parent).
	Levels int
	// Name is that folder's base name, never its path.
	Name string
}

// EnclosingRepository reports the nearest folder above dir that holds a
// repository marker, so a card can explain that a selected subfolder is part
// of a larger project and ask the user to choose that folder instead.
//
// This is the one place the scan looks above a selection, and it is kept
// deliberately small: it asks only whether an entry with a repository marker's
// name exists in at most EnclosingMaxLevels ancestors. It lists no directory,
// opens nothing, follows no link, and stops before stop (the user's home
// folder) and before the filesystem root. Finding a marker grants nothing: the
// user must select that folder for anything to be read from it.
func EnclosingRepository(dir, stop string) (Enclosing, bool) {
	dir = filepath.Clean(dir)
	stop = filepath.Clean(stop)
	current := dir
	for level := 1; level <= EnclosingMaxLevels; level++ {
		parent := filepath.Dir(current)
		if parent == current || parent == stop || parent == string(filepath.Separator) {
			return Enclosing{}, false
		}
		for _, marker := range Markers {
			if !marker.Repository {
				continue
			}
			info, err := os.Lstat(filepath.Join(parent, marker.Name))
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if info.IsDir() || info.Mode().IsRegular() {
				return Enclosing{Levels: level, Name: filepath.Base(parent)}, true
			}
		}
		current = parent
	}
	return Enclosing{}, false
}
