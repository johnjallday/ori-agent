package workspaceplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SM1: a Plans draft prompt carries the workspace goal through the context
// resolver, which the generator's WorkspaceContext input used to leave empty.
func TestAPIDraftPromptCarriesTheWorkspaceContext(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse, validDraftResponse}}
	service := NewService(NewMemoryStore(), WithGenerator(NewGenerator(model)))
	handler := NewHandler(service)
	var asked []string
	handler.SetContextResolver(func(_ context.Context, workspaceID string) string {
		asked = append(asked, workspaceID)
		return "Goal: Keep the brand voice consistent"
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans", handler.PlanCollection)
	mux.HandleFunc("/api/workspaces/{workspaceID}/plans/{planID}/draft", handler.PlanDraft)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan the launch posts"}`)
	planID, _ := created["id"].(string)
	if status, drafted := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans/"+planID+"/draft", `{}`); status != http.StatusOK {
		t.Fatalf("draft status = %d (%v)", status, drafted)
	}
	if len(asked) != 1 || asked[0] != "ws-1" {
		t.Fatalf("context resolver calls = %v", asked)
	}
	if len(model.prompts) == 0 || !strings.Contains(model.prompts[0], "Workspace context:\nGoal: Keep the brand voice consistent") {
		t.Fatalf("draft prompt lacks the workspace goal: %q", model.prompts)
	}
}

func TestAPIDraftWithoutAContextResolverStillDrafts(t *testing.T) {
	model := &scriptedModel{responses: []string{validDraftResponse}}
	server, _ := newTestAPIWithModel(t, model)
	_, created := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans", `{"request":"Plan A"}`)
	planID, _ := created["id"].(string)
	if status, drafted := doJSON(t, server, http.MethodPost, "/api/workspaces/ws-1/plans/"+planID+"/draft", `{}`); status != http.StatusOK {
		t.Fatalf("draft status = %d (%v)", status, drafted)
	}
	if strings.Contains(model.prompts[0], "Workspace context:") {
		t.Fatalf("no resolver, no workspace context: %q", model.prompts[0])
	}
}
