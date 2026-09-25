package sessionhttp

import (
	"net/http"
	"testing"

	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// createdEventFor returns the one workspace.created event published for a
// workspace.
func createdEventFor(t *testing.T, bus *agentworkspace.EventBus, workspaceID string) agentworkspace.Event {
	t.Helper()
	events := bus.GetHistory(func(ev agentworkspace.Event) bool {
		return ev.Type == agentworkspace.EventWorkspaceCreated && ev.WorkspaceID == workspaceID
	}, 16)
	if len(events) != 1 {
		t.Fatalf("workspace.created events for %s = %d, want 1", workspaceID, len(events))
	}
	return events[0]
}

// A workspace created from the assistant's folder offer tags its created
// event with the entry point, which completes Show your assistant a folder.
// The label alone, without an offer, is not tagged: any page can send a label.
func TestCreateWorkspace_FolderOfferCreateTagsTheCreatedEvent(t *testing.T) {
	handler, _, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	bus := agentworkspace.NewEventBus(4, 16)
	defer bus.Shutdown()
	handler.SetEventBus(bus)

	w, resp := postCreateWorkspace(t, handler,
		`{"name":"Thesis","entry_point":"folder_digest","folder_offer_id":"offer-1"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	fromOffer := resp["folder"].(map[string]any)["id"].(string)
	if got := createdEventFor(t, bus, fromOffer).Data["entry_point"]; got != "folder_digest" {
		t.Fatalf("entry_point = %v, want folder_digest", got)
	}

	w, resp = postCreateWorkspace(t, handler, `{"name":"Plain","entry_point":"folder_digest"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	plain := resp["folder"].(map[string]any)["id"].(string)
	if _, present := createdEventFor(t, bus, plain).Data["entry_point"]; present {
		t.Fatal("a create without an offer was tagged as the folder offer's")
	}
}
