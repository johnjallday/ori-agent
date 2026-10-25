package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	FirstTaskRecipeFolderSummary  = "folder_summary"
	TaskContextFolderSummaryScope = "folder_summary_scope"
	TaskContextReviewedExecutor   = "folder_summary_reviewed_executor"
)

// FolderSummaryScope is a host-only snapshot of the exact directory grant and
// optional saved entry. It contains no absolute path and grants no tool access.
type FolderSummaryScope struct {
	DirectoryID string `json:"directory_id"`
	Identity    string `json:"identity"`
	Entry       string `json:"entry,omitempty"`
}

func FolderSummaryScopeOf(task Task) (FolderSummaryScope, bool) {
	var scope FolderSummaryScope
	data, err := json.Marshal(task.Context[TaskContextFolderSummaryScope])
	if err != nil || len(data) > 2048 || json.Unmarshal(data, &scope) != nil ||
		strings.TrimSpace(scope.DirectoryID) == "" || strings.TrimSpace(scope.Identity) == "" {
		return FolderSummaryScope{}, false
	}
	return scope, true
}

// canonicalRecipeTask prevents a caller's copied/edited task payload from
// dropping a restriction attached to the persisted task of the same identity.
func (h *LLMTaskHandler) canonicalRecipeTask(task Task) (Task, error) {
	if h == nil || h.workspaceStore == nil || task.ID == "" || task.WorkspaceID == "" {
		return task, nil
	}
	ws, err := h.workspaceStore.Get(task.WorkspaceID)
	if err != nil || ws == nil {
		return task, fmt.Errorf("canonical task policy is unavailable")
	}
	for _, saved := range ws.Tasks {
		if saved.ID == task.ID && FirstTaskRecipe(&saved) != "" {
			return saved, nil
		}
	}
	return task, nil
}

// RecipeExecutor is configuration evidence for the exact task execution path,
// not a provider connection test. Digest binds identity, model and read scope.
type RecipeExecutor struct {
	Agent    string `json:"agent"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Digest   string `json:"digest"`
}

func recipeExecutor(task Task, agentName string, chosen recipeProvider) RecipeExecutor {
	scope, _ := FolderSummaryScopeOf(task)
	encoded, _ := json.Marshal([]any{task.WorkspaceID, task.ID, task.To, agentName, chosen.name, chosen.model, chosen.effort, FirstTaskRecipe(&task), scope})
	sum := sha256.Sum256(encoded)
	return RecipeExecutor{Agent: agentName, Provider: chosen.name, Model: chosen.model, Digest: hex.EncodeToString(sum[:])}
}

func (h *LLMTaskHandler) RecipeExecutorDisclosure(task Task) (*RecipeExecutor, string) {
	var err error
	task, err = h.canonicalRecipeTask(task)
	if err != nil || h == nil {
		return nil, RecipeUnavailable
	}
	chosen, blocked := h.resolveRecipeProvider(task.To, task, FirstTaskRecipe(&task))
	if blocked != nil {
		return nil, blocked.ReasonCode
	}
	executor := recipeExecutor(task, task.To, chosen)
	return &executor, ""
}
