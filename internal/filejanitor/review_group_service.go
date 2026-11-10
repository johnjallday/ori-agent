package filejanitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ReadReviewGroups uses saved Ori metadata only. In particular it must not call
// Status, which probes readiness and the managed filesystem. A known empty
// saved review is distinct from unavailable access or invalid saved provenance.
func (s *Service) ReadReviewGroups(ctx context.Context, owner, workspaceID, batchID string) (ReviewGroups, error) {
	if ctx.Err() != nil || s == nil || s.store == nil {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	install, applies, err := s.reviewInstall(owner, workspaceID)
	if err != nil || !applies {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	root, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	batchID = strings.TrimSpace(batchID)
	var batch JanitorBatch
	var candidates []JanitorCandidate
	if batchID == "latest" {
		// Index once: finding a retained pending batch must not repeatedly build
		// a map of every retained candidate for every resolved batch.
		pending := make(map[string]bool, len(state.Batches))
		for _, candidate := range state.Candidates {
			if candidate.Actionable() || candidate.State == CandidateApplying {
				pending[candidate.BatchID] = true
			}
		}
		for i := len(state.Batches) - 1; i >= 0; i-- {
			if pending[state.Batches[i].ID] {
				batch = state.Batches[i]
				break
			}
		}
	} else {
		var found bool
		batch, found = state.Batch(batchID)
		if !found {
			return ReviewGroups{}, ErrBatchNotFound
		}
	}
	out := ReviewGroups{Groups: []ReviewGroup{}, RuleVersion: ReviewGroupRuleVersion}
	if batch.ID != "" {
		candidates = state.CandidatesFor(batch.ID)
		if !reviewBatchScope(batch, candidates, workspaceID, root.Generation, install) {
			return ReviewGroups{}, ErrReviewUnavailable
		}
		out, err = GroupReview(batch, candidates, reviewScopeDigest(root, install))
		if err != nil {
			return ReviewGroups{}, err
		}
	}
	// Owner/install/grant/root changes during projection must not lend the old
	// batch's metadata to a replacement scope. This recheck still does no IO
	// against the managed folder, and never persists reconciliation on a GET.
	current, currentApplies, err := s.reviewInstall(owner, workspaceID)
	if err != nil || !currentApplies || !sameReviewInstall(install, current) || ctx.Err() != nil {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	currentRoot, err := s.StoredManagedRoot(owner, workspaceID)
	if err != nil || currentRoot != root {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	return out, nil
}

// reviewScopeDigest binds server-only identities without exposing paths, grant
// IDs, or the owner's identity in the response. ManagedRoot's JSON tags are all
// private, so explicitly encode its values rather than hashing an empty object.
func reviewScopeDigest(root ManagedRoot, install workspace.InstalledCapability) string {
	data, err := json.Marshal([]any{
		root.WorkspaceID, root.Owner, root.Generation, root.DirectoryReferenceID,
		root.Path, root.BindingID, install.ID, install.Version, install.InstalledAt.UTC(),
	})
	if err != nil {
		return "" // GroupReview rejects empty scope; never hash invalid identity as a common scope
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
