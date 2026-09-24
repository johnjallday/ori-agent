package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type stagingFixture struct {
	staging string
	root    string
}

func newStagingFixture(t *testing.T) stagingFixture {
	t.Helper()
	base := t.TempDir()
	f := stagingFixture{staging: filepath.Join(base, "data", "workspace-staging"), root: filepath.Join(base, "Ori Workspaces")}
	if err := os.MkdirAll(f.staging, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return f
}

// stageAgent writes an agent the way the root store does before confirmation.
func (f stagingFixture) stageAgent(t *testing.T) {
	const name = "Scout"
	t.Helper()
	dir := filepath.Join(f.staging, "Agents", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent_settings.json"), []byte(`{"Settings":{}}`+"\n"), 0o600); err != nil {
		t.Fatalf("write agent: %v", err)
	}
}

// stageWorkspace saves a workspace into the staging root through the real
// folder store, with a directory reference and an MCP root pointing into its
// own folder, as a created workspace has.
func (f stagingFixture) stageWorkspace(t *testing.T, ws *Workspace) {
	t.Helper()
	folders, err := NewFileStore(f.staging)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer func() { _ = folders.Close() }()
	folder := filepath.Join(f.staging, Slugify(ws.Name))
	ws.DirectoryReferences = []DirectoryReference{{ID: "ref", WorkspaceID: ws.ID, Name: ws.Name, Path: folder}}
	ws.MCPBindings = []MCPBinding{{ID: "files", ServerName: "filesystem", Alias: "workspace-files", Enabled: true,
		Config: map[string]any{"roots": []any{folder}}}}
	if err := folders.Save(ws); err != nil {
		t.Fatalf("save staged workspace: %v", err)
	}
}

func readWorkspaceAt(t *testing.T, folder string) *Workspace {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(folder, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read %s: %v", folder, err)
	}
	ws, err := FromJSON(data)
	if err != nil {
		t.Fatalf("decode %s: %v", folder, err)
	}
	return ws
}

func TestFirstConfirmationMovesStagedAgentsAndWorkspaces(t *testing.T) {
	f := newStagingFixture(t)
	f.stageAgent(t)
	f.stageWorkspace(t, &Workspace{ID: "ws-1", Name: "Studio", Status: StatusActive})

	result := MoveStagedContent(f.staging, f.root)
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if !result.AgentsMoved {
		t.Error("the staged agents were not moved")
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Scout", "agent_settings.json")); err != nil {
		t.Errorf("Scout is not in the new root: %v", err)
	}
	newFolder := filepath.Join(f.root, "studio")
	moved := readWorkspaceAt(t, newFolder)
	if moved.DirectoryReferences[0].Path != newFolder {
		t.Errorf("directory reference = %q, want %q", moved.DirectoryReferences[0].Path, newFolder)
	}
	if roots := moved.MCPBindings[0].Config["roots"].([]any); roots[0] != newFolder {
		t.Errorf("workspace-files root = %v, want %s", roots, newFolder)
	}
	if len(result.Moved) != 1 || result.Moved[0].ID != "ws-1" || result.Moved[0].NewPath != newFolder ||
		result.Moved[0].OldPath != filepath.Join(f.staging, "studio") {
		t.Errorf("Moved = %+v", result.Moved)
	}
	remaining, _ := os.ReadDir(f.staging)
	for _, entry := range remaining {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("staging still holds %s", entry.Name())
		}
	}
}

func TestAnAgentsFolderAlreadyInTheRootWins(t *testing.T) {
	f := newStagingFixture(t)
	f.stageAgent(t)
	synced := filepath.Join(f.root, "Agents", "Synced")
	if err := os.MkdirAll(synced, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	result := MoveStagedContent(f.staging, f.root)
	if result.AgentsMoved || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "already has an Agents folder") {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.staging, "Agents", "Scout")); err != nil {
		t.Error("the staged agents must stay where they are")
	}
	if _, err := os.Stat(filepath.Join(f.root, "Agents", "Scout")); !os.IsNotExist(err) {
		t.Error("the staged agents were merged into the existing folder")
	}
}

// stageSkill writes an installed skill the way the Skills folder holds it
// before confirmation.
func (f stagingFixture) stageSkill(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(f.staging, "Skills", name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nbody\n"), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func TestFirstConfirmationMovesTheStagedSkillsFolder(t *testing.T) {
	f := newStagingFixture(t)
	f.stageAgent(t)
	f.stageSkill(t, "find-skills")

	result := MoveStagedContent(f.staging, f.root)
	if len(result.Warnings) != 0 || !result.SkillsMoved || !result.AgentsMoved {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Skills", "find-skills", "SKILL.md")); err != nil {
		t.Errorf("the skill is not in the new root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.staging, "Skills")); !os.IsNotExist(err) {
		t.Error("the staged Skills folder is still in staging")
	}
	if len(result.Moved) != 0 {
		t.Errorf("the Skills folder was treated as a workspace: %+v", result.Moved)
	}
}

func TestASkillsFolderAlreadyInTheRootWins(t *testing.T) {
	f := newStagingFixture(t)
	f.stageSkill(t, "staged-skill")
	synced := filepath.Join(f.root, "Skills", "synced-skill")
	if err := os.MkdirAll(synced, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	result := MoveStagedContent(f.staging, f.root)
	if result.SkillsMoved || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "already has a Skills folder") {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.staging, "Skills", "staged-skill", "SKILL.md")); err != nil {
		t.Error("the staged skills must stay where they are")
	}
	if _, err := os.Stat(filepath.Join(f.root, "Skills", "staged-skill")); !os.IsNotExist(err) {
		t.Error("the staged skills were merged into the existing folder")
	}
	if _, err := os.Stat(synced); err != nil {
		t.Errorf("the synced skill was touched: %v", err)
	}
}

func TestThePluginListMovesUnlessTheRootHasOne(t *testing.T) {
	f := newStagingFixture(t)
	staged := filepath.Join(f.staging, "Plugins.json")
	if err := os.WriteFile(staged, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := MoveStagedContent(f.staging, f.root)
	if !result.PluginListMoved || len(result.Warnings) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.root, "Plugins.json")); err != nil {
		t.Fatalf("the list is not in the root: %v", err)
	}

	g := newStagingFixture(t)
	if err := os.WriteFile(filepath.Join(g.staging, "Plugins.json"), []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(g.root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.root, "Plugins.json"), []byte("synced"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = MoveStagedContent(g.staging, g.root)
	if result.PluginListMoved || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "already has a plugin list") {
		t.Fatalf("result = %+v", result)
	}
	if data, _ := os.ReadFile(filepath.Join(g.root, "Plugins.json")); string(data) != "synced" {
		t.Fatalf("the synced list was overwritten: %q", data)
	}
}

func TestAStagedWorkspaceWhoseNameIsTakenStaysInStaging(t *testing.T) {
	f := newStagingFixture(t)
	f.stageWorkspace(t, &Workspace{ID: "ws-1", Name: "Studio", Status: StatusActive})
	if err := os.MkdirAll(filepath.Join(f.root, "studio"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	result := MoveStagedContent(f.staging, f.root)
	if len(result.Moved) != 0 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "already exists") {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.staging, "studio", WorkspaceConfigFile)); err != nil {
		t.Error("the staged workspace must stay in staging")
	}
}

func TestAStagedWorkspaceWithWorkInProgressStays(t *testing.T) {
	f := newStagingFixture(t)
	f.stageWorkspace(t, &Workspace{ID: "ws-1", Name: "Busy", Status: StatusActive,
		Tasks: []Task{{ID: "t1", Description: "running", Status: TaskStatusInProgress}}})

	result := MoveStagedContent(f.staging, f.root)
	if len(result.Moved) != 0 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "work in progress") {
		t.Fatalf("result = %+v", result)
	}
}

func TestStagedMoveFallsBackToCopyAcrossDevices(t *testing.T) {
	f := newStagingFixture(t)
	f.stageAgent(t)
	f.stageWorkspace(t, &Workspace{ID: "ws-1", Name: "Studio", Status: StatusActive})
	original := stagedRename
	stagedRename = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}
	t.Cleanup(func() { stagedRename = original })

	result := MoveStagedContent(f.staging, f.root)
	if len(result.Warnings) != 0 || !result.AgentsMoved || len(result.Moved) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.root, "studio", WorkspaceConfigFile)); err != nil {
		t.Errorf("the copied workspace is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.staging, "studio")); !os.IsNotExist(err) {
		t.Error("the copy did not remove the staged original")
	}
}

func TestASecondConfirmationMovesNothing(t *testing.T) {
	f := newStagingFixture(t)
	f.stageAgent(t)
	f.stageWorkspace(t, &Workspace{ID: "ws-1", Name: "Studio", Status: StatusActive})
	MoveStagedContent(f.staging, f.root)

	again := MoveStagedContent(f.staging, f.root)
	if again.AgentsMoved || len(again.Moved) != 0 || len(again.Warnings) != 0 {
		t.Errorf("second run = %+v, want nothing to do", again)
	}
	if same := MoveStagedContent(f.root, f.root); len(same.Moved) != 0 || same.AgentsMoved {
		t.Errorf("moving a root onto itself = %+v", same)
	}
}
