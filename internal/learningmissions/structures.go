// Package learningmissions owns local learning contracts and practice evidence.
// It never owns execution, permissions, onboarding completions or rewards.
package learningmissions

import "slices"

const DefinitionVersion = 1

type StructureID string

const (
	DelegateInspect StructureID = "delegate_inspect"
	ImproveResult   StructureID = "improve_result"
	RepeatSuccess   StructureID = "repeat_success"
)

type Structure struct {
	ID         StructureID `json:"id"`
	Version    int         `json:"version"`
	Label      string      `json:"label"`
	Steps      []string    `json:"steps"`
	Criteria   string      `json:"criteria"`
	Competency string      `json:"competency"`
}

// Lookup returns fresh slices; callers cannot mutate the compiled definitions.
func Lookup(id StructureID, version int) (Structure, error) {
	if version != DefinitionVersion {
		return Structure{}, ErrUnsupported
	}
	s := Structure{ID: id, Version: version}
	switch id {
	case DelegateInspect:
		s.Label, s.Competency = "Delegate and inspect", "delegate"
		s.Steps = []string{"scope", "execute", "review"}
		s.Criteria = "Review the scope, start a real Ticket, then decide whether to use, revise or reject its exact completed result."
	case ImproveResult:
		s.Label, s.Competency = "Improve a result", "improve"
		s.Steps = []string{"original", "correction", "execute", "compare"}
		s.Criteria = "Review a correction to a pinned result, run it explicitly, then compare the distinct revised result and describe what changed."
	case RepeatSuccess:
		s.Label, s.Competency = "Repeat a success", "reuse"
		s.Steps = []string{"process", "inputs", "execute", "review"}
		s.Criteria = "Reuse a previously useful process with reviewed, materially different inputs, then review and use its distinct completed result."
	default:
		return Structure{}, ErrUnsupported
	}
	return s, nil
}

func validSteps(id StructureID, version int, steps []string) bool {
	s, err := Lookup(id, version)
	return err == nil && slices.Equal(s.Steps, steps)
}
