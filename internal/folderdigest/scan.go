// Package folderdigest looks at the shape of a folder — entry names, dates,
// file kinds, and well-known project markers — and says what the assistant
// can do with it: set up a workspace, tidy it, or ask.
//
// Everything here is metadata-only. The scan reads directory entries and
// Lstat results and never opens a file; TestScan_NeverOpensFiles proves it
// against a tree whose every file is unreadable. Verdicts are computed from
// counts alone, so no model call is needed to decide one.
package folderdigest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bounds for one scan (FR11). They keep a look at a huge folder cheap and
// interruptible; when one trips, the scan keeps what it has and the offer
// says "(partial look)".
const (
	// DefaultMaxDepth is how many directory levels below the root are read.
	// The root's entries are level 1; a directory at level 3 is read, its
	// subdirectories are not.
	DefaultMaxDepth = 3
	// DefaultMaxEntries caps the directory entries examined across the tree.
	DefaultMaxEntries = 5000
	// DefaultBudget caps wall time. Three seconds is a wait the chooser can
	// show a spinner for without the user thinking it hung.
	DefaultBudget = 3 * time.Second
)

// Partial reasons recorded on a Result when a bound trips.
const (
	PartialEntries = "entries"
	PartialTime    = "time"
)

// ErrNotDirectory reports that the root is not a directory (or is a symbolic
// link, which the scan never follows).
var ErrNotDirectory = errors.New("folderdigest: root is not a directory")

// skippedFolders are never descended into (FR12). They are either tooling
// output, the OS's own folders, or deleted items, and none of them say
// anything about what the user works on.
var skippedFolders = map[string]bool{
	"node_modules": true,
	"Library":      true,
	"Trash":        true,
	".Trash":       true,
}

// Options tunes one scan. Zero values take the defaults above.
type Options struct {
	MaxDepth   int
	MaxEntries int
	Budget     time.Duration
	// Now supplies the clock, for tests that exercise the time budget.
	Now func() time.Time
}

func (o Options) withDefaults() Options {
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = DefaultMaxEntries
	}
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Candidate is the root or one of its immediate subfolders, considered as a
// possible project (FR13). Counts cover the candidate's whole subtree within
// the scan bounds; LooseFiles and LooseExtensions are computed for the root
// only and cover its direct children.
type Candidate struct {
	// Name is the folder's base name — the only file name a reason line may
	// use (FR19).
	Name string
	// RelPath is the path below the root ("" for the root itself).
	RelPath string
	// Path is the absolute path. It lives in memory for the life of an offer
	// and is never persisted (FR27).
	Path   string
	IsRoot bool
	// Marker is the highest-precedence marker found directly inside the
	// candidate, if any (FR14).
	Marker *Marker
	// NewestModTime is the most recent modification time among the
	// candidate's files. Zero when it has none.
	NewestModTime time.Time
	FileCount     int
	// Extensions counts files per lower-case extension (with the dot; "" for
	// none).
	Extensions        map[string]int
	DominantExtension string
	DominantCount     int
	// DominantShare is DominantCount / FileCount, 0 when there are no files.
	DominantShare float64
	// LooseFiles counts the root's direct child files (FR15). Root only.
	LooseFiles int
	// LooseExtensions counts the root's loose files per extension. Root only.
	LooseExtensions map[string]int
}

// HasMarker reports whether a project marker sits directly inside the
// candidate.
func (c Candidate) HasMarker() bool { return c.Marker != nil }

// DistinctExtensions is the number of file kinds in the candidate's subtree.
func (c Candidate) DistinctExtensions() int { return len(c.Extensions) }

// LooseKinds is the number of file kinds among the root's loose files.
func (c Candidate) LooseKinds() int { return len(c.LooseExtensions) }

// Result is one scan's outcome.
type Result struct {
	// Root is the scanned folder's absolute path; in memory only.
	Root string
	// Name is the root's base name.
	Name string
	// Candidates holds the root first, then each immediate subfolder in
	// name order.
	Candidates []Candidate
	// Entries is how many directory entries were examined.
	Entries int
	// SkippedLinks counts symbolic links the scan refused to follow (FR10).
	SkippedLinks int
	// Partial is set when a bound stopped the scan early (FR11).
	Partial       bool
	PartialReason string
	ScannedAt     time.Time
	Elapsed       time.Duration
}

// RootCandidate returns the root's own stats.
func (r Result) RootCandidate() Candidate {
	if len(r.Candidates) == 0 {
		return Candidate{}
	}
	return r.Candidates[0]
}

// Subfolders returns the immediate-subfolder candidates.
func (r Result) Subfolders() []Candidate {
	if len(r.Candidates) <= 1 {
		return nil
	}
	return r.Candidates[1:]
}

// HasMarker reports whether the marker row named markerName still sits
// directly inside dir, for revalidating a fact learned from it (FR38). It
// reads one directory listing and never opens a file.
func HasMarker(dir, markerName string) bool {
	var row *Marker
	for i := range Markers {
		if Markers[i].Name == markerName {
			row = &Markers[i]
			break
		}
	}
	if row == nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if row.matches(entry.Name(), entry.IsDir()) {
			return true
		}
	}
	return false
}

// Scan reads the shape of root within the bounds in opts. The caller has
// already validated root (FR7); Scan only refuses a path that is not a real
// directory. Errors reading a subfolder (permissions, a vanished entry) skip
// that subfolder rather than failing the scan.
func Scan(root string, opts Options) (Result, error) {
	opts = opts.withDefaults()
	root = filepath.Clean(strings.TrimSpace(root))
	info, err := os.Lstat(root)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, ErrNotDirectory
	}

	s := &scanner{opts: opts, start: opts.Now()}
	s.root = newAccumulator(root, "", true)
	if err := s.walk(root, 0, []*accumulator{s.root}, s.root, true); err != nil {
		return Result{}, err
	}

	result := Result{
		Root:          root,
		Name:          filepath.Base(root),
		Entries:       s.entries,
		SkippedLinks:  s.skippedLinks,
		Partial:       s.partialReason != "",
		PartialReason: s.partialReason,
		ScannedAt:     s.start,
		Elapsed:       opts.Now().Sub(s.start),
	}
	result.Candidates = append(result.Candidates, s.root.finish())
	for _, sub := range s.subfolders {
		result.Candidates = append(result.Candidates, sub.finish())
	}
	return result, nil
}

type scanner struct {
	opts          Options
	start         time.Time
	entries       int
	skippedLinks  int
	partialReason string
	root          *accumulator
	subfolders    []*accumulator
}

// stopped reports whether a bound has tripped, marking the reason the first
// time the time budget is exceeded.
func (s *scanner) stopped() bool {
	if s.partialReason != "" {
		return true
	}
	if s.opts.Now().Sub(s.start) > s.opts.Budget {
		s.partialReason = PartialTime
		return true
	}
	return false
}

// walk reads one directory. owners are the candidates whose counts its files
// feed (the root, plus the immediate subfolder the directory sits under).
// markerOwner is the candidate whose direct entries these are, or nil deeper
// down, since a marker only counts directly inside a candidate (FR14).
// fatal says whether a read error fails the scan (root) or skips the
// directory (everything else).
func (s *scanner) walk(dir string, depth int, owners []*accumulator, markerOwner *accumulator, fatal bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if fatal {
			return err
		}
		return nil
	}
	for _, entry := range entries {
		if s.stopped() {
			return nil
		}
		s.entries++
		if s.entries > s.opts.MaxEntries {
			s.partialReason = PartialEntries
			return nil
		}

		name := entry.Name()
		mode := entry.Type()
		isLink := mode&os.ModeSymlink != 0
		isDir := entry.IsDir()

		// Hidden entries (.git, .obsidian) still count as markers; they are
		// skipped for everything else just below.
		if markerOwner != nil && !isLink {
			if m, ok := MatchMarker(name, isDir); ok {
				markerOwner.noteMarker(m)
			}
		}
		if isLink {
			s.skippedLinks++
			continue
		}
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".icloud") {
			continue
		}

		if isDir {
			if skippedFolders[name] {
				continue
			}
			child := filepath.Join(dir, name)
			childOwners := owners
			var childMarkerOwner *accumulator
			if depth == 0 {
				sub := newAccumulator(child, name, false)
				s.subfolders = append(s.subfolders, sub)
				childOwners = append(append([]*accumulator{}, owners...), sub)
				childMarkerOwner = sub
			}
			if depth+1 <= s.opts.MaxDepth {
				if err := s.walk(child, depth+1, childOwners, childMarkerOwner, false); err != nil {
					return err
				}
			}
			continue
		}

		// Lstat, never Stat: a file's metadata is all the scan may look at.
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || isDataless(info) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		for _, owner := range owners {
			owner.addFile(ext, info.ModTime())
		}
		if depth == 0 {
			s.root.addLoose(ext)
		}
	}
	return nil
}

// accumulator gathers one candidate's counts during the walk.
type accumulator struct {
	cand Candidate
}

func newAccumulator(path, rel string, isRoot bool) *accumulator {
	c := Candidate{
		Name:       filepath.Base(path),
		RelPath:    rel,
		Path:       path,
		IsRoot:     isRoot,
		Extensions: map[string]int{},
	}
	if isRoot {
		c.LooseExtensions = map[string]int{}
	}
	return &accumulator{cand: c}
}

func (a *accumulator) noteMarker(m Marker) {
	if a.cand.Marker == nil || markerRank(m) < markerRank(*a.cand.Marker) {
		marker := m
		a.cand.Marker = &marker
	}
}

func (a *accumulator) addFile(ext string, modTime time.Time) {
	a.cand.FileCount++
	a.cand.Extensions[ext]++
	if modTime.After(a.cand.NewestModTime) {
		a.cand.NewestModTime = modTime
	}
}

func (a *accumulator) addLoose(ext string) {
	a.cand.LooseFiles++
	a.cand.LooseExtensions[ext]++
}

// finish computes the dominant extension. Ties break on the extension name
// so a result is stable across runs.
func (a *accumulator) finish() Candidate {
	c := a.cand
	for ext, count := range c.Extensions {
		if count > c.DominantCount || (count == c.DominantCount && ext < c.DominantExtension) {
			c.DominantExtension = ext
			c.DominantCount = count
		}
	}
	if c.FileCount > 0 {
		c.DominantShare = float64(c.DominantCount) / float64(c.FileCount)
	}
	return c
}
