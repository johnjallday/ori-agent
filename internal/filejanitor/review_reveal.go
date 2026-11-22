package filejanitor

import (
	"context"
	"path/filepath"

	"github.com/johnjallday/ori-agent/internal/platform"
)

// ReviewInspectionViewer describes the native choices for a saved candidate.
// Eligibility here is about saved state only. Dispatch still revalidates the
// scope and physical file; an empty viewer is Finder-only, not ineligible.
func ReviewInspectionViewer(candidate JanitorCandidate) (viewer string, eligible bool) {
	if !proposalPending(candidate) {
		return "", false
	}
	return platform.ReviewDocumentViewer(candidate.Name), true
}

// ReadBatchReview reads the candidate list and, when desktop inspection is
// available, its optional inspection revision from the SAME scan snapshot.
// An unavailable inspection binding does not hide the saved list.
func (s *Service) ReadBatchReview(ctx context.Context, owner, workspaceID, batchID string) (JanitorBatch, []JanitorCandidate, string, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return JanitorBatch{}, nil, "", err
	}
	var batch JanitorBatch
	if batchID == "latest" {
		batch, _ = latestCandidateBatch(state)
		if batch.ID == "" {
			return batch, nil, "", nil
		}
	} else {
		var found bool
		batch, found = state.Batch(batchID)
		if !found {
			return JanitorBatch{}, nil, "", ErrBatchNotFound
		}
	}
	candidates := state.CandidatesFor(batch.ID)
	if owner != "" && batch.RootID != "" && ctx.Err() == nil {
		groups, _, root, err := s.reviewDraftSnapshot(owner, workspaceID, batch.ID, state)
		if err == nil && batch.RootID == root.Generation {
			return batch, candidates, groups.Revision, nil
		}
	}
	return batch, candidates, "", nil
}

// ReviewInspectionPath resolves one file from the displayed review for native
// inspection. Never accept a client path, read contents or change filing intent.
func (s *Service) ReviewInspectionPath(ctx context.Context, owner, workspaceID, batchID, revision, id string) (string, error) {
	if s == nil || s.store == nil || ctx.Err() != nil || revision == "" {
		return "", ErrReviewUnavailable
	}
	state, err := s.store.loadScanState(workspaceID, true)
	if err != nil {
		return "", ErrReviewUnavailable
	}
	groups, _, root, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil {
		return "", err
	}
	batch, _ := state.Batch(batchID)
	candidate, found := state.Candidate(id)
	if groups.Revision != revision || batch.RootID != root.Generation || !found || candidate.BatchID != batchID || !proposalPending(candidate) {
		return "", ErrReviewConflict
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil || settings.RootID != root.Generation {
		return "", ErrReviewUnavailable
	}
	resolved, err := s.scannerFor().ResolveRoot(settings)
	if err != nil || resolved != root.Path {
		return "", ErrReviewUnavailable
	}
	current, err := currentFingerprint(resolved, candidate.Name)
	if err != nil || !candidate.Fingerprint.Matches(current) || ctx.Err() != nil {
		return "", ErrReviewConflict
	}
	return filepath.Join(resolved, candidate.Name), nil
}
