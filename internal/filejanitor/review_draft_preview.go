package filejanitor

// reviewApprovalBinding bridges exact saved intent to the existing approval
// store. It must run inside the same scan-state transaction as token issuance
// or consumption. A batch with a draft cannot fall back to the legacy unbound
// path merely because an old tab omits the expected revision.
func (s *Service) reviewApprovalBinding(state ScanState, workspaceID, owner, batchID, expected string, plan []PlanItem) (string, string, error) {
	hasDraft := false
	for _, draft := range state.ReviewDrafts {
		if draft.BatchID == batchID {
			hasDraft = true
			break
		}
	}
	if !hasDraft && expected == "" {
		return "", "", nil
	}
	groups, binding, root, err := s.reviewDraftSnapshot(owner, workspaceID, batchID, state)
	if err != nil {
		return "", "", err
	}
	view := s.revalidatedReviewDraft(state, batchID, binding, groups, root)
	if view.State == "invalid" || view.State == "stale" {
		return "", "", ErrReviewDraftInvalid
	}
	if expected == "" || view.Revision != expected || len(view.Choices) != len(plan) {
		return "", "", ErrReviewConflict
	}
	byID := make(map[string]ReviewChoiceView, len(view.Choices))
	for _, choice := range view.Choices {
		byID[choice.CandidateID] = choice
	}
	seen := make(map[string]bool, len(plan))
	for _, item := range plan {
		choice, found := byID[item.CandidateID]
		if !found || seen[item.CandidateID] || choice.Operation != item.Operation || choice.Category != item.Category {
			return "", "", ErrReviewConflict
		}
		seen[item.CandidateID] = true
	}
	return view.Revision, binding.Digest, nil
}
