package planning

import (
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	// MaxBacklogBytes bounds how much of BACKLOG.md is parsed.
	MaxBacklogBytes = 512 * 1024
	// MaxBacklogEntries bounds how many lifecycle entries are retained.
	MaxBacklogEntries = 500
	// MaxEntryRunes bounds one displayed backlog entry.
	MaxEntryRunes = 200
)

// Lifecycle is the BACKLOG.md section an entry was found under.
type Lifecycle string

const (
	LifecycleDoing   Lifecycle = "doing"
	LifecycleShipped Lifecycle = "shipped"
	LifecycleDropped Lifecycle = "dropped"
)

// Entry is one tracked BACKLOG.md lifecycle line.
type Entry struct {
	// Slug is the exact feature slug the entry resolves to, empty when the
	// entry is free-form prose that cannot be joined safely.
	Slug string
	// Lifecycle is the section the entry appeared under.
	Lifecycle Lifecycle
	// Text is the bounded, sanitized entry as written.
	Text string
	// Line is the 1-based line number in BACKLOG.md.
	Line int
}

// Backlog is the parsed lifecycle view of BACKLOG.md. The Ideas section is
// deliberately not parsed: matching free-form ideas to slugs is unsafe.
type Backlog struct {
	// Path is the canonical file that was read, empty when absent.
	Path string
	// State is the outcome of reading the file.
	State State
	// Detail is a sanitized reason for a non-available state.
	Detail string
	// Entries are the resolvable lifecycle entries keyed by exact slug. Later
	// sections win, so a slug listed in both Doing and Shipped resolves to the
	// terminal record while the drift stays visible via Unresolved/All.
	Entries map[string]Entry
	// All preserves every parsed lifecycle entry in file order, including ones
	// whose slug could not be resolved.
	All []Entry
	// Truncated is true when a bound capped parsing.
	Truncated bool
	// ObservedAt is when the file was read.
	ObservedAt time.Time
}

// Entry returns the lifecycle record for an exact slug.
func (b Backlog) Entry(slug string) (Entry, bool) {
	entry, ok := b.Entries[slug]
	return entry, ok
}

var (
	sectionHeading = regexp.MustCompile(`(?i)^##\s+(.*?)\s*$`)
	prdReference   = regexp.MustCompile(`(?:^|[^a-z0-9-])prd-([a-z0-9][a-z0-9-]{0,79})\.md`)
	leadingSlug    = regexp.MustCompile(`^(?:\d{4}-\d{2}-\d{2}\s+)?([a-z0-9][a-z0-9-]{0,79})(?:\s|$)`)
	droppedMarker  = regexp.MustCompile(`(?i)\b(dropped|abandoned|superseded|won'?t\s+do)\b`)
	mergedMarker   = regexp.MustCompile(`(?i)\bPR\s*#\d+\b.*\bmerged\b|\bmerged\b.*\bPR\s*#\d+\b`)
)

// ReadBacklog parses the tracked lifecycle sections of BACKLOG.md. It never
// writes the file and never reinterprets the Ideas section.
func ReadBacklog(path string, now time.Time) Backlog {
	backlog := Backlog{Path: path, State: StateAvailable, Entries: map[string]Entry{}, ObservedAt: now}
	info, err := os.Stat(path)
	if err != nil {
		backlog.Path = ""
		if os.IsNotExist(err) {
			backlog.State = StateAbsent
			backlog.Detail = "BACKLOG.md does not exist"
			return backlog
		}
		backlog.State = StateUnavailable
		backlog.Detail = "BACKLOG.md could not be inspected"
		return backlog
	}
	if info.Size() > MaxBacklogBytes {
		backlog.Truncated = true
	}
	// #nosec G304 -- path is composed by the caller from the canonical
	// repository root and the fixed BACKLOG.md filename.
	contents, err := os.ReadFile(path)
	if err != nil {
		backlog.State = StateUnavailable
		backlog.Detail = "BACKLOG.md could not be read"
		return backlog
	}
	if len(contents) > MaxBacklogBytes {
		contents = contents[:MaxBacklogBytes]
		backlog.Truncated = true
	}

	section := Lifecycle("")
	for index, line := range strings.Split(string(contents), "\n") {
		if heading := sectionHeading.FindStringSubmatch(line); heading != nil {
			section = classifySection(heading[1])
			continue
		}
		if section == "" || !strings.HasPrefix(line, "- ") {
			continue
		}
		if len(backlog.All) >= MaxBacklogEntries {
			backlog.Truncated = true
			break
		}
		text := Sanitize(strings.TrimSpace(line[2:]), MaxEntryRunes)
		if text == "" {
			continue
		}
		entry := Entry{
			Slug:      resolveSlug(text),
			Lifecycle: resolveLifecycle(section, text),
			Text:      text,
			Line:      index + 1,
		}
		backlog.All = append(backlog.All, entry)
		if entry.Slug != "" {
			backlog.Entries[entry.Slug] = entry
		}
	}
	return backlog
}

// classifySection maps a heading to a lifecycle. The repository uses a single
// combined "Shipped / dropped" section, so entries under it are classified
// individually by their own text.
func classifySection(heading string) Lifecycle {
	normalized := strings.ToLower(strings.TrimSpace(heading))
	switch {
	case normalized == "doing":
		return LifecycleDoing
	case strings.HasPrefix(normalized, "shipped"):
		return LifecycleShipped
	case strings.HasPrefix(normalized, "dropped"):
		return LifecycleDropped
	default:
		return ""
	}
}

// resolveLifecycle refines a combined shipped/dropped section per entry. An
// entry only counts as dropped when it says so and shows no merge evidence.
func resolveLifecycle(section Lifecycle, text string) Lifecycle {
	if section != LifecycleShipped {
		return section
	}
	if droppedMarker.MatchString(text) && !mergedMarker.MatchString(text) {
		return LifecycleDropped
	}
	return LifecycleShipped
}

// resolveSlug prefers an explicit prd-<slug>.md reference and falls back to a
// leading slug token. Free-form prose resolves to no slug rather than to a
// guess, so an unjoinable entry stays visible without corrupting the join.
func resolveSlug(text string) string {
	if match := prdReference.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	// The date prefix is optional, so an unprefixed regex would happily accept
	// the date itself as a hyphenated slug. Require a non-numeric first
	// segment instead of trusting the optional prefix to be consumed.
	if match := leadingSlug.FindStringSubmatch(text); match != nil && strings.Contains(match[1], "-") {
		if head, _, _ := strings.Cut(match[1], "-"); strings.Trim(head, "0123456789") != "" {
			return match[1]
		}
	}
	return ""
}
