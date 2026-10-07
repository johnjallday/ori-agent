package plugin

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
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
