package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesLayoutPlacesOnlyScopedExternalLinks(t *testing.T) {
	store, ws, h := newFolderHandlerTest(t, "group", "Group")
	disk := t.TempDir()
	if err := store.Update(ws.ID, func(w *Workspace) error {
		w.Kind = "group"
		w.OwnerUserID = "me"
		w.DirectoryReferences = []DirectoryReference{{WorkspaceID: w.ID, ID: "disk", Name: "Projects", Path: disk}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, parent, owner, path string }{{"child", ws.ID, "me", filepath.Join(disk, "albums", "Track")}, {"unrelated", "", "me", disk + "2"}, {"foreign", ws.ID, "them", disk}} {
		child := newTestWorkspace(tc.id, tc.id)
		child.ParentID = tc.parent
		child.OwnerUserID = tc.owner
		child.DirectoryReferences = []DirectoryReference{{WorkspaceID: tc.id, ID: "disk", Name: "Projects", Path: tc.path}}
		if err := store.Save(child); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest("GET", "/files-layout", nil)
	req.SetPathValue("workspaceID", ws.ID)
	res := httptest.NewRecorder()
	h.GetFilesLayout(res, req)
	var view struct {
		Folders []filesLink `json:"folders"`
		Local   bool        `json:"local_mac"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if res.Code != 200 || view.Local || strings.Contains(res.Body.String(), disk) || len(view.Folders) != 2 || view.Folders[0].Inside != nil || view.Folders[1].Inside == nil || *view.Folders[1].Inside != (filesPlacement{ws.ID, "disk", "albums/Track"}) {
		t.Fatalf("unexpected layout: %s", res.Body)
	}
}
