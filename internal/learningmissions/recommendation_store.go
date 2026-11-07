package learningmissions

import (
	"context"
	"database/sql"
	"slices"
)

// recommendationContext reads only local receipt metadata, never result bytes,
// notes, chat or workspace content. Missing receipts mean unknown coverage.
func (s *Service) recommendationContext(ctx context.Context, owner Owner, candidates []EligibleCandidate, requested StructureID) (policyContext, error) {
	if s == nil || s.store == nil || s.store.db == nil {
		return policyContext{}, ErrUnavailable
	}
	p := owner.Preferences
	out := policyContext{Mode: p.Mode, WorkspaceIDs: slices.Clone(p.WorkspaceIDs), Requested: requested, Focus: p.Focus,
		Effort: p.Effort, Now: s.now().UnixMilli(), Exclusions: slices.Clone(p.Exclusions), PracticeKnown: true, Practice: map[string]int{}, Independent: map[string]int{}}
	if !validID(owner.ID) || p.Validate() != nil || len(candidates) > MaxCandidates {
		return out, ErrUnavailable
	}
	scopes := map[string]string{}
	for _, c := range candidates {
		if !slices.Contains(p.WorkspaceIDs, c.WorkspaceID) || !validID(c.Incarnation) {
			return out, ErrUnavailable
		}
		if old, exists := scopes[c.WorkspaceID]; exists && old != c.Incarnation {
			return out, ErrUnavailable
		}
		scopes[c.WorkspaceID] = c.Incarnation
	}
	bindings, err := encode(scopes, MaxResponseBytes)
	if err != nil {
		return out, err
	}
	var occupied int
	if err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM learning_instances WHERE owner_id=? AND state IN ('active','blocked','offered')`, owner.ID).Scan(&occupied); err != nil {
		return out, ErrUnavailable
	}
	out.Active = occupied != 0
	if reader, ok := s.source.(ObservedSource); ok && !out.Active && p.Observe && p.ObservationRevision > 0 {
		works, err := s.store.pendingObservations(ctx, owner.ID)
		if err != nil {
			return out, err
		}
		for _, work := range works {
			if !work.allowed(owner) || scopes[work.WorkspaceID] != work.Incarnation {
				continue
			}
			ready, _ := reader.ObservedReady(ctx, owner.ID, work)
			out.ReviewableWork = out.ReviewableWork || ready
		}
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT json_extract(e.record,'$.competency'),r.id,
 json_extract(r.record,'$.result.studio_id'),json_extract(r.record,'$.result.incarnation'),json_extract(r.record,'$.decision'),
 json_extract(e.record,'$.guidance'),json_extract(e.record,'$.support_version')
 FROM learning_evidence e LEFT JOIN learning_reviews r ON r.owner_id=e.owner_id AND r.id=json_extract(e.record,'$.review_id')
 WHERE e.owner_id=? LIMIT ?`, owner.ID, MaxEvidence+1)
	if err != nil {
		return out, ErrUnavailable
	}
	count := 0
	for rows.Next() {
		var competency, receipt, workspaceID, incarnation, decision, guidance sql.NullString
		var support sql.NullInt64
		if err := rows.Scan(&competency, &receipt, &workspaceID, &incarnation, &decision, &guidance, &support); err != nil {
			_ = rows.Close()
			return out, ErrUnavailable
		}
		count++
		if count > MaxEvidence || !receipt.Valid || !validID(workspaceID.String) || !validID(incarnation.String) || !slices.Contains([]string{"use", "revise", "reject"}, decision.String) ||
			!slices.Contains([]string{"delegate", "evaluate", "improve", "reuse"}, competency.String) {
			out.PracticeKnown = false
			continue
		}
		if incarnation.Valid && scopes[workspaceID.String] == incarnation.String {
			out.Practice[competency.String]++
			if support.Valid && support.Int64 == GuidanceProtocolVersion && guidance.String == string(GuidanceBasic) {
				out.Independent[competency.String]++
			}
		}
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return out, ErrUnavailable
	}
	if !out.PracticeKnown {
		out.Practice, out.Independent = map[string]int{}, map[string]int{}
	}
	rows, err = s.store.db.QueryContext(ctx, `SELECT json_extract(definition,'$.structure'),json_extract(definition,'$.version') FROM learning_instances
 WHERE owner_id=? AND EXISTS (SELECT 1 FROM json_each(?) WHERE key=json_extract(definition,'$.studio_id')
 AND value=json_extract(definition,'$.incarnation')) ORDER BY created_at_ms DESC,id LIMIT ?`, owner.ID, bindings, MaxPolicyRecent)
	if err != nil {
		return out, ErrUnavailable
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var structure StructureID
		var version sql.NullInt64
		if err := rows.Scan(&structure, &version); err != nil {
			return out, ErrUnavailable
		}
		if _, err := Lookup(structure, DefinitionVersion); version.Valid && version.Int64 == int64(DefinitionVersion) && err == nil {
			out.Recent = append(out.Recent, structure)
		}
	}
	if rows.Err() != nil {
		return out, ErrUnavailable
	}
	return out, nil
}

func (s *Service) rankContext(ctx context.Context, owner Owner, snapshot ContextSnapshot) (ContextSnapshot, error) {
	candidates := make([]EligibleCandidate, 0, len(snapshot.Data.Candidates))
	byRef := map[string]DesignCandidate{}
	for _, candidate := range snapshot.Data.Candidates {
		candidates = append(candidates, snapshot.Bindings[candidate.Ref])
		byRef[candidate.Ref] = candidate
	}
	policy, err := s.recommendationContext(ctx, owner, candidates, snapshot.StructureFilter)
	if err != nil {
		return snapshot, err
	}
	if policy.Active {
		return snapshot, ErrConflict
	}
	ranked := rankCandidates(candidates, policy)
	if len(ranked) == 0 {
		return snapshot, ErrUnavailable
	}
	// The host chooses the next practice; the model designs only its subject and
	// coaching. It cannot override practice/decline policy by selecting another.
	chosen := ranked[0]
	candidate := byRef[chosen.Candidate.Ref]
	candidate.Signals = slices.Clone(chosen.Signals)
	snapshot.Data.Candidates = []DesignCandidate{candidate}
	snapshot.Bindings = map[string]EligibleCandidate{candidate.Ref: chosen.Candidate}
	snapshot.Data.CoachingDepth, snapshot.Data.MaxCoachingHints = coachingBudget(owner.Preferences, policy, chosen.Candidate.Structure)
	if policy.PracticeKnown {
		snapshot.Data.Practice, snapshot.Data.PracticeCounts = "local_review_records_only", policy.Practice
	}
	return snapshot, nil
}

func (m *Mutation) excludeRecommendation(i Instance, state State, now int64) error {
	next := RecommendationExclusion{WorkspaceID: i.Definition.WorkspaceID, Structure: i.Definition.Structure, Topic: i.Definition.FocusTopic}
	if state == Deferred {
		next.Until = now + DeferCooldown.Milliseconds()
	}
	kept := make([]RecommendationExclusion, 0, MaxRecommendationExclusions)
	matched := false
	for _, old := range m.owner.Preferences.Exclusions {
		if old.Until > 0 && old.Until <= now {
			continue
		}
		if old.WorkspaceID == next.WorkspaceID && old.Structure == next.Structure && old.Topic == next.Topic {
			if old.Until == 0 {
				next.Until = 0
			} // a defer cannot undo an earlier decline
			kept, matched = append(kept, next), true
		} else {
			kept = append(kept, old)
		}
	}
	if !matched {
		kept = append(kept, next)
	}
	if len(kept) > MaxRecommendationExclusions {
		return ErrLimit
	}
	m.owner.Preferences.Exclusions = kept
	return nil
}
