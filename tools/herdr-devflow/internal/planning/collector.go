// Package planning discovers the exact PRD and task-list artifacts that
// describe a repository's features.
//
// It reads planning documents and nothing else. The repository's backlog lives
// in GitHub Issues, which this package deliberately knows nothing about: an
// idea nobody has planned yet has no PRD and no task list, so there is nothing
// here to discover about it.
//
// Discovery is read-only and bounded. It reads only files whose names it
// constructs itself from a validated slug, never arbitrary paths, and it
// records where each fact came from and when it was observed so callers can
// separate planning intent from observed truth.
package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// MaxFeatures bounds how many planning artifacts one directory may
	// contribute, so a stray directory cannot stall the overview.
	MaxFeatures = 200
	// MaxTitleBytes bounds how much of a PRD is read to recover its title.
	MaxTitleBytes = 4096
	// MaxTitleRunes bounds the displayed title length.
	MaxTitleRunes = 120
)

// slugPattern is the exact feature-slug shape shared by PRD filenames, task
// list filenames, branch suffixes, and worktree basenames.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

// ValidSlug reports whether value is an exact, canonical feature slug.
func ValidSlug(value string) bool { return slugPattern.MatchString(value) }

// State describes the outcome of consulting one planning artifact. It mirrors
// the overview availability vocabulary without importing the read model, which
// depends on this package.
type State string

const (
	// StateAbsent means the directory was readable and the file is not there.
	StateAbsent State = "absent"
	// StateAvailable means the file exists and was read.
	StateAvailable State = "available"
	// StateMalformed means the file exists but could not be understood.
	StateMalformed State = "malformed"
	// StateUnavailable means the file or directory could not be consulted.
	StateUnavailable State = "unavailable"
)

// Artifact is one discovered planning file.
type Artifact struct {
	// Path is the canonical absolute path, empty when absent.
	Path string
	// State is the outcome of looking for and reading this file.
	State State
	// Title is the artifact's first Markdown heading, when readable.
	Title string
	// Detail is a sanitized reason for a malformed or unavailable state.
	Detail string
	// ModTime is the file's modification time, zero when absent.
	ModTime time.Time
}

// Exists reports whether the artifact was found on disk.
func (a Artifact) Exists() bool { return a.State == StateAvailable || a.State == StateMalformed }

// Feature groups the planning artifacts discovered for one exact slug.
type Feature struct {
	Slug     string
	PRD      Artifact
	TaskList Artifact
}

// Set is one directory's planning discovery result.
type Set struct {
	// Dir is the canonical tasks directory that was scanned.
	Dir string
	// State is the outcome of scanning the directory itself.
	State State
	// Detail is a sanitized reason for a non-available directory state.
	Detail string
	// Features maps exact slug to its discovered artifacts.
	Features map[string]Feature
	// Truncated is true when MaxFeatures capped the scan.
	Truncated bool
	// ObservedAt is when the directory was scanned.
	ObservedAt time.Time
}

// Feature returns the artifacts discovered for an exact slug.
func (s Set) Feature(slug string) (Feature, bool) {
	feature, ok := s.Features[slug]
	return feature, ok
}

// Slugs returns the discovered slugs in sorted order.
func (s Set) Slugs() []string {
	slugs := make([]string, 0, len(s.Features))
	for slug := range s.Features {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// Discover scans one tasks directory for exact prd-<slug>.md and
// tasks-<slug>.md pairs. Files that do not match those exact shapes — test
// guides, notes, drafts — are ignored rather than guessed at.
func Discover(dir string, now time.Time) Set {
	set := Set{Dir: dir, State: StateAvailable, Features: map[string]Feature{}, ObservedAt: now}
	canonical, err := filepath.Abs(dir)
	if err != nil {
		set.State = StateUnavailable
		set.Detail = "tasks directory path could not be resolved"
		return set
	}
	set.Dir = filepath.Clean(canonical)

	entries, err := os.ReadDir(set.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			set.State = StateAbsent
			set.Detail = "tasks directory does not exist"
			return set
		}
		set.State = StateUnavailable
		set.Detail = "tasks directory could not be read"
		return set
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		slug, kind, ok := classify(name)
		if !ok {
			continue
		}
		if _, known := set.Features[slug]; !known && len(set.Features) >= MaxFeatures {
			set.Truncated = true
			continue
		}
		feature := set.Features[slug]
		feature.Slug = slug
		artifact := readArtifact(filepath.Join(set.Dir, name), kind == kindPRD)
		if kind == kindPRD {
			feature.PRD = artifact
		} else {
			feature.TaskList = artifact
		}
		set.Features[slug] = feature
	}

	// Absent halves are recorded explicitly so a missing PRD reads as a real
	// gap rather than as an unknown.
	for slug, feature := range set.Features {
		if feature.PRD.State == "" {
			feature.PRD.State = StateAbsent
		}
		if feature.TaskList.State == "" {
			feature.TaskList.State = StateAbsent
		}
		set.Features[slug] = feature
	}
	return set
}

// Lookup reads the artifacts for one exact slug from a directory without
// scanning it. It is used for the active feature worktree, where the slug is
// already known and the copy there is authoritative.
func Lookup(dir, slug string, now time.Time) (Feature, error) {
	if !ValidSlug(slug) {
		return Feature{}, fmt.Errorf("feature slug is not a canonical slug")
	}
	canonical, err := filepath.Abs(dir)
	if err != nil {
		return Feature{}, fmt.Errorf("resolve tasks directory: %w", err)
	}
	canonical = filepath.Clean(canonical)
	return Feature{
		Slug:     slug,
		PRD:      readArtifact(filepath.Join(canonical, "prd-"+slug+".md"), true),
		TaskList: readArtifact(filepath.Join(canonical, "tasks-"+slug+".md"), false),
	}, nil
}

type artifactKind int

const (
	kindPRD artifactKind = iota
	kindTaskList
)

func classify(name string) (slug string, kind artifactKind, ok bool) {
	if !strings.HasSuffix(name, ".md") {
		return "", 0, false
	}
	stem := strings.TrimSuffix(name, ".md")
	switch {
	case strings.HasPrefix(stem, "prd-"):
		slug = strings.TrimPrefix(stem, "prd-")
		kind = kindPRD
	case strings.HasPrefix(stem, "tasks-"):
		slug = strings.TrimPrefix(stem, "tasks-")
		kind = kindTaskList
	default:
		return "", 0, false
	}
	if !ValidSlug(slug) {
		return "", 0, false
	}
	return slug, kind, true
}

// readArtifact stats the file and, for a PRD, recovers its first heading. Only
// the leading MaxTitleBytes are read; the body is never retained.
func readArtifact(path string, wantTitle bool) Artifact {
	artifact := Artifact{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		artifact.Path = ""
		if os.IsNotExist(err) {
			artifact.State = StateAbsent
			return artifact
		}
		artifact.State = StateUnavailable
		artifact.Detail = "planning artifact could not be inspected"
		return artifact
	}
	if info.IsDir() {
		artifact.Path = ""
		artifact.State = StateMalformed
		artifact.Detail = "planning artifact is a directory"
		return artifact
	}
	artifact.State = StateAvailable
	artifact.ModTime = info.ModTime()
	if info.Size() == 0 {
		artifact.State = StateMalformed
		artifact.Detail = "planning artifact is empty"
		return artifact
	}
	if !wantTitle {
		return artifact
	}
	title, err := readTitle(path)
	if err != nil {
		artifact.State = StateUnavailable
		artifact.Detail = "planning artifact could not be read"
		return artifact
	}
	artifact.Title = title
	return artifact
}

func readTitle(path string) (string, error) {
	// #nosec G304 -- path is composed from a canonical tasks directory and a
	// filename validated against the exact planning-artifact pattern.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	buffer := make([]byte, MaxTitleBytes)
	read, err := file.Read(buffer)
	if err != nil && read == 0 {
		return "", err
	}
	for _, line := range strings.Split(string(buffer[:read]), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "# ") {
			continue
		}
		return Sanitize(strings.TrimSpace(trimmed[2:]), MaxTitleRunes), nil
	}
	return "", nil
}

// Sanitize removes control characters and bounds a value for display in a
// terminal, JSON payload, or Herdr board cell.
func Sanitize(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	cleaned = strings.TrimSpace(cleaned)
	runes := []rune(cleaned)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return cleaned
}
