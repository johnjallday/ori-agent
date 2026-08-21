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

// Confirmation tiers sort every runnable action by how much ceremony it earns.
// Confirming a reversible action is friction, not safety, so only genuinely
// destructive work asks first and everything else offers an Undo afterwards.
const (
	// TierSilent runs immediately and says nothing. Non-mutating actions only.
	TierSilent = "silent"
	// TierUndoable runs immediately and offers an Undo. Reversible mutations.
	TierUndoable = "undoable"
	// TierConfirm asks before running. Destructive or unreviewed actions.
	TierConfirm = "confirm"
)

// Action is one command the live REAPER control surface can execute. IDs are
// REAPER command IDs rather than Ori-generated aliases, so the UI and agent
// tools always describe the same runnable catalog.
//
// Tier is the authority on confirmation; NeedsConfirmation remains the wire
// field older console builds read. Callers must resolve the tier through
// ResolveTier so an action assembled without an explicit tier still fails
// closed onto NeedsConfirmation rather than onto the zero value.
type Action struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Description       string `json:"description"`
	Source            string `json:"source"`
	Mutates           bool   `json:"mutates"`
	NeedsConfirmation bool   `json:"needs_confirmation"`
	Tier              string `json:"tier,omitempty"`
	// UndoSummary is what the toast says this action did, in the user's own
	// words. Empty falls back to the label.
	UndoSummary string `json:"undo_summary,omitempty"`
}

// ResolveTier returns the action's confirmation tier. An unset or unrecognized
// tier falls back to NeedsConfirmation, so no action can lose its confirmation
// gate by being constructed without a tier.
func (a Action) ResolveTier() string {
	switch strings.TrimSpace(a.Tier) {
	case TierSilent:
		return TierSilent
	case TierUndoable:
		return TierUndoable
	case TierConfirm:
		return TierConfirm
	}
	if a.NeedsConfirmation {
		return TierConfirm
	}
	return TierSilent
}

var (
	rawCommandIDPattern        = regexp.MustCompile(`^(?:[0-9]{1,10}|_RS[0-9A-Fa-f]+)$`)
	executableCommandIDPattern = regexp.MustCompile(`^(?:[0-9]{1,10}|_RS[A-Za-z0-9_]{1,96})$`)
)

var builtinActions = []Action{
	{ID: "1007", Label: "Play", Description: "Start playback from the current position.", Source: ActionSourceBuiltin, Tier: TierSilent},
	{ID: "1016", Label: "Stop", Description: "Stop playback or recording.", Source: ActionSourceBuiltin, Tier: TierSilent},
	{ID: "1013", Label: "Record", Description: "Begin recording into armed tracks.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true, Tier: TierConfirm},
	{ID: "1008", Label: "Pause", Description: "Pause or resume the transport.", Source: ActionSourceBuiltin, Tier: TierSilent},
	{ID: "40026", Label: "Save project", Description: "Save the open REAPER project.", Source: ActionSourceBuiltin, Mutates: true, NeedsConfirmation: true, Tier: TierConfirm},
	{ID: "40001", Label: "Insert new track", Description: "Add a new track to the open project.", Source: ActionSourceBuiltin, Mutates: true, Tier: TierUndoable, UndoSummary: "Inserted a new track"},
	{ID: "40364", Label: "Toggle metronome", Description: "Toggle REAPER's metronome for the open project.", Source: ActionSourceBuiltin, Mutates: true, Tier: TierUndoable, UndoSummary: "Toggled the metronome"},
	{ID: "40029", Label: "Undo", Description: "Undo the most recent project change.", Source: ActionSourceBuiltin, Mutates: true, Tier: TierUndoable},
	{ID: "40030", Label: "Redo", Description: "Redo the most recently undone project change.", Source: ActionSourceBuiltin, Mutates: true, Tier: TierUndoable},
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
