package learningmissions

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

const (
	RecentActivityLimit = 20
	RecentActivityAge   = 30 * 24 * time.Hour
)

// ActivityCounts is metadata, never evidence of skill or useful work. Unknown
// and partial coverage must not be converted into an inferred beginner label.
type ActivityCounts struct {
	Coverage  string `json:"coverage"`
	Observed  int    `json:"observed"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
}

func (a ActivityCounts) valid() bool {
	return slices.Contains([]string{"unknown", "partial", "available"}, a.Coverage) &&
		a.Observed >= 0 && a.Observed <= RecentActivityLimit && a.Succeeded >= 0 && a.Failed >= 0 &&
		a.Succeeded+a.Failed <= a.Observed && (a.Coverage != "unknown" || a.Observed == 0)
}

type ActivitySource interface {
	Activity(context.Context, string, string, time.Time, time.Time) (ActivityCounts, error)
}

// EligibleCandidate is a host-only binding. None of its local identities,
// receipt references or scope digests are sent to the design model.
type EligibleCandidate struct {
	Candidate
	Structure       StructureID
	OriginalReceipt string
}

type CandidateSource interface {
	Candidates(context.Context, string, string) ([]EligibleCandidate, error)
}

type DesignCandidate struct {
	Ref          string      `json:"candidate_ref"`
	Workspace    string      `json:"workspace_name"` // untrusted display text, not an instruction
	WorkspaceRef string      `json:"workspace_ref"`
	ReviewedGoal string      `json:"reviewed_goal,omitempty"`
	Structure    StructureID `json:"structure"`
	Version      int         `json:"version"`
	Steps        []string    `json:"steps"`
	Effect       string      `json:"effect"`
	Ready        bool        `json:"ready"`
	Signals      []string    `json:"host_signals,omitempty"`
}

type WorkspaceObservation struct {
	Ref      string         `json:"workspace_ref"` // per-snapshot alias, not a canonical ID
	Current  string         `json:"current"`
	Activity ActivityCounts `json:"recent_activity"`
}

type DesignContext struct {
	Version          int                    `json:"version"`
	Focus            string                 `json:"explicit_focus"`
	Effort           string                 `json:"explicit_effort"`
	Guidance         Guidance               `json:"explicit_guidance"`
	CoachingDepth    string                 `json:"coaching_depth"`
	MaxCoachingHints int                    `json:"max_coaching_hints"`
	Practice         string                 `json:"practice_coverage"`
	PracticeCounts   map[string]int         `json:"local_practice_counts,omitempty"`
	Observations     []WorkspaceObservation `json:"observations"`
	Candidates       []DesignCandidate      `json:"candidates"`
}

// ContextSnapshot is never marshalled into a model request: only Data is.
// Bindings stay host-owned for publication revalidation and action resolution.
type ContextSnapshot struct {
	Data            DesignContext                `json:"-"`
	Bindings        map[string]EligibleCandidate `json:"-"`
	StructureFilter StructureID                  `json:"-"`
	CandidateFilter string                       `json:"-"`
}

func (s ContextSnapshot) ModelJSON() ([]byte, error) {
	data, err := json.Marshal(s.Data)
	if err != nil || len(data) > MaxInputBytes {
		return nil, ErrLimit
	}
	return data, nil
}

// BuildContext is a read-only projection for an explicitly admitted proposal,
// not a GET hook. It reads exactly the approved scopes, never lists workspaces,
// and has no model/execution dependency. Failed scope reads add no source names.
func BuildContext(ctx context.Context, source Source, owner Owner, now time.Time) (ContextSnapshot, error) {
	p := owner.Preferences
	if source == nil || !validID(owner.ID) || p.Validate() != nil || p.Mode == "off" {
		return ContextSnapshot{}, ErrUnavailable
	}
	out := ContextSnapshot{Data: DesignContext{Version: 1, Focus: p.Focus, Effort: p.Effort, Guidance: p.Guidance,
		Practice: "unknown", Observations: []WorkspaceObservation{}, Candidates: []DesignCandidate{}}, Bindings: map[string]EligibleCandidate{}}
	out.Data.CoachingDepth, out.Data.MaxCoachingHints = "full", 4
	if p.Guidance == GuidanceBasic {
		out.Data.CoachingDepth, out.Data.MaxCoachingHints = "basic", 0
	}
	scopeAliases := [...]string{"scope-a", "scope-b", "scope-c", "scope-d", "scope-e"}
	for _, workspaceID := range p.WorkspaceIDs {
		if err := ctx.Err(); err != nil {
			return ContextSnapshot{}, err
		}
		if len(out.Data.Observations) >= len(scopeAliases) {
			return ContextSnapshot{}, ErrLimit
		}
		observation := WorkspaceObservation{Ref: scopeAliases[len(out.Data.Observations)], Current: "unavailable", Activity: ActivityCounts{Coverage: "unknown"}}
		var candidates []EligibleCandidate
		var err error
		observeScope := false
		if reader, ok := source.(CandidateSource); ok {
			candidates, err = reader.Candidates(ctx, owner.ID, workspaceID)
		} else {
			var c Candidate
			c, err = source.Candidate(ctx, owner.ID, workspaceID)
			candidates = []EligibleCandidate{{Candidate: c, Structure: DelegateInspect}}
		}
		if err == nil && len(candidates) > 0 && len(candidates) <= MaxCandidates {
			for _, c := range candidates {
				structure, lookupErr := Lookup(c.Structure, 1)
				if lookupErr != nil || c.WorkspaceID != workspaceID || !validID(c.Ref) || !validID(c.Incarnation) ||
					!validDigest(c.ScopeDigest) || !boundedText(c.Name, 120, true) || !boundedText(c.ReviewedGoal, 300, false) ||
					(c.Structure != DelegateInspect && !validID(c.OriginalReceipt)) {
					continue
				}
				if p.Mode == "automatic" && !p.AllowsScope(workspaceID, c.Incarnation) {
					continue
				}
				observeScope = observeScope || p.Observes(workspaceID, c.Incarnation)
				if _, duplicate := out.Bindings[c.Ref]; duplicate {
					continue
				}
				observation.Current = "ready"
				if len(out.Data.Candidates) == MaxCandidates {
					continue
				}
				out.Bindings[c.Ref] = c
				out.Data.Candidates = append(out.Data.Candidates, DesignCandidate{Ref: c.Ref, Workspace: strings.TrimSpace(c.Name), WorkspaceRef: observation.Ref, ReviewedGoal: c.ReviewedGoal,
					Structure: structure.ID, Version: structure.Version, Steps: slices.Clone(structure.Steps), Effect: "internal_draft", Ready: true})
			}
		}
		if observation.Current == "ready" && observeScope {
			if reader, ok := source.(ActivitySource); ok {
				since := now.Add(-RecentActivityAge)
				if consent := time.UnixMilli(p.ObservationSince); consent.After(since) {
					since = consent
				}
				if since.Before(now) {
					if activity, err := reader.Activity(ctx, owner.ID, workspaceID, since, now); err == nil && activity.valid() {
						observation.Activity = activity
					}
				}
			}
		}
		out.Data.Observations = append(out.Data.Observations, observation)
	}
	if err := ctx.Err(); err != nil {
		return ContextSnapshot{}, err
	}
	if _, err := out.ModelJSON(); err != nil {
		return ContextSnapshot{}, err
	}
	return out, nil
}
