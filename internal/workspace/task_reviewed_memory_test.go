package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type taskReviewedReader struct {
	calls      int
	workspaces []string
}

func (s *taskReviewedReader) Section(_ context.Context, userID, workspaceID, instanceID string) (string, error) {
	s.calls++
	s.workspaces = append(s.workspaces, workspaceID)
	if userID == userprofile.LocalUserID && workspaceID == "hq" && instanceID == "entry" {
		return "## Global memory (Personal HQ, user-approved)\n\n- user approved: \"trusted\"", nil
	}
	return "", nil
}

func TestTaskReviewedMemoryUsesResolvedAssigneeAndRawVariableWithoutDuplicate(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Home"})
	ws.ID = "hq"
	ws.OwnerUserID = userprofile.LocalUserID
	ws.AgentInstances = []AgentInstance{{ID: "entry", Name: "Atlas", EntryPoint: true}, {ID: "other", Name: "Helper"}}
	if err := folders.Save(ws); err != nil {
		t.Fatal(err)
	}
	spy := &taskReviewedReader{}
	h := &LLMTaskHandler{workspaceStore: folders, reviewedMemory: spy}
	task := Task{ID: "task-1", WorkspaceID: ws.ID, To: "Atlas", Description: "Read the workspace."}
	if section := h.buildTaskMemorySection(context.Background(), task, false); strings.Count(section, `user approved: "trusted"`) != 1 {
		t.Fatalf("task reviewed memory missing/duplicated: %q", section)
	}
	ag := &resolvedTaskAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Data: {{workspace.memory}}"}}}
	raw, hadVars := h.resolveTaskAgentBasePrompt(context.Background(), ag, "Atlas", task)
	if !hadVars || strings.Count(raw, `user approved: "trusted"`) != 1 {
		t.Fatalf("raw task variable missing approved fact: %q", raw)
	}
	if section := h.buildTaskMemorySection(context.Background(), task, true); strings.Contains(section, "trusted") {
		t.Fatalf("raw variable and structured section would duplicate: %q", section)
	}
	untrusted := task
	untrusted.To = "Helper"
	if section := h.buildTaskMemorySection(context.Background(), untrusted, false); strings.Contains(section, "trusted") {
		t.Fatalf("other HQ agent received reviewed memory: %q", section)
	}
	untrusted.To = "Atlas"
	untrusted.AssignedNodeID = "missing-node"
	before := spy.calls
	if section := h.buildTaskMemorySection(context.Background(), untrusted, false); strings.Contains(section, "trusted") || spy.calls != before {
		t.Fatalf("unresolved assignment reached reader: %q calls=%d", section, spy.calls-before)
	}
}

// Global memory is read by the personal assistant only (global-memory FR 3).
// A project workspace's task prompt never asks for the HQ's reviewed facts and
// never renders a managed ori-hq line, even one copied into its own MEMORY.md.
func TestProjectWorkspaceTaskPromptNeverCarriesGlobalMemory(t *testing.T) {
	folders, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hq := NewWorkspace(CreateWorkspaceParams{Name: "Home"})
	hq.ID = "hq"
	hq.OwnerUserID = userprofile.LocalUserID
	hq.AgentInstances = []AgentInstance{{ID: "entry", Name: "Atlas", EntryPoint: true}}
	album := NewWorkspace(CreateWorkspaceParams{Name: "Album 3"})
	album.ID = "album"
	album.OwnerUserID = userprofile.LocalUserID
	album.AgentInstances = []AgentInstance{{ID: "scout", Name: "Scout", EntryPoint: true}, {ID: "atlas-copy", Name: "Atlas"}}
	for _, ws := range []*Workspace{hq, album} {
		if err := folders.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewMemoryStore(folders).Append(album.ID, MemoryEntry{
		Type: MemoryTypeFact, Date: "2026-10-17", Provenance: "agent:Scout", Text: "Album masters at -14 LUFS.",
	}); err != nil {
		t.Fatal(err)
	}
	folder, err := folders.GetFolderPath(album.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, MemoryFileName)
	existing, err := os.ReadFile(path) // #nosec G304 -- disposable test workspace
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(existing, "- [fact, 2026-10-17, ori-hq:item-1:rev-1] COPIED-MANAGED-LINE\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	spy := &taskReviewedReader{}
	h := &LLMTaskHandler{workspaceStore: folders, reviewedMemory: spy}
	for _, assignee := range []string{"Scout", "Atlas"} {
		task := Task{ID: "task-1", WorkspaceID: album.ID, To: assignee, Description: "Plan the mix."}
		structured := h.buildTaskMemorySection(context.Background(), task, false)
		ag := &resolvedTaskAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Data: {{workspace.memory}}"}}}
		raw, _ := h.resolveTaskAgentBasePrompt(context.Background(), ag, assignee, task)
		for _, text := range []string{structured, raw} {
			if !strings.Contains(text, "-14 LUFS") {
				t.Fatalf("%s lost its own workspace memory: %q", assignee, text)
			}
			for _, leaked := range []string{"trusted", "reviewed facts", "Global memory", "ori-hq:", "COPIED-MANAGED-LINE"} {
				if strings.Contains(text, leaked) {
					t.Fatalf("%s in a project workspace received %q: %q", assignee, leaked, text)
				}
			}
		}
	}
	for _, asked := range spy.workspaces {
		if asked == hq.ID {
			t.Fatalf("a project workspace task asked for the HQ's reviewed facts: %v", spy.workspaces)
		}
	}
}
