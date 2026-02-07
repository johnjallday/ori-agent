package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListSkills_DiscoveryPaths(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	agentSkillDir := filepath.Join(tmpDir, "agents", "default", "skills", "agent-skill")
	repoSkillDir := filepath.Join(tmpDir, "agents", "skills", "repo-skill")
	compatSkillDir := filepath.Join(tmpDir, ".agents", "skills", "compat-skill")

	writeTestSkill(t, agentSkillDir, "agent-skill", "Agent skill", "Agent prompt")
	writeTestSkill(t, repoSkillDir, "repo-skill", "Repo skill", "Repo prompt")
	writeTestSkill(t, compatSkillDir, "compat-skill", "Compat skill", "Compat prompt")
	scriptsDir := filepath.Join(compatSkillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("create scripts dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "run.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	manager := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	skills, err := manager.ListSkills("default")
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}

	if len(skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(skills))
	}

	byName := map[string]Skill{}
	for _, skill := range skills {
		byName[skill.Name] = skill
	}

	if byName["agent-skill"].Source != SourceAgent {
		t.Fatalf("agent skill source = %q, want %q", byName["agent-skill"].Source, SourceAgent)
	}
	if byName["repo-skill"].Source != SourceRepo {
		t.Fatalf("repo skill source = %q, want %q", byName["repo-skill"].Source, SourceRepo)
	}
	if byName["compat-skill"].Source != SourceAgentsCompat {
		t.Fatalf("compat skill source = %q, want %q", byName["compat-skill"].Source, SourceAgentsCompat)
	}
	if byName["compat-skill"].HasScripts != true {
		t.Fatalf("compat skill should detect scripts")
	}
	if byName["agent-skill"].Prompt != "" {
		t.Fatalf("expected list prompt to be empty")
	}
}

func TestListSkills_Conflict(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	agentSkillDir := filepath.Join(tmpDir, "agents", "default", "skills", "dup")
	repoSkillDir := filepath.Join(tmpDir, "agents", "skills", "dup")

	writeTestSkill(t, agentSkillDir, "dup", "Agent dup", "Prompt")
	writeTestSkill(t, repoSkillDir, "dup", "Repo dup", "Prompt")

	manager := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	_, err := manager.ListSkills("default")
	if err == nil {
		t.Fatalf("expected conflict error")
	}
	var conflictErr *SkillConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected SkillConflictError, got %T", err)
	}
	if len(conflictErr.Conflicts) != 1 || conflictErr.Conflicts[0].Name != "dup" {
		t.Fatalf("unexpected conflict details: %+v", conflictErr.Conflicts)
	}
}

func writeTestSkill(t *testing.T, dir, name, description, prompt string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := writeSkillMarkdown(filepath.Join(dir, "SKILL.md"), name, description, prompt); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}
