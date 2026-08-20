package reaper

import (
	"regexp"
	"strings"
)

const (
	ActionSourceBuiltin    = "builtin"
	ActionSourceRegistered = "registered"
	ActionSourceCustom     = "custom"
)

// Action is one command the live REAPER control surface can execute. IDs are
// REAPER command IDs rather than Ori-generated aliases, so the UI and agent
// tools always describe the same runnable catalog.
type Action struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Description       string `json:"description"`
	Source            string `json:"source"`
	Mutates           bool   `json:"mutates"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
}

var (
	rawCommandIDPattern        = regexp.MustCompile(`^(?:[0-9]{1,10}|_RS[0-9A-Fa-f]+)$`)
	executableCommandIDPattern = regexp.MustCompile(`^(?:[0-9]{1,10}|_RS[A-Za-z0-9_]{1,96})$`)
)

var builtinActions = []Action{
	{ID: "1007", Label: "Play", Description: "Start playback from the current position.", Source: ActionSourceBuiltin},
	{ID: "1016", Label: "Stop", Description: "Stop playback or recording.", Source: ActionSourceBuiltin},
	{ID: "1013", Label: "Record", Description: "Begin recording into armed tracks.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true},
	{ID: "1008", Label: "Pause", Description: "Pause or resume the transport.", Source: ActionSourceBuiltin},
	{ID: "40026", Label: "Save project", Description: "Save the open REAPER project.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true},
	{ID: "40001", Label: "Insert new track", Description: "Add a new track to the open project.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true},
	{ID: "40364", Label: "Toggle metronome", Description: "Toggle REAPER's metronome for the open project.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true},
	{ID: "40029", Label: "Undo", Description: "Undo the most recent project change.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true},
	{ID: "40030", Label: "Redo", Description: "Redo the most recently undone project change.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true},
}

// BuiltinActions returns a copy so callers cannot mutate the shared catalog.
func BuiltinActions() []Action {
	actions := make([]Action, len(builtinActions))
	copy(actions, builtinActions)
	return actions
}

// BuiltinAction resolves a curated command ID case-insensitively.
func BuiltinAction(id string) (Action, bool) {
	id = strings.TrimSpace(id)
	for _, action := range builtinActions {
		if strings.EqualFold(action.ID, id) {
			return action, true
		}
	}
	return Action{}, false
}

// ValidRawCommandID applies the deliberately narrow user-input grammar from
// the product contract. Registered catalog entries are trusted separately and
// may contain REAPER's section prefix underscore.
func ValidRawCommandID(id string) bool {
	return rawCommandIDPattern.MatchString(strings.TrimSpace(id))
}

func validExecutableCommandID(id string) bool {
	return executableCommandIDPattern.MatchString(strings.TrimSpace(id))
}
