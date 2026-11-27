//go:build darwin

package sessionhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestReleasedGuidedAdviceShowsOnlyItsReceipt(t *testing.T) {
	h, managed, _, cleanup := templateTestEnv(t)
	defer cleanup()
	home := canonicalAnchorTestDir(t)
	root := filepath.Join(home, "Documents")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	policy := filesearch.Policy{Home: home, Protected: []string{managed}}
	h.SetGuidedDocumentsLook(func(_ *http.Request, owner string) bool { return owner == "local" }, policy.DocumentsWitness)
	material, _ := documentsCreationMaterial(t, h, "Shelves", root)
	op, digest := reviewCreationOperation(t, h, material)
	w, result := creationOperationRequest(t, h, http.MethodPost, "/"+op+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	id := result["folder"].(map[string]any)["id"].(string)
	ws, err := h.workspaceStore.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	link := documentsLinkPrefix + ws.AnchorDirectoryReferenceID()
	const agent = "Documents Manager"
	store := folderagent.NewStore(h.workspaceStore.GetFolderPath)
	s := folderagent.NewService(folderagent.Deps{Store: store, AssignedRoot: h.AssignedLookRoot, ReleasedAdviceReceipt: h.ReleasedDocumentsAdviceReceipt})
	if err := os.Mkdir(filepath.Join(root, "Shelf"), 0750); err != nil {
		t.Fatal(err)
	}
	walk, err := foldermap.Walk(root, foldermap.Options{MaxEntries: 1})
	if err != nil || walk.Stopped != "entries" {
		t.Fatal("expected a partial walk", walk, err)
	}
	r := folderagent.Record{WorkspaceID: id, WorkspaceName: "Shelves", FolderName: "Documents", OwnerUserID: "local", AgentName: agent, AssignedLink: link,
		State: folderagent.StateSetUp, Map: walk, Setup: &folderagent.Setup{Status: personalassistant.FolderSetupDone, Lines: []personalassistant.FolderPlanLine{
			{Kind: folderagent.FolderPlanFilingAdvice, State: personalassistant.FolderLineDone},
			{Kind: personalassistant.FolderPlanFolder, State: personalassistant.FolderLineDone},
		}}}
	if err := store.Create(&r); err != nil {
		t.Fatal(err)
	}
	// Recreate the released host recipe an older version left behind. The
	// assignment is absent but the listing -> content history remains.
	if err := h.workspaceStore.Update(id, func(ws *workspace.Workspace) error {
		ref := workspace.ToolboxMCPRef{BindingID: ws.MCPBindings[0].ID, Required: true, AllowedTools: folderagent.ListingTools()}
		tb, err := ws.CreateToolbox(workspace.ToolboxDefinition{ID: "folder-agent-" + ws.AgentInstances[0].ID, Name: "First look", Status: workspace.ToolboxStatusActive, Provenance: folderagent.FirstLookProvenance, Actor: "Ori", MCPBindings: []workspace.ToolboxMCPRef{ref}})
		if err != nil {
			return err
		}
		ref.AllowedTools = append(folderagent.ListingTools(), "read_file", "read_text_file", "read_media_file", "read_multiple_files")
		_, err = ws.SaveToolboxVersion(tb.ID, nil, []workspace.ToolboxMCPRef{ref}, folderagent.FirstLookProvenance, "Ori")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The independent Build projection comes from workspace creation, not the
	// advice receipt. Removing that projection below leaves the receipt inert.
	if look, ok := h.GuidedStation("local", id); !ok || look.ReferenceID != ws.AnchorDirectoryReferenceID() || look.Manager != agent || look.Witness == "" {
		t.Fatal("missing independently verified Build", look)
	}
	if _, ok := h.GuidedStation("someone else", id); ok {
		t.Fatal("foreign owner offered Build")
	}
	v, err := s.View(t.Context(), "local", id)
	if err != nil || v.State != folderagent.StateSetUp || !v.FilingRecommendationDone || v.FilingReviewHint != "partial" || v.Plan != nil || len(v.Actions) != 0 || v.FilingPile != nil || v.ProjectHolders != nil || v.OrganizationReview {
		t.Fatalf("expected a finished, read-only partial receipt: %+v %v", v, err)
	}
	if _, err := s.Record("local", id); err == nil {
		t.Fatal("historical pin reopened the live report")
	}
	if _, err := s.StationLook("local", id); err == nil {
		t.Fatal("receipt authorized Build")
	}
	if _, err := s.OrganizationPreview("local", id); err == nil {
		t.Fatal("receipt authorized Documents review")
	}
	if _, err := s.View(t.Context(), "someone else", id); err == nil {
		t.Fatal("foreign owner saw receipt")
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.GuidedStation("local", id); ok {
		t.Fatal("Build survived replacing the anchor")
	}
	if _, err := s.View(t.Context(), "local", id); err == nil {
		t.Fatal("receipt survived replacing the anchor")
	}
}
