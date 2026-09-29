package projectlibrary

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// LibraryDigest is the deterministic, model-free result of the most recent
// complete or partial scan. It is data, not a proposal: it grants nothing
// and a newer completed scan replaces it. It holds opaque IDs and counts
// only; the shelf derives a display name from the owner-only roots list.
type LibraryDigest struct {
	ScanID            string    `json:"scan_id"`
	RootID            string    `json:"root_id"`
	ScannedAt         time.Time `json:"scanned_at"`
	Coverage          string    `json:"coverage"`
	Projects          int       `json:"projects"`
	New               int       `json:"new"`
	Updated           int       `json:"updated"`
	Unavailable       int       `json:"unavailable"`
	UnsupportedFormat int       `json:"unsupported_format"`
	Activatable       int       `json:"activatable"`
	// SetupNote explains why Activatable and UnsupportedFormat are zero
	// without implying the entries are unusable.
	SetupNote string `json:"setup_note,omitempty"`
}

// Setup notes. They name a missing piece of host evidence, never a remedy
// the digest could perform.
const (
	setupNoteUnchecked               = "setup_check_unavailable"
	setupNoteHomeProviderUnavailable = "home_provider_unavailable"
	setupNoteProjectProviderMissing  = "project_provider_unavailable"
	setupNoteProviderAmbiguous       = "provider_ambiguous"
)

func (d LibraryDigest) valid(scans []Scan) bool {
	if d.ScanID == "" || !validText(d.ScanID, 160) || d.RootID == "" || !validText(d.RootID, 160) ||
		d.ScannedAt.IsZero() || (d.Coverage != "complete" && d.Coverage != "partial") {
		return false
	}
	for _, count := range []int{d.Projects, d.New, d.Updated, d.Unavailable, d.UnsupportedFormat, d.Activatable} {
		if count < 0 || count > maxEntries {
			return false
		}
	}
	if d.New > d.Projects || d.Updated > d.Projects || d.Activatable+d.UnsupportedFormat > d.Projects {
		return false
	}
	switch d.SetupNote {
	case "":
	case setupNoteUnchecked, setupNoteHomeProviderUnavailable, setupNoteProjectProviderMissing, setupNoteProviderAmbiguous:
		if d.Activatable != 0 || d.UnsupportedFormat != 0 {
			return false
		}
	default:
		return false
	}
	for _, scan := range scans {
		if scan.ID == d.ScanID {
			return scan.RootID == d.RootID && scan.Status == d.Coverage
		}
	}
	return false
}

// setupEvidence is the persisted-state half of activation eligibility: the
// file extensions the one compatible installed blueprint can attach, or why
// none applies. It reads no folder, project file or folder owner, so a
// setup review must still repeat every check before anything is created.
type setupEvidence struct {
	extensions []string
	note       string
}

func (s *Store) setupEvidence(scope Scope, state *workspace.AssistantProgramState, installed []plugin.InstalledPlugin, listErr error) setupEvidence {
	if s == nil || s.installed == nil || state == nil {
		return setupEvidence{note: setupNoteUnchecked}
	}
	owner := state.HomeProvider
	if owner == nil && state.GroupTemplate != nil {
		owner = state.GroupTemplate.ProgramHomeOwner
	}
	if errors.Is(listErr, ErrUnavailable) {
		return setupEvidence{note: setupNoteUnchecked}
	}
	if listErr != nil || owner == nil || !plugin.IndependentHomeProviderEvidenceAvailable(installed, owner) {
		return setupEvidence{note: setupNoteHomeProviderUnavailable}
	}
	chosen, _, ambiguous := compatibleProjectBlueprint(installed, owner, scope)
	switch {
	case ambiguous:
		return setupEvidence{note: setupNoteProviderAmbiguous}
	case chosen == nil:
		return setupEvidence{note: setupNoteProjectProviderMissing}
	}
	return setupEvidence{extensions: slices.Clone(chosen.Template.ProjectConnection.AttachExisting.EntryExtensions)}
}

func (e setupEvidence) supports(alternates []string) bool {
	for _, name := range alternates {
		for _, ext := range e.extensions {
			if strings.EqualFold(filepath.Ext(name), ext) {
				return true
			}
		}
	}
	return false
}

// buildLibraryDigest compares the catalog before and after one scan's
// reconciliation. Every input is already persisted or about to be, in the
// same Home update; nothing here touches the filesystem.
func buildLibraryDigest(before, after []Entry, scan Scan, finishedAt time.Time, coverage string, evidence setupEvidence) LibraryDigest {
	digest := LibraryDigest{ScanID: scan.ID, RootID: scan.RootID, ScannedAt: finishedAt,
		Coverage: coverage, SetupNote: evidence.note}
	prior := make(map[string]Entry, len(before))
	for _, entry := range before {
		prior[entry.ID] = entry
	}
	for _, entry := range after {
		current := observationFor(entry, scan.RootID)
		if current == nil {
			continue
		}
		previousEntry, existed := prior[entry.ID]
		var previous *Observation
		if existed {
			previous = observationFor(previousEntry, scan.RootID)
		}
		if current.ScanID != scan.ID {
			if current.Availability == "unavailable" && previous != nil && previous.Availability != "unavailable" {
				digest.Unavailable++
			}
			continue
		}
		digest.Projects++
		switch {
		case !existed:
			digest.New++
		case previous == nil || observationChanged(*previous, *current):
			digest.Updated++
		}
		if entry.Link != nil || evidence.note != "" ||
			(current.Availability != "available" && current.Availability != "ambiguous") {
			continue
		}
		if evidence.supports(current.Alternates) {
			digest.Activatable++
		} else {
			digest.UnsupportedFormat++
		}
	}
	return digest
}

func observationFor(entry Entry, rootID string) *Observation {
	for i := range entry.Observations {
		if entry.Observations[i].RootID == rootID {
			return &entry.Observations[i]
		}
	}
	return nil
}

func observationChanged(before, after Observation) bool {
	return before.FileIdentity != after.FileIdentity || before.Format != after.Format ||
		before.Availability != after.Availability || !slices.Equal(before.Alternates, after.Alternates)
}

// publishScanCompleted runs only after the fenced Home write that recorded
// the digest succeeded. It never runs for a replayed commit or on a read.
func (s *Store) publishScanCompleted(scope Scope, digest LibraryDigest) {
	if s == nil || s.events == nil {
		return
	}
	payload := workspace.LibraryScanCompleted{HomeID: scope.HomeID, ScanID: digest.ScanID,
		RootID: digest.RootID, Coverage: digest.Coverage, Projects: digest.Projects, New: digest.New,
		Updated: digest.Updated, Unavailable: digest.Unavailable,
		UnsupportedFormat: digest.UnsupportedFormat, Activatable: digest.Activatable}
	s.events.Publish(payload.Event("project_library"))
	logger.Info("Project library scan completed", logger.Fields{"home_id": scope.HomeID,
		"scan_id": digest.ScanID, "coverage": digest.Coverage, "projects": digest.Projects})
}

// LibrarySummary is the bounded owner-Home projection behind the map
// badge, the Action Center card and the shelf's digest line. Building it
// never scans, reads a folder or mutates the Home.
type LibrarySummary struct {
	Initialized    bool           `json:"initialized"`
	Revision       int64          `json:"revision,omitempty"`
	Digest         *LibraryDigest `json:"digest"`
	ReadyProposals int            `json:"ready_proposals"`
	// ProposalRun is the Manager review receipt for the digest's scan, if any:
	// the shelf shows its skipped reason or its source.
	ProposalRun *ProposalRun `json:"proposal_run,omitempty"`
}

// Summary returns zeros for an uninitialized Home. A digest whose root was
// disconnected since the scan is no longer current and is left out.
func (s *Store) Summary(scope Scope) (LibrarySummary, error) {
	doc, state, err := s.readSnapshot(scope)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return LibrarySummary{}, nil
		}
		return LibrarySummary{}, err
	}
	summary := LibrarySummary{Initialized: true, Revision: doc.Revision}
	if doc.Digest != nil && digestRootActive(doc, state, doc.Digest.RootID) {
		current := *doc.Digest
		summary.Digest = &current
		if run, ok := findProposalRun(doc, current.ScanID); ok {
			presented := s.presentRun(run)
			summary.ProposalRun = &presented
		}
	}
	for _, row := range s.managerProposalRows(scope, doc, state) {
		if row.Status == "ready" {
			summary.ReadyProposals++
		}
	}
	return summary, nil
}

func digestRootActive(doc Document, state *workspace.AssistantProgramState, rootID string) bool {
	if state != nil && slices.Contains(state.ProjectLibraryInactiveRoots, rootID) {
		return false
	}
	for _, root := range doc.Roots {
		if root.ID == rootID {
			return root.RevokedAt == nil
		}
	}
	return false
}
