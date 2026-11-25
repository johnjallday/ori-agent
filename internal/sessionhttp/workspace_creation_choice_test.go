package sessionhttp

import (
	"context"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"testing"
)

func TestWorkspaceCreationOperation_OnlyOneAgentlessResultForOffer(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	h.currentUserID = func(context.Context) (string, error) { return "local", nil }
	h.SetHireConversationAllowed(func(_ context.Context, user, id string) bool { return user == "local" && id == "thread" })
	proposals := personalassistant.NewSQLiteHireProposals(h.store.DB())
	p, err := proposals.Create(t.Context(), personalassistant.HireProposal{ID: "offer", ConversationID: "thread", UserID: "local", Name: "Ruth", Role: "researcher", WorkspaceName: "Interview findings", Brief: "Compare interviews", Blueprint: "research-project", BlueprintLabel: "Research Project", State: personalassistant.HireProposalWorkspaceDraft})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"entry_point": "assistant_workspace_only", "hire_proposal_id": p.ID, "proposal_conversation_id": "thread", "name": p.WorkspaceName, "mission": p.Brief, "blank": true, "create_template_agents": false, "role_staffing": []string{}, "team_intent": map[string]any{"version": 1, "mode": "agentless", "plan_revision": h.buildTemplateAgentPlan(blankWorkspaceTemplate()).Revision}}
	first, digest := reviewCreationOperation(t, h, body)
	body["name"] = "Other tab"
	second, other := reviewCreationOperation(t, h, body)
	w, result := creationOperationRequest(t, h, "POST", "/"+first+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != 201 {
		t.Fatalf("confirm %d %s", w.Code, w.Body)
	}
	ws, err := h.workspaceTaskStore.Get(result["folder"].(map[string]any)["id"].(string))
	if err != nil || ws.Mission != p.Brief || ws.GoalFirstRunPending || ws.MissionExecutionCount != 0 || h.buildWorkspaceRoster(ws).FilledCount != 0 {
		t.Fatalf("unexpected workspace result: %v %v", ws, err)
	}
	saved, err := proposals.Get(t.Context(), p.ID)
	if err != nil || saved.State != personalassistant.HireProposalWorkspaceCreated || saved.WorkspaceID != ws.ID {
		t.Fatalf("proposal receipt: %+v %v", saved, err)
	}
	w, _ = creationOperationRequest(t, h, "POST", "/"+second+"/confirm", map[string]any{"review_digest": other})
	if w.Code != 409 {
		t.Fatalf("second confirmation %d %s", w.Code, w.Body)
	}
	rows, err := h.store.ListWorkspaces(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("duplicate result %v %v", rows, err)
	}
}
