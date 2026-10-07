package plugin

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// homeProfileContributionJSON is the Music candidate fixture with the profile
// section a 0.2.0 release declares.
func homeProfileContributionJSON(t *testing.T, mutate func(root, home, profile map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(independentHomeContributionJSON(t), &root); err != nil {
		t.Fatal(err)
	}
	root["version"] = "0.2.0"
	root["requires_host_features"] = []any{"independent_program_homes_v1", "home_profile_v1"}
	profile := map[string]any{
		"schema_version": float64(1),
		"title":          "Your studio",
		"intro":          "What Ori knows about where you make music. Detected values are hints until you confirm them.",
		"fields": []any{
			map[string]any{"id": "apps", "kind": "apps", "label": "DAWs on this Mac"},
			map[string]any{"id": "main_app", "kind": "main_app", "label": "Main DAW"},
			map[string]any{"id": "templates", "kind": "templates", "label": "Project templates"},
			map[string]any{"id": "defaults", "kind": "defaults", "label": "New-song defaults"},
		},
	}
	home := root["assistant_program_homes"].([]any)[0].(map[string]any)
	home["home_profile"] = profile
	if mutate != nil {
		mutate(root, home, profile)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestHomeProfileContributionIsAccepted(t *testing.T) {
	contribution, err := ParseSurfaceContribution(homeProfileContributionJSON(t, nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	profile := contribution.AssistantProgramHomes[0].HomeProfile
	if profile == nil || profile.Title != "Your studio" || len(profile.Fields) != 4 ||
		profile.Fields[2].Kind != projecttemplates.HomeProfileKindTemplates {
		t.Fatalf("profile = %+v", profile)
	}
	if !slices.Contains(HostFeatures(), HostFeatureHomeProfileV1) || !HostSupportsFeatures(contribution.RequiresHostFeatures) {
		t.Fatal("this build does not advertise home_profile_v1")
	}
}

func TestHomeProfileContributionFailsClosed(t *testing.T) {
	tests := map[string]func(root, home, profile map[string]any){
		"feature omitted": func(root, _, _ map[string]any) {
			root["requires_host_features"] = []any{"independent_program_homes_v1"}
		},
		"unknown section key": func(_, _, profile map[string]any) { profile["operation"] = "profile.read" },
		"unknown field key": func(_, _, profile map[string]any) {
			profile["fields"].([]any)[0].(map[string]any)["path"] = "/Applications"
		},
		"unknown kind": func(_, _, profile map[string]any) {
			profile["fields"].([]any)[0].(map[string]any)["kind"] = "hardware"
		},
		"duplicate kind": func(_, _, profile map[string]any) {
			profile["fields"].([]any)[1].(map[string]any)["kind"] = "apps"
		},
		"too many fields": func(_, _, profile map[string]any) {
			profile["fields"] = append(profile["fields"].([]any), map[string]any{"id": "more", "kind": "apps", "label": "More"})
		},
		"empty title": func(_, _, profile map[string]any) { profile["title"] = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSurfaceContribution(homeProfileContributionJSON(t, mutate))
			var contributionErr *ContributionError
			if !errors.As(err, &contributionErr) {
				t.Fatalf("err = %v, want a rejected contribution", err)
			}
		})
	}
}

// An Ori build from before the feature never registers the package partially:
// the unknown host feature refuses the whole contribution.
func TestHomeProfileContributionIsRefusedByAnOlderHost(t *testing.T) {
	contribution, err := ParseSurfaceContribution(homeProfileContributionJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	older := slices.DeleteFunc(HostFeatures(), func(feature string) bool { return feature == HostFeatureHomeProfileV1 })
	err = contribution.ValidateForHost(SurfaceProtocolVersion, older)
	var contributionErr *ContributionError
	if !errors.As(err, &contributionErr) || contributionErr.Code != CodeHostFeatureUnsupported {
		t.Fatalf("err = %v, want host_feature_unsupported", err)
	}
}

// The card's words come from the installed declaration a Home is pinned to,
// byte for byte, even while the package is switched off. They never come from
// another release or another package of the same name.
func TestPinnedHomeDeclarationReturnsExactlyThePinnedRelease(t *testing.T) {
	contribution, err := ParseSurfaceContribution(homeProfileContributionJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	home := contribution.AssistantProgramHomes[0]
	installed := InstalledPlugin{Name: "music-project-management", Version: "0.2.0", Enabled: true,
		ContentGeneration: 3, ComponentFingerprint: strings.Repeat("a", 64), WorkspaceSurfaces: contribution}
	pin := workspace.AssistantProgramHomeOwner{
		PluginID: installed.Name, PluginVersion: installed.Version, ProgramID: home.ID,
		HomeSchemaVersion: home.SchemaVersion, HomeVersion: home.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
		PluginGeneration:  installed.EvidenceGeneration(), ComponentFingerprint: installed.ComponentFingerprint,
	}
	owner, declared, ok := PinnedHomeDeclaration([]InstalledPlugin{installed}, &pin)
	if !ok || owner.Version != "0.2.0" || declared.HomeProfile == nil || declared.HomeProfile.Title != "Your studio" {
		t.Fatalf("pinned declaration = %+v ok = %v", declared.HomeProfile, ok)
	}
	// The copy is detached from the installed record.
	declared.HomeProfile.Fields[0].Label = "changed"
	if contribution.AssistantProgramHomes[0].HomeProfile.Fields[0].Label == "changed" {
		t.Fatal("the returned declaration shares the installed one's fields")
	}
	// A disabled package still shows its words (the Home is read-only, not blank).
	disabled := installed
	disabled.Enabled = false
	if _, _, ok := PinnedHomeDeclaration([]InstalledPlugin{disabled}, &pin); !ok {
		t.Fatal("a disabled package lost the Home's card")
	}
	if IndependentHomeProviderEvidenceAvailable([]InstalledPlugin{disabled}, &pin) {
		t.Fatal("a disabled package counts as available evidence")
	}

	other := pin
	other.DeclarationDigest = strings.Repeat("0", 64)
	newer := installed
	newer.Version = "0.3.0"
	for name, tc := range map[string]struct {
		installed []InstalledPlugin
		pin       *workspace.AssistantProgramHomeOwner
	}{
		"another declaration": {[]InstalledPlugin{installed}, &other},
		"another release":     {[]InstalledPlugin{newer}, &pin},
		"not installed":       {nil, &pin},
		"no pin":              {[]InstalledPlugin{installed}, nil},
		"two matching copies": {[]InstalledPlugin{installed, installed}, &pin},
		"MCP-only plugin":     {[]InstalledPlugin{{Name: installed.Name, Version: installed.Version, Enabled: true}}, &pin},
	} {
		if _, _, ok := PinnedHomeDeclaration(tc.installed, tc.pin); ok {
			t.Errorf("%s resolved a declaration", name)
		}
	}
}

// A package without the section is unchanged by the feature: same digest, no
// new requirement.
func TestHomeWithoutProfileKeepsItsDeclarationDigest(t *testing.T) {
	contribution, err := ParseSurfaceContribution(independentHomeContributionJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	home := contribution.AssistantProgramHomes[0]
	encoded, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["home_profile"]; present || home.HomeProfile != nil {
		t.Fatalf("a Home without a profile encodes one: %s", encoded)
	}
}
