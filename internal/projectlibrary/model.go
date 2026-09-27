// Package projectlibrary keeps metadata-only, Home-owned project catalog state.
// No catalog record itself grants access to source folders or child workspaces.
package projectlibrary

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

const (
	SchemaVersion    = 1
	maxDocumentBytes = 8 << 20
	maxRoots         = 64
	maxEntries       = 5000
	maxScans         = 512
	maxKnownScopes   = 1024
	maxSessions      = 4096
	maxProposals     = 256  // Agent suggestions are bounded; expired entries are pruned only on a later proposal.
	maxReviews       = 4096 // Keep consumed reviews for replay; bounded by the 8-MiB Home record.
	maxOperations    = 8192 // Never evict a retryable consequence to make a replay unsafe.
)

var (
	ErrUnavailable    = errors.New("project library Home is unavailable")
	ErrNotInitialized = errors.New("project library has not been initialized")
	ErrCorrupt        = errors.New("project library record is invalid")
	ErrConflict       = errors.New("project library changed; review again")
	ErrLimit          = errors.New("project library storage limit reached")
	ErrMirrorDiverged = errors.New("project library Home mirrors disagree; repair before writing")
)

// Scope is supplied by a host-authenticated caller, never from request JSON.
type Scope struct {
	OwnerUserID string
	HomeID      string
	ProviderID  string
	ProgramID   string
}

func (s Scope) valid() bool {
	return s.OwnerUserID != "" && s.HomeID != "" && s.ProviderID != "" && s.ProgramID != "" &&
		strings.TrimSpace(s.OwnerUserID) == s.OwnerUserID && strings.TrimSpace(s.HomeID) == s.HomeID &&
		strings.TrimSpace(s.ProviderID) == s.ProviderID && strings.TrimSpace(s.ProgramID) == s.ProgramID &&
		validText(s.OwnerUserID, 160) && validText(s.HomeID, 160) &&
		validText(s.ProviderID, 160) && validText(s.ProgramID, 160)
}

// Document is stored in the Home's canonical assistant-program envelope. Its
// independent revision covers observations as well as reviewed user edits.
type Document struct {
	SchemaVersion int                `json:"schema_version"`
	OwnerUserID   string             `json:"owner_user_id"`
	HomeID        string             `json:"home_id"`
	ProviderID    string             `json:"provider_id"`
	ProgramID     string             `json:"program_id"`
	Revision      int64              `json:"revision"`
	Roots         []Root             `json:"roots,omitempty"`
	Entries       []Entry            `json:"entries,omitempty"`
	Scans         []Scan             `json:"scans,omitempty"`
	Sessions      []StudioSession    `json:"sessions,omitempty"`
	Proposals     []ManagerProposal  `json:"proposals,omitempty"`
	Reviews       []ReviewReceipt    `json:"reviews,omitempty"`
	Operations    []OperationReceipt `json:"operations,omitempty"`
}

type Root struct {
	ID            string     `json:"id"`
	Path          string     `json:"path"` // Server-only. Never expose in agent search results.
	FileIdentity  string     `json:"file_identity"`
	Revision      int64      `json:"revision"`
	ApprovedAt    time.Time  `json:"approved_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
}

type Observation struct {
	RootID         string    `json:"root_id"`
	RelativeFolder string    `json:"relative_folder"`
	FileIdentity   string    `json:"file_identity"`
	Format         string    `json:"format"`
	Alternates     []string  `json:"alternates,omitempty"`
	ScanID         string    `json:"scan_id"`
	ScannedAt      time.Time `json:"scanned_at"`
	LastCheckedAt  time.Time `json:"last_checked_at,omitempty"`
	FileModifiedAt time.Time `json:"file_modified_at,omitempty"`
	Availability   string    `json:"availability"`
}

// Milestone preserves the legacy Home's stable milestone ID, due date and
// completion state rather than flattening an edited checklist into labels.
type Milestone struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	DueDate  string `json:"due_date,omitempty"`
	Complete bool   `json:"complete,omitempty"`
}

type Fields struct {
	DisplayName        string      `json:"display_name"`
	Purpose            string      `json:"purpose,omitempty"`
	Stage              string      `json:"stage,omitempty"`
	Status             string      `json:"status,omitempty"`
	Priority           *int        `json:"priority,omitempty"`
	NextAction         string      `json:"next_action,omitempty"`
	SessionDate        string      `json:"session_date,omitempty"`
	ReleaseDate        string      `json:"release_date,omitempty"`
	ArchiveReviewState string      `json:"archive_review_state,omitempty"`
	Milestones         []Milestone `json:"milestones,omitempty"`
	Blockers           []string    `json:"blockers,omitempty"`
	Deliverables       []string    `json:"deliverables,omitempty"`
	Revision           int64       `json:"revision"`
	Author             string      `json:"author,omitempty"`
	Source             string      `json:"source,omitempty"`
	UpdatedAt          time.Time   `json:"updated_at,omitempty"`
}

type ExactLink struct {
	WorkspaceID string `json:"workspace_id"`
	LinkID      string `json:"link_id"`
	Revision    int64  `json:"revision"`
}

type Entry struct {
	ID           string        `json:"id"`
	Revision     int64         `json:"revision"`
	Observations []Observation `json:"observations,omitempty"`
	Link         *ExactLink    `json:"link,omitempty"` // Current authority is always rechecked against the project.
	Fields       Fields        `json:"fields"`
}

type ScanScope struct {
	ID             string `json:"id"`
	RelativeFolder string `json:"relative_folder"`
	FileIdentity   string `json:"file_identity"`
}

type Scan struct {
	ID               string      `json:"id"`
	RootID           string      `json:"root_id"`
	RootRevision     int64       `json:"root_revision"`
	RootDigest       string      `json:"root_digest,omitempty"`
	Scope            string      `json:"scope,omitempty"`
	ScopeID          string      `json:"scope_id,omitempty"`
	ScopeIdentity    string      `json:"scope_identity,omitempty"`
	KnownScopes      []ScanScope `json:"known_scopes,omitempty"`
	ProviderRevision int64       `json:"provider_revision,omitempty"`
	ResultDigest     string      `json:"result_digest,omitempty"`
	Status           string      `json:"status"`
	StartedAt        time.Time   `json:"started_at"`
	FinishedAt       *time.Time  `json:"finished_at,omitempty"`
	EntriesSeen      int         `json:"entries_seen"`
	SkippedLinks     int         `json:"skipped_links"`
	SkippedOther     int         `json:"skipped_other"`
	PartialReason    string      `json:"partial_reason,omitempty"`
}

type StudioSession struct {
	ID          string     `json:"id"`
	EntryID     string     `json:"entry_id"`
	Revision    int64      `json:"revision"`
	Goal        string     `json:"goal"`
	Outcome     string     `json:"outcome,omitempty"` // Desired outcome, not observed DAW progress.
	TimeMinutes int        `json:"time_minutes,omitempty"`
	PlannedDate string     `json:"planned_date,omitempty"`
	ActualDate  string     `json:"actual_date,omitempty"`
	Recap       string     `json:"recap,omitempty"`
	Decisions   []string   `json:"decisions,omitempty"`
	Blockers    []string   `json:"blockers,omitempty"`
	Next        string     `json:"next_action,omitempty"`
	Author      string     `json:"author"`
	Source      string     `json:"source,omitempty"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ActivationBinding persists only server-derived identities and the creator's
// review digests. A browser sees the review projection, never this binding or
// a path-based selection. A creator retry is tied to one deterministic run ID.
type ActivationBinding struct {
	EntryID             string    `json:"entry_id"`
	EntryRevision       int64     `json:"entry_revision"`
	RootID              string    `json:"root_id"`
	RootRevision        int64     `json:"root_revision"`
	RelativeFolder      string    `json:"relative_folder"`
	FolderIdentity      string    `json:"folder_identity"`
	ProjectFile         string    `json:"project_file"`
	ProjectFileIdentity string    `json:"project_file_identity"`
	WorkspaceName       string    `json:"workspace_name"`
	BlueprintID         string    `json:"blueprint_id"`
	ProviderFingerprint string    `json:"provider_fingerprint"`
	ProviderGeneration  uint64    `json:"provider_generation"`
	ProviderInstalledAt time.Time `json:"provider_installed_at,omitempty"`
	CreatorInputDigest  string    `json:"creator_input_digest"`
	CreatorOwnerDigest  string    `json:"creator_owner_digest"`
}

func (b ActivationBinding) valid() bool {
	return b.EntryID != "" && validText(b.EntryID, 160) && b.EntryRevision > 0 &&
		b.RootID != "" && validText(b.RootID, 160) && b.RootRevision > 0 &&
		validRelative(b.RelativeFolder) && b.FolderIdentity != "" && validText(b.FolderIdentity, 160) &&
		b.ProjectFile != "" && len(b.ProjectFile) <= 255 && validText(b.ProjectFile, 255) &&
		b.ProjectFileIdentity != "" && validText(b.ProjectFileIdentity, 160) &&
		filepath.Base(b.ProjectFile) == b.ProjectFile && !strings.ContainsAny(b.ProjectFile, `/\\`) &&
		b.WorkspaceName != "" && validText(b.WorkspaceName, 128) &&
		b.BlueprintID != "" && validText(b.BlueprintID, 160) &&
		validDigest(b.ProviderFingerprint) && b.ProviderGeneration > 0 &&
		validDigest(b.CreatorInputDigest) && validDigest(b.CreatorOwnerDigest)
}

type ReviewReceipt struct {
	Activation       *ActivationBinding `json:"activation,omitempty"`
	Token            string             `json:"token"`
	Action           string             `json:"action"`
	Digest           string             `json:"digest"`
	Revision         int64              `json:"revision"`
	TargetID         string             `json:"target_id,omitempty"`
	FieldsRevision   int64              `json:"fields_revision,omitempty"`
	EntryRevision    int64              `json:"entry_revision,omitempty"`
	ProviderRevision int64              `json:"provider_revision,omitempty"`
	ExpiresAt        time.Time          `json:"expires_at"`
	ConsumedAt       *time.Time         `json:"consumed_at,omitempty"`
}

type OperationReceipt struct {
	Key           string    `json:"key"`
	Action        string    `json:"action"`
	Digest        string    `json:"digest"`
	ConsequenceID string    `json:"consequence_id"`
	Revision      int64     `json:"revision"`
	RecordedAt    time.Time `json:"recorded_at"`
}

func validText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

func validRelative(s string) bool {
	if s == "" {
		return true // the selected root itself
	}
	if !validText(s, 1024) || filepath.IsAbs(s) || filepath.Clean(s) != s || s == "." {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(s), "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, "\\") {
			return false
		}
	}
	return true
}

func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (f Fields) valid() bool {
	if !validText(f.DisplayName, 160) || !validText(f.Purpose, 240) ||
		!validText(f.NextAction, 240) || !validText(f.Author, 160) || !validText(f.Source, 80) ||
		!validDate(f.SessionDate) || !validDate(f.ReleaseDate) || f.Revision < 0 ||
		len(f.Milestones) > 16 || len(f.Blockers) > 16 || len(f.Deliverables) > 16 {
		return false
	}
	if f.Priority != nil && (*f.Priority < 0 || *f.Priority > 5) {
		return false
	}
	switch f.Stage {
	case "", "idea", "writing", "recording", "editing", "mixing", "mastering":
	default:
		return false
	}
	switch f.Status {
	case "", "planning", "active", "on_hold", "complete", "archived":
	default:
		return false
	}
	switch f.ArchiveReviewState {
	case "", "not_ready", "ready", "reviewed":
	default:
		return false
	}
	seenMilestones := map[string]bool{}
	for _, milestone := range f.Milestones {
		if milestone.ID == "" || seenMilestones[milestone.ID] || !validText(milestone.ID, 160) ||
			milestone.Label == "" || !validText(milestone.Label, 240) || !validDate(milestone.DueDate) {
			return false
		}
		seenMilestones[milestone.ID] = true
	}
	for _, list := range [][]string{f.Blockers, f.Deliverables} {
		for _, item := range list {
			if item == "" || !validText(item, 240) {
				return false
			}
		}
	}
	return true
}

func (d Document) valid(scope Scope) bool {
	if !scope.valid() || d.SchemaVersion != SchemaVersion || d.Revision < 1 ||
		d.OwnerUserID != scope.OwnerUserID || d.HomeID != scope.HomeID ||
		d.ProviderID != scope.ProviderID || d.ProgramID != scope.ProgramID ||
		len(d.Roots) > maxRoots || len(d.Entries) > maxEntries || len(d.Scans) > maxScans ||
		len(d.Sessions) > maxSessions || len(d.Proposals) > maxProposals || len(d.Reviews) > maxReviews || len(d.Operations) > maxOperations {
		return false
	}
	roots := make(map[string]bool, len(d.Roots))
	for _, r := range d.Roots {
		if r.ID == "" || roots[r.ID] || !filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path ||
			!validText(r.Path, 4096) || !validText(r.FileIdentity, 160) || r.FileIdentity == "" ||
			r.Revision < 1 || r.ApprovedAt.IsZero() {
			return false
		}
		roots[r.ID] = true
	}
	entries := make(map[string]bool, len(d.Entries))
	for _, e := range d.Entries {
		if e.ID == "" || entries[e.ID] || e.Revision < 1 || !e.Fields.valid() ||
			(len(e.Observations) == 0 && e.Link == nil) || len(e.Observations) > maxRoots {
			return false
		}
		entries[e.ID] = true
		if e.Link != nil && (e.Link.WorkspaceID == "" || e.Link.LinkID == "" || e.Link.Revision < 1) {
			return false
		}
		seenRoots := map[string]bool{}
		for _, o := range e.Observations {
			if !roots[o.RootID] || seenRoots[o.RootID] || !validRelative(o.RelativeFolder) ||
				o.FileIdentity == "" || !validText(o.FileIdentity, 160) || o.ScanID == "" || o.ScannedAt.IsZero() ||
				len(o.Alternates) > 64 {
				return false
			}
			if !folderdigest.KnownProjectFormat(o.Format) {
				return false
			}
			switch o.Availability {
			case "available", "unavailable", "ambiguous", "revoked_source", "not_scanned":
			default:
				return false
			}
			seenRoots[o.RootID] = true
			for _, alternate := range o.Alternates {
				if alternate == "" || filepath.Base(alternate) != alternate || !validText(alternate, 240) {
					return false
				}
			}
		}
	}
	scans := map[string]bool{}
	for _, scan := range d.Scans {
		if scan.ID == "" || scans[scan.ID] || !roots[scan.RootID] || scan.RootRevision < 1 ||
			!validRelative(scan.Scope) || (scan.Scope != "" && (scan.ScopeID == "" || scan.ScopeIdentity == "")) ||
			(scan.Scope == "" && (scan.ScopeID != "" || scan.ScopeIdentity != "")) ||
			len(scan.KnownScopes) > maxKnownScopes || scan.StartedAt.IsZero() || scan.ProviderRevision < 0 ||
			!validDigest(scan.RootDigest) || (scan.ResultDigest != "" && !validDigest(scan.ResultDigest)) ||
			scan.EntriesSeen < 0 || scan.EntriesSeen > scanEntryLimit ||
			scan.SkippedLinks < 0 || scan.SkippedOther < 0 || !validText(scan.PartialReason, 120) {
			return false
		}
		switch scan.Status {
		case "running":
			if scan.FinishedAt != nil || scan.ResultDigest != "" {
				return false
			}
		case "interrupted":
			if scan.FinishedAt == nil {
				return false
			}
		case "complete", "partial", "failed", "cancelled":
			if scan.FinishedAt == nil || !validDigest(scan.ResultDigest) {
				return false
			}
		default:
			return false
		}
		if scan.FinishedAt != nil && scan.FinishedAt.Before(scan.StartedAt) {
			return false
		}
		scans[scan.ID] = true
		knownIDs := map[string]bool{}
		for _, known := range scan.KnownScopes {
			if known.ID == "" || knownIDs[known.ID] || known.FileIdentity == "" ||
				known.RelativeFolder == "" || !validRelative(known.RelativeFolder) ||
				(scan.Scope != "" && !strings.HasPrefix(known.RelativeFolder, scan.Scope+string(filepath.Separator))) {
				return false
			}
			knownIDs[known.ID] = true
		}
	}
	for _, e := range d.Entries {
		for _, o := range e.Observations {
			if !scans[o.ScanID] {
				return false
			}
		}
	}
	sessions := map[string]bool{}
	for _, session := range d.Sessions {
		if session.ID == "" || sessions[session.ID] || !entries[session.EntryID] || session.Revision < 1 ||
			!validText(session.Goal, 500) || !validText(session.Outcome, 500) ||
			!validText(session.Recap, 2000) || !validText(session.Next, 240) ||
			!validText(session.Author, 160) || !validDate(session.PlannedDate) || !validDate(session.ActualDate) ||
			session.TimeMinutes < 0 || session.TimeMinutes > 480 || len(session.Decisions) > 16 ||
			len(session.Blockers) > 16 || session.UpdatedAt.IsZero() ||
			(session.AcceptedAt != nil && (session.AcceptedAt.IsZero() || session.AcceptedAt.After(session.UpdatedAt))) ||
			(!session.CreatedAt.IsZero() && session.CreatedAt.After(session.UpdatedAt)) ||
			(session.Source != "" && session.Source != "reviewed_user") {
			return false
		}
		for _, list := range [][]string{session.Decisions, session.Blockers} {
			for _, item := range list {
				if item == "" || !validText(item, 240) {
					return false
				}
			}
		}
		switch session.State {
		case "draft", "reviewed", "accepted", "cancelled":
		default:
			return false
		}
		sessions[session.ID] = true
	}
	proposals := map[string]bool{}
	for _, proposal := range d.Proposals {
		if proposal.ID == "" || proposals[proposal.ID] || !validText(proposal.ID, 160) ||
			!entries[proposal.EntryID] || proposal.FieldsRevision < 0 || proposal.BindingRevision < 1 ||
			proposal.AgentInstanceID == "" || !validText(proposal.AgentInstanceID, 160) ||
			proposal.AgentName == "" || !validText(proposal.AgentName, 160) ||
			proposal.NextAction == "" || !validText(proposal.NextAction, 240) ||
			!validText(proposal.Reason, 500) || !validDigest(proposal.Digest) ||
			proposal.CreatedAt.IsZero() || !proposal.ExpiresAt.After(proposal.CreatedAt) {
			return false
		}
		proposals[proposal.ID] = true
	}
	used := map[string]bool{}
	for _, review := range d.Reviews {
		if review.Token == "" || used[review.Token] || review.Action == "" || review.Digest == "" ||
			review.Revision < 0 || review.FieldsRevision < 0 || review.EntryRevision < 0 || review.ProviderRevision < 0 ||
			(review.Action == "edit_fields" && review.TargetID == "") || review.ExpiresAt.IsZero() ||
			(review.Action == "activate_project" && (review.Activation == nil || !review.Activation.valid() || review.TargetID == "")) ||
			(review.Activation != nil && (review.Action != "activate_project" || !review.Activation.valid())) {
			return false
		}
		used[review.Token] = true
	}
	used = map[string]bool{}
	for _, op := range d.Operations {
		if op.Key == "" || used[op.Key] || op.Action == "" || op.Digest == "" ||
			op.Revision < 1 || op.Revision > d.Revision || op.RecordedAt.IsZero() {
			return false
		}
		used[op.Key] = true
	}
	return true
}

// The canonical workspace envelope persists raw JSON, so re-decoding here
// rejects unknown fields even when an older workspace envelope permits them.
func decodeDocument(raw json.RawMessage, scope Scope) (Document, error) {
	if len(raw) == 0 || len(raw) > maxDocumentBytes {
		return Document{}, ErrCorrupt
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var doc Document
	if err := decoder.Decode(&doc); err != nil || !doc.valid(scope) {
		return Document{}, ErrCorrupt
	}
	var tail any
	if err := decoder.Decode(&tail); !errors.Is(err, io.EOF) {
		return Document{}, ErrCorrupt
	}
	return doc, nil
}
