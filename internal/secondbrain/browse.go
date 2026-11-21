package secondbrain

import (
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	BrowseMaxDepth   = 8
	browseMaxSeen    = 1024
	browseMaxEntries = 200
)

var ErrBrowsePath = errors.New("secondbrain: invalid notebook folder")

type BrowseEntry struct {
	Name   string `json:"name"`
	Folder bool   `json:"folder"`
}

type Directory struct {
	Entries []BrowseEntry `json:"entries"`
	Partial bool          `json:"partial,omitempty"`
}

// ListDirectory reads one directory's entries, never the contents of a note.
// root comes from the approved connection; parts are checked independently.
func ListDirectory(ctx context.Context, root string, parts []string) (Directory, error) {
	return listDirectory(ctx, root, parts, browseMaxSeen, browseMaxEntries)
}

func validBrowseName(name string) bool {
	return name != "" && len(name) <= 255 && utf8.ValidString(name) &&
		!strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "/\\:") &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func browseFolder(name string) bool {
	return !skippedFolders[strings.ToLower(name)]
}

func listDirectory(ctx context.Context, root string, parts []string, maxSeen, maxEntries int) (Directory, error) {
	if len(parts) > BrowseMaxDepth {
		return Directory{}, ErrBrowsePath
	}
	for _, part := range parts {
		if !validBrowseName(part) || !browseFolder(part) {
			return Directory{}, ErrBrowsePath
		}
	}
	if err := ctx.Err(); err != nil {
		return Directory{}, err
	}
	anchor, err := pinFolder(root)
	if err != nil {
		return Directory{}, err
	}
	defer func() { _ = anchor.Close() }()
	pinned := anchor
	for _, part := range parts {
		next, err := pinOneFolder(pinned, part, false)
		if err != nil {
			return Directory{}, err
		}
		pinned = next
		defer func() { _ = next.Close() }()
	}
	file, err := pinned.Open(".")
	if err != nil {
		return Directory{}, err
	}
	defer func() { _ = file.Close() }()
	out := Directory{Entries: []BrowseEntry{}}
	for seen := 0; seen < maxSeen; {
		if err := ctx.Err(); err != nil {
			return Directory{}, err
		}
		entries, err := file.ReadDir(min(64, maxSeen-seen))
		if err != nil && !errors.Is(err, io.EOF) {
			return Directory{}, err
		}
		seen += len(entries)
		for _, entry := range entries {
			name := entry.Name()
			if !validBrowseName(name) || (entry.IsDir() && !browseFolder(name)) ||
				(!entry.IsDir() && (!entry.Type().IsRegular() || !IsMarkdown(name))) {
				continue
			}
			if (entry.IsDir() && len(parts) == BrowseMaxDepth) || len(out.Entries) >= maxEntries {
				out.Partial = true
				continue
			}
			out.Entries = append(out.Entries, BrowseEntry{Name: name, Folder: entry.IsDir()})
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if seen == maxSeen {
			out.Partial = true
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Folder != b.Folder {
			return a.Folder
		}
		return a.Name < b.Name
	})
	return out, nil
}
