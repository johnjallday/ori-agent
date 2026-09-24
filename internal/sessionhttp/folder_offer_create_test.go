package sessionhttp

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// The assistant's setup goes through the ordinary creation pipeline: the
// workspace carries the offer id, the created event is tagged for the
// mission, and a blueprint is honoured.
func TestCreateFolderOfferWorkspace_UsesTheOrdinaryPipeline(t *testing.T) {
	handler, libDir, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	writeCapabilityTemplate(t, libDir, "code-project", `{
		"name": "Code Project",
		"builtin": true,
		"agents": []
	}`)
	bus := agentworkspace.NewEventBus(4, 16)
	defer bus.Shutdown()
	handler.SetEventBus(bus)

	id, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{
		Name: "ori-agent", TemplateID: "code-project", OfferID: "offer-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := handler.workspaceStore.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ws.Name != "ori-agent" || !ws.IsFromTemplate("code-project") {
		t.Fatalf("workspace=%+v provenance=%+v", ws.Name, ws.GetTemplateProvenance())
	}
	if recorded, _ := ws.SharedData[projecttemplates.FolderOfferIDKey].(string); recorded != "offer-1" {
		t.Fatalf("offer id = %q", recorded)
	}
	if got := createdEventFor(t, bus, id).Data["entry_point"]; got != "folder_digest" {
		t.Fatalf("entry_point = %v", got)
	}

	// Blank when the shape has no blueprint.
	blank, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{Name: "Notes", OfferID: "offer-2"})
	if err != nil {
		t.Fatal(err)
	}
	if ws, err := handler.workspaceStore.Get(blank); err != nil || ws.GetTemplateProvenance() != nil {
		t.Fatalf("blank workspace=%+v err=%v", ws, err)
	}

	// A refused create is an error, never a half-made workspace.
	if _, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{Name: "", OfferID: "offer-3"}); err == nil {
		t.Fatal("a nameless create succeeded")
	}
	if _, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{Name: "Ghost", OfferID: ""}); err == nil {
		t.Fatal("a create without an offer id succeeded")
	}
	// The pipeline treats a blueprint it cannot find as blank (it logs and
	// carries on), which is why the service checks the blueprint is installed
	// before asking; the create itself still succeeds.
	ghost, err := handler.CreateFolderOfferWorkspace(context.Background(), FolderOfferWorkspaceRequest{Name: "Ghost", TemplateID: "no-such-blueprint", OfferID: "offer-4"})
	if err != nil || strings.TrimSpace(ghost) == "" {
		t.Fatalf("unknown blueprint id=%q err=%v", ghost, err)
	}
}
