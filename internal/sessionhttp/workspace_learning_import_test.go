package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLegacyWorkspaceImportStripsLearningAuthorityInRootChildrenAndSQLMirror(t *testing.T) {
	h, cleanup := newPersonalHQImportHarness(t)
	defer cleanup()
	root := exportedWorkspaceFixture("learning-root", "Copied practice", "")
	child := exportedWorkspaceFixture("learning-child", "Child practice", "")
	for _, ws := range []*agentworkspace.Workspace{root, child} {
		ws.Tasks = []agentworkspace.Task{{ID: "retained-ticket", WorkspaceID: ws.ID, Description: "Retained ordinary work", Details: "Supplied fictional text", Result: "Historical draft", Status: agentworkspace.TaskStatusCompleted, CreatedAt: time.Now(), Context: map[string]any{
			agentworkspace.TaskContextFirstTaskRecipe: agentworkspace.LearningDraftRecipe, agentworkspace.TaskContextFirstTaskRecipeVersion: 1,
			agentworkspace.TaskContextLearning:         agentworkspace.LearningTicketBinding{OwnerID: "local", InstanceID: "copied-mission", OperationID: "copied-prepare", Epoch: 1, InputDigest: strings.Repeat("a", 64)},
			agentworkspace.TaskContextReviewedExecutor: strings.Repeat("b", 64), agentworkspace.TaskContextLearningStart: "11111111-1111-4111-8111-111111111111",
			agentworkspace.TaskContextLearningObservation: map[string]any{"owner_id": "local", "operation_id": "copied-observation", "start_token": "11111111-1111-4111-8111-111111111111"},
		}}}
	}
	path := writeExportedWorkspaceFixture(t, filepath.Join(t.TempDir(), "source"), root)
	writeExportedWorkspaceFixture(t, filepath.Join(path, agentworkspace.SubWorkspacesDir, child.FolderSlug), child)
	before, err := os.ReadFile(filepath.Join(path, agentworkspace.WorkspaceConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	code, response := h.importFolder(t, path)
	if code != http.StatusCreated {
		t.Fatal(code, response)
	}
	for _, ws := range []*agentworkspace.Workspace{root, child} {
		imported, err := h.fileStore.Get(ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		sql, err := h.handler.store.GetWorkspace(t.Context(), ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		var mirrored []agentworkspace.Task
		if err := json.Unmarshal(sql.TasksJSON, &mirrored); err != nil {
			t.Fatal(err)
		}
		for _, task := range []agentworkspace.Task{imported.Tasks[0], mirrored[0]} {
			for _, key := range []string{agentworkspace.TaskContextLearning, agentworkspace.TaskContextReviewedExecutor, agentworkspace.TaskContextLearningStart, agentworkspace.TaskContextLearningObservation} {
				if task.Context[key] != nil {
					t.Fatal("legacy import revived source authority", key)
				}
			}
			if agentworkspace.FirstTaskRecipe(&task) != agentworkspace.LearningDraftRecipe || task.Result != "Historical draft" || task.Details != "Supplied fictional text" {
				t.Fatal("denial changed ordinary work or relaxed the recipe", task)
			}
		}
	}
	after, err := os.ReadFile(filepath.Join(path, agentworkspace.WorkspaceConfigFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("import modified the external source", err)
	}
	for _, table := range []string{"learning_owners", "learning_sources", "learning_instances", "learning_operations", "learning_reviews", "learning_evidence"} {
		var count int
		if err := h.handler.store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("import enrolled or restored local evidence", table, count, err)
		}
	}
}
