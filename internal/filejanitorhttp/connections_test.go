package filejanitorhttp

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// listingFakeStore adds the List capability connections use to find the other
// side. It is separate from fakeStore so existing tests keep their behavior.
type listingFakeStore struct{ *fakeStore }

func (l listingFakeStore) List() ([]string, error) {
	ids := make([]string, 0, len(l.workspaces))
	for id := range l.workspaces {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// connectionsHandler is Downloads (ws-1) and Documents (ws-2) with Filing set up
// on sibling folders, Music (ws-3) without Filing, and someone else's workspace.
func connectionsHandler(t *testing.T) (*Handler, map[string]string) {
	t.Helper()
	store := &fakeStore{root: t.TempDir(), workspaces: map[string]*workspace.Workspace{}}
	for id, name := range map[string]string{"ws-1": "Downloads", "ws-2": "Documents", "ws-3": "Music", "ws-other": "Theirs"} {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: name})
		ws.ID = id
		ws.OwnerUserID = userprofile.LocalUserID
		if id == "ws-other" {
			ws.OwnerUserID = "someone-else"
		}
		store.workspaces[id] = ws
	}
	listing := listingFakeStore{store}
	service := filejanitor.NewService(filejanitor.NewStore(store), listing)
	h := NewHandler(service, store, fixedUser(userprofile.LocalUserID))

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{}
	for id, dir := range map[string]string{"ws-1": "Downloads", "ws-2": "Documents"} {
		root := filepath.Join(base, dir)
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmSetup(filejanitor.SetupRequest{WorkspaceID: id, Path: root}); err != nil {
			t.Fatalf("setup %s: %v", id, err)
		}
		store.workspaces[id].InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace}}
		roots[id] = root
	}
	if err := service.ConditionalMoveBetween(roots["ws-1"], "", roots["ws-2"], ""); err != nil && strings.Contains(err.Error(), "cannot be filed into safely on this system") {
		t.Skip("this system cannot send files safely between folders")
	}
	return h, roots
}

func noRootIn(t *testing.T, body string, roots map[string]string) {
	t.Helper()
	for id, root := range roots {
		if strings.Contains(body, root) {
			t.Fatalf("response carries %s's folder path: %s", id, body)
		}
	}
}

func TestConnectionsAPI_ConnectListAndTurnOffFromTheReceiver(t *testing.T) {
	h, roots := connectionsHandler(t)

	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/connections",
		`{"receiver_workspace_id":"ws-2","kinds":["documents"]}`)
	if rec.Code != http.StatusOK {
		t.Skipf("this system cannot turn a connection on (%d): %s", rec.Code, rec.Body.String())
	}
	noRootIn(t, rec.Body.String(), roots)
	connections, _ := body["connections"].(map[string]any)
	sendsTo, _ := connections["sends_to"].([]any)
	if len(sendsTo) != 1 {
		t.Fatalf("sends_to = %v", connections["sends_to"])
	}
	first, _ := sendsTo[0].(map[string]any)
	if first["on"] != true || first["receiver_name"] != "Documents" {
		t.Fatalf("connection = %v", first)
	}
	id, _ := first["id"].(string)

	for _, prefix := range []string{"file-janitor", "downloads-janitor"} {
		rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-2/"+prefix+"/connections", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s list: %d %s", prefix, rec.Code, rec.Body.String())
		}
		noRootIn(t, rec.Body.String(), roots)
		connections, _ := body["connections"].(map[string]any)
		if receives, _ := connections["receives_from"].([]any); len(receives) != 1 {
			t.Fatalf("%s: the receiver should list one connection: %v", prefix, connections)
		}
	}

	rec, body = serve(t, h, http.MethodPost, "/api/workspaces/ws-2/file-janitor/connections/"+id+"/turn-off", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("turn off: %d %s", rec.Code, rec.Body.String())
	}
	connections, _ = body["connections"].(map[string]any)
	if receives, _ := connections["receives_from"].([]any); len(receives) != 0 {
		t.Fatalf("still listed after turning off: %v", connections)
	}
}

// A refusal is one plain sentence with a stable code, and carries no path.
func TestConnectionsAPI_RefusalsAreOnePlainSentence(t *testing.T) {
	h, roots := connectionsHandler(t)
	cases := []struct {
		name, body, want string
	}{
		{"a workspace with no Filing", `{"receiver_workspace_id":"ws-3","kinds":["documents"]}`, "Music has no Filing set up on its folder"},
		{"no kinds", `{"receiver_workspace_id":"ws-2","kinds":[]}`, "Tick at least one kind"},
		{"itself", `{"receiver_workspace_id":"ws-1","kinds":["documents"]}`, "another workspace"},
		{"someone else's workspace", `{"receiver_workspace_id":"ws-other","kinds":["documents"]}`, "That workspace is not available."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/connections", tc.body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
			}
			noRootIn(t, rec.Body.String(), roots)
			message, _ := body["message"].(string)
			if body["code"] != filejanitor.CodeConnectionRefused || !strings.Contains(message, tc.want) {
				t.Fatalf("error = %v, want code %s and %q", body, filejanitor.CodeConnectionRefused, tc.want)
			}
			if strings.Contains(strings.ToLower(message), "linked") || strings.Count(message, ". ") > 1 {
				t.Fatalf("not one plain sentence: %q", message)
			}
		})
	}
}

func TestConnectionsAPI_RequestShapesAreClosed(t *testing.T) {
	h, _ := connectionsHandler(t)
	for name, body := range map[string]string{
		"a folder path":   `{"receiver_workspace_id":"ws-2","kinds":["documents"],"path":"/tmp"}`,
		"not json":        `receiver=ws-2`,
		"two requests":    `{"receiver_workspace_id":"ws-2","kinds":["documents"]}{}`,
		"too many kinds":  `{"receiver_workspace_id":"ws-2","kinds":["documents","images","audio","video","archives","installers","data","other","documents"]}`,
		"a kinds string":  `{"receiver_workspace_id":"ws-2","kinds":"documents"}`,
		"a receiver list": `{"receiver_workspace_id":["ws-2"],"kinds":["documents"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/connections", body); rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/connections/connection-1/turn-off", `{"x":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("turn off with a body: %d", rec.Code)
	}
}
