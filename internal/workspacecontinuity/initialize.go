package workspacecontinuity

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

const initializationPrefix = ".continuity-init-"

// Build just the fixed ownership marker in a private sibling, then publish the
// directory with an exclusive rename. A crash cannot strand an empty final
// directory that would be indistinguishable from somebody else's collision.
// Unpublished initialization directories contain NO history or configuration.
func initializeManaged(ori *os.Root) (*os.Root, error) {
	name := initializationPrefix + uuid.NewString()
	if err := ori.Mkdir(name, 0o750); err != nil {
		return nil, err
	}
	root, err := openDirectory(ori, name)
	if err != nil {
		return nil, ErrUnsafe
	}
	defer func() { _ = root.Close() }()
	published, markerCreated := false, false
	defer func() {
		if !published {
			// Only these two newly allocated objects are owned by this attempt.
			// No recursive deletion: an unexpected injected entry stays intact.
			if markerCreated {
				_ = root.Remove("format.json")
			}
			_ = ori.Remove(name)
		}
	}()
	file, err := root.OpenFile("format.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	markerCreated = true
	_, writeErr := io.WriteString(file, formatMarker)
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return nil, err
	}
	if err := syncDirectory(root); err != nil {
		return nil, err
	}
	if err := renameNewDirectory(ori, name, "continuity"); err != nil {
		return nil, ErrCollision
	}
	published = true
	if err := syncDirectory(ori); err != nil {
		return nil, err
	}
	return openDirectory(ori, "continuity")
}

func hasUnpublishedInitialization(workspace *os.Root) (bool, error) {
	ori, err := openDirectory(workspace, ".ori")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, ErrUnsafe
	}
	defer func() { _ = ori.Close() }()
	file, err := ori.Open(".")
	if err != nil {
		return false, ErrUnsafe
	}
	defer func() { _ = file.Close() }()
	count := 0
	for {
		entries, err := file.ReadDir(256)
		if err != nil && err != io.EOF {
			return false, ErrUnsafe
		}
		for _, entry := range entries {
			count++
			if count > MaxFiles {
				return false, ErrLimit
			}
			if strings.HasPrefix(entry.Name(), initializationPrefix) && validGeneration(strings.TrimPrefix(entry.Name(), initializationPrefix)) {
				return true, nil
			}
		}
		if err == io.EOF {
			return false, nil
		}
	}
}

func validInitializationRename(from, to string) bool {
	return to == "continuity" && strings.HasPrefix(from, initializationPrefix) && validGeneration(strings.TrimPrefix(from, initializationPrefix))
}
