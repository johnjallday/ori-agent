package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestAssistantWorkspaceResolver_RealHostMetadataCannotMoveOwnerOrGrantReads(t *testing.T) {
	f := newWorkspaceAwarenessFixture(t)
	before, err := f.builder.sessionStore.GetSession(context.Background(), f.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range []*workspace.Workspace{f.home, f.project, f.other} {
		status, reply := f.call(t, http.MethodPost, "/api/home-assistant/context", f.turn("Hello", location))
		if status != http.StatusOK || reply["status"] != "available" {
			t.Fatalf("context: %d %v", status, reply)
		}
		if reply["location"].(map[string]any)["id"] != location.ID || reply["subject"].(map[string]any)["id"] != location.ID {
			t.Fatal("browser reference not canonically resolved", reply)
		}
		body := mustJSON(t, reply)
		for _, absent := range []string{f.sentinel, f.source, f.offerID, "MCPServers", "ExecutionScope"} {
			if strings.Contains(body, absent) {
				t.Fatalf("metadata endpoint disclosed content or authority: %s", absent)
			}
		}
	}
	status, app := f.call(t, http.MethodPost, "/api/home-assistant/context", map[string]any{"context": map[string]string{"page_path": "/settings", "workspace_id": f.project.ID}})
	if status != http.StatusOK || app["subject"] != nil || app["location"] != nil {
		t.Fatal("app-wide lookup adopted stale project/HQ", app)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("opening context called model")
	}
	after, err := f.builder.sessionStore.GetSession(context.Background(), f.conversationID)
	if err != nil || after.FolderID != f.hqID || after.MessageCount != before.MessageCount || after.UpdatedAt != before.UpdatedAt {
		t.Fatal("metadata refresh wrote or moved conversation", err)
	}
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+f.conversationID, nil)
	if status != http.StatusOK || history["folder_reviews"].(map[string]any)[f.offerID].(map[string]any)["status"] != "pending" {
		t.Fatal("metadata refresh changed review", history)
	}
	if err := f.builder.userStore.Upsert(context.Background(), &userprofile.UserProfile{ID: "other-user"}); err != nil {
		t.Fatal(err)
	}
	if err := f.builder.workspaceStore.Update(f.project.ID, func(ws *workspace.Workspace) error { ws.OwnerUserID = "other-user"; return nil }); err != nil {
		t.Fatal(err)
	}
	status, denied := f.call(t, http.MethodPost, "/api/home-assistant/context", f.turn("Hello", f.project))
	if status != http.StatusOK || denied["overview"] != nil || strings.Contains(mustJSON(t, denied), f.project.Name) {
		t.Fatal("fresh ownership revoke ignored", denied)
	}
}
