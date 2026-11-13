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
	// Connected counts songs found by this scan that already have an exact
	// project link; they are neither "can be set up" nor "unsupported". A digest
	// stored before this field existed reads as zero, which stays valid.
	Connected int `json:"connected,omitempty"`
	// NeedsFileChoice is the part of Activatable whose folder holds several
	// candidate project files, so setup requires an explicit file choice. It is
	// never a guess about which file is authoritative.
	NeedsFileChoice int `json:"needs_file_choice,omitempty"`
	// SetupNote explains why Activatable and UnsupportedFormat are zero
	// without implying the entries are unusable.
	SetupNote string `json:"setup_note,omitempty"`
	// Moved counts songs this scan found at a new folder and relinked to
	// their existing record (library-upkeep FR8); they are part of Projects,
	// never New or Updated. RelinkConflicts counts folders that matched more
	// than one prior record (or one record matched by several folders), so
	// nothing was relinked for them. Older digests read both as zero.
	Moved           int `json:"moved,omitempty"`
	RelinkConflicts int `json:"relink_conflicts,omitempty"`
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
	for _, count := range []int{d.Projects, d.New, d.Updated, d.Unavailable, d.UnsupportedFormat, d.Activatable,
		d.Connected, d.NeedsFileChoice, d.Moved, d.RelinkConflicts} {
		if count < 0 || count > maxEntries {
			return false
		}
	}
	if d.New > d.Projects || d.Updated > d.Projects || d.Connected+d.Activatable+d.UnsupportedFormat > d.Projects ||
		d.NeedsFileChoice > d.Activatable || d.Moved > d.Projects || d.New+d.Updated+d.Moved > d.Projects {
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
		case current.MovedFrom != nil && current.MovedFrom.ScanID == scan.ID:
			digest.Moved++
		case previous == nil || observationChanged(*previous, *current):
			digest.Updated++
		}
		if entry.Link != nil {
			digest.Connected++
			continue
		}
		if evidence.note != "" ||
			(current.Availability != "available" && current.Availability != "ambiguous") {
			continue
		}
		if evidence.supports(current.Alternates) {
			digest.Activatable++
			if current.Availability == "ambiguous" {
				digest.NeedsFileChoice++
			}
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
		UnsupportedFormat: digest.UnsupportedFormat, Activatable: digest.Activatable, Moved: digest.Moved}
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
	// FactsRead is the Home assistant's newest song-reading turn, shown as it is
	// now (a turn a previous process cut off reads as stopped by a restart). Nil
	// when no read has been recorded (assistant-reads-projects).
	FactsRead *FactsReadRun `json:"facts_read,omitempty"`
	// LastCompleteScanAt is when the newest complete scan of a connected root
	// finished. The shelf's freshness line and the map badge's age read it; a
	// partial, failed or cancelled scan never moves it. Nil means no root has
	// a complete scan yet.
	LastCompleteScanAt *time.Time `json:"last_complete_scan_at,omitempty"`
	// ToSetUp counts the shelf's projects the installed tools could set up
	// now, across every connected root; NeedsFileChoice is the part of it
	// whose folder holds several project files. The digest says what the last
	// scan found with the tools installed at that moment; these say what the
	// projects sheet offers today, so the map's chip and badge agree with it
	// after a second folder joins the Home or tools are installed later. Nil
	// when the installed tools could not be checked.
	ToSetUp         *int `json:"to_set_up,omitempty"`
	NeedsFileChoice *int `json:"needs_file_choice,omitempty"`
}

// lastCompleteScanAt is the finish time of the newest complete scan whose root
// is still connected. A scan of a smaller folder counts: it completed.
func lastCompleteScanAt(doc Document, state *workspace.AssistantProgramState) *time.Time {
	var newest *time.Time
	for _, scan := range doc.Scans {
		if scan.Status != "complete" || scan.FinishedAt == nil || !digestRootActive(doc, state, scan.RootID) {
			continue
		}
		if newest == nil || scan.FinishedAt.After(*newest) {
			finished := *scan.FinishedAt
			newest = &finished
		}
	}
	return newest
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
	summary := LibrarySummary{Initialized: true, Revision: doc.Revision,
		LastCompleteScanAt: lastCompleteScanAt(doc, state)}
	if run, ok := latestFactsRead(doc); ok {
		presented := presentFactsRead(run, currentFactsReadBoot)
		summary.FactsRead = &presented
	}
	if doc.Digest != nil && digestRootActive(doc, state, doc.Digest.RootID) {
		current := digestWithLinks(doc, *doc.Digest)
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
	if s.installed != nil {
		installed, listErr := s.installed.List()
		if evidence := s.setupEvidence(scope, state, installed, listErr); evidence.note != setupNoteUnchecked {
			ready, choice := shelfToSetUp(doc, state, evidence)
			summary.ToSetUp, summary.NeedsFileChoice = &ready, &choice
		}
	}
	return summary, nil
}

// shelfToSetUp counts, from the stored entries alone, the projects without a
// link whose observation on a connected root is available (or ambiguous, a
// file choice) in a format the evidence's tools attach. It reads no folder;
// the sheet's per-project review repeats every check before anything is made.
func shelfToSetUp(doc Document, state *workspace.AssistantProgramState, evidence setupEvidence) (ready, choice int) {
	if evidence.note != "" {
		return 0, 0
	}
	for _, entry := range doc.Entries {
		if entry.Link != nil {
			continue
		}
		for _, obs := range entry.Observations {
			if !digestRootActive(doc, state, obs.RootID) ||
				(obs.Availability != "available" && obs.Availability != "ambiguous") || !evidence.supports(obs.Alternates) {
				continue
			}
			ready++
			if obs.Availability == "ambiguous" {
				choice++
			}
			break
		}
	}
	return ready, choice
}

// digestWithLinks presents a stored digest against the Home's current links. A
// project set up since the scan is connected now, not activatable, so the map
// badge and the dashboard digest say what is left to do rather than what the
// scan saw (map-library-setup FR-18). The stored digest is never changed; a
// digest with no new links comes back as it is.
func digestWithLinks(doc Document, digest LibraryDigest) LibraryDigest {
	connected := 0
	for _, entry := range doc.Entries {
		if entry.Link == nil {
			continue
		}
		current := observationFor(entry, digest.RootID)
		if current == nil || current.ScanID != digest.ScanID {
			continue
		}
		connected++
	}
	delta := connected - digest.Connected
	if delta <= 0 {
		return digest
	}
	digest.Connected = connected
	digest.Activatable = max(0, digest.Activatable-delta)
	digest.NeedsFileChoice = min(digest.NeedsFileChoice, digest.Activatable)
	return digest
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
