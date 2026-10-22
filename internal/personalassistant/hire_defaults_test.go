package personalassistant

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/agentcatalog"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestHireDefaultBrief_EveryCatalogRoleHasOneThatFitsTheGoal(t *testing.T) {
	for _, entry := range agentcatalog.Registry() {
		brief := HireDefaultBrief(entry.Slug, "")
		if brief == "" {
			t.Errorf("%s has no default brief", entry.Slug)
			continue
		}
		if normalized, err := NormalizeFolderBrief(brief); err != nil || normalized != brief {
			t.Errorf("%s: default brief %q does not survive normalization: %q %v", entry.Slug, brief, normalized, err)
		}
	}
	if got := HireDefaultBrief(types.RoleSpecialist, "  music \n mixing "); got != "Act as the music mixing expert for this workspace." {
		t.Errorf("specialist with a domain: %q", got)
	}
	if got := HireDefaultBrief(types.RoleValidator, "ignored"); got != "Check the work here for mistakes and flag problems before they ship." {
		t.Errorf("a domain only shapes the Specialist's brief: %q", got)
	}
	if got := HireDefaultBrief(types.AgentRole("wizard"), ""); got != "" {
		t.Errorf("an unknown role has no default, got %q", got)
	}
}

func TestHireWorkspaceName_TitleCasesTheBriefsFirstWords(t *testing.T) {
	for brief, want := range map[string]string{
		"Compare the three note-taking apps I'm considering and recommend one": "Compare the Three Note-taking Apps",
		"Find and summarize what's known about this topic, with sources.":      "Find and Summarize What's Known",
		"Check the work here for mistakes and flag problems before they ship.": "Check the Work Here",
		"Take requests for this workspace and route them to the right work.":   "Take Requests for This Workspace",
		"Act as the mixing expert for this workspace.":                         "Act as the Mixing Expert",
		"   \"review   the API docs\" ":                                        "Review the API Docs",
		"…":                                                                    "",
		"":                                                                     "",
		"the and of":                                                           "",
		"Écrire un résumé net":                                                 "Écrire Un Résumé Net",
	} {
		if got := HireWorkspaceName(brief); got != want {
			t.Errorf("HireWorkspaceName(%q) = %q, want %q", brief, got, want)
		}
	}
	long := strings.Repeat("Supercalifragilisticexpialidocious ", 5)
	if got := HireWorkspaceName(long); utf8.RuneCountInString(got) > HireWorkspaceNameMaxRunes {
		t.Errorf("name of %d runes exceeds the bound", utf8.RuneCountInString(got))
	}
}
