package charactercatalog

import (
	"fmt"
	"strings"
)

// Character tone: a bounded, low-priority presentation hint.
//
// The entire input is a reviewed catalog entry plus one boolean. There is no
// field anywhere in this package that can carry free-form prompt text, so a
// character cannot smuggle an instruction into a runtime prompt however its
// metadata is edited (PRD FR-53/FR-62/FR-75).
//
// What the hint may influence: brevity, warmth, cadence. What it explicitly must
// not touch is stated in the hint itself, because the model reads it — role,
// factual standards, the user's system prompt, task instructions, tool policy,
// safety policy, confirmation policy, and workspace permissions (FR-61).
//
// It is composed as a separate layer rather than merged into the stored prompt,
// so turning it off restores the previous behaviour exactly and the user's own
// text is never rewritten (FR-62).

// ToneHintPrefix marks the layer wherever it appears, so effective-prompt
// inspection can point at it and say where it came from (FR-63).
const ToneHintPrefix = "Character tone layer"

// ToneHint returns the runtime hint for a character, or "" when the character
// has no usable tone traits.
//
// The traits come from the catalog and were reviewed alongside the art. They are
// joined into a fixed sentence template — the catalog supplies adjectives, never
// sentences, so there is no path from catalog data to an arbitrary instruction.
func (c *Catalog) ToneHint(id CharacterID) string {
	ch, ok := c.Get(id)
	if !ok || ch.Kind != KindWorking {
		return ""
	}
	return ch.ToneHint()
}

// ToneHint builds the bounded hint for one character.
func (ch Character) ToneHint() string {
	traits := make([]string, 0, len(ch.ToneTraits))
	for _, t := range ch.ToneTraits {
		t = strings.TrimSpace(t)
		// Adjectives only. Anything with sentence punctuation is not a trait and
		// is dropped rather than passed through.
		if t == "" || strings.ContainsAny(t, ".!?:;\n\"'") {
			continue
		}
		if len(t) > 40 {
			continue
		}
		traits = append(traits, t)
	}
	if len(traits) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"%s (%s): when it does not conflict with anything else, phrase replies as %s. "+
			"This affects wording only. It does not change your role, your instructions, "+
			"what tools you may use, what you are permitted to do, when you must ask for "+
			"confirmation, or your obligation to be accurate. If this tone would conflict "+
			"with any of those, ignore it.",
		ToneHintPrefix,
		ch.Name,
		strings.Join(traits, ", "),
	)
}
