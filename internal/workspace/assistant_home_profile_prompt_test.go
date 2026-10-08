package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func promptProfile() *HomeProfile {
	profile := homeProfileFixture()
	profile.DeclaredBy.Labels = map[string]string{
		HomeProfileKindApps: "DAWs on this Mac", HomeProfileKindMainApp: "Main DAW",
		HomeProfileKindTemplates: "Project templates", HomeProfileKindDefaults: "New-song defaults",
	}
	read := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	profile.Templates.ReadAt = &read
	profile.Templates.Items = []HomeProfileTemplate{
		{Name: "Band Session", Kind: HomeProfileTemplateProject, File: "Band Session.RPP"},
		{Name: "Vocal Comp", Kind: HomeProfileTemplateProject, File: "Vocal Comp.RPP"},
		{Name: "Drum Bus", Kind: HomeProfileTemplateTrack, File: "Drum Bus.RTrackTemplate"},
	}
	return profile
}

func TestRenderHomeProfilePromptLinesMatchesThePRDExample(t *testing.T) {
	got := strings.Join(RenderHomeProfilePromptLines(promptProfile()), "\n")
	want := strings.Join([]string{
		"## Your studio",
		"- Main DAW: REAPER 7.28 (confirmed by the owner)",
		"- Also found: Logic Pro (detected, not confirmed)",
		"- REAPER templates (read 2026-10-07): project: Band Session, Vocal Comp; track: Drum Bus",
		"- New-song defaults: 120 BPM, 4/4, 48 kHz, 24-bit (set by the owner)",
		`- Detected values are hints. Confirmed values are the owner's instructions. Do not ask the owner for "Main DAW" when it is confirmed. Never claim a template was applied or a file was read from this list.`,
	}, "\n")
	if got != want {
		t.Fatalf("prompt section:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestRenderHomeProfilePromptLinesSaysWhereTheMainAppCameFrom(t *testing.T) {
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		main *HomeProfileMainApp
		want string
	}{
		"detected":  {&HomeProfileMainApp{ID: "reaper", Source: HomeProfileSourceDetected, Reason: HomeProfileMainAppOnly}, "- Main DAW: REAPER 7.28 (detected, not confirmed)"},
		"confirmed": {&HomeProfileMainApp{ID: "reaper", Source: HomeProfileSourceDetected, Reason: HomeProfileMainAppLibrary, ConfirmedAt: &at}, "- Main DAW: REAPER 7.28 (confirmed by the owner)"},
		"chosen":    {&HomeProfileMainApp{ID: "logic-pro", Source: HomeProfileSourceOwner, ConfirmedAt: &at}, "- Main DAW: Logic Pro (chosen by the owner)"},
		"unset":     {nil, "- Main DAW: not chosen; REAPER 7.28 and Logic Pro were found."},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			profile := promptProfile()
			profile.MainApp = tc.main
			lines := RenderHomeProfilePromptLines(profile)
			if len(lines) < 2 || lines[1] != tc.want {
				t.Fatalf("lines = %q, want second line %q", lines, tc.want)
			}
			if tc.main == nil && strings.Contains(strings.Join(lines, "\n"), "Also found") {
				t.Fatalf("an unset main app repeated the list: %q", lines)
			}
		})
	}
	// One application and no pick reads in the singular.
	profile := promptProfile()
	profile.MainApp, profile.Templates = nil, nil
	profile.Apps = profile.Apps[1:]
	if lines := RenderHomeProfilePromptLines(profile); lines[1] != "- Main DAW: not chosen; Logic Pro was found." {
		t.Fatalf("lines = %q", lines)
	}
}

func TestRenderHomeProfilePromptLinesOmitsHiddenApps(t *testing.T) {
	profile := promptProfile()
	profile.Apps[1].Hidden = true
	text := strings.Join(RenderHomeProfilePromptLines(profile), "\n")
	if strings.Contains(text, "Logic Pro") || strings.Contains(text, "Also found") {
		t.Fatalf("a hidden app reached the agents:\n%s", text)
	}
	// With nothing chosen, a hidden app is not offered as found either.
	profile.MainApp, profile.Templates = nil, nil
	if text = strings.Join(RenderHomeProfilePromptLines(profile), "\n"); strings.Contains(text, "Logic Pro") ||
		!strings.Contains(text, "- Main DAW: not chosen; REAPER 7.28 was found.") {
		t.Fatalf("section:\n%s", text)
	}
}

func TestRenderHomeProfilePromptLinesCapsTemplateNames(t *testing.T) {
	profile := promptProfile()
	profile.Templates.Items = nil
	for i := 0; i < 30; i++ {
		kind, file := HomeProfileTemplateProject, fmt.Sprintf("Session %02d.RPP", i)
		if i >= 20 {
			kind, file = HomeProfileTemplateTrack, fmt.Sprintf("Track %02d.RTrackTemplate", i)
		}
		profile.Templates.Items = append(profile.Templates.Items, HomeProfileTemplate{Name: fmt.Sprintf("Template %02d", i), Kind: kind, File: file})
	}
	var line string
	for _, candidate := range RenderHomeProfilePromptLines(profile) {
		if strings.Contains(candidate, "templates (read") {
			line = candidate
		}
	}
	if !strings.HasSuffix(line, "; and 6 more") || strings.Count(line, "Template ") != 24 ||
		!strings.Contains(line, "project: Template 00") || !strings.Contains(line, "track: Template 20, Template 21, Template 22, Template 23;") {
		t.Fatalf("templates line = %q", line)
	}
	// A folder with more than the plugin lists says so without inventing a count.
	profile.Templates.Items = profile.Templates.Items[:2]
	profile.Templates.Truncated = true
	for _, candidate := range RenderHomeProfilePromptLines(profile) {
		if strings.Contains(candidate, "templates (read") && !strings.HasSuffix(candidate, "; and more that were not listed") {
			t.Fatalf("truncated templates line = %q", candidate)
		}
	}
}

func TestRenderHomeProfilePromptLinesDropsTemplatesWithoutAnActiveConsent(t *testing.T) {
	profile := promptProfile()
	revoked := profile.Templates.ReadAt.Add(time.Hour)
	profile.Templates = &HomeProfileTemplates{Consent: &HomeProfileTemplatesConsent{
		GrantedAt: profile.Templates.Consent.GrantedAt, Source: HomeProfileTemplatesHomeReview, RevokedAt: &revoked}}
	if text := strings.Join(RenderHomeProfilePromptLines(profile), "\n"); strings.Contains(text, "templates (read") {
		t.Fatalf("forgotten templates reached the agents:\n%s", text)
	}
}

func TestRenderHomeProfilePromptLinesKeepsEachNameOnOneLine(t *testing.T) {
	profile := promptProfile()
	// Stored names are validated as single lines; the renderer still collapses
	// whitespace and bounds length so a name can never forge a prompt line.
	profile.Templates.Items[0].Name = "Long  " + strings.Repeat("N", 114)
	lines := RenderHomeProfilePromptLines(profile)
	if len(lines) == 0 {
		t.Fatal("a valid profile rendered nothing")
	}
	for _, line := range lines {
		if strings.ContainsAny(line, "\r\n") {
			t.Fatalf("a line carries a line break: %q", line)
		}
	}
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "…") {
		t.Fatalf("an over-long name was not shortened:\n%s", joined)
	}
}

func TestRenderHomeProfilePromptLinesHasNothingToSayForAnEmptyOrInvalidProfile(t *testing.T) {
	if lines := RenderHomeProfilePromptLines(nil); lines != nil {
		t.Fatalf("nil profile rendered %q", lines)
	}
	empty := &HomeProfile{SchemaVersion: HomeProfileSchemaVersion, Revision: 1,
		DeclaredBy: HomeProfileDeclaredBy{PluginID: "music-project-management", Version: "0.2.0", Title: "Your studio"}}
	if lines := RenderHomeProfilePromptLines(empty); lines != nil {
		t.Fatalf("an empty profile rendered %q", lines)
	}
	invalid := promptProfile()
	invalid.Revision = 0
	if lines := RenderHomeProfilePromptLines(invalid); lines != nil {
		t.Fatalf("an invalid profile rendered %q", lines)
	}
}

func TestRenderHomeProfilePromptLinesFallsBackToHostWordsWithoutLabels(t *testing.T) {
	profile := promptProfile()
	profile.DeclaredBy.Labels, profile.DeclaredBy.Title = nil, ""
	text := strings.Join(RenderHomeProfilePromptLines(profile), "\n")
	for _, want := range []string{"## Home profile", "- Main application: REAPER 7.28", "- New-project defaults: 120 BPM"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}

func TestRenderHomeProfilePromptLinesFormatsEachDefault(t *testing.T) {
	profile := promptProfile()
	profile.Defaults.TempoBPM, profile.Defaults.TimeSignature, profile.Defaults.SampleRateHz, profile.Defaults.BitDepth = 0, "6 8", 44100, 0
	text := strings.Join(RenderHomeProfilePromptLines(profile), "\n")
	if !strings.Contains(text, "- New-song defaults: 6/8, 44.1 kHz (set by the owner)") {
		t.Fatalf("defaults line:\n%s", text)
	}
}

// The task path reaches a linked song's agents through the same station.
func TestRenderAssistantProgramPromptSectionCarriesTheHomeProfile(t *testing.T) {
	station := &Workspace{ID: "music-home", Name: "Music Home"}
	station.SetAssistantProgramState(&AssistantProgramState{
		SchemaVersion: AssistantProgramStateSchemaVersion, PluginAvailable: true,
		Key:         AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
		Declaration: &AssistantProgramDeclaration{SchemaVersion: AssistantProgramSchemaVersion, ID: "music-producer-assistant"},
		HomeProfile: promptProfile(),
	})
	song := &Workspace{ID: "song", Name: "Neon Song"}
	for name, current := range map[string]*Workspace{"home": station, "song": song} {
		section := RenderAssistantProgramPromptSection(current, station)
		if !strings.Contains(section, "## Assistant Program Context") || !strings.Contains(section, "\n## Your studio\n- Main DAW: REAPER 7.28 (confirmed by the owner)\n") {
			t.Fatalf("%s task context:\n%s", name, section)
		}
	}
	state := station.GetAssistantProgramState()
	state.HomeProfile = nil
	station.SetAssistantProgramState(state)
	if section := RenderAssistantProgramPromptSection(song, station); strings.Contains(section, "Your studio") {
		t.Fatalf("a Home without a profile rendered one:\n%s", section)
	}
}
