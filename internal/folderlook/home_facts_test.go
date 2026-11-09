package folderlook

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func albumsPortfolioFacts() foldermap.Facts {
	members := []foldermap.Project{}
	for _, p := range []string{"Album-1", "Album-2", "Album-3"} {
		members = append(members, foldermap.Project{Path: p, Depth: 1, Shape: "audio", Format: "reaper", Marker: "*.rpp"})
	}
	return foldermap.Facts{Members: members, Portfolio: &foldermap.Portfolio{Shape: "audio", Count: 3,
		HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}}
}

func homeCandidate(t *testing.T, ready Readiness) Candidate {
	t.Helper()
	for _, c := range RequiredCandidates(albumsPortfolioFacts(), ready) {
		if c.Kind == KindHomeProvider {
			return c
		}
	}
	t.Fatal("no Home candidate")
	return Candidate{}
}

func TestRequiredCandidates_CarryTheHostsHomeFacts(t *testing.T) {
	c := homeCandidate(t, Readiness{Home: func(key string) (HomeFacts, bool) {
		if key != "music_project_management" {
			t.Fatalf("asked about %q", key)
		}
		return HomeFacts{ProviderName: "Music Project Management", ProviderVersion: "0.5.0", ProviderReady: true,
			HomeExists: true, RolesKnown: true, MissingRoles: []string{"Library Manager"}}, true
	}})
	if c.Action != ActionAlreadyHere || c.Home == nil || !c.Home.HomeExists || strings.Join(c.Home.MissingRoles, ",") != "Library Manager" {
		t.Fatalf("candidate = %+v home = %+v", c, c.Home)
	}

	// A host that cannot tell leaves the facts out and falls back to HomeExists.
	c = homeCandidate(t, Readiness{
		Home:       func(string) (HomeFacts, bool) { return HomeFacts{}, false },
		HomeExists: func(string) bool { return true },
	})
	if c.Home != nil || c.Action != ActionAlreadyHere {
		t.Fatalf("fallback candidate = %+v", c)
	}
}

func TestRequiredCandidates_BoundTheHomeFacts(t *testing.T) {
	c := homeCandidate(t, Readiness{Home: func(string) (HomeFacts, bool) {
		return HomeFacts{ProviderName: "/Users/someone/plugin", ProviderVersion: strings.Repeat("9", 200),
			RolesKnown: true, MissingRoles: []string{"Portfolio Manager", "Bad\nName"}}, true
	}})
	if c.Home.ProviderName != "" || c.Home.ProviderVersion != "" {
		t.Fatalf("a path or an overlong version was kept: %+v", c.Home)
	}
	if c.Home.RolesKnown || c.Home.MissingRoles != nil {
		t.Fatalf("a bad role name left the roles known: %+v", c.Home)
	}
	many := make([]string, maxHomeRoles+1)
	for i := range many {
		many[i] = "Role"
	}
	c = homeCandidate(t, Readiness{Home: func(string) (HomeFacts, bool) {
		return HomeFacts{RolesKnown: true, MissingRoles: many}, true
	}})
	if c.Home.RolesKnown {
		t.Fatal("more roles than a Home declares stayed known")
	}
}
