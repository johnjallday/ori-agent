package workspacearrangement

import (
	"encoding/json"
	"time"
)

// MaxSummaryBytes bounds the browser projection, separately from model intent.
// Stored coverage/provenance adds bytes beyond the model's 32-KiB draft budget.
const MaxSummaryBytes = 64 << 10

// Summary is bounded canonical history, not a compiled review. It omits member
// IDs, revision maps, selectors and source/authority fingerprints. Readiness
// must never be inferred from the record's historical state.
type Summary struct {
	Origin              string                  `json:"origin"`
	CreatedAt           time.Time               `json:"created_at"`
	UpdatedAt           time.Time               `json:"updated_at"`
	ID                  string                  `json:"id"`
	Revision            int64                   `json:"revision"`
	State               string                  `json:"state"`
	Goal                string                  `json:"goal"`
	Rationale           string                  `json:"rationale"`
	Unknowns            []string                `json:"unknowns"`
	Operations          int                     `json:"operations"`
	ProposedOperations  []OperationSummary      `json:"proposed_operations"`
	ExplicitUnchanged   int                     `json:"explicit_unchanged"`
	ExplicitNeedsReview int                     `json:"explicit_needs_review"`
	ReviewQuestions     []RecommendationSummary `json:"review_questions"`
	Coverage            Coverage                `json:"coverage"`
	ReviewAvailable     bool                    `json:"review_available"`
}

type RecommendationSummary struct {
	Reason  string `json:"reason"`
	Members int    `json:"members"`
}

type OperationSummary struct {
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Members int    `json:"members"`
}

// Summarize never resolves a path or revives live source authority. Validate the
// stored record first so callers cannot use an unbounded arbitrary Draft as a
// supposedly bounded browser projection.
func (r Record) Summarize() (Summary, error) {
	if err := r.Validate(); err != nil {
		return Summary{}, err
	}
	out := Summary{Origin: r.Origin, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ID: r.ID, Revision: r.Revision, State: r.State, Goal: r.Draft.Goal, Rationale: r.Draft.Rationale,
		Unknowns: append([]string{}, r.Draft.Unknowns...), Operations: len(r.Draft.Operations),
		ProposedOperations: make([]OperationSummary, 0, len(r.Draft.Operations)), ExplicitUnchanged: len(r.Draft.Unchanged.IDs), Coverage: r.Coverage}
	for _, op := range r.Draft.Operations {
		out.ProposedOperations = append(out.ProposedOperations, OperationSummary{Kind: op.Kind, Name: op.Name, Members: len(op.Members.IDs)})
	}
	// These are explicit dispositions, not a partition of the inventory:
	// unmentioned items remain unchanged and one item may have several questions.
	needsReview := map[string]bool{}
	out.ReviewQuestions = make([]RecommendationSummary, 0, len(r.Draft.NeedsReview))
	for _, item := range r.Draft.NeedsReview {
		out.ReviewQuestions = append(out.ReviewQuestions, RecommendationSummary{Reason: item.Reason, Members: len(item.Items.IDs)})
		for _, id := range item.Items.IDs {
			needsReview[id] = true
		}
	}
	out.ExplicitNeedsReview = len(needsReview)
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > MaxSummaryBytes {
		return Summary{}, ErrLimit
	}
	return out, nil
}
