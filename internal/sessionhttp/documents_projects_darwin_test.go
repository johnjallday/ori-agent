//go:build darwin

package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filesearch"
	"github.com/johnjallday/ori-agent/internal/folderagent"
)

func TestProjectHandoffRefusesReplacementAndForeignRequest(t *testing.T) {
	h, managed, _, cleanup := templateTestEnv(t)
	defer cleanup()
	home := canonicalAnchorTestDir(t)
	root := filepath.Join(home, "Documents")
	course := filepath.Join(root, "School", "BIO 101")
	seed := func() {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(course, "Labs"), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(course, "Syllabus.pdf"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	seed()
	policy := filesearch.Policy{Home: home, Protected: []string{managed}}
	local := true
	h.SetGuidedDocumentsLook(func(_ *http.Request, owner string) bool { return local && owner == "local" }, policy.DocumentsWitness)
	material, _ := documentsCreationMaterial(t, h, "Shelves", root)
	op, digest := reviewCreationOperation(t, h, material)
	w, result := creationOperationRequest(t, h, "POST", "/"+op+"/confirm", map[string]any{"review_digest": digest})
	if w.Code != 201 {
		t.Fatal(w.Body)
	}
	id := result["folder"].(map[string]any)["id"].(string)
	call := func(q projectAction) (int, folderagent.ProjectLook) {
		t.Helper()
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.HandleWorkspaces(w, httptest.NewRequest("POST", "/api/workspaces/"+id+"/documents-projects", bytes.NewReader(b)))
		var look folderagent.ProjectLook
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &look); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, look
	}
	_, choices := call(projectAction{Action: "folders"})
	if len(choices.Folders) != 2 {
		t.Fatal(choices)
	}
	p := choices.Folders[1].Ref
	_, look := call(projectAction{Action: "find", Parent: p})
	if len(look.Suggestions) != 1 {
		t.Fatal(look)
	}
	open := projectAction{Action: "open", Parent: p, Candidate: look.Suggestions[0].Ref}
	local = false
	if code, _ := call(open); code != 403 {
		t.Fatal(code)
	}
	local = true
	if err := os.Rename(course, course+"-moved"); err != nil {
		t.Fatal(err)
	}
	seed()
	if code, _ := call(open); code != 409 {
		t.Fatal("accepted replacement", code)
	}
	ids, err := h.workspaceStore.List()
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
}
