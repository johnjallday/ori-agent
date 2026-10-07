package projecttemplates

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	HomeProfileSchemaVersion = 1
	// HomeProfileMaxFields is one row per host-known kind.
	HomeProfileMaxFields = 4

	homeProfileMaxTitle = 60
	homeProfileMaxIntro = 240
	homeProfileMaxLabel = 40
)

// The four kinds of row a Home profile card can have. The host owns what each
// one stores, detects and renders; a package only chooses which to show, in
// what order and under which words.
const (
	HomeProfileKindApps      = "apps"
	HomeProfileKindMainApp   = "main_app"
	HomeProfileKindTemplates = "templates"
	HomeProfileKindDefaults  = "defaults"
)

// HomeProfileDeclaration is the optional closed `home_profile` section of a
// Home declaration: the title, introduction and row labels of the Home's
// profile card. It is inert display data, like the rest of the declaration: no
// path, URL, operation, route or command.
type HomeProfileDeclaration struct {
	SchemaVersion int                `json:"schema_version"`
	Title         string             `json:"title"`
	Intro         string             `json:"intro"`
	Fields        []HomeProfileField `json:"fields"`
}

// HomeProfileField is one declared row.
type HomeProfileField struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// Clone returns a deep copy.
func (d *HomeProfileDeclaration) Clone() *HomeProfileDeclaration {
	if d == nil {
		return nil
	}
	clone := *d
	clone.Fields = append([]HomeProfileField(nil), d.Fields...)
	return &clone
}

// Declares reports whether the card has a row of this kind.
func (d *HomeProfileDeclaration) Declares(kind string) bool {
	if d == nil {
		return false
	}
	for _, field := range d.Fields {
		if field.Kind == kind {
			return true
		}
	}
	return false
}

// Label returns the declared label of the row of this kind, or "".
func (d *HomeProfileDeclaration) Label(kind string) string {
	if d == nil {
		return ""
	}
	for _, field := range d.Fields {
		if field.Kind == kind {
			return field.Label
		}
	}
	return ""
}

func knownHomeProfileKind(kind string) bool {
	switch kind {
	case HomeProfileKindApps, HomeProfileKindMainApp, HomeProfileKindTemplates, HomeProfileKindDefaults:
		return true
	}
	return false
}

// normalizeHomeProfileDeclaration validates and canonicalizes the section in
// place. Unknown keys are already refused by the manifest decoder.
func normalizeHomeProfileDeclaration(profile *HomeProfileDeclaration) error {
	if profile == nil {
		return nil
	}
	invalid := func(what string) error {
		return fmt.Errorf("%w: home_profile %s", ErrInvalidAssistantProgram, what)
	}
	if profile.SchemaVersion != HomeProfileSchemaVersion {
		return invalid("schema_version must be 1")
	}
	var err error
	if profile.Title, err = homeProfileText("title", profile.Title, homeProfileMaxTitle); err != nil {
		return err
	}
	if profile.Intro, err = homeProfileText("intro", profile.Intro, homeProfileMaxIntro); err != nil {
		return err
	}
	if len(profile.Fields) == 0 || len(profile.Fields) > HomeProfileMaxFields {
		return invalid("needs one to four fields")
	}
	ids := make(map[string]struct{}, len(profile.Fields))
	kinds := make(map[string]struct{}, len(profile.Fields))
	for index := range profile.Fields {
		field := &profile.Fields[index]
		if field.ID != strings.ToLower(strings.TrimSpace(field.ID)) || !assistantProgramIDPattern.MatchString(field.ID) {
			return invalid(fmt.Sprintf("field %d has an invalid id", index))
		}
		if _, duplicate := ids[field.ID]; duplicate {
			return invalid(fmt.Sprintf("field id %q is duplicated", field.ID))
		}
		ids[field.ID] = struct{}{}
		if !knownHomeProfileKind(field.Kind) {
			return invalid(fmt.Sprintf("field %q has an unknown kind", field.ID))
		}
		if _, duplicate := kinds[field.Kind]; duplicate {
			return invalid(fmt.Sprintf("kind %q is declared twice", field.Kind))
		}
		kinds[field.Kind] = struct{}{}
		if field.Label, err = homeProfileText("label", field.Label, homeProfileMaxLabel); err != nil {
			return err
		}
	}
	return nil
}

// homeProfileText accepts one line of plain text of 1 to limit characters.
func homeProfileText(field, value string, limit int) (string, error) {
	value = strings.TrimSpace(value)
	invalid := func(what string) error {
		return fmt.Errorf("%w: home_profile %s %s", ErrInvalidAssistantProgram, field, what)
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
