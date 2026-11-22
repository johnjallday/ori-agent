package filejanitor

import (
	"context"
	"path/filepath"
)

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
