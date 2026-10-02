package projectlibrary

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Candidate is one observed folder, not one project per authoritative file.
// Alternatives are basename-only evidence. Nothing here activates a project.
// FileModifiedAt is the newest save time among the Alternates, taken from the
// listing's own stat; it is never read from inside a project file.
type Candidate struct {
	RelativeFolder string    `json:"relative_folder"`
	FileIdentity   string    `json:"file_identity"`
	Format         string    `json:"format"`
	Alternates     []string  `json:"alternates,omitempty"`
	Ambiguous      bool      `json:"ambiguous"`
	FileModifiedAt time.Time `json:"file_modified_at,omitempty"`
}

// ObservedScope is inert server-known directory evidence for a separately
// reviewed narrower scan. Its path is relative and its identity is pinned.
type ObservedScope struct {
	RelativeFolder string `json:"relative_folder"`
	FileIdentity   string `json:"file_identity"`
}

type Discovery struct {
	Scope         string          `json:"scope,omitempty"`
	KnownScopes   []ObservedScope `json:"known_scopes,omitempty"`
	RootID        string          `json:"root_id"`
	RootRevision  int64           `json:"root_revision"`
	StartedAt     time.Time       `json:"started_at"`
	EntriesSeen   int             `json:"entries_seen"`
	SkippedLinks  int             `json:"skipped_links"`
	SkippedOther  int             `json:"skipped_other"`
	PartialReason string          `json:"partial_reason,omitempty"`
	Candidates    []Candidate     `json:"candidates"`
}

const scanEntryLimit = 5000

var ignoredFolders = map[string]bool{"node_modules": true, "Library": true, "Trash": true, ".Trash": true}

// Discover inspects the selected root and its immediate child directories.
// A deeper folder requires a separate reviewed, server-known follow-up scope;
// this method never traverses recursively or opens a project/audio file.
// The scanned observations are inert until task 2.5's reviewed persistence.
func (r *Roots) Discover(ctx context.Context, scope Scope, rootID string) (Discovery, error) {
	root, err := r.VerifyConnectedRoot(scope, rootID)
	if err != nil {
		return Discovery{}, err
	}
	return r.discoverAt(ctx, scope, root, "", root.FileIdentity)
}

// discoverAt accepts only scope evidence reloaded from the Home document by
// CommitScopedScan; callers must never pass client-authored relative strings.
func (r *Roots) discoverAt(ctx context.Context, scope Scope, root Root, relative, identity string) (Discovery, error) {
	result := Discovery{RootID: root.ID, RootRevision: root.Revision, Scope: relative, StartedAt: r.library.now().UTC(),
		Candidates: make([]Candidate, 0)}
	deadline := time.Now().Add(folderdigest.DefaultBudget)
	scanCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	list := func(relative, expectedIdentity string) ([]DirectoryRow, bool, error) {
		if err := ctx.Err(); err != nil {
			result.PartialReason = "cancelled"
			return nil, false, err
		}
		if time.Now().After(deadline) {
			result.PartialReason = "time"
			return nil, false, ErrLimit
		}
		remaining := scanEntryLimit - result.EntriesSeen
		if remaining <= 0 {
			result.PartialReason = "entries"
			return nil, false, ErrLimit
		}
		rows, partial, err := r.readDirectoryWithIdentity(scanCtx, scope, root.ID, relative, expectedIdentity, remaining)
		if errors.Is(err, context.DeadlineExceeded) {
			result.PartialReason = "time"
			return nil, false, ErrLimit
		}
		result.EntriesSeen += len(rows)
		if partial && result.PartialReason == "" {
			result.PartialReason = "entries_or_unreadable"
		}
		return rows, partial, err
	}
	rows, partial, err := list(relative, identity)
	if err != nil {
		if result.PartialReason == "time" {
			return result, nil
		}
		return Discovery{}, err
	}
	if candidate, ok := recognizedProject(relative, identity, rows); ok {
		result.Candidates = append(result.Candidates, candidate)
	}
	// ReadDirectory walks each child through its pinned root descriptor. A
	// folder can move or be replaced between listings: a failed child is
	// partial, not evidence that any previously cataloged record disappeared.
	for _, row := range rows {
		if row.IsLink {
			result.SkippedLinks++
			continue
		}
		if row.Unreadable || strings.HasPrefix(row.Name, ".") || strings.HasSuffix(row.Name, ".icloud") {
			result.SkippedOther++
			continue
		}
		marker, isMarker := folderdigest.MatchMarker(row.Name, row.IsDir)
		if !row.IsDir || (isMarker && marker.ProjectFormat != "") {
			// A recognized project bundle is one marker of its containing
			// folder, not a second project or a directory to walk inside.
			continue
		}
		if ignoredFolders[row.Name] || strings.HasPrefix(row.Name, ".") {
			result.SkippedOther++
			continue
		}
		if result.PartialReason == "entries" || result.PartialReason == "time" || result.PartialReason == "cancelled" {
			break
		}
		childRelative := filepath.Join(relative, row.Name)
		childRows, childPartial, readErr := list(childRelative, row.Identity)
		if readErr != nil {
			if result.PartialReason == "time" || result.PartialReason == "entries" {
				break
			}
			if _, checkErr := r.VerifyConnectedRoot(scope, root.ID); checkErr != nil {
				return Discovery{}, ErrUnavailable
			}
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if result.PartialReason == "" {
				result.PartialReason = "unreadable_or_unavailable"
			}
			result.SkippedOther++
			continue
		}
		result.KnownScopes = append(result.KnownScopes, ObservedScope{RelativeFolder: childRelative, FileIdentity: row.Identity})
		if candidate, ok := recognizedProject(childRelative, row.Identity, childRows); ok {
			result.Candidates = append(result.Candidates, candidate)
		}
		for _, child := range childRows {
			if child.IsLink {
				result.SkippedLinks++
				continue
			}
			if child.Unreadable || strings.HasPrefix(child.Name, ".") ||
				strings.HasSuffix(child.Name, ".icloud") || (child.IsDir && ignoredFolders[child.Name]) {
				result.SkippedOther++
				continue
			}
			marker, isMarker := folderdigest.MatchMarker(child.Name, child.IsDir)
			if child.IsDir && (!isMarker || marker.ProjectFormat == "") {
				result.KnownScopes = append(result.KnownScopes, ObservedScope{
					RelativeFolder: filepath.Join(childRelative, child.Name), FileIdentity: child.Identity})
			}
		}
		if childPartial && result.PartialReason == "" {
			result.PartialReason = "entries_or_unreadable"
		}
	}
	if partial && result.PartialReason == "" {
		result.PartialReason = "entries_or_unreadable"
	}
	if time.Now().After(deadline) && result.PartialReason == "" {
		result.PartialReason = "time"
	}
	if live, err := r.VerifyConnectedRoot(scope, root.ID); err != nil || live.Revision != root.Revision {
		return Discovery{}, ErrUnavailable
	}
	return result, nil
}

func recognizedProject(relative, identity string, rows []DirectoryRow) (Candidate, bool) {
	groups := map[string][]string{}
	newest := map[string]time.Time{}
	for _, row := range rows {
		if row.IsLink || row.Unreadable || strings.HasPrefix(row.Name, ".") || strings.HasSuffix(row.Name, ".icloud") {
			continue
		}
		marker, ok := folderdigest.MatchMarker(row.Name, row.IsDir)
		if ok && marker.ProjectFormat != "" {
			groups[marker.ProjectFormat] = append(groups[marker.ProjectFormat], row.Name)
			if row.ModifiedAt.After(newest[marker.ProjectFormat]) {
				newest[marker.ProjectFormat] = row.ModifiedAt
			}
		}
	}
	candidate := Candidate{RelativeFolder: relative, FileIdentity: identity}
	// Marker table order is the host's deterministic choice of a primary
	// format. Alternatives and mixed formats remain explicit ambiguity, not
	// implicit permission to connect through a particular plugin. Only the
	// primary format's files date the song.
	for _, marker := range folderdigest.Markers {
		if choices := groups[marker.ProjectFormat]; len(choices) > 0 {
			candidate.Format, candidate.Alternates = marker.ProjectFormat, choices
			candidate.FileModifiedAt = newest[marker.ProjectFormat].UTC()
			break
		}
	}
	if candidate.Format == "" {
		return Candidate{}, false
	}
	sort.Strings(candidate.Alternates)
	candidate.Ambiguous = len(candidate.Alternates) > 1 || len(groups) > 1
	return candidate, true
}
