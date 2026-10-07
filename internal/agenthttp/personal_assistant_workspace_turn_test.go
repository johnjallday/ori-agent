package agenthttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type workspaceRelationshipSnapshot struct{ work PersonalAssistantWorkContext }

func (p *workspaceRelationshipSnapshot) ResolvePersonalAssistantContext(context.Context, string) (*PersonalAssistantWorkContext, error) {
	work := p.work
	return &work, nil
}
func (p *workspaceRelationshipSnapshot) ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error) {
	work := p.work
	return &work, nil
}

func TestAssistantWorkspaceTurn_FreezesReferencesAndRevalidatesAccess(t *testing.T) {
	r, store, _, alpha, beta := workspaceResolverFixture(t)
	relationship := &workspaceRelationshipSnapshot{work: PersonalAssistantWorkContext{State: "active", StateVersion: 3, HQWorkspaceID: "hired-hq", ConversationAgent: "hired-profile"}}
	h := &HomeAssistantAskHandler{WorkspaceContext: r, UserID: "local", PersonalAssistantContext: relationship}
	refs := &HomeAssistantRouteContext{WorkspaceID: alpha.ID, PagePath: "/workspaces/album-1", Origin: "personal_assistant_panel"}
	turn := h.bindWorkspaceTurn(context.Background(), "Hello", refs, &relationship.work)
	if turn == nil || turn.projection.Subject.ID != alpha.ID {
		t.Fatal("no accepted scope")
	}
	refs.WorkspaceID, refs.PagePath = beta.ID, "/workspaces/second-project"
	if err := h.revalidateWorkspaceTurn(context.Background(), turn); err != nil || turn.projection.Subject.ID != alpha.ID {
		t.Fatal("navigation retargeted pinned read", err)
	}
	// Ordinary record changes can be read fresh; they never change the ID.
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error { ws.Description = "New current fact"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.revalidateWorkspaceTurn(context.Background(), turn); err != nil {
		t.Fatal("ordinary update became access revocation", err)
	}
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error { ws.OwnerUserID = "foreign"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.revalidateWorkspaceTurn(context.Background(), turn); err == nil {
		t.Fatal("revoked owner still readable")
	}
	if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error { ws.OwnerUserID = "local"; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(){
		func() { relationship.work.StateVersion++ },
		func() { relationship.work.HQWorkspaceID = "replacement-hq" },
		func() { relationship.work.ConversationAgent = "replacement-profile" },
		func() { relationship.work.State = "repair_needed" },
	} {
		original := relationship.work
		alter()
		if err := h.revalidateWorkspaceTurn(context.Background(), turn); err == nil {
			t.Fatal("replacement relationship revived scope")
		}
		relationship.work = original
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.revalidateWorkspaceTurn(ctx, turn); err == nil {
		t.Fatal("cancelled turn kept reads")
	}
}
