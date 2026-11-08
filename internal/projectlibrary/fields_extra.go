package projectlibrary

import "regexp"

// Limits on plugin-declared field values (PRD #792 FR3, FR28). They hold
// without the declaration; whether an id is declared is checked where the
// declaration is known (owner edit, proposal save and review, responses).
const (
	MaxExtraFields     = 8
	MaxExtraFieldLines = 16
	maxExtraLineRunes  = 240
)

// extraFieldIDPattern is the declaration id pattern of a project_card field.
var extraFieldIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// validExtraFields enforces the storage limits: at most eight ids of the
// declaration pattern, each with 1 to 16 lines of 1 to 240 characters.
func validExtraFields(extra map[string][]string) bool {
	if len(extra) > MaxExtraFields {
		return false
	}
	for id, lines := range extra {
		if !extraFieldIDPattern.MatchString(id) || len(lines) == 0 || len(lines) > MaxExtraFieldLines {
			return false
		}
		for _, line := range lines {
			if line == "" || !validText(line, maxExtraLineRunes) {
				return false
			}
		}
	}
	return true
}

// DeclaredExtraFields reports whether every id in extra is a field the
// Home's words declare, and a `text` field holds exactly one line. The
// boundaries that know the declaration call it: the owner's edit, a proposal
// save and its review.
func DeclaredExtraFields(extra map[string][]string, words Words) bool {
	if !validExtraFields(extra) {
		return false
	}
	for id, lines := range extra {
		field, ok := words.Field(id)
		if !ok || (field.Kind == "text" && len(lines) != 1) {
			return false
		}
	}
	return true
}

// ShownExtraFields keeps only the ids the words declare, in a new map: a
// stored id the package no longer declares is kept on disk but sent to no
// client (FR30). Nil when nothing is shown.
func ShownExtraFields(extra map[string][]string, words Words) map[string][]string {
	var shown map[string][]string
	for id, lines := range extra {
		if _, ok := words.Field(id); !ok {
			continue
		}
		if shown == nil {
			shown = make(map[string][]string, len(extra))
		}
		shown[id] = append([]string(nil), lines...)
	}
	return shown
}

// cloneExtraFields copies the map and its lines; empty is nil.
func cloneExtraFields(extra map[string][]string) map[string][]string {
	if len(extra) == 0 {
		return nil
	}
	clone := make(map[string][]string, len(extra))
	for id, lines := range extra {
		clone[id] = append([]string(nil), lines...)
	}
	return clone
}
