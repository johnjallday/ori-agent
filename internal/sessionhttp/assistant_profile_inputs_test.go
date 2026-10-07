package sessionhttp

import (
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func inputDefaultsTemplate() projecttemplates.Template {
	return projecttemplates.Template{ID: "reaper-song", Inputs: &projecttemplates.InputsDeclaration{Fields: []projecttemplates.InputField{
		{ID: "tempo", Label: "Tempo", Type: projecttemplates.InputFieldNumber, Min: 40, Max: 200, Step: 1, Default: float64(120)},
		{ID: "time_signature", Label: "Time signature", Type: projecttemplates.InputFieldSelect, Default: "4 4",
			Options: []projecttemplates.InputOption{{Value: "4 4", Label: "4/4"}, {Value: "3 4", Label: "3/4"}}},
	}}}
}

func inputDefaultsHome(defaults *workspace.HomeProfileDefaults) *workspace.Workspace {
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	home := &workspace.Workspace{ID: "music-home", OwnerUserID: "local", Status: workspace.StatusActive}
	state := &workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion,
		Key:           workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: "music-project-management", ProgramID: "music-producer-assistant"},
	}
	if defaults != nil {
		defaults.Source, defaults.ConfirmedAt = workspace.HomeProfileSourceOwner, &at
		state.HomeProfile = &workspace.HomeProfile{SchemaVersion: workspace.HomeProfileSchemaVersion, Revision: 1,
			DeclaredBy: workspace.HomeProfileDeclaredBy{PluginID: "music-project-management", Version: "0.2.0", Title: "Your studio"},
			Defaults:   defaults}
	}
	home.SetAssistantProgramState(state)
	return home
}

func TestHomeProfileInputDefaultsOffersOnlyValuesTheBlueprintAccepts(t *testing.T) {
	template := inputDefaultsTemplate()
	tests := map[string]struct {
		defaults *workspace.HomeProfileDefaults
		want     map[string]string
	}{
		"both":                 {&workspace.HomeProfileDefaults{TempoBPM: 96, TimeSignature: "3 4"}, map[string]string{"tempo": "96", "time_signature": "3 4"}},
		"tempo only":           {&workspace.HomeProfileDefaults{TempoBPM: 92}, map[string]string{"tempo": "92"}},
		"time signature only":  {&workspace.HomeProfileDefaults{TimeSignature: "3 4", SampleRateHz: 48000}, map[string]string{"time_signature": "3 4"}},
		"tempo above the max":  {&workspace.HomeProfileDefaults{TempoBPM: 220, TimeSignature: "3 4"}, map[string]string{"time_signature": "3 4"}},
		"meter it dropped":     {&workspace.HomeProfileDefaults{TempoBPM: 96, TimeSignature: "6 8"}, map[string]string{"tempo": "96"}},
		"nothing it can use":   {&workspace.HomeProfileDefaults{SampleRateHz: 48000, BitDepth: 24}, nil},
		"a Home with no value": {nil, nil},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := homeProfileInputDefaults(template, inputDefaultsHome(tc.defaults), "local")
			if tc.want == nil {
				if got != nil {
					t.Fatalf("defaults = %+v, want none", got)
				}
				return
			}
			if got == nil || !reflect.DeepEqual(got.Values, tc.want) || got.Note != "From your studio defaults" {
				t.Fatalf("defaults = %+v, want %v", got, tc.want)
			}
		})
	}
}

func TestHomeProfileInputDefaultsNeedsTheOwnersHomeAndDeclaredInputs(t *testing.T) {
	home := inputDefaultsHome(&workspace.HomeProfileDefaults{TempoBPM: 96})
	if got := homeProfileInputDefaults(inputDefaultsTemplate(), home, "someone-else"); got != nil {
		t.Fatalf("another user read the Home's defaults: %+v", got)
	}
	if got := homeProfileInputDefaults(inputDefaultsTemplate(), home, ""); got != nil {
		t.Fatalf("nobody read the Home's defaults: %+v", got)
	}
	if got := homeProfileInputDefaults(projecttemplates.Template{ID: "writing-project"}, home, "local"); got != nil {
		t.Fatalf("a blueprint without inputs got defaults: %+v", got)
	}
	// An input of the same name but another type is not filled.
	other := inputDefaultsTemplate()
	other.Inputs.Fields[0] = projecttemplates.InputField{ID: "tempo", Label: "Tempo", Type: projecttemplates.InputFieldSelect, Default: "slow",
		Options: []projecttemplates.InputOption{{Value: "slow", Label: "Slow"}, {Value: "fast", Label: "Fast"}}}
	if got := homeProfileInputDefaults(other, home, "local"); got != nil {
		t.Fatalf("a select named tempo took a number: %+v", got)
	}
	if got := homeProfileInputDefaults(inputDefaultsTemplate(), nil, "local"); got != nil {
		t.Fatalf("no Home gave defaults: %+v", got)
	}
}

func TestHomeProfileDefaultsNoteUsesThePackagesTitle(t *testing.T) {
	for title, want := range map[string]string{
		"Your studio": "From your studio defaults",
		"  Lab  ":     "From lab defaults",
		"":            "From this Home's defaults",
	} {
		if got := homeProfileDefaultsNote(title); got != want {
			t.Errorf("note(%q) = %q, want %q", title, got, want)
		}
	}
}
