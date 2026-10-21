package secondbrain

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Bounds for one read of a notes folder (FR17). When one trips the read keeps
// what it has and the look says "(partial look)".
const (
	// MaxNoteDepth is how deep a note may sit: "Inbox.md" is at depth 1,
	// "a/b/c/d/e/Note.md" at depth 6.
	MaxNoteDepth = 6
	// MaxNotesListed caps the Markdown files listed across the folder.
	MaxNotesListed = 2000
	// MaxNotesOpened caps the files opened, most recently modified first.
	MaxNotesOpened = 300
	// MaxNoteBytes is the largest file opened; larger ones are skipped and
	// counted, never read in part.
	MaxNoteBytes = 256 << 10
	// MaxReadBytes caps the bytes read in total.
	MaxReadBytes = 20 << 20
	// ReadBudget caps wall time for the listing and the reading together.
	ReadBudget = 5 * time.Second
)

// Partial reasons recorded on a Reading when a bound trips.
const (
	PartialListed = "listed"
	PartialOpened = "opened"
	PartialBytes  = "bytes"
	PartialTime   = "time"
)

// ErrNotDirectory reports that the notes folder is not a directory, or is a
// symbolic link, which the reader never follows.
var ErrNotDirectory = errors.New("secondbrain: notes folder is not a directory")

// skippedFolders are never descended into, beside every hidden folder. They
// hold Obsidian's settings, deleted notes, version control and dependencies,
// not notes the user keeps.
var skippedFolders = map[string]bool{
	ObsidianFolder: true,
	".trash":       true,
	".git":         true,
	"node_modules": true,
}

// ReadOptions tunes one read. Zero values take the bounds above.
type ReadOptions struct {
	MaxDepth      int
	MaxListed     int
	MaxOpened     int
	MaxFileBytes  int64
	MaxTotalBytes int64
	Budget        time.Duration
	// Now supplies the clock, for tests that exercise the time budget.
	Now func() time.Time

	// onOpen sees every relative path the reader opens, for tests that prove
	// what it never opens.
	onOpen func(rel string)
}

func (o ReadOptions) withDefaults() ReadOptions {
	if o.MaxDepth <= 0 {
		o.MaxDepth = MaxNoteDepth
	}
	if o.MaxListed <= 0 {
		o.MaxListed = MaxNotesListed
	}
	if o.MaxOpened <= 0 {
		o.MaxOpened = MaxNotesOpened
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = MaxNoteBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = MaxReadBytes
	}
	if o.Budget <= 0 {
		o.Budget = ReadBudget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Note is one Markdown file the reader opened. Content lives in memory for
// the length of one look and is never persisted (FR20).
type Note struct {
	// RelPath is slash-separated and relative to the notes folder.
	RelPath string
	ModTime time.Time
	Content []byte
}

// Reading is the outcome of one bounded read.
type Reading struct {
	// Root is the folder's absolute path and Name its base name.
	Root string
	Name string
	// Notes are the opened files, most recently modified first.
	Notes []Note
	// Listed counts the Markdown files listed; Unopened those listed but not
	// opened because a bound tripped first.
	Listed   int
	Unopened int
	// TooLarge counts files over the per-file cap, never opened.
	TooLarge int
	// Links counts symbolic links seen, none of them followed.
	Links int
	// Unreadable counts files and folders that could not be opened.
	Unreadable int
	// NotDownloaded counts evicted iCloud placeholders, never opened.
	NotDownloaded int
	// Partial is set when a bound stopped the read early.
	Partial       bool
	PartialReason string
	ReadAt        time.Time
	Elapsed       time.Duration
}

// IsMarkdown reports whether a file name is a note the reader lists.
func IsMarkdown(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

// Bounds on the chooser's check that a folder holds notes (FR13).
const (
	// NotesCheckDepth is how deep HasNotes looks for one note.
	NotesCheckDepth   = 3
	maxNotesCheckSeen = 5000
)

// HasNotes reports whether root holds at least one Markdown file within
// NotesCheckDepth, under the reader's skip rules. It lists directories
// through a root pinned to the folder and opens no file.
func HasNotes(root string) bool {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return false
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	defer func() { _ = pinned.Close() }()
	seen := 0
	var look func(rel string, depth int) bool
	look = func(rel string, depth int) bool {
		dir, err := pinned.Open(rel)
		if err != nil {
			return false
		}
		entries, err := dir.ReadDir(-1)
		_ = dir.Close()
		if err != nil {
			return false
		}
		var folders []string
		for _, entry := range entries {
			if seen++; seen > maxNotesCheckSeen {
				return false
			}
			name := entry.Name()
			if entry.Type()&fs.ModeSymlink != 0 || strings.HasPrefix(name, ".") {
				continue
			}
			if entry.IsDir() {
				if !skippedFolders[name] && depth+2 <= NotesCheckDepth {
					folders = append(folders, path.Join(rel, name))
				}
				continue
			}
			if entry.Type().IsRegular() && IsMarkdown(name) {
				return true
			}
		}
		for _, folder := range folders {
			if look(folder, depth+1) {
				return true
			}
		}
		return false
	}
	return look(".", 0)
}

// Read lists and reads the Markdown notes under root within the bounds in
// opts (FR17). Every listing and every open goes through an os.Root pinned to
// root, so nothing outside it can be reached even through a link swapped in
// mid-read; symbolic links are counted and never followed, and nothing is
// ever written (FR21). Only a root that is missing, unreadable or not a real
// directory is an error; a subfolder or file that cannot be read is counted.
func Read(ctx context.Context, root string, opts ReadOptions) (Reading, error) {
	opts = opts.withDefaults()
	root = filepath.Clean(strings.TrimSpace(root))
	info, err := os.Lstat(root)
	if err != nil {
		return Reading{}, err
	}
	if !info.IsDir() {
		return Reading{}, ErrNotDirectory
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		return Reading{}, err
	}
	defer func() { _ = pinned.Close() }()

	r := &reader{ctx: ctx, opts: opts, root: pinned, start: opts.Now()}
	if err := r.list(".", 0, true); err != nil {
		return Reading{}, err
	}
	r.open()

	return Reading{
		Root:          root,
		Name:          filepath.Base(root),
		Notes:         r.notes,
		Listed:        len(r.listed),
		Unopened:      r.unopened,
		TooLarge:      r.tooLarge,
		Links:         r.links,
		Unreadable:    r.unreadable,
		NotDownloaded: r.notDownloaded,
		Partial:       r.partialReason != "",
		PartialReason: r.partialReason,
		ReadAt:        r.start,
		Elapsed:       opts.Now().Sub(r.start),
	}, nil
}

type listedNote struct {
	rel     string
	modTime time.Time
	size    int64
}

type reader struct {
	ctx           context.Context
	opts          ReadOptions
	root          *os.Root
	start         time.Time
	listed        []listedNote
	notes         []Note
	readBytes     int64
	unopened      int
	tooLarge      int
	links         int
	unreadable    int
	notDownloaded int
	listFull      bool
	timedOut      bool
	partialReason string
}

// trip records the first bound that stopped the read.
func (r *reader) trip(reason string) {
	if r.partialReason == "" {
		r.partialReason = reason
	}
}

// outOfTime reports whether the budget is spent or the caller gave up.
func (r *reader) outOfTime() bool {
	if !r.timedOut && (r.ctx.Err() != nil || r.opts.Now().Sub(r.start) > r.opts.Budget) {
		r.timedOut = true
		r.trip(PartialTime)
	}
	return r.timedOut
}

// list reads one directory, rel to the root, at depth (the root is 0). A read
// error fails the whole read only for the root itself.
func (r *reader) list(rel string, depth int, fatal bool) error {
	dir, err := r.root.Open(rel)
	if err != nil {
		if fatal {
			return err
		}
		r.unreadable++
		return nil
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		if fatal {
			return err
		}
		r.unreadable++
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if r.listFull || r.outOfTime() {
			return nil
		}
		name := entry.Name()
		mode := entry.Type()
		child := path.Join(rel, name)
		switch {
		case mode&fs.ModeSymlink != 0:
			r.links++
		case strings.HasPrefix(name, "."):
			// Hidden folders and files are never notes the user keeps.
		case entry.IsDir():
			if skippedFolders[name] || depth+2 > r.opts.MaxDepth {
				continue
			}
			if err := r.list(child, depth+1, false); err != nil {
				return err
			}
		case mode.IsRegular() && IsMarkdown(name):
			info, err := entry.Info() // Lstat: the listing never opens a file.
			if err != nil {
				r.unreadable++
				continue
			}
			if folderdigest.IsDataless(info) {
				r.notDownloaded++
				continue
			}
			if len(r.listed) == r.opts.MaxListed {
				r.listFull = true
				r.trip(PartialListed)
				return nil
			}
			r.listed = append(r.listed, listedNote{rel: child, modTime: info.ModTime(), size: info.Size()})
		}
	}
	return nil
}

// open reads the listed notes, most recently modified first, until a bound
// trips. Files over the per-file cap are counted and never opened.
func (r *reader) open() {
	sort.SliceStable(r.listed, func(i, j int) bool {
		if !r.listed[i].modTime.Equal(r.listed[j].modTime) {
			return r.listed[i].modTime.After(r.listed[j].modTime)
		}
		return r.listed[i].rel < r.listed[j].rel
	})
	for index, note := range r.listed {
		if r.outOfTime() {
			r.unopened = len(r.listed) - index
			return
		}
		if note.size > r.opts.MaxFileBytes {
			r.tooLarge++
			continue
		}
		if len(r.notes) == r.opts.MaxOpened {
			r.trip(PartialOpened)
			r.unopened = len(r.listed) - index
			return
		}
		if r.readBytes+note.size > r.opts.MaxTotalBytes {
			r.trip(PartialBytes)
			r.unopened = len(r.listed) - index
			return
		}
		content, modTime, err := r.read(note.rel)
		switch {
		case errors.Is(err, errTooLarge):
			r.tooLarge++
		case err != nil:
			r.unreadable++
		default:
			r.readBytes += int64(len(content))
			r.notes = append(r.notes, Note{RelPath: note.rel, ModTime: modTime, Content: content})
		}
	}
}

var (
	errTooLarge   = errors.New("secondbrain: note is over the size cap")
	errNotRegular = errors.New("secondbrain: note is no longer a regular file")
)

// read opens one listed note without following a link and reads it whole,
// refusing anything that is no longer a small regular file.
func (r *reader) read(rel string) ([]byte, time.Time, error) {
	if r.opts.onOpen != nil {
		r.opts.onOpen(rel)
	}
	file, err := openNote(r.root, rel)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}
	if !info.Mode().IsRegular() {
		return nil, time.Time{}, errNotRegular
	}
	if info.Size() > r.opts.MaxFileBytes {
		return nil, time.Time{}, errTooLarge
	}
	limit := r.opts.MaxFileBytes
	if remaining := r.opts.MaxTotalBytes - r.readBytes; remaining < limit {
		limit = remaining
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, time.Time{}, err
	}
	if int64(len(content)) > limit {
		// The file grew between the listing and the read.
		return nil, time.Time{}, errTooLarge
	}
	return content, info.ModTime(), nil
}
