package workspace

import (
	"errors"
	"strings"
)

// TaskBlockedError indicates execution paused pending user input.
type TaskBlockedError struct {
	ReasonCode       string
	Reason           string
	Question         string
	SuggestedActions []string
	RawResponse      string
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
