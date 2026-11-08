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

// ShownFields is fields as a client may see them: plugin fields only under
// ids the words declare. Every response carrying Fields goes through it.
func ShownFields(fields Fields, words Words) Fields {
	fields.Extra = ShownExtraFields(fields.Extra, words)
	return fields
}

// OwnerExtraPatch is the owner's plugin-field edit as it is stored: every id
// in the browser's map must be declared (and a text field one line), and the
// entry's stored ids the words no longer declare are carried over unchanged,
// so a package change never loses the owner's words (FR29, FR30). Review and
// commit both call it on the same entry revision, so they digest the same
// patch. A patch without extra is returned as it is.
func OwnerExtraPatch(patch FieldsPatch, stored Fields, words Words) (FieldsPatch, error) {
	if patch.Extra == nil {
		return patch, nil
	}
	if !DeclaredExtraFields(*patch.Extra, words) {
		return FieldsPatch{}, ErrConflict
	}
	merged := cloneExtraFields(*patch.Extra)
	for id, lines := range stored.Extra {
		if _, declared := words.Field(id); declared {
			continue
		}
		if merged == nil {
			merged = map[string][]string{}
		}
		merged[id] = append([]string(nil), lines...)
	}
	if merged == nil {
		merged = map[string][]string{}
	}
	patch.Extra = &merged
	return patch, nil
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
