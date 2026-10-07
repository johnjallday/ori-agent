package workspace

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// homeProfilePromptTemplates caps how many template names one context
	// block carries; the rest is a count.
	homeProfilePromptTemplates = 24
	homeProfilePromptName      = 80
)

// RenderHomeProfilePromptLines formats a Home's profile for an agent's context
// block, headed by the title its package declared. It returns nil when there is
// nothing to say. The lines say where each value came from: a detected value is
// a hint, a confirmed one is the owner's instruction. Applications the owner
// hid are never mentioned. The row words come from the package's declaration,
// so nothing here names an application or a trade.
func RenderHomeProfilePromptLines(profile *HomeProfile) []string {
	if profile == nil || profile.Validate() != nil {
		return nil
	}
	mainLabel := profile.DeclaredBy.Label(HomeProfileKindMainApp, "Main application")
	visible := profile.VisibleApps()
	var lines []string

	main := profile.MainApp
	if main != nil {
		if app, ok := profile.App(main.ID); ok {
			lines = append(lines, fmt.Sprintf("- %s: %s (%s)", homeProfilePromptText(mainLabel),
				homeProfilePromptApp(app), homeProfileMainAppSource(main)))
		}
		var others []string
		for _, app := range visible {
			if app.ID == main.ID {
				continue
			}
			others = append(others, fmt.Sprintf("%s (%s)", homeProfilePromptApp(app), homeProfileAppSource(app)))
		}
		if len(others) > 0 {
			lines = append(lines, "- Also found: "+strings.Join(others, ", "))
		}
	} else if len(visible) > 0 {
		names := make([]string, 0, len(visible))
		for _, app := range visible {
			names = append(names, homeProfilePromptApp(app))
		}
		verb := "were"
		if len(names) == 1 {
			verb = "was"
		}
		lines = append(lines, fmt.Sprintf("- %s: not chosen; %s %s found.", homeProfilePromptText(mainLabel), joinHomeProfileNames(names), verb))
	}

	if line := homeProfileTemplatesLine(profile); line != "" {
		lines = append(lines, line)
	}
	if defaults := profile.Defaults; !defaults.Empty() {
		lines = append(lines, fmt.Sprintf("- %s: %s (set by the owner)",
			homeProfilePromptText(profile.DeclaredBy.Label(HomeProfileKindDefaults, "New-project defaults")),
			strings.Join(homeProfileDefaultParts(defaults), ", ")))
	}
	if len(lines) == 0 {
		return nil
	}
	title := homeProfilePromptText(profile.DeclaredBy.Title)
	if title == "" {
		title = "Home profile"
	}
	lines = append(lines, fmt.Sprintf("- Detected values are hints. Confirmed values are the owner's instructions. "+
		"Do not ask the owner for %q when it is confirmed. Never claim a template was applied or a file was read from this list.",
		homeProfilePromptText(mainLabel)))
	return append([]string{"## " + title}, lines...)
}

// HomeProfileFacts is the same content as the context block, as data, for a
// Manager turn whose block was trimmed. It carries no path, no hidden
// application and no receipt.
type HomeProfileFacts struct {
	Title     string                    `json:"title"`
	MainApp   *HomeProfileFactApp       `json:"main_app"`
	OtherApps []HomeProfileFactApp      `json:"other_apps,omitempty"`
	Templates *HomeProfileFactTemplates `json:"templates,omitempty"`
	Defaults  *HomeProfileFactDefaults  `json:"defaults,omitempty"`
	// Note is the rule that goes with every value.
	Note string `json:"note"`
}

// HomeProfileFactApp is one application and where the value came from.
type HomeProfileFactApp struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source"`
}

// HomeProfileFactTemplates lists template names by kind.
type HomeProfileFactTemplates struct {
	App     string   `json:"app"`
	ReadOn  string   `json:"read_on"`
	Project []string `json:"project,omitempty"`
	Track   []string `json:"track,omitempty"`
	// More counts listed templates left out of this answer; MoreUnlisted is
	// true when the folders hold more than were ever listed.
	More         int  `json:"more,omitempty"`
	MoreUnlisted bool `json:"more_unlisted,omitempty"`
}

// HomeProfileFactDefaults are the owner's new-project defaults.
type HomeProfileFactDefaults struct {
	TempoBPM      int    `json:"tempo_bpm,omitempty"`
	TimeSignature string `json:"time_signature,omitempty"`
	SampleRateHz  int    `json:"sample_rate_hz,omitempty"`
	BitDepth      int    `json:"bit_depth,omitempty"`
	Source        string `json:"source"`
}

const homeProfileFactsNote = "Detected values are hints. Confirmed values are the owner's instructions. " +
	"Never claim a template was applied or a file was read from this list."

// BuildHomeProfileFacts returns the profile's facts, or nil when the Home has
// no valid profile.
func BuildHomeProfileFacts(profile *HomeProfile) *HomeProfileFacts {
	if profile == nil || profile.Validate() != nil {
		return nil
	}
	facts := &HomeProfileFacts{Title: homeProfilePromptText(profile.DeclaredBy.Title), Note: homeProfileFactsNote}
	mainID := ""
	if main := profile.MainApp; main != nil {
		if app, ok := profile.App(main.ID); ok {
			mainID = app.ID
			facts.MainApp = &HomeProfileFactApp{Name: homeProfilePromptText(app.Name), Version: homeProfilePromptText(app.Version),
				Source: homeProfileMainAppSource(main)}
		}
	}
	for _, app := range profile.VisibleApps() {
		if app.ID == mainID {
			continue
		}
		facts.OtherApps = append(facts.OtherApps, HomeProfileFactApp{Name: homeProfilePromptText(app.Name),
			Version: homeProfilePromptText(app.Version), Source: homeProfileAppSource(app)})
	}
	if templates := profile.Templates; templates != nil && len(templates.Items) > 0 && templates.Consent.Active() && templates.ReadAt != nil {
		if app, ok := profile.App(templates.AppID); ok && !app.Hidden {
			listed := &HomeProfileFactTemplates{App: homeProfilePromptText(app.Name),
				ReadOn: templates.ReadAt.UTC().Format("2006-01-02"), MoreUnlisted: templates.Truncated}
			for index, item := range templates.Items {
				if index >= homeProfilePromptTemplates {
					listed.More = len(templates.Items) - index
					break
				}
				if item.Kind == HomeProfileTemplateTrack {
					listed.Track = append(listed.Track, homeProfilePromptText(item.Name))
				} else {
					listed.Project = append(listed.Project, homeProfilePromptText(item.Name))
				}
			}
			facts.Templates = listed
		}
	}
	if defaults := profile.Defaults; !defaults.Empty() {
		facts.Defaults = &HomeProfileFactDefaults{TempoBPM: defaults.TempoBPM,
			TimeSignature: strings.Join(strings.Fields(homeProfilePromptText(defaults.TimeSignature)), "/"),
			SampleRateHz:  defaults.SampleRateHz, BitDepth: defaults.BitDepth, Source: "set by the owner"}
	}
	return facts
}

func homeProfileMainAppSource(main *HomeProfileMainApp) string {
	switch {
	case main.Source == HomeProfileSourceOwner:
		return "chosen by the owner"
	case main.ConfirmedAt != nil:
		return "confirmed by the owner"
	default:
		return "detected, not confirmed"
	}
}

func homeProfileAppSource(app HomeProfileApp) string {
	source := "detected, not confirmed"
	if app.ConfirmedAt != nil {
		source = "confirmed by the owner"
	}
	if !app.Detected {
		source += ", not found now"
	}
	return source
}

func homeProfilePromptApp(app HomeProfileApp) string {
	name := homeProfilePromptText(app.Name)
	if version := homeProfilePromptText(app.Version); version != "" {
		name += " " + version
	}
	return name
}

// homeProfileTemplatesLine lists template names by kind under an active
// consent: at most homeProfilePromptTemplates names, then a count.
func homeProfileTemplatesLine(profile *HomeProfile) string {
	templates := profile.Templates
	if templates == nil || len(templates.Items) == 0 || !templates.Consent.Active() || templates.ReadAt == nil {
		return ""
	}
	app, ok := profile.App(templates.AppID)
	if !ok || app.Hidden {
		return ""
	}
	groups := []struct {
		kind  string
		names []string
	}{{kind: HomeProfileTemplateProject}, {kind: HomeProfileTemplateTrack}}
	shown := 0
	for _, item := range templates.Items {
		if shown == homeProfilePromptTemplates {
			break
		}
		for index := range groups {
			if groups[index].kind == item.Kind {
				groups[index].names = append(groups[index].names, homeProfilePromptText(item.Name))
				shown++
			}
		}
	}
	var parts []string
	for _, group := range groups {
		if len(group.names) > 0 {
			parts = append(parts, group.kind+": "+strings.Join(group.names, ", "))
		}
	}
	line := fmt.Sprintf("- %s templates (read %s): %s", homeProfilePromptText(app.Name),
		templates.ReadAt.UTC().Format("2006-01-02"), strings.Join(parts, "; "))
	if more := len(templates.Items) - shown; more > 0 {
		line += fmt.Sprintf("; and %d more", more)
	} else if templates.Truncated {
		line += "; and more that were not listed"
	}
	return line
}

func homeProfileDefaultParts(defaults *HomeProfileDefaults) []string {
	var parts []string
	if defaults.TempoBPM != 0 {
		parts = append(parts, strconv.Itoa(defaults.TempoBPM)+" BPM")
	}
	if defaults.TimeSignature != "" {
		parts = append(parts, strings.Join(strings.Fields(homeProfilePromptText(defaults.TimeSignature)), "/"))
	}
	if defaults.SampleRateHz != 0 {
		parts = append(parts, strconv.FormatFloat(float64(defaults.SampleRateHz)/1000, 'f', -1, 64)+" kHz")
	}
	if defaults.BitDepth != 0 {
		parts = append(parts, strconv.Itoa(defaults.BitDepth)+"-bit")
	}
	return parts
}

// homeProfilePromptText keeps one bounded line: whitespace collapsed, so a
// stored name can never start a new prompt line.
func homeProfilePromptText(value string) string {
	cleaned := strings.Join(strings.Fields(value), " ")
	if runes := []rune(cleaned); len(runes) > homeProfilePromptName {
		return string(runes[:homeProfilePromptName-1]) + "…"
	}
	return cleaned
}

func joinHomeProfileNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
