package projecttemplates

import (
	"errors"
	"strings"
	"testing"
)

func homeProfileFixture() *HomeProfileDeclaration {
	return &HomeProfileDeclaration{
		SchemaVersion: 1,
		Title:         "Your studio",
		Intro:         "What Ori knows about where you make music. Detected values are hints until you confirm them.",
		Fields: []HomeProfileField{
			{ID: "apps", Kind: HomeProfileKindApps, Label: "DAWs on this Mac"},
			{ID: "main_app", Kind: HomeProfileKindMainApp, Label: "Main DAW"},
			{ID: "templates", Kind: HomeProfileKindTemplates, Label: "Project templates"},
			{ID: "defaults", Kind: HomeProfileKindDefaults, Label: "New-song defaults"},
		},
	}
}

func TestHomeProfileDeclarationIsAcceptedOnAHome(t *testing.T) {
	home := independentHomeFixture()
	home.HomeProfile = homeProfileFixture()
	home.HomeProfile.Title = "  Your studio "
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	profile := home.HomeProfile
	if profile.Title != "Your studio" || len(profile.Fields) != 4 {
		t.Fatalf("profile = %+v", profile)
	}
	if !profile.Declares(HomeProfileKindTemplates) || profile.Label(HomeProfileKindMainApp) != "Main DAW" {
		t.Fatalf("lookups = %+v", profile)
	}
	// The durable declaration a Home stores has no profile section: the card's
	// words come from the installed package.
	if without := independentHomeFixture(); AssistantProgramHomeDigest(home) == AssistantProgramHomeDigest(without) {
		t.Fatal("declaring a profile did not change the Home declaration digest")
	}
	clone := CloneAssistantProgramHome(home)
	clone.HomeProfile.Fields[0].Label = "changed"
	if home.HomeProfile.Fields[0].Label == "changed" {
		t.Fatal("clone shares the profile's fields")
	}
}

func TestHomeProfileDeclarationMayOmitKinds(t *testing.T) {
	home := independentHomeFixture()
	home.HomeProfile = homeProfileFixture()
	home.HomeProfile.Fields = home.HomeProfile.Fields[:2]
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if home.HomeProfile.Declares(HomeProfileKindDefaults) || home.HomeProfile.Label(HomeProfileKindDefaults) != "" {
		t.Fatalf("an omitted kind is declared: %+v", home.HomeProfile)
	}
	var none *HomeProfileDeclaration
	if none.Declares(HomeProfileKindApps) || none.Label(HomeProfileKindApps) != "" || none.Clone() != nil {
		t.Fatal("a Home without a profile declares a row")
	}
}

func TestHomeProfileDeclarationRejections(t *testing.T) {
	tests := map[string]func(*HomeProfileDeclaration){
		"schema version":   func(p *HomeProfileDeclaration) { p.SchemaVersion = 2 },
		"empty title":      func(p *HomeProfileDeclaration) { p.Title = "  " },
		"long title":       func(p *HomeProfileDeclaration) { p.Title = strings.Repeat("a", 61) },
		"empty intro":      func(p *HomeProfileDeclaration) { p.Intro = "" },
		"long intro":       func(p *HomeProfileDeclaration) { p.Intro = strings.Repeat("a", 241) },
		"url in intro":     func(p *HomeProfileDeclaration) { p.Intro = "See https://example.com" },
		"newline in title": func(p *HomeProfileDeclaration) { p.Title = "Your\nstudio" },
		"no fields":        func(p *HomeProfileDeclaration) { p.Fields = nil },
		"five fields": func(p *HomeProfileDeclaration) {
			p.Fields = append(p.Fields, HomeProfileField{ID: "more", Kind: HomeProfileKindApps, Label: "More"})
		},
		"unknown kind":   func(p *HomeProfileDeclaration) { p.Fields[0].Kind = "hardware" },
		"duplicate kind": func(p *HomeProfileDeclaration) { p.Fields[1].Kind = HomeProfileKindApps },
		"duplicate id":   func(p *HomeProfileDeclaration) { p.Fields[1].ID = "apps" },
		"invalid id":     func(p *HomeProfileDeclaration) { p.Fields[0].ID = "Apps On Mac" },
		"empty label":    func(p *HomeProfileDeclaration) { p.Fields[0].Label = "" },
		"long label":     func(p *HomeProfileDeclaration) { p.Fields[0].Label = strings.Repeat("a", 41) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			home := independentHomeFixture()
			home.HomeProfile = homeProfileFixture()
			mutate(home.HomeProfile)
			err := NormalizeAssistantProgramHome(&home)
			if !errors.Is(err, ErrInvalidAssistantProgram) {
				t.Fatalf("err = %v, want a rejected declaration", err)
			}
		})
	}
}

func TestHomeProfileDeclarationBoundsCountCharactersNotBytes(t *testing.T) {
	home := independentHomeFixture()
	home.HomeProfile = homeProfileFixture()
	home.HomeProfile.Title = strings.Repeat("é", 60)
	if err := NormalizeAssistantProgramHome(&home); err != nil {
		t.Fatalf("a 60-character title was rejected: %v", err)
	}
}
