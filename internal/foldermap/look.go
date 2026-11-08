package foldermap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// LookEntry is one entry a targeted look reports, with the host's own
// recognition facts. Names only: never a path, never a file's contents.
type LookEntry struct {
	Name   string             `json:"name"`
	Kind   string             `json:"kind"` // "folder" or "file"
	Marker string             `json:"marker,omitempty"`
	Label  string             `json:"label,omitempty"`
	Shape  folderdigest.Shape `json:"shape,omitempty"`
}

// LookResult is what one targeted look returns to the agent.
type LookResult struct {
	Folder  string      `json:"folder"`
	State   State       `json:"state"`
	Entries []LookEntry `json:"entries"`
	// More says the folder holds more than LookMaxEntries entries; only that
	// many are listed (sorted by name) and the folder is not recorded.
	More bool `json:"more,omitempty"`
	// Project is the marker that makes this folder a project, if any.
	Project string `json:"project,omitempty"`
}

// ErrNotInMap refuses a look at a folder the walk never recorded: the agent
// opens folders the map knows, never a path of its own.
var ErrNotInMap = errors.New("foldermap: that folder is not in the map")

// Look lists one folder of the map for the agent (PRD FR13): at most
// LookMaxEntries entries with a more marker, the walk's exclusions, metadata
// only. A folder it lists in full is recorded into the map exactly as the
// walk would record it (its project marker closes it; otherwise its
// subfolders join the map as unopened). rel is relative to root and must
// name a folder the map already holds.
func Look(root string, m *Map, rel string) (LookResult, error) {
	if err := checkRoot(root); err != nil {
		return LookResult{}, err
	}
	if m == nil {
		return LookResult{}, ErrNotInMap
	}
	cleaned, err := CleanRel(rel)
	if err != nil {
		return LookResult{}, err
	}
	known, ok := m.Folder(cleaned)
	if !ok {
		return LookResult{}, ErrNotInMap
	}
	dir, err := realFolder(root, cleaned)
	if err != nil {
		return LookResult{}, err
	}
	entries, more, err := readDirFirst(dir, LookMaxEntries)
	if err != nil {
		return LookResult{}, err
	}
	result := LookResult{Folder: cleaned, State: known.State, More: more}
	for _, entry := range entries {
		name := entry.Name()
		isLink := entry.Type()&os.ModeSymlink != 0
		isDir := entry.IsDir()
		var marker folderdigest.Marker
		matched := false
		if !isLink {
			marker, matched = folderdigest.MatchMarker(name, isDir)
		}
		hidden := strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".icloud")
		switch {
		case isLink:
			continue
		case hidden && !matched:
			continue
		case isDir && folderdigest.IsSkippedFolder(name):
			continue
		}
		item := LookEntry{Name: name, Kind: "file"}
		if isDir {
			item.Kind = "folder"
		}
		if matched {
			item.Marker, item.Label, item.Shape = marker.Name, marker.Label, marker.Shape
		}
		result.Entries = append(result.Entries, item)
	}
	if !more && known.State != StateClosed {
		// Record it as the walk would. A look has no entry budget of its own
		// beyond LookMaxEntries; the map's folder bound still applies.
		w := newWalker(root, m, Options{})
		f, err := w.list(cleaned)
		if err != nil {
			return LookResult{}, err
		}
		m.put(f)
		result.State = f.State
	}
	if f, ok := m.Folder(cleaned); ok && f.Marker != "" {
		result.Project = f.Marker
	}
	return result, nil
}

// realFolder resolves rel under root, refusing any segment that is a link or
// not a directory, so a folder swapped for a link since the walk is never
// followed out of the fed folder.
func realFolder(root, rel string) (string, error) {
	dir := root
	if rel == "" {
		return dir, nil
	}
	for segment := range strings.SplitSeq(rel, "/") {
		dir = filepath.Join(dir, segment)
		info, err := os.Lstat(dir)
		if err != nil {
			return "", fmt.Errorf("foldermap: %s is gone", rel)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("foldermap: %s is not a folder inside the fed folder", rel)
		}
	}
	return dir, nil
}

// readDirFirst lists at most limit entries, sorted by name, and says whether
// there were more.
func readDirFirst(dir string, limit int) ([]os.DirEntry, bool, error) {
	f, err := os.Open(dir) // #nosec G304 -- dir is resolved under the fed folder by realFolder, every segment Lstat-checked
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	more := len(entries) > limit
	if more {
		entries = entries[:limit]
	}
	sortEntries(entries)
	return entries, more, nil
}

func sortEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
}
