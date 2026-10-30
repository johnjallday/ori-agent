package filejanitor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CuratorWelcome is a counts-only projection of File Janitor's own saved state.
// It carries no filenames, paths, content, approval tokens or raw failures.
// No goal assessment is required to discuss a filing batch.
type CuratorWelcome struct {
	Configured       bool
	Pending          int
	NeedsReview      int
	BatchCompletedAt time.Time
	ReadAt           time.Time
}

var errCuratorWelcomeUnavailable = errors.New("file janitor's saved review state is unavailable")

// ReadCuratorWelcome reads only Ori's stored settings/batches and workspace
// metadata. Unlike Status, it does not probe the managed folder's readiness;
// unlike ScanNow, it cannot produce a batch or inspect files. Reads are scoped
// to the owner and current capability-associated lead, not an agent's name.
func (s *Service) ReadCuratorWelcome(ctx context.Context, userID, workspaceID, instanceID string) (CuratorWelcome, error) {
	if err := ctx.Err(); err != nil {
		return CuratorWelcome{}, err
	}
	ws, err := s.readWorkspace(workspaceID)
	if err != nil || !curatorWelcomeScope(ws, userID, workspaceID, instanceID) || s.store == nil {
		return CuratorWelcome{}, errCuratorWelcomeUnavailable
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return CuratorWelcome{}, errCuratorWelcomeUnavailable
	}
	out := CuratorWelcome{ReadAt: s.clock().UTC()}
	if !settings.IsSetUp() {
		return out, nil
	}
	root, err := s.StoredManagedRoot(userID, workspaceID)
	if err != nil {
		return CuratorWelcome{}, errCuratorWelcomeUnavailable
	}
	out.Configured = true
	batch, candidates, found, err := s.LatestPendingBatch(workspaceID)
	if err != nil {
		return CuratorWelcome{}, errCuratorWelcomeUnavailable
	}
	if found {
		if batch.WorkspaceID != workspaceID || len(candidates) != len(batch.CandidateIDs) {
			return CuratorWelcome{}, errCuratorWelcomeUnavailable
		}
		seen := make(map[string]bool)
		for _, candidate := range candidates {
			if candidate.WorkspaceID != workspaceID || candidate.BatchID != batch.ID || candidate.ID == "" || seen[candidate.ID] {
				return CuratorWelcome{}, errCuratorWelcomeUnavailable
			}
			seen[candidate.ID] = true
			if candidate.State != CandidatePending && candidate.State != CandidateApproved {
				continue
			}
			out.Pending++
			if candidate.NeedsReview {
				out.NeedsReview++
			}
		}
		out.BatchCompletedAt = batch.CompletedAt
	}
	// Revocation, relinking, removal and changed associations cannot lend the
	// old batch to a newly resolved root or Curator while this read is in flight.
	latest, workspaceErr := s.readWorkspace(workspaceID)
	latestRoot, rootErr := s.StoredManagedRoot(userID, workspaceID)
	if ctx.Err() != nil || workspaceErr != nil || rootErr != nil || root != latestRoot ||
		!curatorWelcomeScope(latest, userID, workspaceID, instanceID) {
		return CuratorWelcome{}, errCuratorWelcomeUnavailable
	}
	return out, nil
}

func curatorWelcomeScope(ws *workspace.Workspace, owner, workspaceID, instanceID string) bool {
	if ws == nil || ws.ID != workspaceID || owner == "" || instanceID == "" || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return false
	}
	actualOwner := strings.TrimSpace(ws.OwnerUserID)
	if actualOwner == "" {
		actualOwner = userprofile.LocalUserID
	}
	curator, ok := ws.FileCurator()
	lead, unique := ws.FindUniqueAgentInstance(ws.EntryAgentName(), "")
	return actualOwner == owner && ok && unique && curator.ID == instanceID && lead.ID == instanceID
}
