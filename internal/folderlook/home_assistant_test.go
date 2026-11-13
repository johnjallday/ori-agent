package folderlook

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// assistant-reads-projects: the Home candidate carries the shared assistant its
// songs' team declares, asked of the host by the integration that team comes
// from, bounded like every other Home fact.
func TestRequiredCandidates_AskAboutTheSharedAssistant(t *testing.T) {
	facts := albumsPortfolioFacts()
	facts.Shapes = []foldermap.ShapeCount{{Shape: "audio", Format: "reaper", Count: 3}}
	candidate := func(ready Readiness) Candidate {
		t.Helper()
		for _, c := range RequiredCandidates(facts, ready) {
			if c.Kind == KindHomeProvider {
				return c
			}
		}
		t.Fatal("no Home candidate")
		return Candidate{}
	}
	home := func(string) (HomeFacts, bool) {
		return HomeFacts{ProviderName: "Music Project Management", RolesKnown: true}, true
	}
	var asked []string
	c := candidate(Readiness{Home: home, Assistant: func(provider, integration string) (HomeAssistant, bool) {
		asked = append(asked, provider+" "+integration)
		return HomeAssistant{Label: "REAPER Assistant", Name: "REAPER Assistant", State: AssistantHire,
			Job: "Handles everything REAPER-related for this project: session setup, tracks, routing."}, true
	}})
	if strings.Join(asked, ",") != "music_project_management ori_reaper" || c.ProjectIntegrationKey != "ori_reaper" {
		t.Fatalf("asked %q, key %q", asked, c.ProjectIntegrationKey)
	}
	if a := c.Home.Assistant; a == nil || a.Name != "REAPER Assistant" || a.State != AssistantHire ||
		a.Job != "Handles everything REAPER-related for this project: session setup, tracks, routing." {
		t.Fatalf("assistant = %+v", c.Home.Assistant)
	}

	// The host could not tell: nothing is said about it.
	c = candidate(Readiness{Home: home, Assistant: func(string, string) (HomeAssistant, bool) { return HomeAssistant{}, false }})
	if c.Home.Assistant != nil {
		t.Fatalf("an unknown assistant was named: %+v", c.Home.Assistant)
	}
}

func TestCleanAssistant(t *testing.T) {
	for name, in := range map[string]HomeAssistant{
		"an unknown state":       {Label: "A", Name: "A", State: "adopt"},
		"no label":               {Name: "A", State: AssistantHire},
		"a hire with no name":    {Label: "A", State: AssistantHire},
		"a path for a name":      {Label: "A", Name: "~/Agents/A", State: AssistantHire},
		"a name over the bound":  {Label: "A", Name: strings.Repeat("n", maxHomeFactText+1), State: AssistantHas},
		"a label across lines":   {Label: "A\nB", Name: "A", State: AssistantOff},
		"a label shaped as path": {Label: "/A", Name: "A", State: AssistantOff},
	} {
		if got := cleanAssistant(&in); got != nil {
			t.Errorf("%s passed: %+v", name, got)
		}
	}
	// A note state needs no name; a path-shaped job line is dropped, not the row.
	got := cleanAssistant(&HomeAssistant{Label: "A", State: AssistantOff, Job: "/etc/passwd"})
	if got == nil || got.Job != "" {
		t.Fatalf("off = %+v", got)
	}
}
