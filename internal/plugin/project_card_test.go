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

// projectCardContributionJSON is the Music candidate fixture with the words
// section a 0.5.0 release declares.
func projectCardContributionJSON(t *testing.T, mutate func(root, home, card map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(independentHomeContributionJSON(t), &root); err != nil {
		t.Fatal(err)
	}
	root["version"] = "0.5.0"
	root["requires_host_features"] = []any{"independent_program_homes_v1", "project_card_words_v1"}
	card := map[string]any{
		"schema_version": float64(1),
		"noun":           "Song",
		"noun_plural":    "Songs",
		"roster_title":   "Songs and assistants",
		"facts": []any{
			map[string]any{"kind": "track_count", "unit": "tracks", "unit_one": "track"},
			map[string]any{"kind": "tempo", "unit": "BPM"},
			map[string]any{"kind": "length"},
		},
		"fields": []any{
			map[string]any{"id": "key", "label": "Key", "kind": "text"},
			map[string]any{"id": "references", "label": "Reference tracks", "kind": "list"},
		},
	}
	home := root["assistant_program_homes"].([]any)[0].(map[string]any)
	home["project_card"] = card
	if mutate != nil {
		mutate(root, home, card)
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestProjectCardContributionIsAccepted(t *testing.T) {
	contribution, err := ParseSurfaceContribution(projectCardContributionJSON(t, nil))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	card := contribution.AssistantProgramHomes[0].ProjectCard
	if card == nil || card.Noun != "Song" || card.RosterTitle != "Songs and assistants" || len(card.Facts) != 3 ||
		card.Facts[0].Kind != projecttemplates.ProjectCardFactTrackCount || card.Facts[0].UnitOne != "track" ||
		len(card.Fields) != 2 || card.Fields[1].Kind != projecttemplates.ProjectCardFieldList {
		t.Fatalf("card = %+v", card)
	}
	if !slices.Contains(HostFeatures(), HostFeatureProjectCardWordsV1) || !HostSupportsFeatures(contribution.RequiresHostFeatures) {
		t.Fatal("this build does not advertise project_card_words_v1")
	}
}

func TestProjectCardContributionFailsClosed(t *testing.T) {
	fact := func(card map[string]any, index int) map[string]any {
		return card["facts"].([]any)[index].(map[string]any)
	}
	field := func(card map[string]any, index int) map[string]any {
		return card["fields"].([]any)[index].(map[string]any)
	}
	tests := map[string]func(root, home, card map[string]any){
		"feature omitted": func(root, _, _ map[string]any) {
			root["requires_host_features"] = []any{"independent_program_homes_v1"}
		},
		"unknown section key": func(_, _, card map[string]any) { card["route"] = "/songs" },
		"unknown fact key":    func(_, _, card map[string]any) { fact(card, 0)["path"] = "x" },
		"unknown field key":   func(_, _, card map[string]any) { field(card, 0)["operation"] = "x" },
		"schema version 2":    func(_, _, card map[string]any) { card["schema_version"] = float64(2) },
		"empty noun":          func(_, _, card map[string]any) { card["noun"] = "" },
		"missing plural":      func(_, _, card map[string]any) { delete(card, "noun_plural") },
		"noun too long":       func(_, _, card map[string]any) { card["noun"] = strings.Repeat("a", 41) },
		"roster title too long": func(_, _, card map[string]any) {
			card["roster_title"] = strings.Repeat("a", 61)
		},
		"url in noun":       func(_, _, card map[string]any) { card["noun"] = "https://x" },
		"newline in title":  func(_, _, card map[string]any) { card["roster_title"] = "Songs\nand assistants" },
		"unknown fact kind": func(_, _, card map[string]any) { fact(card, 0)["kind"] = "key" },
		"duplicate fact kind": func(_, _, card map[string]any) {
			fact(card, 1)["kind"] = "track_count"
		},
		"empty facts": func(_, _, card map[string]any) { card["facts"] = []any{} },
		"too many facts": func(_, _, card map[string]any) {
			card["facts"] = append(card["facts"].([]any), map[string]any{"kind": "tempo", "unit": "BPM"})
		},
		"tempo without unit": func(_, _, card map[string]any) { delete(fact(card, 1), "unit") },
		"length with unit":   func(_, _, card map[string]any) { fact(card, 2)["unit"] = "minutes" },
		"unknown field kind": func(_, _, card map[string]any) { field(card, 0)["kind"] = "number" },
		"duplicate field id": func(_, _, card map[string]any) { field(card, 1)["id"] = "key" },
		"invalid field id":   func(_, _, card map[string]any) { field(card, 0)["id"] = "Key" },
		"empty field label":  func(_, _, card map[string]any) { field(card, 0)["label"] = "" },
		"too many fields": func(_, _, card map[string]any) {
			fields := []any{}
			for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
				fields = append(fields, map[string]any{"id": id, "label": id, "kind": "text"})
			}
			card["fields"] = fields
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSurfaceContribution(projectCardContributionJSON(t, mutate))
			var contributionErr *ContributionError
			if !errors.As(err, &contributionErr) {
				t.Fatalf("err = %v, want a rejected contribution", err)
			}
		})
	}
}

// Facts and fields are optional: a section with only the three words is a
// complete declaration.
func TestProjectCardContributionWithWordsOnly(t *testing.T) {
	contribution, err := ParseSurfaceContribution(projectCardContributionJSON(t, func(_, _, card map[string]any) {
		delete(card, "facts")
		delete(card, "fields")
	}))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	card := contribution.AssistantProgramHomes[0].ProjectCard
	if card == nil || card.Facts != nil || card.Fields != nil {
		t.Fatalf("card = %+v", card)
	}
}

// An Ori build from before the feature never registers the package without
// its words: the unknown host feature refuses the whole contribution.
func TestProjectCardContributionIsRefusedByAnOlderHost(t *testing.T) {
	contribution, err := ParseSurfaceContribution(projectCardContributionJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	older := slices.DeleteFunc(HostFeatures(), func(feature string) bool { return feature == HostFeatureProjectCardWordsV1 })
	err = contribution.ValidateForHost(SurfaceProtocolVersion, older)
	var contributionErr *ContributionError
	if !errors.As(err, &contributionErr) || contributionErr.Code != CodeHostFeatureUnsupported {
		t.Fatalf("err = %v, want host_feature_unsupported", err)
	}
}

// The section is part of the Home's digest, a Home without it keeps the
// digest it had, and the pinned declaration carries a detached copy.
func TestProjectCardDigestAndPinnedCopy(t *testing.T) {
	without, err := ParseSurfaceContribution(independentHomeContributionJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	with, err := ParseSurfaceContribution(projectCardContributionJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	plain := without.AssistantProgramHomes[0]
	encoded, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "project_card") {
		t.Fatalf("a Home without the section encodes one: %s", encoded)
	}
	home := with.AssistantProgramHomes[0]
	if projecttemplates.AssistantProgramHomeDigest(home) == projecttemplates.AssistantProgramHomeDigest(plain) {
		t.Fatal("the section does not change the declaration digest")
	}
	changed := projecttemplates.CloneAssistantProgramHome(home)
	changed.ProjectCard.Noun = "Track"
	if projecttemplates.AssistantProgramHomeDigest(changed) == projecttemplates.AssistantProgramHomeDigest(home) {
		t.Fatal("a changed noun keeps the digest")
	}
	if home.ProjectCard.Noun != "Song" {
		t.Fatal("the clone shares the section")
	}

	installed := InstalledPlugin{Name: "music-project-management", Version: "0.5.0", Enabled: true,
		ContentGeneration: 3, ComponentFingerprint: strings.Repeat("a", 64), WorkspaceSurfaces: with}
	pin := workspace.AssistantProgramHomeOwner{
		PluginID: installed.Name, PluginVersion: installed.Version, ProgramID: home.ID,
		HomeSchemaVersion: home.SchemaVersion, HomeVersion: home.Version,
		DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(home),
		PluginGeneration:  installed.EvidenceGeneration(), ComponentFingerprint: installed.ComponentFingerprint,
	}
	_, declared, ok := PinnedHomeDeclaration([]InstalledPlugin{installed}, &pin)
	if !ok || declared.ProjectCard == nil || declared.ProjectCard.Noun != "Song" {
		t.Fatalf("pinned card = %+v ok = %v", declared.ProjectCard, ok)
	}
	declared.ProjectCard.Facts[0].Unit = "changed"
	if with.AssistantProgramHomes[0].ProjectCard.Facts[0].Unit == "changed" {
		t.Fatal("the returned declaration shares the installed one's facts")
	}
}
