package learningmissions

import "context"

func validSelection(structure StructureID, ref string) bool {
	if ref != "" && !validID(ref) {
		return false
	}
	if structure != "" {
		if _, err := Lookup(structure, DefinitionVersion); err != nil {
			return false
		}
	}
	return true
}

func (s *Service) selectCandidate(ctx context.Context, owner Owner, workspaceID string, structure StructureID, ref string) (rankedCandidate, error) {
	if s.source == nil || !validSelection(structure, ref) {
		return rankedCandidate{}, ErrUnavailable
	}
	var candidates []EligibleCandidate
	if reader, ok := s.source.(CandidateSource); ok {
		var err error
		candidates, err = reader.Candidates(ctx, owner.ID, workspaceID)
		if err != nil || len(candidates) > MaxCandidates {
			return rankedCandidate{}, ErrUnavailable
		}
	} else {
		candidate, err := s.source.Candidate(ctx, owner.ID, workspaceID)
		if err != nil {
			return rankedCandidate{}, ErrUnavailable
		}
		candidates = []EligibleCandidate{{Candidate: candidate, Structure: DelegateInspect}}
	}
	selected := make([]EligibleCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.WorkspaceID == workspaceID && (ref == "" || ref == candidate.Ref) {
			selected = append(selected, candidate)
		}
	}
	policy, err := s.recommendationContext(ctx, generationOwner(owner, workspaceID), selected, structure)
	if err != nil {
		return rankedCandidate{}, err
	}
	if policy.Active {
		return rankedCandidate{}, ErrConflict
	}
	ranked := rankCandidates(selected, policy)
	if len(ranked) == 0 {
		return rankedCandidate{}, ErrUnavailable
	}
	return ranked[0], nil
}

func filterContext(snapshot ContextSnapshot, structure StructureID, ref string) (ContextSnapshot, error) {
	if !validSelection(structure, ref) {
		return ContextSnapshot{}, ErrInvalid
	}
	snapshot.StructureFilter, snapshot.CandidateFilter = structure, ref
	selected := make([]DesignCandidate, 0, len(snapshot.Data.Candidates))
	for _, candidate := range snapshot.Data.Candidates {
		if (structure == "" || candidate.Structure == structure) && (ref == "" || candidate.Ref == ref) {
			selected = append(selected, candidate)
		}
	}
	snapshot.Data.Candidates = selected
	if len(selected) == 0 {
		return snapshot, ErrUnavailable
	}
	return snapshot, nil
}

func genericCopy(structure StructureID) (goal, scope, why string) {
	switch structure {
	case ImproveResult:
		return "Improve a reviewed draft", "Apply a correction to the exact selected original result. Review and start a separate Ticket before any model call.", "A local result-specific decision identifies an available original; the comparison will be your report, not a model grade."
	case RepeatSuccess:
		return "Repeat a useful draft process", "Supply materially different text and explain what changed. Review and start a new Ticket, then decide whether its result is useful.", "You previously chose to use a result from this bounded supplied-text process."
	default:
		return "Delegate a small draft and inspect the result", "Use only text you explicitly supply in this workspace. Nothing is sent or run until you review and start a Ticket.", "You chose this workspace to practice reviewing useful work."
	}
}
