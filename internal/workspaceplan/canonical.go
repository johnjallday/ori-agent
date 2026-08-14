package workspaceplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// The approval hash.
//
// An approval binds to one exact version by its content hash (FR-32). That
// makes the hash a security boundary, not a cache key: whatever it covers is
// what the user is held to have agreed to, and whatever it omits can change
// after approval without anyone noticing.
//
// So the rule here is deliberately conservative. Everything that could change
// WHAT WORK HAPPENS or WHAT THE USER AGREED TO is included (FR-33). Only fields
// that are purely how the plan reads — prose, titles on non-actionable
// elements, provenance, timestamps — are excluded, and each exclusion is named
// below and tested in canonical_test.go (FR-34).
//
// When in doubt, include. Over-inclusion costs a re-approval the user did not
// strictly need; under-inclusion lets approved work change silently.
//
// Excluded, with the reason each one cannot change what happens:
//
//   - Explanation, Rationale, TaskGroup.Notes — prose for the reader. Nothing
//     reads them for dependency, assignment, or approval meaning (FR-53).
//   - Source.Title, Source.Excerpt — display text for a reference. The Ref is
//     what anything acts on, and it is included.
//   - ProposedArtifact.Title, ProposedArtifact.Description — labels. Kind,
//     Path, and Enabled decide whether and where a file is written, and all
//     three are included.
//   - Clarification.Detail, Clarification.Options — how a question was
//     presented. The prompt, its requiredness, its status, and the authored
//     answer are included, because those shaped the plan.
//   - Author on every element — provenance, not content.
//   - All timestamps and IDs of non-actionable records — bookkeeping.
//
// Ordering is preserved everywhere, because reordering task groups and items
// changes execution order and is therefore approval-relevant (FR-36).

// canonicalPlan is the exact shape hashed for approval. It is written out
// field by field rather than derived by reflection so that adding a field to
// PlanContent cannot silently join or skip the approval hash: a new field is
// invisible here until someone decides which side it belongs on.
type canonicalPlan struct {
	Objective   string                `json:"objective"`
	InScope     []string              `json:"in_scope"`
	NonGoals    []string              `json:"non_goals"`
	Assumptions []canonicalAssumption `json:"assumptions"`
	Risks       []canonicalRisk       `json:"risks"`
	Sources     []canonicalSource     `json:"sources"`
	Artifacts   []canonicalArtifact   `json:"artifacts"`
	Groups      []canonicalGroup      `json:"groups"`
	Validations []canonicalValidation `json:"validations"`
	Execution   canonicalExecution    `json:"execution"`
	// Clarifications are included because the answers shaped the plan the user
	// is approving. Changing an answer after review means the plan was built on
	// a different premise than the one being approved.
	Clarifications []canonicalClarification `json:"clarifications"`
	// Policy is the enforced policy snapshot. Approving a plan under "block
	// code execution on an unsafe branch" is a different decision from
	// approving the same tasks without it (FR-144).
	Policy canonicalPolicy `json:"policy"`
}

type canonicalAssumption struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	// ClarificationID is included because an assumption standing in for a
	// skipped question is materially different from a freely-stated one.
	ClarificationID string `json:"clarification_id,omitempty"`
}

type canonicalRisk struct {
	ID         string       `json:"id"`
	Statement  string       `json:"statement"`
	Severity   RiskSeverity `json:"severity,omitempty"`
	Mitigation string       `json:"mitigation,omitempty"`
}

type canonicalSource struct {
	ID   string     `json:"id"`
	Kind SourceKind `json:"kind"`
	Ref  string     `json:"ref"`
}

type canonicalArtifact struct {
	ID      string       `json:"id"`
	Kind    ArtifactKind `json:"kind"`
	Path    string       `json:"path"`
	Enabled bool         `json:"enabled"`
}

type canonicalGroup struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Outcome   string          `json:"outcome"`
	DependsOn []string        `json:"depends_on"`
	Items     []canonicalItem `json:"items"`
}

type canonicalItem struct {
	ID                   string   `json:"id"`
	Description          string   `json:"description"`
	Details              string   `json:"details"`
	Assignee             string   `json:"assignee"`
	AssigneeNodeID       string   `json:"assignee_node_id"`
	RequiredCapabilities []string `json:"required_capabilities"`
	DependsOn            []string `json:"depends_on"`
	ExpectedResult       string   `json:"expected_result"`
	Priority             int      `json:"priority"`
	ReferenceURL         string   `json:"reference_url"`
}

type canonicalValidation struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	AppliesTo   []string `json:"applies_to"`
	Expectation string   `json:"expectation"`
	Required    bool     `json:"required"`
}

type canonicalExecution struct {
	Mode          ExecutionMode `json:"mode"`
	Preconditions []string      `json:"preconditions"`
}

type canonicalClarification struct {
	ID       string              `json:"id"`
	Prompt   string              `json:"prompt"`
	Required bool                `json:"required"`
	Status   ClarificationStatus `json:"status"`
	Answer   string              `json:"answer"`
}

type canonicalPolicy struct {
	Profile       string          `json:"profile,omitempty"`
	Preset        string          `json:"preset,omitempty"`
	Enforced      map[string]bool `json:"enforced,omitempty"`
	ExecutionMode ExecutionMode   `json:"execution_mode,omitempty"`
}

// Canonicalize reduces a Plan version to its approval-relevant form.
func Canonicalize(objective string, content PlanContent, policy PolicySnapshot) canonicalPlan {
	canonical := canonicalPlan{
		Objective: objective,
		InScope:   nonNil(content.InScope),
		NonGoals:  nonNil(content.NonGoals),
		Execution: canonicalExecution{
			Mode:          content.Execution.Mode,
			Preconditions: nonNil(content.Execution.Preconditions),
		},
		Policy: canonicalPolicy{
			Profile:       policy.Profile,
			Preset:        policy.Preset,
			Enforced:      policy.Enforced,
			ExecutionMode: policy.ExecutionMode,
		},
	}

	canonical.Assumptions = make([]canonicalAssumption, 0, len(content.Assumptions))
	for _, assumption := range content.Assumptions {
		canonical.Assumptions = append(canonical.Assumptions, canonicalAssumption{
			ID:              assumption.ID,
			Statement:       assumption.Statement,
			ClarificationID: assumption.ClarificationID,
		})
	}

	canonical.Risks = make([]canonicalRisk, 0, len(content.Risks))
	for _, risk := range content.Risks {
		canonical.Risks = append(canonical.Risks, canonicalRisk{
			ID: risk.ID, Statement: risk.Statement,
			Severity: risk.Severity, Mitigation: risk.Mitigation,
		})
	}

	canonical.Sources = make([]canonicalSource, 0, len(content.Sources))
	for _, source := range content.Sources {
		canonical.Sources = append(canonical.Sources, canonicalSource{
			ID: source.ID, Kind: source.Kind, Ref: source.Ref,
		})
	}

	canonical.Artifacts = make([]canonicalArtifact, 0, len(content.Artifacts))
	for _, artifact := range content.Artifacts {
		canonical.Artifacts = append(canonical.Artifacts, canonicalArtifact{
			ID: artifact.ID, Kind: artifact.Kind,
			Path: artifact.Path, Enabled: artifact.Enabled,
		})
	}

	canonical.Groups = make([]canonicalGroup, 0, len(content.Groups))
	for _, group := range content.Groups {
		canonicalGrp := canonicalGroup{
			ID: group.ID, Title: group.Title, Outcome: group.Outcome,
			DependsOn: nonNil(group.DependsOn),
			Items:     make([]canonicalItem, 0, len(group.Items)),
		}
		for _, item := range group.Items {
			canonicalGrp.Items = append(canonicalGrp.Items, canonicalItem{
				ID: item.ID, Description: item.Description, Details: item.Details,
				Assignee: item.Assignee, AssigneeNodeID: item.AssigneeNodeID,
				RequiredCapabilities: nonNil(item.RequiredCapabilities),
				DependsOn:            nonNil(item.DependsOn),
				ExpectedResult:       item.ExpectedResult,
				Priority:             item.Priority,
				ReferenceURL:         item.ReferenceURL,
			})
		}
		canonical.Groups = append(canonical.Groups, canonicalGrp)
	}

	canonical.Validations = make([]canonicalValidation, 0, len(content.Validations))
	for _, checkpoint := range content.Validations {
		canonical.Validations = append(canonical.Validations, canonicalValidation{
			ID: checkpoint.ID, Title: checkpoint.Title,
			AppliesTo:   nonNil(checkpoint.AppliesTo),
			Expectation: checkpoint.Expectation, Required: checkpoint.Required,
		})
	}

	canonical.Clarifications = make([]canonicalClarification, 0, len(content.Clarifications))
	for _, question := range content.Clarifications {
		canonical.Clarifications = append(canonical.Clarifications, canonicalClarification{
			ID: question.ID, Prompt: question.Prompt, Required: question.Required,
			Status: question.Status, Answer: question.Answer,
		})
	}

	return canonical
}

// ContentHash returns the deterministic hash an approval binds to (FR-32).
//
// Two versions with the same approval-relevant content hash identically no
// matter how they were produced, and any approval-relevant edit changes the
// hash — which is what invalidates an outstanding approval view (FR-68).
func ContentHash(objective string, content PlanContent, policy PolicySnapshot) (string, error) {
	encoded, err := json.Marshal(Canonicalize(objective, content, policy))
	if err != nil {
		return "", fmt.Errorf("hash plan content: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// nonNil normalizes a nil slice to an empty one so that "no dependencies" and
// "an empty dependency list" hash identically. They mean the same thing, and a
// user should not be asked to re-approve because a serializer round-trip
// changed nil into [].
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
