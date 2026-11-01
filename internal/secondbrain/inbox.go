package secondbrain

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

const (
	// MaxInboxNotes bounds the notes one listing returns.
	MaxInboxNotes = 100
	// inboxPeekBytes is how much of a note is read for its kind and title.
	inboxPeekBytes = 4 * 1024
	// maxInboxTitleRunes caps the title a listing shows.
	maxInboxTitleRunes = 160
)

// The kinds an Inbox note can be. NoteKindCapture, NoteKindSeed and
// NoteKindFollowUp mean the note is exactly as Ori wrote it; anything else is the
// user's.
const NoteKindUser = "user"

// InboxNote is one note directly under Inbox. Title is read for this listing
// and never stored.
type InboxNote struct {
	Name       string
	Kind       string
	Title      string
	Modified   time.Time
	OwnedByOri bool
}

// Inbox is a bounded listing of Inbox, newest first.
type Inbox struct {
	Notes []InboxNote
	// Total counts the notes seen, which may be more than Notes holds.
	Total int
	// Partial says the listing hit a bound.
	Partial bool
}

// ListInbox lists the Markdown notes directly under Inbox. It reads the folder
// the way CountWritten does: pinned, no link followed, a bounded number of
// entries, and at most inboxPeekBytes of each listed note. It writes nothing and
// keeps nothing. A note's kind comes from that prefix, so a note longer than it
// cannot be proved Ori's and reads as the user's. Each folder's README is the
// layout's, not a note in the Inbox, and is left out.
func ListInbox(root string) (Inbox, error) {
	pinned, err := pinFolder(root)
	if err != nil {
		return Inbox{}, err
	}
	defer func() { _ = pinned.Close() }()
	info, err := pinned.Lstat(FolderInbox)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
		return Inbox{Notes: []InboxNote{}}, nil
	}
	if err != nil {
		return Inbox{}, err
	}
	dir, err := pinned.OpenRoot(FolderInbox)
	if err != nil {
		return Inbox{}, err
	}
	defer func() { _ = dir.Close() }()
	listing, err := dir.Open(".")
	if err != nil {
		return Inbox{}, err
	}
	entries, err := listing.ReadDir(maxWrittenListed + 1)
	_ = listing.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return Inbox{}, err
	}
	out := Inbox{Notes: []InboxNote{}}
	if len(entries) > maxWrittenListed {
		entries, out.Partial = entries[:maxWrittenListed], true
	}
	type candidate struct {
		name     string
		modified time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() || name[0] == '.' || !IsMarkdown(name) || strings.EqualFold(name, "README.md") {
			continue
		}
		stat, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{name, stat.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].modified.Equal(candidates[j].modified) {
			return candidates[i].modified.After(candidates[j].modified)
		}
		return candidates[i].name > candidates[j].name
	})
	out.Total = len(candidates)
	for _, found := range candidates {
		if len(out.Notes) == MaxInboxNotes {
			out.Partial = true
			break
		}
		if note, ok := peekInboxNote(dir, found.name); ok {
			out.Notes = append(out.Notes, note)
		} else {
			out.Total--
		}
	}
	return out, nil
}

// peekInboxNote opens one listed note, no-follow, and reads its first bytes. An
// entry that cannot be opened is skipped.
func peekInboxNote(dir *os.Root, name string) (InboxNote, bool) {
	file, err := openNote(dir, name)
	if err != nil {
		return InboxNote{}, false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return InboxNote{}, false
	}
	head, err := io.ReadAll(io.LimitReader(file, inboxPeekBytes))
	if err != nil {
		return InboxNote{}, false
	}
	note := InboxNote{Name: name, Kind: NoteKindUser, Modified: info.ModTime()}
	mark := ReadMark(head)
	if mark.OwnedByOri && (mark.Kind == NoteKindCapture || mark.Kind == NoteKindSeed || mark.Kind == NoteKindFollowUp) {
		note.Kind, note.OwnedByOri = mark.Kind, true
	}
	note.Title = inboxTitle(head, name)
	return note, true
}

// inboxTitle is the note's first "# " heading, or its first non-empty line, on
// one line with angle brackets removed. A note with neither, or whose line looks
// like a credential, is called by its file name.
func inboxTitle(head []byte, name string) string {
	body := head
	if bytes.HasPrefix(head, []byte(markerOpen)) {
		_, rest, found := cutFrontmatter(head[len(markerOpen):])
		if !found {
			return noteStem(name)
		}
		body = rest
	}
	first := ""
	for line := range strings.SplitSeq(strings.ToValidUTF8(string(body), ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if heading, ok := strings.CutPrefix(line, "# "); ok {
			first = heading
			break
		}
		if first == "" {
			first = line
		}
	}
	title := foldTitle(strings.NewReplacer("<", "", ">", "").Replace(first), maxInboxTitleRunes)
	if title == "" || sensitive.ContainsSecretLikeText(title) {
		return noteStem(name)
	}
	return title
}

// Who a day's note in Daily belongs to.
const (
	TodayNoteOri  = "ori"
	TodayNoteUser = "user"
	TodayNoteNone = "none"
)

// ReadTodayNote says whether Daily/<date>.md exists and whether it is exactly as
// Ori wrote it. Anything there that Ori cannot prove it wrote, a link included,
// is the user's. Nothing is followed and nothing is kept.
func ReadTodayNote(root, date string) (string, error) {
	pinned, err := pinFolder(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = pinned.Close() }()
	info, err := pinned.Lstat(FolderDaily)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
		return TodayNoteNone, nil
	}
	if err != nil {
		return "", err
	}
	dir, err := pinned.OpenRoot(FolderDaily)
	if err != nil {
		return "", err
	}
	defer func() { _ = dir.Close() }()
	name := date + ".md"
	stat, err := dir.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return TodayNoteNone, nil
	}
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > MaxNoteBytes {
		return TodayNoteUser, nil
	}
	file, err := openNote(dir, name)
	if err != nil {
		return TodayNoteUser, nil
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, MaxNoteBytes))
	if err != nil {
		return TodayNoteUser, nil
	}
	if mark := ReadMark(content); mark.OwnedByOri && mark.Kind == NoteKindJournal {
		return TodayNoteOri, nil
	}
	return TodayNoteUser, nil
}
