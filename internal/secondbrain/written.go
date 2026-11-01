package secondbrain

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
)

const (
	// maxWrittenListed bounds the entries looked at in each owned folder.
	maxWrittenListed = 2000
	// markerPeekBytes is how much of a note is read to see its marker: the
	// whole marker is two short lines.
	markerPeekBytes = 160
)

// Written is what Ori has put in the places it keeps notes (FR16, and FR8 of the
// fill-in).
type Written struct {
	// Count is the notes that still carry Ori's marker, edited or not, seeded
	// ones included.
	Count int
	// ByKind splits Count by the marker's kind.
	ByKind map[string]int
	// LastAt is the newest such note's modification time; zero when Count is 0.
	LastAt time.Time
	// Partial says a folder had more entries than were looked at.
	Partial bool
}

// CountWritten counts the notes carrying Ori's marker in Inbox, Daily, People,
// the project folders and the root facts note. The listing is bounded and a note
// is opened only for its first lines; it keeps no content. Links are never
// followed and an unreadable entry is not counted.
func CountWritten(root string) (Written, error) {
	pinned, err := pinFolder(root)
	if err != nil {
		return Written{}, err
	}
	defer func() { _ = pinned.Close() }()
	out := Written{ByKind: map[string]int{}}
	for _, folder := range []string{FolderInbox, FolderDaily, FolderPeople} {
		countFolder(pinned, folder, &out)
	}
	countProjects(pinned, &out)
	countNote(pinned, FactsNoteRel, &out)
	return out, nil
}

// listFolder opens one real folder under root and lists it. A link in the
// folder's place, or a folder that is gone, lists nothing.
func listFolder(root *os.Root, folder string) (*os.Root, []fs.DirEntry) {
	info, err := root.Lstat(folder)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	dir, err := root.OpenRoot(folder)
	if err != nil {
		return nil, nil
	}
	listing, err := dir.Open(".")
	if err != nil {
		_ = dir.Close()
		return nil, nil
	}
	entries, err := listing.ReadDir(-1)
	_ = listing.Close()
	if err != nil {
		_ = dir.Close()
		return nil, nil
	}
	return dir, entries
}

func countFolder(root *os.Root, folder string, out *Written) {
	dir, entries := listFolder(root, folder)
	if dir == nil {
		return
	}
	defer func() { _ = dir.Close() }()
	for index, entry := range entries {
		if index == maxWrittenListed {
			out.Partial = true
			return
		}
		name := entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() || name[0] == '.' || !IsMarkdown(name) {
			continue
		}
		countNote(dir, name, out)
	}
}

// countProjects looks at the one note Ori keeps in each project folder.
func countProjects(root *os.Root, out *Written) {
	dir, entries := listFolder(root, FolderProjects)
	if dir == nil {
		return
	}
	defer func() { _ = dir.Close() }()
	for index, entry := range entries {
		if index == maxWrittenListed {
			out.Partial = true
			return
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		project, err := dir.OpenRoot(entry.Name())
		if err != nil {
			continue
		}
		countNote(project, projectNoteName, out)
		_ = project.Close()
	}
}

func countNote(dir *os.Root, name string, out *Written) {
	if info, err := dir.Lstat(name); err != nil || !info.Mode().IsRegular() {
		return
	}
	modTime, kind, ok := markedNote(dir, name)
	if !ok {
		return
	}
	out.Count++
	out.ByKind[kind]++
	if modTime.After(out.LastAt) {
		out.LastAt = modTime
	}
}

// markedNote reports whether the note opens with Ori's marker, its kind, and when
// it was last changed.
func markedNote(dir *os.Root, name string) (time.Time, string, bool) {
	file, err := openNote(dir, name)
	if err != nil {
		return time.Time{}, "", false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return time.Time{}, "", false
	}
	head, err := io.ReadAll(io.LimitReader(file, markerPeekBytes))
	if err != nil {
		return time.Time{}, "", false
	}
	if !bytes.HasPrefix(head, []byte(markerOpen)) {
		return time.Time{}, "", false
	}
	match := markerKindLine.FindSubmatch(head)
	if match == nil {
		return time.Time{}, "", false
	}
	return info.ModTime(), string(match[1]), true
}
