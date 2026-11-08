package projectlibrary

import (
	"fmt"
	"math"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Where a Words value came from: the Home's pinned package declaration, or
// the host's neutral fallback.
const (
	WordsSourcePackage = "package"
	WordsSourceNeutral = "neutral"
)

// Words are what the project card, the Home roster, the map tile facts and the
// status line call things. A Home's pinned package supplies them through its
// `project_card` section; without one the host uses NeutralWords. Ori keeps no
// domain word for these surfaces.
type Words struct {
	Source      string       `json:"source"`
	Noun        string       `json:"noun"`
	NounPlural  string       `json:"noun_plural"`
	RosterTitle string       `json:"roster_title"`
	Facts       []WordsFact  `json:"facts"`
	Fields      []WordsField `json:"fields"`
}

// WordsFact is one declared fact in card order. An empty Facts list means the
// host's neutral facts: fixed order, label-and-value pairs, no unit.
type WordsFact struct {
	Kind    string `json:"kind"`
	Unit    string `json:"unit,omitempty"`
	UnitOne string `json:"unit_one,omitempty"`
}

// WordsField is one plugin-declared field shown on the card under Label.
type WordsField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// NeutralWords is the host's fallback: a complete "Project" card and roster.
func NeutralWords() Words {
	return Words{Source: WordsSourceNeutral, Noun: "Project", NounPlural: "Projects",
		RosterTitle: "Projects and assistants", Facts: []WordsFact{}, Fields: []WordsField{}}
}

// WordsFromDeclaration turns a normalized `project_card` section into Words.
// No section is the neutral words.
func WordsFromDeclaration(card *projecttemplates.ProjectCardDeclaration) Words {
	if card == nil {
		return NeutralWords()
	}
	words := Words{Source: WordsSourcePackage, Noun: card.Noun, NounPlural: card.NounPlural,
		RosterTitle: card.RosterTitle, Facts: make([]WordsFact, 0, len(card.Facts)),
		Fields: make([]WordsField, 0, len(card.Fields))}
	for _, fact := range card.Facts {
		words.Facts = append(words.Facts, WordsFact{Kind: fact.Kind, Unit: fact.Unit, UnitOne: fact.UnitOne})
	}
	for _, field := range card.Fields {
		words.Fields = append(words.Fields, WordsField{ID: field.ID, Label: field.Label, Kind: field.Kind})
	}
	return words
}

// WordsSnapshot is the Home's prompt snapshot of a declaration's words
// (FR9); nil when the declaration has no section.
func WordsSnapshot(pluginID, version string, card *projecttemplates.ProjectCardDeclaration) *workspace.ProjectCardWords {
	if card == nil {
		return nil
	}
	snapshot := &workspace.ProjectCardWords{PluginID: pluginID, Version: version, Noun: card.Noun,
		NounPlural: card.NounPlural, RosterTitle: card.RosterTitle}
	for _, fact := range card.Facts {
		snapshot.Facts = append(snapshot.Facts, workspace.ProjectCardWordsFact{Kind: fact.Kind, Unit: fact.Unit, UnitOne: fact.UnitOne})
	}
	for _, field := range card.Fields {
		snapshot.Fields = append(snapshot.Fields, workspace.ProjectCardWordsField{ID: field.ID, Label: field.Label, Kind: field.Kind})
	}
	return snapshot
}

// SnapshotWords are the words agent prompts and agent tools use: the Home's
// snapshot when it is valid, the neutral words otherwise.
func SnapshotWords(state *workspace.AssistantProgramState) Words {
	if state == nil || !state.ProjectCardWords.Valid() {
		return NeutralWords()
	}
	snapshot := state.ProjectCardWords
	words := Words{Source: WordsSourcePackage, Noun: snapshot.Noun, NounPlural: snapshot.NounPlural,
		RosterTitle: snapshot.RosterTitle, Facts: []WordsFact{}, Fields: []WordsField{}}
	for _, fact := range snapshot.Facts {
		words.Facts = append(words.Facts, WordsFact{Kind: fact.Kind, Unit: fact.Unit, UnitOne: fact.UnitOne})
	}
	for _, field := range snapshot.Fields {
		words.Fields = append(words.Fields, WordsField{ID: field.ID, Label: field.Label, Kind: field.Kind})
	}
	return words
}

// orNeutral fills in the neutral words a caller left out, so a missing value
// reads "Project", never a blank.
func (w Words) orNeutral() Words {
	neutral := NeutralWords()
	if strings.TrimSpace(w.Noun) == "" || strings.TrimSpace(w.NounPlural) == "" || strings.TrimSpace(w.RosterTitle) == "" {
		w.Source, w.Noun, w.NounPlural, w.RosterTitle = neutral.Source, neutral.Noun, neutral.NounPlural, neutral.RosterTitle
	}
	if w.Facts == nil {
		w.Facts = []WordsFact{}
	}
	if w.Fields == nil {
		w.Fields = []WordsField{}
	}
	return w
}

// Field returns the declared field with this id.
func (w Words) Field(id string) (WordsField, bool) {
	for _, field := range w.Fields {
		if field.ID == id {
			return field, true
		}
	}
	return WordsField{}, false
}

// neutralFactOrder and neutralFactLabel are the host's own names for the
// three stored fact kinds, used only when no package words apply.
var neutralFactOrder = []string{projecttemplates.ProjectCardFactTrackCount, projecttemplates.ProjectCardFactTempo, projecttemplates.ProjectCardFactLength}

var neutralFactLabel = map[string]string{
	projecttemplates.ProjectCardFactTrackCount: "Tracks",
	projecttemplates.ProjectCardFactTempo:      "Tempo",
}

// FactsLabel words the facts as the browser's songFactsLabel does
// (library-open.js). With package words: declared order and units, "14
// tracks · 92 BPM · 3:41", "1 track", "92 BPM, varies". With neutral words:
// "Tracks 14 · Tempo 92 · 3:41", "Tempo 92, varies". An unknown part is left
// out; no facts is "".
func FactsLabel(facts *SongFacts, words Words) string {
	if facts == nil {
		return ""
	}
	words = words.orNeutral()
	parts := []string{}
	if len(words.Facts) == 0 {
		for _, kind := range neutralFactOrder {
			if value := factValue(facts, kind); value != "" {
				if label := neutralFactLabel[kind]; label != "" {
					value = label + " " + value
				}
				parts = append(parts, value+factVariesNote(facts, kind))
			}
		}
		return strings.Join(parts, " · ")
	}
	for _, fact := range words.Facts {
		value := factValue(facts, fact.Kind)
		if value == "" {
			continue
		}
		if unit := fact.Unit; unit != "" {
			if fact.UnitOne != "" && value == "1" {
				unit = fact.UnitOne
			}
			value += " " + unit
		}
		parts = append(parts, value+factVariesNote(facts, fact.Kind))
	}
	return strings.Join(parts, " · ")
}

// factValue is one stored fact as a bare number or duration, "" when unknown.
func factValue(facts *SongFacts, kind string) string {
	switch kind {
	case projecttemplates.ProjectCardFactTrackCount:
		if facts.TrackCount > 0 {
			return fmt.Sprintf("%d", facts.TrackCount)
		}
	case projecttemplates.ProjectCardFactTempo:
		if facts.TempoBPM > 0 {
			return strings.TrimSuffix(fmt.Sprintf("%.1f", math.Round(facts.TempoBPM*10)/10), ".0")
		}
	case projecttemplates.ProjectCardFactLength:
		if total := int(math.Round(facts.LengthSeconds)); facts.LengthSeconds > 0 && total > 0 {
			hours, minutes, seconds := total/3600, (total%3600)/60, total%60
			if hours > 0 {
				return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
			}
			return fmt.Sprintf("%d:%02d", minutes, seconds)
		}
	}
	return ""
}

// factVariesNote is the host's note after a tempo that changes over the
// project. It is host wording, never declared.
func factVariesNote(facts *SongFacts, kind string) string {
	if kind == projecttemplates.ProjectCardFactTempo && facts.TempoVaries {
		return ", varies"
	}
	return ""
}
