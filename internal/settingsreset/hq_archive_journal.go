package settingsreset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

type archiveStage string

const (
	archivePending  archiveStage = "pending"
	archiveIntent   archiveStage = "move_intent"
	archiveVerified archiveStage = "verified"
)

// archiveJournal exists only for an expressly confirmed archive. The first
// digest seals the reviewed authority and the second seals the post-drain
// evidence. Neither digest replaces ownership checks against disk and SQLite.
type archiveJournal struct {
	Consent   string       `json:"consent"`
	Remainder string       `json:"retained_remainder,omitempty"`
	Baseline  string       `json:"post_drain_baseline,omitempty"`
	Stage     archiveStage `json:"stage"`
}

func validArchiveDigest(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil
}

func archiveHash(value any) string {
	data, _ := json.Marshal(value) // Fixed JSON-safe evidence fields.
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func archiveConsent(j *journal) string {
	return archiveHash(struct {
		Installation, PreviewID, OperationID, ConfirmedScope, RequestID string
		Selected                                                        []CategoryID
		Target                                                          *hqArchiveEvidence
	}{j.Plan.Installation, j.Plan.Preview.ID, j.Operation.ID, j.Plan.Preview.ScopeDigest, j.RequestID,
		j.Plan.Preview.Selected, j.Plan.Evidence.HQArchive})
}

func archiveBaseline(j *journal) string {
	return archiveHash(struct {
		Consent, Remainder string
		TargetPaths        []resolvedTarget
		Protected          []protectedDigest
	}{j.Archive.Consent, j.Archive.Remainder, j.Plan.Targets, j.Plan.Evidence.ProtectedDigests})
}

func publicArchiveResult(j *journal, outcome Outcome) *HQArchiveResult {
	message := "On relaunch Ori will move the reviewed folder only if it can still prove the exact target."
	switch outcome {
	case OutcomeCompleted:
		message = "The Personal HQ folder was archived intact. To restore an existing portable checkpoint, review this folder with Import Folder. Ori does not import it or resume routines automatically."
	case OutcomeFailed:
		message = "Ori could not verify this archive. Keep both locations intact. Follow the recovery guidance before relaunching; do not submit another reset."
	case OutcomeUnknown:
		message = "Ori has not verified the archive outcome. Keep both locations and the reset receipt for recovery."
	}
	return &HQArchiveResult{Outcome: outcome, Destination: j.Plan.Evidence.HQArchive.Destination, Message: message}
}

func validateArchiveJournal(j *journal) error {
	e, view, archive, result := j.Plan.Evidence.HQArchive, j.Plan.Preview.HQArchive, j.Archive, j.Operation.HQArchive
	if e == nil && view == nil && archive == nil && result == nil {
		return nil // Legacy or no opt-in: no authority to move anything.
	}
	if e == nil || view == nil || archive == nil || result == nil || !view.Requested ||
		!archiveSelection(j.Plan.Preview.Selected) || !j.Plan.Evidence.WorkspaceRootConfirmed ||
		view.Source != e.Source || view.Destination != e.Destination || view.Notice == "" || len(view.Notice) > 1024 || view.UnavailableReason != "" ||
		!workspacecontinuity.ValidID(e.WorkspaceID) || !workspace.IsCanonicalWorkspaceSlug(e.Slug) || workspace.IsReservedTopLevelSlug(e.Slug) ||
		!slices.Contains(j.Plan.Evidence.ProtectedPaths, e.Root) || e.Source != filepath.Join(e.Root, e.Slug) ||
		e.Destination != hqArchiveDestination(j.Plan.Installation, j.Operation.ID, e.Slug) ||
		len(e.Source) > 4096 || len(e.Destination) > 4096 || containsPath(e.Root, e.Destination) || containsPath(e.Source, j.Plan.Installation) ||
		!e.SourceID.valid() || !e.RootID.valid() || !e.ParentID.valid() || e.SourceID.Device != e.RootID.Device || e.RootID.Device != e.ParentID.Device ||
		!validArchiveDigest(e.Contents) || archive.Consent != archiveConsent(j) {
		return ErrJournalInvalid
	}
	if archive.Remainder != "" || archive.Baseline != "" {
		if !validArchiveDigest(archive.Remainder) || archive.Baseline != archiveBaseline(j) || j.Operation.State == StatePreparing {
			return ErrJournalInvalid
		}
	} else if archive.Stage != archivePending || j.Operation.State != StatePreparing && j.Operation.State != StateBlocked {
		return ErrJournalInvalid
	}
	switch archive.Stage {
	case archivePending:
		if j.Operation.State != StateAwaitingRestart && j.Operation.State != StatePreparing && j.Operation.State != StateBlocked && j.Operation.State != StateApplying ||
			!allResultsPending(j.Operation.Results) {
			return ErrJournalInvalid
		}
	case archiveIntent:
		if (j.Operation.State != StateApplying && j.Operation.State != StateBlocked) || !allResultsPending(j.Operation.Results) {
			return ErrJournalInvalid
		}
	case archiveVerified:
		if j.Operation.State == StateAwaitingRestart || j.Operation.State == StatePreparing || result.Outcome != OutcomeCompleted {
			return ErrJournalInvalid
		}
	default:
		return ErrJournalInvalid
	}
	if archive.Stage != archiveVerified {
		want := OutcomePending
		if j.Operation.State == StateBlocked {
			want = OutcomeFailed
		}
		if result.Outcome != want {
			return ErrJournalInvalid
		}
	}
	if result.Destination != e.Destination || result.Message == "" || len(result.Message) > 1024 {
		return ErrJournalInvalid
	}
	return nil
}
