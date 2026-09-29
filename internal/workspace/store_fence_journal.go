package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// WorkspaceFenceJournalFile records one in-flight protected write beside
// workspace.json: the exact folder bytes before, the exact bytes about to be
// written, and the primary version the write is conditional on. It is written
// and synced before the folder write and removed after the operation reaches a
// terminal state, so a restart can classify what an interrupted write left
// behind instead of guessing from one mirror.
const WorkspaceFenceJournalFile = ".workspace.journal.json"

// fenceJournal is the durable before/after record of one fenced write.
type fenceJournal struct {
	OperationID   string    `json:"operation_id"`
	WorkspaceID   string    `json:"workspace_id"`
	BaseVersion   int64     `json:"base_version"`
	TargetVersion int64     `json:"target_version"`
	FolderBefore  string    `json:"folder_before_digest"`
	FolderAfter   string    `json:"folder_after_digest"`
	StartedAt     time.Time `json:"started_at"`
}

// FenceRecoveryOutcome classifies an interrupted protected write from its
// journal and both independently read mirrors.
type FenceRecoveryOutcome string

const (
	// FenceApplied: both mirrors hold the journaled after image; the write is
	// complete and the journal is only a leftover.
	FenceApplied FenceRecoveryOutcome = "applied"
	// FenceNotApplied: both mirrors hold the journaled before image; nothing
	// of the write survived and the consent it consumed is spent.
	FenceNotApplied FenceRecoveryOutcome = "not_applied"
	// FenceReconcileRequired: one mirror moved and the other did not. New
	// writes stay refused until an explicit reconciliation restores agreement.
	FenceReconcileRequired FenceRecoveryOutcome = "reconcile_required"
	// FenceUnknown: a mirror matches neither image (a third writer, a missing
	// record). Unavailable for manual investigation; never inferred.
	FenceUnknown FenceRecoveryOutcome = "unknown"
)

func fenceDigest(data []byte) string {
	if data == nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (f *folderFence) journalPath() string {
	if f == nil || f.folder == "" {
		return ""
	}
	return filepath.Join(f.folder, WorkspaceFenceJournalFile)
}

// pendingJournal returns the journal of an interrupted earlier write, or nil.
func (f *folderFence) pendingJournal() (*fenceJournal, error) {
	path := f.journalPath()
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- a fixed journal filename inside the store-resolved workspace folder.
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace fence journal: %w", err)
	}
	var journal fenceJournal
	if err := json.Unmarshal(raw, &journal); err != nil || journal.WorkspaceID != f.id {
		return nil, fmt.Errorf("workspace %s: unreadable fence journal; investigate before writing", f.id)
	}
	return &journal, nil
}

// beginJournal durably records the intent of this write before any mirror
// changes. The after digest is filled in by the folder write itself.
func (f *folderFence) beginJournal(base int64) *fenceJournal {
	return &fenceJournal{
		OperationID: uuid.NewString(), WorkspaceID: f.id,
		BaseVersion: base, TargetVersion: base + 1,
		FolderBefore: fenceDigest(f.beforeRaw), StartedAt: time.Now().UTC(),
	}
}

func (f *folderFence) writeJournal(journal *fenceJournal) error {
	path := f.journalPath()
	if path == "" || journal == nil {
		return nil
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(path, data); err != nil {
		return fmt.Errorf("write workspace fence journal: %w", err)
	}
	return nil
}

// clearJournal marks the operation terminal. The directory is synced so the
// removal is as durable as the writes it concludes.
func (f *folderFence) clearJournal() error {
	path := f.journalPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear workspace fence journal: %w", err)
	}
	dir, err := os.Open(f.folder) // #nosec G304 -- the store-resolved workspace folder, not a request path.
	if err != nil {
		return nil
	}
	_ = dir.Sync()
	return dir.Close()
}

// classifyFenceRecovery compares both mirrors with the journal. The folder is
// identified by its exact bytes, the primary by its version; a different
// representation of "the same" record is deliberately not treated as equal.
func classifyFenceRecovery(journal *fenceJournal, folderRaw []byte, primary *Workspace) FenceRecoveryOutcome {
	if journal == nil {
		return FenceUnknown
	}
	folder := FenceUnknown
	switch digest := fenceDigest(folderRaw); {
	case journal.FolderAfter != "" && digest == journal.FolderAfter:
		folder = FenceApplied
	case digest == journal.FolderBefore:
		folder = FenceNotApplied
	}
	primaryState := FenceUnknown
	if primary != nil {
		switch primary.Version {
		case journal.TargetVersion:
			primaryState = FenceApplied
		case journal.BaseVersion:
			primaryState = FenceNotApplied
		}
	}
	switch {
	case folder == FenceApplied && primaryState == FenceApplied:
		return FenceApplied
	case folder == FenceNotApplied && primaryState == FenceNotApplied:
		return FenceNotApplied
	case folder == FenceUnknown || primaryState == FenceUnknown:
		return FenceUnknown
	default:
		return FenceReconcileRequired
	}
}

// ErrWorkspaceFenceUnknown reports a journaled write whose mirrors match
// neither its before nor its after image.
var ErrWorkspaceFenceUnknown = errors.New("workspace fence journal does not match either mirror; investigate before writing")
