package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProjectCardWords is a Home's bounded snapshot of its package's project-card
// words (PRD #792 FR9): the noun, plural, roster title, fact units and
// declared field ids and labels of the release the Home is pinned to. It is
// written at two moments only, when a Home is created under a declaration
// with the section and when a reviewed package upgrade commits, and never on
// a read. Agent prompts and the agent tools read it, so they need no plugin
// access; a missing or unreadable snapshot means the neutral words.
type ProjectCardWords struct {
	PluginID    string                  `json:"plugin_id"`
	Version     string                  `json:"version"`
	Noun        string                  `json:"noun"`
	NounPlural  string                  `json:"noun_plural"`
	RosterTitle string                  `json:"roster_title"`
	Facts       []ProjectCardWordsFact  `json:"facts,omitempty"`
	Fields      []ProjectCardWordsField `json:"fields,omitempty"`
}

// ProjectCardWordsFact is one declared fact unit.
type ProjectCardWordsFact struct {
	Kind    string `json:"kind"`
	Unit    string `json:"unit,omitempty"`
	UnitOne string `json:"unit_one,omitempty"`
}

// ProjectCardWordsField is one declared plugin field.
type ProjectCardWordsField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

var projectCardWordsIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// UnmarshalJSON never fails the Home's load: a snapshot that does not decode
// reads as empty, which Valid reports and readers treat as neutral words.
func (w *ProjectCardWords) UnmarshalJSON(data []byte) error {
	type plain ProjectCardWords
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		*w = ProjectCardWords{}
		return nil
	}
	*w = ProjectCardWords(decoded)
	return nil
}

func projectCardWord(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= limit && !strings.Contains(value, "://") &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

// Valid reports whether the snapshot is complete and within the declaration's
// own limits. An invalid snapshot is never used for a prompt or a check.
func (w *ProjectCardWords) Valid() bool {
	if w == nil || !projectCardWord(w.Noun, 40) || !projectCardWord(w.NounPlural, 40) ||
		!projectCardWord(w.RosterTitle, 60) || len(w.PluginID) > 160 || len(w.Version) > 64 ||
		len(w.Facts) > 3 || len(w.Fields) > 8 {
		return false
	}
	kinds := map[string]bool{}
	for _, fact := range w.Facts {
		switch fact.Kind {
		case "tempo", "length", "track_count":
		default:
			return false
		}
		if kinds[fact.Kind] || (fact.Unit != "" && !projectCardWord(fact.Unit, 40)) ||
			(fact.UnitOne != "" && !projectCardWord(fact.UnitOne, 40)) {
			return false
		}
		kinds[fact.Kind] = true
	}
	ids := map[string]bool{}
	for _, field := range w.Fields {
		if !projectCardWordsIDPattern.MatchString(field.ID) || ids[field.ID] || !projectCardWord(field.Label, 40) ||
			(field.Kind != "text" && field.Kind != "list") {
			return false
		}
		ids[field.ID] = true
	}
	return true
}

// PromptNoun is the noun and plural agent prompts use: the snapshot's when it
// is valid, the neutral "project"/"projects" otherwise. Both lower case, for
// running text.
func (w *ProjectCardWords) PromptNoun() (string, string) {
	if !w.Valid() {
		return "project", "projects"
	}
	return strings.ToLower(w.Noun), strings.ToLower(w.NounPlural)
}

// RenderProjectCardWordsPromptLines are the Assistant Program context's lines
// about what this Home calls one project and which plugin fields its card
// declares (PRD #792 FR9). Words are package display data, quoted.
func RenderProjectCardWordsPromptLines(w *ProjectCardWords) []string {
	noun, plural := w.PromptNoun()
	lines := []string{fmt.Sprintf("- One project of this Home is called a %q (plural %q).", noun, plural)}
	if w.Valid() && len(w.Fields) > 0 {
		fields := make([]string, 0, len(w.Fields))
		for _, field := range w.Fields {
			fields = append(fields, fmt.Sprintf("%s %q (%s)", field.ID, field.Label, field.Kind))
		}
		lines = append(lines, "- Plugin fields on the "+noun+" card (ids for suggestions): "+strings.Join(fields, ", "))
	}
	return lines
}

// projectCardWordsForPin keeps a creation-time snapshot only when it belongs
// to the provider the new Home is pinned to and is valid.
func projectCardWordsForPin(owner *AssistantProgramHomeOwner, words *ProjectCardWords) *ProjectCardWords {
	if owner == nil || !words.Valid() || !strings.EqualFold(words.PluginID, owner.PluginID) || words.Version != owner.PluginVersion {
		return nil
	}
	return words.Clone()
}

// Clone returns a deep copy.
func (w *ProjectCardWords) Clone() *ProjectCardWords {
	if w == nil {
		return nil
	}
	clone := *w
	clone.Facts = append([]ProjectCardWordsFact(nil), w.Facts...)
	clone.Fields = append([]ProjectCardWordsField(nil), w.Fields...)
	return &clone
}
