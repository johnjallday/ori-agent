package projecttemplates

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeAgentSpecs_TrimDropDedupeOrder(t *testing.T) {
	in := []AgentSpec{
		{Name: "  Producer  ", Role: " orchestrator ", Type: " general ", SystemPrompt: "  lead  ", Model: " gpt-5.5 ", Provider: " codex "},
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
	if first.Role != "orchestrator" || first.Type != "general" || first.SystemPrompt != "lead" || first.Model != "gpt-5.5" || first.Provider != "codex" {
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

func TestSetAgents_RoundTripAndPreservesName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "demo"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo", ManifestFileName), []byte(`{"name":"Demo","description":"keep me"}`), 0o600); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}

	tpl, err := SetAgents(dir, "demo", []AgentSpec{
		{Name: "  Lead  ", Role: "orchestrator", SystemPrompt: "run"},
		{Name: ""}, // dropped
		{Name: "Writer", Tools: ToolDefaults{Skills: []string{"draft"}}},
	})
	if err != nil {
		t.Fatalf("SetAgents: %v", err)
	}
	if len(tpl.Agents) != 2 || tpl.Agents[0].Name != "Lead" || tpl.Agents[1].Name != "Writer" {
		t.Fatalf("agents not normalized/persisted: %+v", tpl.Agents)
	}
	// Other keys are preserved (no clobber of name/description).
	if tpl.Name != "Demo" || tpl.Description != "keep me" {
		t.Fatalf("SetAgents clobbered metadata: name=%q desc=%q", tpl.Name, tpl.Description)
	}

	// Re-read from disk to confirm persistence and order.
	reread, err := FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(reread.Agents) != 2 || reread.Agents[0].Name != "Lead" || reread.Agents[1].Tools.Skills[0] != "draft" {
		t.Fatalf("agents did not persist: %+v", reread.Agents)
	}

	// A roster cannot be cleared: every template keeps at least one agent.
	if _, err = SetAgents(dir, "demo", nil); !errors.Is(err, ErrRosterRequired) {
		t.Fatalf("expected ErrRosterRequired for empty roster, got %v", err)
	}
	// A roster that normalizes to empty (blank names) is rejected the same way,
	// and the rejection leaves the stored roster untouched.
	if _, err = SetAgents(dir, "demo", []AgentSpec{{Name: "   "}}); !errors.Is(err, ErrRosterRequired) {
		t.Fatalf("expected ErrRosterRequired for blank-only roster, got %v", err)
	}
	reread, err = FindLibraryTemplate(dir, "demo")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	if len(reread.Agents) != 2 {
		t.Fatalf("rejected clear should not modify the roster, got %+v", reread.Agents)
	}
}

func TestValidateAgentPrompts(t *testing.T) {
	// Known variables pass.
	if err := ValidateAgentPrompts([]AgentSpec{
		{Name: "Writer", SystemPrompt: "You write for {{workspace.name}}. {{workspace.notes.recent}}"},
	}); err != nil {
		t.Fatalf("expected known variables to pass, got %v", err)
	}

	// Unknown variable is rejected, naming the variable + agent, as ErrInvalidPromptVariable.
	err := ValidateAgentPrompts([]AgentSpec{
		{Name: "Writer", SystemPrompt: "Hello {{workspace.bogus}}"},
	})
	if err == nil {
		t.Fatal("expected error for unknown variable")
	}
	if !errors.Is(err, ErrInvalidPromptVariable) {
		t.Errorf("expected ErrInvalidPromptVariable, got %v", err)
	}
	if !strings.Contains(err.Error(), "Writer") || !strings.Contains(err.Error(), "workspace.bogus") {
		t.Errorf("error should name agent + variable, got %q", err.Error())
	}
}

func TestSetAgents_RejectsUnknownVariable(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateBlank(dir, "Demo"); err != nil {
		t.Fatalf("CreateBlank: %v", err)
	}
	_, err := SetAgents(dir, "demo", []AgentSpec{{Name: "Writer", SystemPrompt: "{{nope.var}}"}})
	if !errors.Is(err, ErrInvalidPromptVariable) {
		t.Fatalf("expected SetAgents to reject unknown variable, got %v", err)
	}
}
