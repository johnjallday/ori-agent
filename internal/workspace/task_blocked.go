package workspace

import (
	"errors"
	"strings"
)

// TaskBlockedError indicates execution paused pending user input.
type TaskBlockedChoice struct {
	ID          string `json:"id,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Number      string `json:"number,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

type TaskBlockedFieldOption struct {
	Value       string `json:"value,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type TaskBlockedField struct {
	ID          string                   `json:"id,omitempty"`
	Label       string                   `json:"label,omitempty"`
	Description string                   `json:"description,omitempty"`
	Evidence    string                   `json:"evidence,omitempty"`
	Type        string                   `json:"type,omitempty"`
	Placeholder string                   `json:"placeholder,omitempty"`
	Required    bool                     `json:"required,omitempty"`
	Options     []TaskBlockedFieldOption `json:"options,omitempty"`
}

type TaskBlockedFieldValue struct {
	ID    string `json:"id,omitempty"`
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
}

type TaskBlockedWorkflowStep struct {
	StepType        string              `json:"step_type,omitempty"`
	Title           string              `json:"title,omitempty"`
	Summary         string              `json:"summary,omitempty"`
	Choices         []TaskBlockedChoice `json:"choices,omitempty"`
	Fields          []TaskBlockedField  `json:"fields,omitempty"`
	FreeTextAllowed bool                `json:"free_text_allowed,omitempty"`
}

type TaskBlockedError struct {
	ReasonCode       string
	Reason           string
	Question         string
	SuggestedActions []string
	RawResponse      string
	WorkflowStep     *TaskBlockedWorkflowStep
}

// PrepareTaskBlockedWorkflowStep returns a copy of step with common recovery
// affordances added for user-facing blocked task flows.
func PrepareTaskBlockedWorkflowStep(step *TaskBlockedWorkflowStep, reasonCode string) *TaskBlockedWorkflowStep {
	if step == nil {
		return nil
	}

	prepared := *step
	prepared.Choices = append([]TaskBlockedChoice(nil), step.Choices...)
	prepared.Fields = append([]TaskBlockedField(nil), step.Fields...)

	if strings.EqualFold(strings.TrimSpace(prepared.StepType), "ask_choice") &&
		len(prepared.Choices) >= 2 &&
		shouldOfferAutomaticBlockedChoice(reasonCode) &&
		!taskBlockedChoiceIDExists(prepared.Choices, "ori_decide") {
		prepared.Choices = append(prepared.Choices, TaskBlockedChoice{
			ID:          "ori_decide",
			Label:       "Let Ori decide",
			Description: "Ori will pick the best available path and retry without another prompt.",
			Number:      "AI",
		})
	}

	return &prepared
}

func shouldOfferAutomaticBlockedChoice(reasonCode string) bool {
	switch strings.ToLower(strings.TrimSpace(reasonCode)) {
	case "needs_user_confirmation", "tool_access_unavailable", "placeholder_result", "invalid_status_summary", "tool_only_result", "empty_web_search_results", "location_mismatch":
		return true
	default:
		return false
	}
}

func taskBlockedChoiceIDExists(choices []TaskBlockedChoice, id string) bool {
	target := strings.ToLower(strings.TrimSpace(id))
	if target == "" {
		return false
	}
	for _, choice := range choices {
		if strings.ToLower(strings.TrimSpace(choice.ID)) == target {
			return true
		}
	}
	return false
}

func (e *TaskBlockedError) Error() string {
	if e == nil {
		return "task blocked"
	}
	if strings.TrimSpace(e.Reason) != "" {
		return e.Reason
	}
	return "task blocked"
}

// AsTaskBlockedError unwraps a TaskBlockedError from err if present.
func AsTaskBlockedError(err error) (*TaskBlockedError, bool) {
	if err == nil {
		return nil, false
	}
	var blocked *TaskBlockedError
	if errors.As(err, &blocked) {
		return blocked, true
	}
	return nil, false
}
