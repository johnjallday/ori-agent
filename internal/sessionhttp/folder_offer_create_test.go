package sessionhttp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// The assistant's setup goes through the ordinary creation pipeline: the
// workspace carries the offer id, the created event is tagged for the
// mission, and a blueprint is honoured.
func TestFolderOfferWorkspaceReceipt_ReadsActualCreatedRecordsAndReuse(t *testing.T) {
	handler, _, cleanup := capabilityTemplateEnv(t)
	defer cleanup()
	ctx := context.Background()
	id, err := handler.CreateFolderOfferWorkspace(ctx, FolderOfferWorkspaceRequest{Name: "Thesis", OfferID: "offer-1"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := handler.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(t.TempDir(), "Draft")
	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := projecttemplates.AttachLinkedDirectory(ws, "Draft", folder); err != nil {
		t.Fatal(err)
	}
	primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
	ref, _ := ws.GetDirectoryReference(primary)
	if ref == nil || ref.Name != "Draft" {
		t.Fatalf("linked folder before save = %+v, primary %q", ref, primary)
	}
	if err := handler.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	row, err := handler.store.GetWorkspace(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.SharedData == nil {
		row.SharedData = map[string]any{}
	}
	projecttemplates.SetPrimaryDirectoryID(row.SharedData, primary)
	row.DirectoryReferencesJSON, err = json.Marshal(ws.DirectoryReferences)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.store.UpdateWorkspace(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.SeedStarterTasks(id, projecttemplates.Template{
		ID: "folder-digest", StarterTasks: []projecttemplates.StarterTask{{Description: "Summarize the current draft"}},
	}); err != nil {
		t.Fatal(err)
	}
	blank, err := handler.FolderOfferWorkspaceReceipt(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(blank) != 3 || blank[0].Kind != "workspace" || blank[1].Kind != "folder" || blank[1].Name != "Draft" || blank[2].Kind != "task" {
		t.Fatalf("blank receipt = %+v", blank)
	}
	if blank[0].Route == "" || blank[1].Detail != "linked as primary" {
		t.Fatalf("missing canonical route or link: %+v", blank)
	}
	ws, err = handler.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	ws.SetTemplateProvenance(&agentworkspace.TemplateProvenance{TemplateID: "writing-project", TemplateName: "Writing project"})
	ws.AgentInstances = []agentworkspace.AgentInstance{{Name: "Editor", Role: "Lead"}, {Name: "Researcher", Role: "Sources"}}
	if err := handler.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	withRoles, err := handler.FolderOfferWorkspaceReceipt(id, false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, row := range withRoles {
		kinds = append(kinds, row.Kind)
	}
	if strings.Join(kinds, ",") != "workspace,folder,blueprint,agent,agent,task" ||
		withRoles[0].Detail != "already set up" || withRoles[2].Name != "Writing project" ||
		withRoles[3].Name != "Editor" || withRoles[3].Detail != "Lead" {
		t.Fatalf("blueprint/reuse receipt = %+v", withRoles)
	}
}

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
