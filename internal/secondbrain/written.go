package secondbrain

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"time"
)

const (
	// maxWrittenListed bounds the entries looked at in each owned folder.
	maxWrittenListed = 2000
	// markerPeekBytes is how much of a note is read to see its marker: the
	// whole marker is two short lines.
	markerPeekBytes = 160
)

// Written is what Ori has put in the two folders it owns (FR16).
type Written struct {
	// Count is the notes in Inbox and Daily that still carry Ori's marker,
	// edited or not, seeded ones included.
	Count int
	// LastAt is the newest such note's modification time; zero when Count is 0.
	LastAt time.Time
	// Partial says a folder had more entries than were looked at.
	Partial bool
}

// CountWritten counts the notes carrying Ori's marker in Inbox and Daily. The
// listing is bounded and a note is opened only for its first lines; it keeps
// no content. Links are never followed and an unreadable entry is not counted.
func CountWritten(root string) (Written, error) {
	pinned, err := pinFolder(root)
	if err != nil {
		return Written{}, err
	}
	defer func() { _ = pinned.Close() }()
	var out Written
	for _, folder := range []string{FolderInbox, FolderDaily} {
		countFolder(pinned, folder, &out)
	}
	return out, nil
}

func countFolder(root *os.Root, folder string, out *Written) {
	info, err := root.Lstat(folder)
	if err != nil || !info.IsDir() {
		return
	}
	dir, err := root.OpenRoot(folder)
	if err != nil {
		return
	}
	defer func() { _ = dir.Close() }()
	listing, err := dir.Open(".")
	if err != nil {
		return
	}
	entries, err := listing.ReadDir(-1)
	_ = listing.Close()
	if err != nil {
		return
	}
	for index, entry := range entries {
		if index == maxWrittenListed {
			out.Partial = true
			return
		}
		name := entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() || name[0] == '.' || !IsMarkdown(name) {
			continue
		}
		if modTime, ok := markedNote(dir, name); ok {
			out.Count++
			if modTime.After(out.LastAt) {
				out.LastAt = modTime
			}
		}
	}
}

// markedNote reports whether the note opens with Ori's marker, and when it
// was last changed.
func markedNote(dir *os.Root, name string) (time.Time, bool) {
	file, err := openNote(dir, name)
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return time.Time{}, false
	}
	head, err := io.ReadAll(io.LimitReader(file, markerPeekBytes))
	if err != nil {
		return time.Time{}, false
	}
	if !bytes.HasPrefix(head, []byte(markerOpen)) {
		return time.Time{}, false
	}
	return info.ModTime(), markerKindLine.Match(head)
}
