package projecttemplates

import (
	"strings"
	"testing"
)

func TestAgentRoleID(t *testing.T) {
	cases := map[string]string{
		"Mix Engineer":      "mix-engineer",
		"  Producer  ":      "producer",
		"A&R / Scout":       "a-r-scout",
		"Workspace Manager": "workspace-manager",
		"":                  "role",
		"!!!":               "role",
		"日本語":               "role",
		"agent_2":           "agent-2",
	}
	for name, want := range cases {
		if got := AgentRoleID(name); got != want {
			t.Errorf("AgentRoleID(%q) = %q, want %q", name, got, want)
		}
	}
	long := AgentRoleID(strings.Repeat("engineer ", 40))
	if len(long) > MaxAgentRoleIDLength {
		t.Errorf("AgentRoleID length = %d, want <= %d", len(long), MaxAgentRoleIDLength)
	}
	if strings.HasSuffix(long, "-") || strings.HasPrefix(long, "-") {
		t.Errorf("AgentRoleID(%q) has a dangling hyphen", long)
	}
}

// TestAgentRoleIDsAreStableAcrossTemplateEdits is the point of D4: a positional
// id rebinds the wrong agent the moment a template is reordered or extended.
func TestAgentRoleIDsAreStableAcrossTemplateEdits(t *testing.T) {
	before := AgentRoleIDs([]AgentSpec{{Name: "Producer"}, {Name: "Mix Engineer"}})
	after := AgentRoleIDs([]AgentSpec{{Name: "Songwriter"}, {Name: "Mix Engineer"}, {Name: "Producer"}})
	if before[0] != "producer" || before[1] != "mix-engineer" {
		t.Fatalf("ids before edit = %v", before)
	}
	if after[2] != before[0] || after[1] != before[1] {
		t.Fatalf("ids moved with position: before=%v after=%v", before, after)
	}
}

// TestAgentRoleIDsAreUnique covers the case normalizeAgentSpecs does not:
// two distinct names that slug to the same string. Two roles sharing an id
// would bind to each other's agents.
func TestAgentRoleIDsAreUnique(t *testing.T) {
	ids := AgentRoleIDs([]AgentSpec{
		{Name: "Mix Engineer"},
		{Name: "mix-engineer"},
		{Name: "Mix.Engineer"},
		{Name: "!!!"},
		{Name: "???"},
	})
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			t.Fatalf("empty role id in %v", ids)
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate role id %q in %v", id, ids)
		}
		seen[id] = struct{}{}
	}
	if ids[0] != "mix-engineer" {
		t.Fatalf("first occurrence lost the bare slug: %v", ids)
	}
}
