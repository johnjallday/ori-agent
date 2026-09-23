package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// <root>/Agents holds the user's agents, so no workspace may take that
// top-level folder. The check is case-insensitive because macOS file systems
// are, and it applies at the top level only.

func reservedSlugStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	root := t.TempDir()
	fs, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs, root
}

func TestCreatingATopLevelWorkspaceNamedAgentsIsRejected(t *testing.T) {
	for _, name := range []string{"Agents", "agents", "AGENTS"} {
		fs, root := reservedSlugStore(t)
		err := fs.Save(&Workspace{ID: "ws-" + name, Name: name})
		if !errors.Is(err, ErrReservedWorkspaceSlug) {
			t.Errorf("Save(%q): err = %v, want ErrReservedWorkspaceSlug", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(statErr) {
			t.Errorf("Save(%q) created the reserved folder", name)
		}
	}
}

func TestSaveAtTheRootIsRejectedToo(t *testing.T) {
	fs, root := reservedSlugStore(t)
	if err := fs.SaveAt(&Workspace{ID: "ws-1", Name: "Agents"}, root); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("SaveAt: err = %v, want ErrReservedWorkspaceSlug", err)
	}
}

func TestAWorkspaceNamedAgentsInsideAGroupIsAllowed(t *testing.T) {
	fs, _ := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "group", Name: "Team"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	if err := fs.Save(&Workspace{ID: "member", Name: "Agents", ParentID: "group"}); err != nil {
		t.Fatalf("a member named Agents must be allowed: %v", err)
	}
}

func TestRenamingAWorkspaceToAgentsIsRejected(t *testing.T) {
	fs, root := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "ws-1", Name: "Scouting"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := fs.RenameWithSlug("ws-1", "Agents", ""); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("RenameWithSlug: err = %v, want ErrReservedWorkspaceSlug", err)
	}
	if _, err := os.Stat(filepath.Join(root, "scouting", WorkspaceConfigFile)); err != nil {
		t.Errorf("the refused rename moved the workspace: %v", err)
	}
}

func TestMovingAMemberNamedAgentsToTheTopLevelIsRejected(t *testing.T) {
	fs, _ := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "group", Name: "Team"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	if err := fs.Save(&Workspace{ID: "member", Name: "Agents", ParentID: "group"}); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if _, err := fs.MoveWorkspaceFolder("member", ""); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("MoveWorkspaceFolder: err = %v, want ErrReservedWorkspaceSlug", err)
	}
}

func TestImportingAWorkspaceIntoTheAgentsFolderIsRejected(t *testing.T) {
	fs, root := reservedSlugStore(t)
	outside := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, WorkspaceConfigFile), []byte(`{"id":"imported","name":"Agents"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := fs.Import(outside); !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("Import: err = %v, want ErrReservedWorkspaceSlug", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(err) {
		t.Error("the refused import copied into the reserved folder")
	}
}

// <root>/Skills holds the skills Ori installed, so it is reserved the same way.

func TestCreatingATopLevelWorkspaceNamedSkillsIsRejectedWithItsOwnMessage(t *testing.T) {
	for _, name := range []string{"Skills", "skills", "SKILLS"} {
		fs, root := reservedSlugStore(t)
		err := fs.Save(&Workspace{ID: "ws-" + name, Name: name})
		if !errors.Is(err, ErrReservedWorkspaceSlug) {
			t.Errorf("Save(%q): err = %v, want ErrReservedWorkspaceSlug", name, err)
		}
		if got, want := ReservedWorkspaceSlugMessage(err), `"Skills" is reserved for your skills folder. Choose another name.`; got != want {
			t.Errorf("Save(%q) message = %q, want %q", name, got, want)
		}
		if _, statErr := os.Stat(filepath.Join(root, "skills")); !os.IsNotExist(statErr) {
			t.Errorf("Save(%q) created the reserved folder", name)
		}
	}
}

func TestReservedTopLevelSlugMessageNamesTheFolder(t *testing.T) {
	cases := map[string]string{
		"Agents":   `"Agents" is reserved for your agents folder. Choose another name.`,
		" skills ": `"Skills" is reserved for your skills folder. Choose another name.`,
		"Skillset": "",
		"":         "",
	}
	for name, want := range cases {
		if got := ReservedTopLevelSlugMessage(name); got != want {
			t.Errorf("ReservedTopLevelSlugMessage(%q) = %q, want %q", name, got, want)
		}
		if got := IsReservedTopLevelSlug(name); got != (want != "") {
			t.Errorf("IsReservedTopLevelSlug(%q) = %v", name, got)
		}
	}
}

func TestAWorkspaceNamedSkillsInsideAGroupIsAllowed(t *testing.T) {
	fs, _ := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "group", Name: "Team"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	if err := fs.Save(&Workspace{ID: "member", Name: "Skills", ParentID: "group"}); err != nil {
		t.Fatalf("a member named Skills must be allowed: %v", err)
	}
}

func TestRenamingAWorkspaceToSkillsIsRejected(t *testing.T) {
	fs, _ := reservedSlugStore(t)
	if err := fs.Save(&Workspace{ID: "ws-1", Name: "Scouting"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, err := fs.RenameWithSlug("ws-1", "Skills", "")
	if !errors.Is(err, ErrReservedWorkspaceSlug) {
		t.Fatalf("RenameWithSlug: err = %v, want ErrReservedWorkspaceSlug", err)
	}
	if !strings.Contains(ReservedWorkspaceSlugMessage(err), `"Skills"`) {
		t.Errorf("message = %q, want the Skills message", ReservedWorkspaceSlugMessage(err))
	}
}

// The root scan adopts only folders holding a workspace.json, so the Skills
// folder (skill folders with a SKILL.md each) never becomes a workspace.
func TestTheRootScanDoesNotTreatTheSkillsFolderAsAWorkspace(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "Skills", "find-skills")
	if err := os.MkdirAll(skill, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: find-skills\n---\nbody\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if err := fs.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	workspaces, err := fs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("the root scan adopted %d workspace(s) from the Skills folder", len(workspaces))
	}
	if _, err := os.Stat(filepath.Join(root, "Skills", WorkspaceConfigFile)); !os.IsNotExist(err) {
		t.Error("the root scan wrote a workspace.json into the Skills folder")
	}
}

func TestAnExistingWorkspaceAlreadyNamedAgentsKeepsSaving(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "agents")
	if err := os.MkdirAll(existing, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existing, WorkspaceConfigFile),
		[]byte(`{"id":"old","name":"Agents","folder_slug":"agents"}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs, err := NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	ws, err := fs.Get("old")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ws.Description = "still mine"
	if err := fs.Save(ws); err != nil {
		t.Fatalf("an existing workspace in the reserved folder must keep saving: %v", err)
	}
}
