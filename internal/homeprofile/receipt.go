package homeprofile

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// ReceiptSummary is the one quiet line a finished setup shows about a Home's
// profile: the card's title and what the profile holds now ("<application> ·
// 4 templates", or "<one> and <another> · pick your <main row's label>"). ok
// is false when the Home has no profile or nothing was found.
func ReceiptSummary(profile *workspace.HomeProfile) (title, detail string, ok bool) {
	if profile == nil || profile.Validate() != nil {
		return "", "", false
	}
	title = strings.TrimSpace(profile.DeclaredBy.Title)
	if title == "" {
		title = "Home profile"
	}
	visible := profile.VisibleApps()
	if len(visible) == 0 {
		return "", "", false
	}
	if main := profile.MainApp; main != nil {
		app, _ := profile.App(main.ID)
		detail = app.Name
	} else {
		names := make([]string, 0, len(visible))
		for _, app := range visible {
			names = append(names, app.Name)
		}
		detail = joinNames(names)
		if len(names) > 1 {
			label := strings.TrimSpace(profile.DeclaredBy.Label(workspace.HomeProfileKindMainApp, "Main application"))
			first, size := utf8.DecodeRuneInString(label)
			detail += " · pick your " + string(unicode.ToLower(first)) + label[size:]
		}
	}
	if templates := profile.Templates; templates != nil && templates.Consent.Active() && len(templates.Items) > 0 {
		noun := "templates"
		if len(templates.Items) == 1 {
			noun = "template"
		}
		detail += fmt.Sprintf(" · %d %s", len(templates.Items), noun)
	}
	return title, detail, true
}
