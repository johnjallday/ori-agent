package foldermap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// State says how far the walk got with one folder.
type State string

const (
	// StateWalked folders were listed and their subfolders queued.
	StateWalked State = "walked"
	// StateClosed folders hold a project marker directly inside: they were
	// listed, recorded as one project, and their subfolders not walked.
	StateClosed State = "closed"
	// StateUnopened folders were found but not listed before a budget tripped.
	StateUnopened State = "unopened"
	// StateLarge folders hold more than LargeFolderEntries entries and were
	// not opened.
	StateLarge State = "large"
	// StateUnreadable folders could not be listed (permissions, or the folder
	// vanished or became a link since its parent was listed).
	StateUnreadable State = "unreadable"
)

// Folder is one folder of the map. Path is relative to the fed folder,
// slash-separated, "" for the fed folder itself; the map never holds an
// absolute path or a file's contents.
type Folder struct {
	Path  string `json:"path"`
	State State  `json:"state"`
	// Files and Subfolders count the folder's direct entries the walk kept
	// (regular files that are not cloud placeholders; folders that are not
	// hidden or skipped).
	Files      int `json:"files"`
	Subfolders int `json:"subfolders"`
	// Kinds is the number of distinct file extensions among Files.
	Kinds int `json:"kinds,omitempty"`
	// Newest is the newest modification time among the folder's own files.
	Newest time.Time `json:"newest,omitzero"`
	// Marker is the name of the highest-precedence project marker directly
	// inside ("*.logicx", "go.mod"), and MarkerFiles how many entries matched it.
	Marker      string             `json:"marker,omitempty"`
	MarkerFiles int                `json:"marker_files,omitempty"`
	Shape       folderdigest.Shape `json:"shape,omitempty"`
	Format      string             `json:"format,omitempty"`
}

// Name is the folder's own name ("" for the fed folder).
func (f Folder) Name() string {
	if f.Path == "" {
		return ""
	}
	return path.Base(f.Path)
}

// Depth is how many folders down from the fed folder this one sits.
func (f Folder) Depth() int {
	if f.Path == "" {
		return 0
	}
	return strings.Count(f.Path, "/") + 1
}

// IsProject reports whether a marker sits directly inside the folder.
func (f Folder) IsProject() bool { return f.Marker != "" }

// Map is the folder map: the single record the report, Set it up and Look
// further read. It is metadata only and bounded to MaxFolders folders.
type Map struct {
	Version int      `json:"version"`
	Folders []Folder `json:"folders"`
	// Entries is how many directory entries all walks listed in total.
	Entries int `json:"entries"`
	// SkippedLinks, SkippedFolders and SkippedHidden count entries the walk
	// refused (symbolic links, the digest's skipped folders, hidden and
	// cloud-placeholder entries).
	SkippedLinks   int `json:"skipped_links,omitempty"`
	SkippedFolders int `json:"skipped_folders,omitempty"`
	SkippedHidden  int `json:"skipped_hidden,omitempty"`
	// Stopped is why the last walk stopped early ("" when it opened every
	// folder it found).
	Stopped string    `json:"stopped,omitempty"`
	Walks   int       `json:"walks"`
	Updated time.Time `json:"updated"`

	index map[string]int
}

// Folder returns the folder at a relative path.
func (m *Map) Folder(rel string) (Folder, bool) {
	m.ensureIndex()
	i, ok := m.index[rel]
	if !ok {
		return Folder{}, false
	}
	return m.Folders[i], true
}

// Unopened lists the folders the walk found but did not open, in walk order.
func (m *Map) Unopened() []string {
	var out []string
	for _, f := range m.Folders {
		if f.State == StateUnopened {
			out = append(out, f.Path)
		}
	}
	return out
}

func (m *Map) ensureIndex() {
	if m.index != nil && len(m.index) == len(m.Folders) {
		return
	}
	m.index = make(map[string]int, len(m.Folders))
	for i, f := range m.Folders {
		m.index[f.Path] = i
	}
}

func (m *Map) put(f Folder) {
	m.ensureIndex()
	if i, ok := m.index[f.Path]; ok {
		m.Folders[i] = f
		return
	}
	m.index[f.Path] = len(m.Folders)
	m.Folders = append(m.Folders, f)
}

// Validate checks the bounds a persisted map must hold: version, folder
// count, and relative, clean, unique paths.
func (m *Map) Validate() error {
	if m == nil {
		return errors.New("foldermap: nil map")
	}
	if m.Version != MapVersion {
		return fmt.Errorf("foldermap: unsupported map version %d", m.Version)
	}
	if len(m.Folders) > MaxFolders {
		return fmt.Errorf("foldermap: %d folders exceeds %d", len(m.Folders), MaxFolders)
	}
	seen := make(map[string]bool, len(m.Folders))
	for _, f := range m.Folders {
		if f.Path != "" {
			if _, err := CleanRel(f.Path); err != nil {
				return err
			}
		}
		if seen[f.Path] {
			return fmt.Errorf("foldermap: duplicate folder %q", f.Path)
		}
		seen[f.Path] = true
	}
	return nil
}

// CleanRel validates a client- or model-supplied folder path relative to the
// fed folder. It refuses absolute paths, "..", backslashes and empty
// segments; "" and "." name the fed folder itself.
func CleanRel(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return "", nil
	}
	if strings.Contains(rel, "\\") || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("foldermap: %q is not a folder inside the fed folder", rel)
	}
	if path.IsAbs(rel) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("foldermap: %q is an absolute path", rel)
	}
	cleaned := path.Clean(rel)
	if cleaned != strings.TrimSuffix(rel, "/") {
		return "", fmt.Errorf("foldermap: %q is not a clean relative path", rel)
	}
	for segment := range strings.SplitSeq(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("foldermap: %q leaves the fed folder", rel)
		}
	}
	return cleaned, nil
}

// Options tunes one walk. Zero values take the constants.
type Options struct {
	MaxEntries int
	Budget     time.Duration
	// Now supplies the clock, for tests that exercise the time budget.
	Now func() time.Time
	// Progress, when set, is told after each folder how many entries the
	// map holds and how many found folders are still queued.
	Progress func(entries, foldersLeft int)
}

func (o Options) withDefaults() Options {
	if o.MaxEntries <= 0 {
		o.MaxEntries = WalkMaxEntries
	}
	if o.Budget <= 0 {
		o.Budget = WalkBudget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// ErrNotDirectory reports that the fed folder is not a directory or is a
// symbolic link, which the walk never follows.
var ErrNotDirectory = errors.New("foldermap: root is not a directory")

// Walk lists the fed folder breadth-first under one budget and returns a new
// map. Only a failure to list the fed folder itself is an error.
func Walk(root string, opts Options) (*Map, error) {
	if err := checkRoot(root); err != nil {
		return nil, err
	}
	m := &Map{Version: MapVersion}
	m.put(Folder{Path: "", State: StateUnopened})
	w := newWalker(root, m, opts)
	if err := w.listRoot(); err != nil {
		return nil, err
	}
	w.run()
	return m, nil
}

// Continue walks again from the folders the map left unopened, under a fresh
// budget: first the folders named in first (in that order, when they are
// unopened), then the rest in walk order. It updates the map in place.
func Continue(root string, m *Map, first []string, opts Options) error {
	if err := checkRoot(root); err != nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	w := newWalker(root, m, opts)
	queued := make(map[string]bool)
	for _, rel := range first {
		cleaned, err := CleanRel(rel)
		if err != nil {
			return err
		}
		if f, ok := m.Folder(cleaned); ok && f.State == StateUnopened && !queued[cleaned] {
			w.queue = append(w.queue, cleaned)
			queued[cleaned] = true
		}
	}
	for _, rel := range m.Unopened() {
		if !queued[rel] {
			w.queue = append(w.queue, rel)
			queued[rel] = true
		}
	}
	w.run()
	return nil
}

func checkRoot(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("foldermap: root %q must be absolute", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrNotDirectory
	}
	return nil
}

type walker struct {
	root     string
	m        *Map
	opts     Options
	start    time.Time
	listed   int
	queue    []string
	stopped  string
	rankByID map[string]int
}

func newWalker(root string, m *Map, opts Options) *walker {
	opts = opts.withDefaults()
	w := &walker{root: root, m: m, opts: opts, start: opts.Now()}
	return w
}

func (w *walker) listRoot() error {
	f, err := w.list("")
	if err != nil {
		return err
	}
	w.m.put(f)
	return nil
}

func (w *walker) run() {
	w.m.Stopped = ""
	for len(w.queue) > 0 {
		if reason := w.budgetReason(); reason != "" {
			w.stopped = reason
			break
		}
		rel := w.queue[0]
		w.queue = w.queue[1:]
		f, err := w.list(rel)
		if err != nil {
			f = Folder{Path: rel, State: StateUnreadable}
		}
		w.m.put(f)
		if w.opts.Progress != nil {
			w.opts.Progress(w.m.Entries, len(w.queue))
		}
		if w.stopped != "" {
			break
		}
	}
	w.m.Stopped = w.stopped
	w.m.Walks++
	w.m.Updated = w.opts.Now().UTC()
}

func (w *walker) budgetReason() string {
	if w.listed >= w.opts.MaxEntries {
		return StopEntries
	}
	if w.opts.Now().Sub(w.start) >= w.opts.Budget {
		return StopTime
	}
	return ""
}

// list reads one folder's entries (metadata only) and records its children
// as unopened folders. A folder with a marker directly inside is closed: its
// children are not queued or recorded.
func (w *walker) list(rel string) (Folder, error) {
	dir := w.abs(rel)
	if rel != "" {
		info, err := os.Lstat(dir)
		if err != nil {
			return Folder{}, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Folder{}, ErrNotDirectory
		}
	}
	entries, large, err := readDirBounded(dir, LargeFolderEntries)
	if err != nil {
		return Folder{}, err
	}
	if large {
		return Folder{Path: rel, State: StateLarge}, nil
	}
	w.listed += len(entries)
	w.m.Entries += len(entries)

	f := Folder{Path: rel, State: StateWalked}
	bestRank := -1
	var bestMarker folderdigest.Marker
	extensions := make(map[string]bool)
	var children []string
	for _, entry := range entries {
		name := entry.Name()
		isLink := entry.Type()&os.ModeSymlink != 0
		isDir := entry.IsDir()
		// Hidden entries (.git, .obsidian) still count as markers, as in the
		// digest; links never do.
		if !isLink {
			if marker, ok := folderdigest.MatchMarker(name, isDir); ok {
				rank := w.rank(marker)
				if bestRank < 0 || rank < bestRank {
					bestRank, bestMarker = rank, marker
					f.MarkerFiles = 0
				}
				if rank == bestRank {
					f.MarkerFiles++
				}
			}
		}
		switch {
		case isLink:
			w.m.SkippedLinks++
			continue
		case strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".icloud"):
			w.m.SkippedHidden++
			continue
		}
		if isDir {
			if folderdigest.IsSkippedFolder(name) {
				w.m.SkippedFolders++
				continue
			}
			f.Subfolders++
			children = append(children, joinRel(rel, name))
			continue
		}
		// Lstat through the directory entry: metadata only, never an open.
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || folderdigest.IsDataless(info) {
			continue
		}
		f.Files++
		extensions[strings.ToLower(filepath.Ext(name))] = true
		if mod := info.ModTime().UTC(); mod.After(f.Newest) {
			f.Newest = mod
		}
	}
	f.Kinds = len(extensions)
	if bestRank >= 0 {
		f.State = StateClosed
		f.Marker = bestMarker.Name
		f.Shape = bestMarker.Shape
		f.Format = bestMarker.ProjectFormat
		return f, nil
	}
	for _, child := range children {
		if _, known := w.m.Folder(child); known {
			continue
		}
		if len(w.m.Folders) >= MaxFolders {
			w.stopped = StopMapFull
			break
		}
		w.m.put(Folder{Path: child, State: StateUnopened})
		w.queue = append(w.queue, child)
	}
	return f, nil
}

func (w *walker) abs(rel string) string {
	if rel == "" {
		return w.root
	}
	return filepath.Join(w.root, filepath.FromSlash(rel))
}

// rank is a marker's row in the host table: lower is more specific.
func (w *walker) rank(marker folderdigest.Marker) int {
	if w.rankByID == nil {
		w.rankByID = make(map[string]int, len(folderdigest.Markers))
		for i, row := range folderdigest.Markers {
			key := string(row.Kind) + "\x00" + row.Name
			if _, ok := w.rankByID[key]; !ok {
				w.rankByID[key] = i
			}
		}
	}
	if i, ok := w.rankByID[string(marker.Kind)+"\x00"+marker.Name]; ok {
		return i
	}
	return len(folderdigest.Markers)
}

func joinRel(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// readDirBounded lists at most limit entries; more than that reports large.
// Entries come back sorted by name so the walk order is stable.
func readDirBounded(dir string, limit int) ([]os.DirEntry, bool, error) {
	f, err := os.Open(dir) // #nosec G304 -- dir is the fed folder or a relative path the walk itself recorded under it, Lstat-checked above
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if len(entries) > limit {
		return nil, true, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, false, nil
}
