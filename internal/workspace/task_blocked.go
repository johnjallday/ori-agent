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
}

type TaskBlockedWorkflowStep struct {
	StepType        string              `json:"step_type,omitempty"`
	Title           string              `json:"title,omitempty"`
	Summary         string              `json:"summary,omitempty"`
	Choices         []TaskBlockedChoice `json:"choices,omitempty"`
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
