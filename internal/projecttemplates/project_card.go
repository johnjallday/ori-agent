package projecttemplates

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ProjectCardSchemaVersion = 1
	// ProjectCardMaxFacts is one entry per host-known fact kind.
	ProjectCardMaxFacts = 3
	// ProjectCardMaxFields is the most plugin-declared fields a card shows.
	ProjectCardMaxFields = 8

	projectCardMaxWord        = 40
	projectCardMaxRosterTitle = 60
)

// The three stored facts a project card can show. The host owns how each one
// is read and formatted; a package only chooses which to show, in what order
// and with which unit words.
const (
	ProjectCardFactTempo      = "tempo"
	ProjectCardFactLength     = "length"
	ProjectCardFactTrackCount = "track_count"
)

// The two kinds of plugin-declared field. The host stores the lines and shows
// them under the declared label; it never interprets them.
const (
	ProjectCardFieldText = "text"
	ProjectCardFieldList = "list"
)

// ProjectCardDeclaration is the optional closed `project_card` section of a
// Home declaration: what one project of the Home is called, what the Home's
// roster is called, which stored facts the card shows with which units, and
// any extra plugin-named text fields. It is inert display data, like the rest
// of the declaration: no path, URL, operation, route or command.
type ProjectCardDeclaration struct {
	SchemaVersion int                `json:"schema_version"`
	Noun          string             `json:"noun"`
	NounPlural    string             `json:"noun_plural"`
	RosterTitle   string             `json:"roster_title"`
	Facts         []ProjectCardFact  `json:"facts,omitempty"`
	Fields        []ProjectCardField `json:"fields,omitempty"`
}

// ProjectCardFact is one declared fact, in the order the card shows it.
type ProjectCardFact struct {
	Kind    string `json:"kind"`
	Unit    string `json:"unit,omitempty"`
	UnitOne string `json:"unit_one,omitempty"`
}

// ProjectCardField is one plugin-declared field.
type ProjectCardField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// Clone returns a deep copy.
func (d *ProjectCardDeclaration) Clone() *ProjectCardDeclaration {
	if d == nil {
		return nil
	}
	clone := *d
	clone.Facts = append([]ProjectCardFact(nil), d.Facts...)
	clone.Fields = append([]ProjectCardField(nil), d.Fields...)
	return &clone
}

// Field returns the declared field with this id.
func (d *ProjectCardDeclaration) Field(id string) (ProjectCardField, bool) {
	if d == nil {
		return ProjectCardField{}, false
	}
	for _, field := range d.Fields {
		if field.ID == id {
			return field, true
		}
	}
	return ProjectCardField{}, false
}

func knownProjectCardFact(kind string) bool {
	switch kind {
	case ProjectCardFactTempo, ProjectCardFactLength, ProjectCardFactTrackCount:
		return true
	}
	return false
}

// normalizeProjectCardDeclaration validates and canonicalizes the section in
// place. Unknown keys are already refused by the manifest decoder.
func normalizeProjectCardDeclaration(card *ProjectCardDeclaration) error {
	if card == nil {
		return nil
	}
	invalid := func(what string) error {
		return fmt.Errorf("%w: project_card %s", ErrInvalidAssistantProgram, what)
	}
	if card.SchemaVersion != ProjectCardSchemaVersion {
		return invalid("schema_version must be 1")
	}
	var err error
	if card.Noun, err = projectCardText("noun", card.Noun, projectCardMaxWord); err != nil {
		return err
	}
	if card.NounPlural, err = projectCardText("noun_plural", card.NounPlural, projectCardMaxWord); err != nil {
		return err
	}
	if card.RosterTitle, err = projectCardText("roster_title", card.RosterTitle, projectCardMaxRosterTitle); err != nil {
		return err
	}
	// An empty list is a declaration error, not "use the neutral facts":
	// leaving the key out is how a package asks for those.
	if card.Facts != nil && (len(card.Facts) == 0 || len(card.Facts) > ProjectCardMaxFacts) {
		return invalid("facts needs one to three entries")
	}
	kinds := make(map[string]struct{}, len(card.Facts))
	for index := range card.Facts {
		fact := &card.Facts[index]
		if !knownProjectCardFact(fact.Kind) {
			return invalid(fmt.Sprintf("fact %d has an unknown kind", index))
		}
		if _, duplicate := kinds[fact.Kind]; duplicate {
			return invalid(fmt.Sprintf("fact kind %q is declared twice", fact.Kind))
		}
		kinds[fact.Kind] = struct{}{}
		if fact.Kind == ProjectCardFactLength {
			// Length is printed as m:ss or h:mm:ss; a unit word has no place.
			if fact.Unit != "" || fact.UnitOne != "" {
				return invalid("fact length takes no unit")
			}
			continue
		}
		if fact.Unit, err = projectCardText("unit", fact.Unit, projectCardMaxWord); err != nil {
			return err
		}
		if fact.UnitOne != "" {
			if fact.UnitOne, err = projectCardText("unit_one", fact.UnitOne, projectCardMaxWord); err != nil {
				return err
			}
		}
	}
	if len(card.Fields) > ProjectCardMaxFields {
		return invalid("declares more than eight fields")
	}
	ids := make(map[string]struct{}, len(card.Fields))
	for index := range card.Fields {
		field := &card.Fields[index]
		if field.ID != strings.ToLower(strings.TrimSpace(field.ID)) || !assistantProgramIDPattern.MatchString(field.ID) {
			return invalid(fmt.Sprintf("field %d has an invalid id", index))
		}
		if _, duplicate := ids[field.ID]; duplicate {
			return invalid(fmt.Sprintf("field id %q is duplicated", field.ID))
		}
		ids[field.ID] = struct{}{}
		if field.Kind != ProjectCardFieldText && field.Kind != ProjectCardFieldList {
			return invalid(fmt.Sprintf("field %q has an unknown kind", field.ID))
		}
		if field.Label, err = projectCardText("label", field.Label, projectCardMaxWord); err != nil {
			return err
		}
	}
	return nil
}

// projectCardText accepts one line of plain text of 1 to limit characters,
// with the same rule as home_profile text.
func projectCardText(field, value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	invalid := func(what string) error {
		return fmt.Errorf("%w: project_card %s %s", ErrInvalidAssistantProgram, field, what)
	}
	if value == "" {
		return "", invalid("is required")
	}
	if !utf8.ValidString(value) {
		return "", invalid("is not valid UTF-8")
	}
	if utf8.RuneCountInString(value) > limit {
		return "", invalid(fmt.Sprintf("exceeds %d characters", limit))
	}
	if strings.Contains(value, "://") || strings.ContainsFunc(value, unicode.IsControl) {
		return "", invalid("contains a URL or a control character")
	}
	return value, nil
}
