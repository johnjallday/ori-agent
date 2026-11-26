//go:build darwin

package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestDocumentsManagerVerifiedLook(t *testing.T) {
	h, managed, _, cleanup := templateTestEnv(t)
	defer cleanup()
	home := canonicalAnchorTestDir(t)
	root := filepath.Join(home, "Documents")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	policy := filesearch.Policy{Home: home, Protected: []string{managed}}
	local := true
	h.SetGuidedDocumentsLook(func(_ *http.Request, owner string) bool { return local && owner == "local" }, policy.DocumentsWitness)
	h.SetFolderManagerService(folderagent.NewService(folderagent.Deps{Store: folderagent.NewStore(h.workspaceStore.GetFolderPath)}))
	material, _ := documentsCreationMaterial(t, h, "Shelves", root)
	op, digest := reviewCreationOperation(t, h, material)
	w, result := creationOperationRequest(t, h, http.MethodPost, "/"+op+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != 201 {
		t.Fatal(w.Body)
	}
	id := result["folder"].(map[string]any)["id"].(string)
	call := func(method, action, body string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		h.HandleWorkspaces(w, httptest.NewRequest(method, "/api/workspaces/"+id+"/"+action, strings.NewReader(body)))
		var view map[string]any
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, view
	}
	draft := func() map[string]any { _, d := call("GET", "folder-manager-draft", ""); return d }
	d := draft()
	if d == nil || d["look_link"] == nil || d["manager"].(map[string]any)["name"] != "Documents Manager" {
		t.Fatalf("missing verified look with no Files service: %v", d)
	}
	link := d["look_link"].(string)
	verify := func(owner, name, link string) bool {
		_, _, ok := h.AssignedLookRoot(t.Context(), owner, id, name, link)
		return ok
	}
	if !verify("local", "Documents Manager", link) || verify("someone else", "Documents Manager", link) || verify("local", "Mira", link) || verify("local", "Documents Manager", "unreviewed") {
		t.Fatal("wrong assigned identity")
	}
	local = false
	if draft() != nil {
		t.Fatal("nonlocal draft")
	}
	if code, _ := call("POST", "folder-manager-look", `{"link":"`+link+`","agent":"Documents Manager"}`); code != 403 {
		t.Fatalf("nonlocal start %d", code)
	}
	local = true
	ws, _ := h.workspaceStore.Get(id)
	for _, change := range []func(*workspace.Workspace){func(w *workspace.Workspace) { w.OwnerUserID = "another" }, func(w *workspace.Workspace) { w.Status = workspace.StatusTrashed }, func(w *workspace.Workspace) { w.AgentInstances[0].ID = "replacement" }, func(w *workspace.Workspace) { w.AgentInstances[0].RoleID = "other" }, func(w *workspace.Workspace) { w.MCPBindings[0].Enabled = false }, func(w *workspace.Workspace) { w.MCPBindings[0].Config["roots"] = []string{home} }, func(w *workspace.Workspace) { w.DirectoryReferences[0].ID = "relinked" }, func(w *workspace.Workspace) {
		w.ToolboxAssignments = []workspace.AgentToolboxAssignment{{AgentInstanceID: w.AgentInstances[0].ID, ToolboxID: "my-tools"}}
	}} {
		fresh, err := ws.ToJSON()
		if err != nil {
			t.Fatal(err)
		}
		var changed workspace.Workspace
		if err := json.Unmarshal(fresh, &changed); err != nil {
			t.Fatal(err)
		}
		change(&changed)
		if _, ok := h.guidedDocumentsLook(t.Context(), &changed); ok {
			t.Fatal("changed workspace provided authority")
		}
	}
	// A narrowed alias alone cannot adopt a grant. The active, independently
	// owned Filing root must identify the very same binding, reference and path.
	grant, ref := ws.MCPBindings[0], ws.DirectoryReferences[0]
	if err := h.workspaceStore.Update(id, func(w *workspace.Workspace) error {
		w.MCPBindings[0].Alias = filejanitor.LegacyBindingAlias
		w.MCPBindings[0].AllowedTools = filejanitor.JanitorReadTools
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	filing := filejanitor.ManagedRoot{Owner: "local", WorkspaceID: id, Path: root, DirectoryReferenceID: ref.ID, BindingID: grant.ID, Generation: "generation"}
	if _, err := h.GuidedFilingAccess("local", id, "Documents Manager", link); err == nil {
		t.Fatal("accepted alias without Filing")
	}
	h.SetGuidedFilingRoot(func(string, string) (filejanitor.ManagedRoot, error) { return filing, nil })
	if _, err := h.GuidedFilingAccess("local", id, "Documents Manager", link); err != nil {
		t.Fatal(err)
	}
	filing.BindingID = "different"
	if _, err := h.GuidedFilingAccess("local", id, "Documents Manager", link); err == nil {
		t.Fatal("accepted replacement grant")
	}
	if err := h.workspaceStore.Update(id, func(w *workspace.Workspace) error { w.MCPBindings[0] = grant; return nil }); err != nil {
		t.Fatal(err)
	}
	// Simulate an actual pre-update local creation receipt (not a forged
	// portable workspace). Arrival cannot adopt its current directory.
	if _, err := h.store.DB().Exec(`UPDATE workspace_creation_operations SET documents_witness=NULL WHERE id=?`, op); err != nil {
		t.Fatal(err)
	}
	legacy := draft()
	if legacy["look_link"] != nil || legacy["verify_folder"] == nil || verify("local", "Documents Manager", link) {
		t.Fatalf("legacy: %v", legacy)
	}
	token := legacy["verify_folder"].(string)
	if code, _ := call("POST", "verify-documents-folder", `{"verification":"wrong"}`); code != 409 {
		t.Fatal(code)
	}
	if code, _ := call("POST", "verify-documents-folder", `{"verification":"`+token+`"}`); code != 204 || !verify("local", "Documents Manager", link) {
		t.Fatal("failed explicit verification", code)
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root+"-old", root); err != nil {
		t.Fatal(err)
	}
	if draft() != nil || verify("local", "Documents Manager", link) {
		t.Fatal("accepted redirected anchor")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	if draft() != nil || verify("local", "Documents Manager", link) {
		t.Fatal("accepted replacement directory")
	}
	if code, _ := call("POST", "verify-documents-folder", `{"verification":"`+token+`"}`); code != 409 {
		t.Fatal(code)
	}
	if policy.DocumentsWitness(root, time.Unix(1, 0)) != "" {
		t.Fatal("replacement predates reference")
	}
	var n int
	if err := h.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM filename_workspace_links)+(SELECT COUNT(*) FROM filename_sources)`).Scan(&n); err != nil || n != 0 {
		t.Fatal("search authority changed", n, err)
	}
	if _, err := h.folderManager.Record("local", id); err != folderagent.ErrNoRecord {
		t.Fatal("verification started a ticket", err)
	}
}
