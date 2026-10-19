package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// FR19: task runs, scheduled runs and orchestration all go through
// LLMTaskHandler, which never reads the external memory mirror. With a synced
// mirror in the data dir and every source enabled, even the personal
// assistant's own Personal HQ task (which does get global memory) carries no
// external block, and neither does a scheduled rerun or a project task.
func TestTaskPromptsNeverCarryExternalMemory(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)
	root, err := filepath.Abs(filepath.Join("..", "externalmemory", "testdata", "codex"))
	if err != nil {
		t.Fatal(err)
	}
	store := externalmemory.NewStore(config.DefaultExternalMemoryDir())
	if _, err := store.UpdateSources(func(doc *externalmemory.SourcesDocument) error {
		for _, id := range externalmemory.SourceIDs {
			doc.Sources[id] = externalmemory.SourceState{Enabled: true}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := externalmemory.NewRunner(store, externalmemory.NewRegistry(externalmemory.NewCodexSource(root)))
	if _, err := runner.Run(context.Background(), externalmemory.ReasonManual); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.Reader().ContextBlock(8000), "trading research tools") {
		t.Fatal("the mirror should hold profile-grade entries for this test to mean anything")
	}

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
	album.AgentInstances = []AgentInstance{{ID: "scout", Name: "Scout", EntryPoint: true}}
	for _, ws := range []*Workspace{hq, album} {
		if err := folders.Save(ws); err != nil {
			t.Fatal(err)
		}
	}
	h := &LLMTaskHandler{workspaceStore: folders, reviewedMemory: &taskReviewedReader{}}

	assistantTask := Task{ID: "task-hq", WorkspaceID: hq.ID, To: "Atlas", Description: "Plan my week."}
	scheduled := assistantTask
	scheduled.ID = "task-weekly"
	scheduled.Schedule = &ScheduleConfig{}
	scheduled.ScheduleEnabled = true
	scheduled.ScheduleName = "Weekly plan"
	project := Task{ID: "task-album", WorkspaceID: album.ID, To: "Scout", Description: "Plan the mix."}

	for name, task := range map[string]Task{"assistant HQ task": assistantTask, "scheduled run": scheduled, "project task": project} {
		t.Run(name, func(t *testing.T) {
			structured := h.buildTaskMemorySection(context.Background(), task, false)
			ag := &resolvedTaskAgent{Agent: &agent.Agent{Settings: types.Settings{SystemPrompt: "Data: {{workspace.memory}}"}}}
			raw, _ := h.resolveTaskAgentBasePrompt(context.Background(), ag, task.To, task)
			for _, text := range []string{structured, raw} {
				for _, leaked := range []string{externalmemory.ContextHeading, "trading research tools", "<<<external-memory", externalmemory.RecallToolName} {
					if strings.Contains(text, leaked) {
						t.Fatalf("task prompt received %q:\n%s", leaked, text)
					}
				}
			}
			if task.WorkspaceID == hq.ID && !strings.Contains(structured, `user approved: "trusted"`) {
				t.Fatalf("the assistant's HQ task should still get global memory (the control):\n%s", structured)
			}
		})
	}
}
