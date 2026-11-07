package filejanitor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var ErrReviewUnavailable = errors.New("saved filing review is unavailable")

// FilingReview contains navigation metadata only. Counts come from the same
// saved batch and domain summarizer as Review. No content, paths, grants,
// approval tokens or readiness probes belong in this projection.
type FilingReview struct {
	State             string     `json:"state"`
	CapabilityID      string     `json:"capability_id,omitempty"`
	CapabilityVersion int        `json:"capability_version,omitempty"`
	InstalledAt       *time.Time `json:"installed_at,omitempty"`
	RootID            string     `json:"root_id,omitempty"`
	BatchID           string     `json:"batch_id,omitempty"`
	BatchCompletedAt  *time.Time `json:"batch_completed_at,omitempty"`
	ObservedAt        time.Time  `json:"observed_at"`
	*ReviewCounts
}

type ReviewCounts struct {
	Pending     int `json:"pending_count"`
	NeedsReview int `json:"needs_review_count"`
}

// ReadFilingReview is independent of FileCurator/current conversation identity.
// A valid saved review must not disappear when its optional conversational
// companion is missing. Source state is never inferred from welcome prose.
func (s *Service) ReadFilingReview(ctx context.Context, owner, id string) (FilingReview, error) {
	unavailable := func() (FilingReview, error) { return FilingReview{}, ErrReviewUnavailable }
	if ctx.Err() != nil || s == nil || s.store == nil {
		return unavailable()
	}
	install, applies, err := s.reviewInstall(owner, id)
	if err != nil {
		return unavailable()
	}
	out := FilingReview{State: "not_applicable", ObservedAt: s.clock().UTC()}
	var root ManagedRoot
	if applies {
		out.CapabilityID, out.CapabilityVersion = install.ID, install.Version
		at := install.InstalledAt
		out.InstalledAt = &at
		settings, err := s.store.LoadSettings(id)
		if err != nil || settings.WorkspaceID != id {
			return unavailable()
		}
		out.State = "unconfigured"
		if settings.IsSetUp() {
			root, err = s.StoredManagedRoot(owner, id)
			if err != nil {
				return unavailable()
			}
			out.State, out.RootID = "ready", root.Generation
			out.ReviewCounts = &ReviewCounts{}
			batch, candidates, found, err := s.LatestPendingBatch(id)
			if err != nil {
				return unavailable()
			}
			if found {
				if !reviewBatchScope(batch, candidates, id, root.Generation, install) {
					return unavailable()
				}
				batch = SummarizeBatch(batch, candidates)
				out.Pending, out.NeedsReview = batch.Summary.Proposed, batch.Summary.NeedsReview
				out.BatchID = batch.ID
				if !batch.CompletedAt.IsZero() {
					at := batch.CompletedAt
					out.BatchCompletedAt = &at
				}
			}
		}
	}
	// Neither an install/removal nor an owner/root change during the read may
	// lend the old batch to the replacement scope. Comparing a root includes
	// the saved grant/reference, not merely its display name or opaque ID.
	latest, stillApplies, err := s.reviewInstall(owner, id)
	if ctx.Err() != nil || err != nil || stillApplies != applies || !sameReviewInstall(install, latest) {
		return unavailable()
	}
	if root.Generation != "" {
		current, err := s.StoredManagedRoot(owner, id)
		if err != nil || current != root {
			return unavailable()
		}
	} else if applies {
		settings, err := s.store.LoadSettings(id)
		if err != nil || settings.IsSetUp() {
			return unavailable()
		}
	}
	return out, nil
}

func reviewWorkspaceOwner(ws *workspace.Workspace, owner, id string) bool {
	if ws == nil || ws.ID != id || owner == "" || ws.Status == workspace.StatusTrashed || ws.Status == workspace.StatusMissing {
		return false
	}
	actual := strings.TrimSpace(ws.OwnerUserID)
	if actual == "" {
		actual = userprofile.LocalUserID
	}
	return actual == owner
}

func sameReviewInstall(a, b workspace.InstalledCapability) bool {
	return a.ID == b.ID && a.Version == b.Version && a.InstalledAt.Equal(b.InstalledAt)
}

func (s *Service) reviewInstall(owner, id string) (workspace.InstalledCapability, bool, error) {
	bad := func() (workspace.InstalledCapability, bool, error) {
		return workspace.InstalledCapability{}, false, ErrReviewUnavailable
	}
	if s.workspaces == nil {
		return bad()
	}
	primary, err := s.workspaces.Get(id)
	if err != nil || !reviewWorkspaceOwner(primary, owner, id) {
		return bad()
	}
	install, applies := primary.GetInstalledCapability(workspace.CapabilityFileJanitor)
	if folders, ok := s.workspaces.(FolderWorkspaceSource); ok {
		// Never use the operational fallback when canonical grant metadata is
		// missing. SQLite and folder versions can advance independently.
		canonical, err := folders.GetFolderWorkspace(id)
		if err != nil || !reviewWorkspaceOwner(canonical, owner, id) {
			return bad()
		}
		current, found := canonical.GetInstalledCapability(workspace.CapabilityFileJanitor)
		if found != applies || !sameReviewInstall(install, current) {
			return bad()
		}
	}
	if applies && install.InstalledAt.IsZero() {
		return bad()
	}
	return install, applies, nil
}

func reviewBatchScope(batch JanitorBatch, candidates []JanitorCandidate, id, root string, install workspace.InstalledCapability) bool {
	if batch.WorkspaceID != id || !managedRootGeneration.MatchString(batch.ID) || len(candidates) != len(batch.CandidateIDs) {
		return false
	}
	if batch.RootID != "" && batch.RootID != root {
		return false
	}
	if batch.CapabilityInstalledAt != nil {
		if !batch.CapabilityInstalledAt.Equal(install.InstalledAt) {
			return false
		}
	} else if install.Source != workspace.InstallSourceLegacyMigration && batch.StartedAt.Before(install.InstalledAt) {
		// Old batches remain readable, but a subsequent explicit reinstall
		// cannot adopt them. The additive legacy migration is not a reinstall.
		return false
	}
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate.WorkspaceID != id || candidate.BatchID != batch.ID || candidate.ID == "" || seen[candidate.ID] {
			return false
		}
		seen[candidate.ID] = true
	}
	for _, id := range batch.CandidateIDs {
		if !seen[id] {
			return false
		}
		delete(seen, id)
	}
	return len(seen) == 0
}
