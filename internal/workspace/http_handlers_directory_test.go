package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/pathselection"
)

func createDirectory(t *testing.T, handler *HTTPHandler, workspaceID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/directories", strings.NewReader(body))
	request.SetPathValue("workspaceID", workspaceID)
	recorder := httptest.NewRecorder()
	handler.CreateDirectory(recorder, request)
	return recorder
}

// A typed path is linked by its selection token: the folder-picker endpoint
// checked the path and bound the token to this workspace, so the link call
// carries no path at all.
func TestCreateDirectory_LinksAFolderByItsSelectionToken(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	selections := pathselection.NewStore()
	handler.SetTrustedPathSelectionIssuer(selections)
	for _, id := range []string{"ws-1", "ws-2"} {
		if err := store.Save(&Workspace{ID: id, Name: id, Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	folder := t.TempDir()
	token, err := selections.IssueFor(folder, "ws-1")
	if err != nil {
		t.Fatal(err)
	}

	// Bound to ws-1: another workspace cannot spend it, and an unknown token
	// links nothing.
	for name, attempt := range map[string]struct{ workspace, body string }{
		"another workspace":  {"ws-2", `{"selection_token":"` + token + `"}`},
		"an unknown token":   {"ws-1", `{"selection_token":"not-a-token"}`},
		"a token and a path": {"ws-1", `{"selection_token":"` + token + `","path":"/etc"}`},
	} {
		recorder := createDirectory(t, handler, attempt.workspace, attempt.body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), folder) {
			t.Fatalf("%s: the refusal repeats the folder: %s", name, recorder.Body.String())
		}
	}
	for _, id := range []string{"ws-1", "ws-2"} {
		if ws, _ := store.Get(id); ws == nil || len(ws.DirectoryReferences) != 0 {
			t.Fatalf("%s gained a folder from a refused link: %+v", id, ws)
		}
	}

	recorder := createDirectory(t, handler, "ws-1", `{"selection_token":"`+token+`"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Directory DirectoryReference `json:"directory"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	ws, err := store.Get("ws-1")
	if err != nil || len(ws.DirectoryReferences) != 1 {
		t.Fatalf("workspace=%+v err=%v", ws, err)
	}
	linked := ws.DirectoryReferences[0]
	if linked.Path != folder || response.Directory.Path != folder || linked.Name == "" || !strings.HasSuffix(folder, linked.Name) {
		t.Fatalf("linked=%+v, want the selected folder named after itself", linked)
	}
}

func TestCreateDirectory_SelectionTokenNeedsTheSelectionStore(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	if err := store.Save(&Workspace{ID: "ws-1", Name: "ws-1", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	if recorder := createDirectory(t, handler, "ws-1", `{"selection_token":"anything"}`); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// The picker helper's own callback still names the folder and its path.
func TestCreateDirectory_NameAndPathStillWork(t *testing.T) {
	store := NewInMemoryStore()
	handler := NewHTTPHandler(store, nil, nil)
	if err := store.Save(&Workspace{ID: "ws-1", Name: "ws-1", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	body, err := json.Marshal(map[string]string{"name": "Songs", "path": folder})
	if err != nil {
		t.Fatal(err)
	}
	if recorder := createDirectory(t, handler, "ws-1", string(body)); recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := createDirectory(t, handler, "ws-1", `{"path":"`+folder+`"}`); recorder.Code != http.StatusBadRequest {
		t.Fatalf("a path with no name: status=%d", recorder.Code)
	}
}
