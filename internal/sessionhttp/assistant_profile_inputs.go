package sessionhttp

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// A Home profile's new-project defaults reach a new project through the
// blueprint inputs that carry the same meaning. The pairing is by input id.
const homeProfileTempoInput = "tempo"

// templateHomeInputDefaults is the destination Home's defaults, as the text
// each blueprint input field shows, and one line saying where they came from.
type templateHomeInputDefaults struct {
	Values map[string]string `json:"values"`
	Note   string            `json:"note"`
}

// homeProfileInputDefaults returns the Home's defaults for the inputs template
// declares. Each value is resolved through the blueprint's own declaration, so
// one that no longer fits (a tempo off the blueprint's step, a time signature
// the blueprint dropped) is left out and the blueprint's default stays. It
// reads the Home's saved profile only: nothing is detected and nothing is
// written, and a Home owned by anyone else yields nothing.
func homeProfileInputDefaults(template projecttemplates.Template, home *workspace.Workspace, ownerUserID string) *templateHomeInputDefaults {
	if template.Inputs == nil || home == nil || strings.TrimSpace(ownerUserID) == "" || home.OwnerUserID != strings.TrimSpace(ownerUserID) {
		return nil
	}
	profile := home.GetAssistantProgramState().GetHomeProfile()
	if profile == nil || profile.Defaults.Empty() {
		return nil
	}
	offered := map[string]json.RawMessage{}
	if tempo := profile.Defaults.TempoBPM; tempo != 0 {
		offered[homeProfileTempoInput] = json.RawMessage(strconv.Itoa(tempo))
	}
	if signature := profile.Defaults.TimeSignature; signature != "" {
		if encoded, err := json.Marshal(signature); err == nil {
			offered[homeProfileTimeSignatureInput] = encoded
		}
	}
	values := make(map[string]string, len(offered))
	for _, field := range template.Inputs.Fields {
		raw, ok := offered[field.ID]
		if !ok {
			continue
		}
		resolved, err := projecttemplates.ResolveInputValues(template.Inputs, map[string]json.RawMessage{field.ID: raw})
		if err != nil || resolved[field.ID] == "" {
			continue
		}
		values[field.ID] = resolved[field.ID]
	}
	if len(values) == 0 {
		return nil
	}
	return &templateHomeInputDefaults{Values: values, Note: homeProfileDefaultsNote(profile.DeclaredBy.Title)}
}

// homeProfileDefaultsNote words the note from the package's own title:
// "Your studio" gives "From your studio defaults".
func homeProfileDefaultsNote(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "From this Home's defaults"
	}
	first, size := utf8.DecodeRuneInString(title)
	return "From " + string(unicode.ToLower(first)) + title[size:] + " defaults"
}
