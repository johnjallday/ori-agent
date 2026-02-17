package skills

import (
	"encoding/json"
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

func TestListSkills_PersonalSkillsIncluded(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	personalSkillsDir := filepath.Join(tmpDir, "personal-skills")
	personalSkillDir := filepath.Join(personalSkillsDir, "frontend-design")
	writeTestSkill(t, personalSkillDir, "frontend-design", "Frontend helper", "Prompt")

	manager := NewManager(ManagerConfig{
		AgentStorePath:    agentStorePath,
		PersonalSkillsDir: personalSkillsDir,
	})

	skills, err := manager.ListSkills("default")
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "frontend-design" {
		t.Fatalf("expected skill name frontend-design, got %q", skills[0].Name)
	}
	if skills[0].Source != SourcePersonal {
		t.Fatalf("expected personal source, got %q", skills[0].Source)
	}
}

func TestListSkills_PersonalSkillDuplicateDoesNotOverrideLocal(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	agentSkillDir := filepath.Join(tmpDir, "agents", "default", "skills", "shared-skill")
	writeTestSkill(t, agentSkillDir, "shared-skill", "Agent skill", "Agent prompt")

	personalSkillsDir := filepath.Join(tmpDir, "personal-skills")
	personalSkillDir := filepath.Join(personalSkillsDir, "shared-skill")
	writeTestSkill(t, personalSkillDir, "shared-skill", "Personal skill", "Personal prompt")

	manager := NewManager(ManagerConfig{
		AgentStorePath:    agentStorePath,
		PersonalSkillsDir: personalSkillsDir,
	})

	skills, err := manager.ListSkills("default")
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 merged skill, got %d", len(skills))
	}
	if skills[0].Source != SourceAgent {
		t.Fatalf("expected local agent source precedence, got %q", skills[0].Source)
	}
}

func TestListSkills_DefaultEnabledWithoutRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	repoSkillDir := filepath.Join(tmpDir, "agents", "skills", "repo-skill")
	writeTestSkill(t, repoSkillDir, "repo-skill", "Repo skill", "Repo prompt")

	manager := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	skills, err := manager.ListSkills("default")
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if !skills[0].Enabled {
		t.Fatalf("expected skill to default enabled when no registry is present")
	}
}

func TestListSkills_WildcardDefaultState(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	repoSkillDir := filepath.Join(tmpDir, "agents", "skills", "repo-skill")
	writeTestSkill(t, repoSkillDir, "repo-skill", "Repo skill", "Repo prompt")

	registryPath := filepath.Join(tmpDir, "agents", "default", "skills_state.json")
	registry := map[string]any{
		"skills": map[string]any{
			"*": map[string]any{
				"enabled": false,
				"trusted": false,
			},
		},
	}
	registryBytes, err := json.Marshal(registry)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	if err := os.WriteFile(registryPath, registryBytes, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	manager := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	skills, err := manager.ListSkills("default")
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Enabled {
		t.Fatalf("expected wildcard default to disable skill")
	}
}

func TestListSkills_ExplicitStateOverridesWildcard(t *testing.T) {
	tmpDir := t.TempDir()
	agentStorePath := filepath.Join(tmpDir, "agents.json")
	if err := os.WriteFile(agentStorePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write agents.json: %v", err)
	}

	repoSkillDir := filepath.Join(tmpDir, "agents", "skills", "repo-skill")
	writeTestSkill(t, repoSkillDir, "repo-skill", "Repo skill", "Repo prompt")

	registryPath := filepath.Join(tmpDir, "agents", "default", "skills_state.json")
	registry := map[string]any{
		"skills": map[string]any{
			"*": map[string]any{
				"enabled": false,
				"trusted": false,
			},
			"repo-skill": map[string]any{
				"enabled": true,
				"trusted": true,
			},
		},
	}
	registryBytes, err := json.Marshal(registry)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	if err := os.WriteFile(registryPath, registryBytes, 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	manager := NewManager(ManagerConfig{AgentStorePath: agentStorePath})
	skills, err := manager.ListSkills("default")
	if err != nil {
		t.Fatalf("ListSkills error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if !skills[0].Enabled {
		t.Fatalf("expected explicit state to override wildcard default")
	}
	if !skills[0].Trusted {
		t.Fatalf("expected explicit trusted state to override wildcard default")
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
