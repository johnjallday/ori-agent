package filejanitor

// validateExecutionScope is a per-item admission fence, not a revalidation of
// the original whole selection: preceding items legitimately changed lifecycle
// state after token consumption. The immutable approval scope must still match.
// It never scans or classifies, and must precede destination preparation as well
// as the journal and the final handoff to the existing execution mechanism.
func (s *Service) validateExecutionScope(settings JanitorSettings, root string, approval ApprovalRecord) error {
	fresh, err := s.store.LoadSettings(settings.WorkspaceID)
	if err != nil {
		return err
	}
	if !fresh.IsSetUp() || fresh.RootID != settings.RootID || fresh.RootPath != settings.RootPath || fresh.DirectoryReferenceID != settings.DirectoryReferenceID {
		return ErrReviewUnavailable
	}
	if approval.ReviewScope != "" {
		install, applies, err := s.reviewInstall(approval.UserID, settings.WorkspaceID)
		if err != nil || !applies {
			return ErrReviewUnavailable
		}
		current, err := s.StoredManagedRoot(approval.UserID, settings.WorkspaceID)
		if err != nil || reviewScopeDigest(current, install) != approval.ReviewScope {
			return ErrReviewUnavailable
		}
	}
	resolved, err := s.scannerFor().ResolveRoot(fresh)
	if err != nil || resolved != root {
		return ErrReviewUnavailable
	}
	return nil
}

func validExecutionCandidate(candidate JanitorCandidate, item PlanItem, approval ApprovalRecord, applying bool) bool {
	expectedState := proposalPending(candidate)
	if approval.ReviewRevision == "" {
		expectedState = candidate.Actionable()
	} // preserve fresh legacy per-file retry previews
	if applying {
		expectedState = candidate.State == CandidateApplying
	}
	if !expectedState || candidate.WorkspaceID != approval.WorkspaceID || candidate.BatchID != approval.BatchID || candidate.Fingerprint.Key() != item.FingerprintKey {
		return false
	}
	if approval.ReviewRevision != "" && item.Operation == OperationMove {
		category, err := LookupCategory(string(candidate.EffectiveCategory()))
		if err != nil || category.ID != item.Category || (candidate.NeedsReview && (candidate.DecisionCategory == "" || candidate.DecisionGroupID != "")) {
			return false
		}
	}
	return true
}

func (s *Service) appendApprovedAction(settings JanitorSettings, root string, approval ApprovalRecord, item PlanItem, action FileAction) error {
	_, err := s.store.UpdateScanState(settings.WorkspaceID, func(state *ScanState) error {
		if err := s.validateExecutionScope(settings, root, approval); err != nil {
			return err
		}
		candidate, found := state.Candidate(item.CandidateID)
		if !found || state.IsSkipped(candidate.Fingerprint) || !validExecutionCandidate(candidate, item, approval, false) {
			return ErrReviewConflict
		}
		state.Actions = append(state.Actions, action)
		for i := range state.Candidates {
			if state.Candidates[i].ID == item.CandidateID {
				state.Candidates[i].State = CandidateApplying
				resummarizeBatch(state, candidate.BatchID)
				break
			}
		}
		return nil
	})
	return err
}

// executionHandoff rechecks after the durable journal. Passing this check admits
// this one handoff; an already-started external operation cannot be retroactively
// undone by a later revoke. Every subsequent item has a new admission check.
func (s *Service) executionHandoff(settings JanitorSettings, root string, approval ApprovalRecord, item PlanItem) error {
	if err := s.validateExecutionScope(settings, root, approval); err != nil {
		return err
	}
	state, err := s.store.LoadScanState(settings.WorkspaceID)
	if err != nil {
		return err
	}
	candidate, found := state.Candidate(item.CandidateID)
	if !found || state.IsSkipped(candidate.Fingerprint) || !validExecutionCandidate(candidate, item, approval, true) {
		return ErrReviewConflict
	}
	current, err := currentFingerprint(root, candidate.Name)
	if err != nil || !candidate.Fingerprint.Matches(current) {
		return ErrReviewConflict
	}
	return nil
}
