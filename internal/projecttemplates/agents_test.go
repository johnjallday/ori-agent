package projecttemplates

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAgentSpecs_TrimDropDedupeOrder(t *testing.T) {
	in := []AgentSpec{
		{Name: "  Producer  ", Role: " orchestrator ", Type: " general ", SystemPrompt: "  lead  ", Model: " gpt-5.5 "},
		{Name: ""},    // blank name -> dropped
		{Name: "   "}, // whitespace-only -> dropped
		{Name: "Copywriter"},
		{Name: "producer"}, // case-insensitive duplicate of "Producer" -> dropped
		{Name: "Designer"},
	}

	out := normalizeAgentSpecs(in)

	gotNames := make([]string, len(out))
	for i, a := range out {
		gotNames[i] = a.Name
	}
	want := []string{"Producer", "Copywriter", "Designer"}
	if len(gotNames) != len(want) {
		t.Fatalf("expected %d agents, got %d (%v)", len(want), len(gotNames), gotNames)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Fatalf("order/name mismatch at %d: got %q want %q (full %v)", i, gotNames[i], want[i], gotNames)
		}
	}

	first := out[0]
	if first.Role != "orchestrator" || first.Type != "general" || first.SystemPrompt != "lead" || first.Model != "gpt-5.5" {
		t.Fatalf("fields not trimmed: %+v", first)
	}
}

func TestNormalizeAgentSpecs_EmptyReturnsNil(t *testing.T) {
	if got := normalizeAgentSpecs(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
	if got := normalizeAgentSpecs([]AgentSpec{{Name: "  "}, {Name: ""}}); got != nil {
		t.Fatalf("expected nil when all entries drop out, got %v", got)
	}
}

func TestNormalizeAgentSpecs_SoftCap(t *testing.T) {
	in := make([]AgentSpec, MaxTemplateAgents+5)
	for i := range in {
		in[i] = AgentSpec{Name: fmt.Sprintf("Agent %d", i)}
	}
	out := normalizeAgentSpecs(in)
	if len(out) != MaxTemplateAgents {
		t.Fatalf("expected cap of %d, got %d", MaxTemplateAgents, len(out))
	}
	// Cap keeps the first N in declaration order (entry agent must survive).
	if out[0].Name != "Agent 0" {
		t.Fatalf("expected first entry preserved, got %q", out[0].Name)
	}
}

func TestNormalizeAgentSpecs_NormalizesTools(t *testing.T) {
	out := normalizeAgentSpecs([]AgentSpec{
		{Name: "Mixer", Tools: ToolDefaults{
			Skills:     []string{" b-skill ", "a-skill", "a-skill", ""},
			MCPServers: []string{"reaper"},
		}},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(out))
	}
	skills := out[0].Tools.Skills
	// normalizeNameList trims, drops blanks, de-dupes, and sorts.
	if len(skills) != 2 || skills[0] != "a-skill" || skills[1] != "b-skill" {
		t.Fatalf("tools not normalized: %v", skills)
	}
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func TestNewTemplate_ParsesAgents(t *testing.T) {
	dir := writeManifest(t, `{
      "name": "Campaign",
      "agents": [
        {"name": "Campaign Lead", "role": "orchestrator", "system_prompt": "Run the campaign", "model": "gpt-5.5",
         "tools": {"skills": ["planning"], "mcp_servers": ["drive"]}},
        {"name": "Copywriter", "type": "general", "unknown_field": "ignored"}
      ]
    }`)

	tpl := newTemplate(dir)

	if !tpl.HasAgents() {
		t.Fatal("expected HasAgents() true")
	}
	if len(tpl.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(tpl.Agents))
	}
	if tpl.Agents[0].Name != "Campaign Lead" || tpl.Agents[1].Name != "Copywriter" {
		t.Fatalf("unexpected roster order: %+v", tpl.Agents)
	}
	if tpl.Agents[0].Tools.Skills[0] != "planning" || tpl.Agents[0].Tools.MCPServers[0] != "drive" {
		t.Fatalf("per-agent tools not parsed: %+v", tpl.Agents[0].Tools)
	}
}

func TestNewTemplate_NoAgentsBackwardsCompat(t *testing.T) {
	dir := writeManifest(t, `{"name": "Plain"}`)
	tpl := newTemplate(dir)
	if tpl.HasAgents() {
		t.Fatal("expected HasAgents() false for a manifest without an agents key")
	}
	if tpl.Agents != nil {
		t.Fatalf("expected nil Agents, got %v", tpl.Agents)
	}
}

func TestNewTemplate_MalformedManifestDegrades(t *testing.T) {
	dir := writeManifest(t, `{ this is not valid json `)
	tpl := newTemplate(dir)
	// A malformed manifest falls back to folder-name display and no agents.
	if tpl.HasAgents() {
		t.Fatal("expected no agents from a malformed manifest")
	}
}
