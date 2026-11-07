package learningmissions

import (
	"context"
	"maps"
	"slices"
	"strings"
)

// ScopeSource resolves owned workspace metadata without requiring a ready
// executor or granting file/tool access. Only an explicit mutation binds it.
type ScopeSource interface {
	LearningScope(context.Context, string, string) (string, error)
}

type PreferencesRequest struct {
	MutationRequest
	Mode             string   `json:"mode"`
	WorkspaceIDs     []string `json:"workspace_ids"`
	Observe          bool     `json:"observe"`
	Guidance         Guidance `json:"guidance"`
	FullGuidance     bool     `json:"full_guidance,omitempty"`
	Focus            string   `json:"focus"`
	Effort           string   `json:"effort"`
	ConsentVersion   int      `json:"consent_version"`
	ModelFingerprint string   `json:"model_fingerprint,omitempty"`
	ResetExclusions  bool     `json:"reset_exclusions,omitempty"`
}

// UpdatePreferences revokes future recommendations, not existing application
// work. No execution, observation backfill or model request occurs here.
func (s *Service) UpdatePreferences(ctx context.Context, owner string, req PreferencesRequest) (Outcome, error) {
	if s == nil {
		return Outcome{}, ErrUnavailable
	}
	s.effects.Lock()
	defer s.effects.Unlock()
	req.Focus = strings.TrimSpace(req.Focus)
	req.WorkspaceIDs = slices.Clone(req.WorkspaceIDs)
	slices.Sort(req.WorkspaceIDs)
	var next Preferences
	return s.apply(ctx, owner, req.MutationRequest, "preferences", req, func(current Owner) error {
		next = Preferences{Mode: req.Mode, WorkspaceIDs: slices.Clone(req.WorkspaceIDs), Observe: req.Observe,
			Guidance: req.Guidance, FullGuidance: req.FullGuidance, Focus: req.Focus, Effort: req.Effort, ConsentVersion: req.ConsentVersion,
			Exclusions: slices.Clone(current.Preferences.Exclusions), ScopeBindings: map[string]string{}}
		if req.ResetExclusions {
			next.Exclusions = nil
		}
		if next.Mode == "off" {
			next.Observe, next.WorkspaceIDs, next.ConsentVersion = false, []string{}, 0
		}
		if next.Observe {
			next.ObservationSince = s.now().UnixMilli()
			next.ObservationRevision = current.Revision + 1
		}
		if next.Mode == "automatic" {
			next.ModelFingerprint = req.ModelFingerprint
			next.SuggestionSince, next.SuggestionRevision = s.now().UnixMilli(), current.Revision+1
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if next.Mode == "automatic" {
			if s.designer == nil {
				return ErrUnavailable
			}
			model, err := s.designer.Configuration(ctx)
			if err != nil || model.Validate() != nil {
				return ErrUnavailable
			}
			if model.Fingerprint != req.ModelFingerprint {
				return ErrConflict
			}
		}
		if len(next.WorkspaceIDs) > 0 {
			reader, ok := s.source.(ScopeSource)
			if !ok {
				return ErrUnavailable
			}
			for _, id := range next.WorkspaceIDs {
				incarnation, err := reader.LearningScope(ctx, owner, id)
				if err != nil || !validID(incarnation) {
					return ErrUnavailable
				}
				next.ScopeBindings[id] = incarnation
			}
		}
		previousIDs := slices.Clone(current.Preferences.WorkspaceIDs)
		slices.Sort(previousIDs)
		if next.Observe && current.Preferences.Observe && current.Preferences.Mode != "off" && slices.Equal(previousIDs, next.WorkspaceIDs) && maps.Equal(current.Preferences.ScopeBindings, next.ScopeBindings) {
			next.ObservationSince = current.Preferences.ObservationSince
			if current.Preferences.ObservationRevision > 0 {
				next.ObservationRevision = current.Preferences.ObservationRevision
			}
		}
		if next.Mode == "automatic" && current.Preferences.Mode == "automatic" && current.Preferences.SuggestionRevision > 0 &&
			current.Preferences.SuggestionSince > 0 && current.Preferences.ModelFingerprint == next.ModelFingerprint &&
			slices.Equal(previousIDs, next.WorkspaceIDs) && maps.Equal(current.Preferences.ScopeBindings, next.ScopeBindings) {
			next.SuggestionSince, next.SuggestionRevision = current.Preferences.SuggestionSince, current.Preferences.SuggestionRevision
		}
		return next.Validate()
	}, func(m *Mutation, op *Operation) error {
		for id, incarnation := range next.ScopeBindings {
			if err := m.BindSource(id, incarnation); err != nil {
				return err
			}
		}
		if (next.Mode == "off" && m.owner.Preferences.Mode != "off") || (m.owner.Preferences.Mode == "automatic" && next.Mode != "automatic") || (m.owner.Preferences.Observe && !next.Observe) {
			m.storageControl = true
		}
		m.owner.Preferences = next
		return m.invalidateRecommendations(op.CreatedAt)
	})
}

// ClearPreferences requires fresh consent for ongoing observation/suggestions.
// Earned evidence, actual Tickets/Runs and accepted contracts remain untouched.
func (s *Service) ClearPreferences(ctx context.Context, owner string, req MutationRequest) (Outcome, error) {
	if s == nil {
		return Outcome{}, ErrUnavailable
	}
	s.effects.Lock()
	defer s.effects.Unlock()
	return s.apply(ctx, owner, req, "clear_preferences", req, nil, func(m *Mutation, op *Operation) error {
		m.storageControl = true
		m.owner.Preferences = DefaultPreferences()
		return m.invalidateRecommendations(op.CreatedAt)
	})
}

func (m *Mutation) invalidateRecommendations(now int64) error {
	// This restricted terminal transition also works for retired definitions;
	// disabling coaching must not require executing/validating an old recipe.
	if _, err := m.tx.ExecContext(m.ctx, `UPDATE learning_instances SET state='invalidated',revision=revision+1,
 progress=CASE WHEN json_extract(definition,'$.version')=? AND COALESCE(json_extract(definition,'$.support_version'),0)<=? AND json_extract(definition,'$.structure') IN ('delegate_inspect','improve_result','repeat_success') THEN json_set(progress,'$.blocker','preferences_changed') ELSE progress END,
 updated_at_ms=? WHERE owner_id=? AND state='offered'`, DefinitionVersion, GuidanceProtocolVersion, now, m.owner.ID); err != nil {
		return ErrUnavailable
	}
	if err := m.invalidatePendingSuggestions("preferences_changed"); err != nil {
		return err
	}
	observing := m.owner.Preferences.Observe && m.owner.Preferences.Mode != "off" && m.owner.Preferences.ObservationRevision > 0
	if _, err := m.tx.ExecContext(m.ctx, `UPDATE learning_operations SET status='invalidated',record=json_set(record,'$.status','invalidated')
 WHERE owner_id=? AND kind='observe' AND status='pending' AND (?=0 OR json_extract(record,'$.observation.observation_revision')!=?)`, m.owner.ID, observing, m.owner.Preferences.ObservationRevision); err != nil {
		return ErrUnavailable
	}
	return nil
}
