package folderagenthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

type inboxWorkspace struct {
	folderagent.Workspaces
	root string
}

func (w inboxWorkspace) AnchorRoot(string) (string, error) { return w.root, nil }
func (w inboxWorkspace) Route(string) string               { return "/workspaces/downloads" }

func TestDownloadsHTTP_LegacyReportAndStaleConfirmation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Downloads")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	m, err := foldermap.Walk(root, foldermap.Options{})
	if err != nil {
		t.Fatal(err)
	}
	sidecar := t.TempDir()
	store := folderagent.NewStore(func(string) (string, error) { return sidecar, nil })
	rec := &folderagent.Record{WorkspaceID: "ws", OwnerUserID: "u1", FolderName: "Downloads", WorkspaceName: "Downloads", State: folderagent.StateReported, Map: m, Facts: foldermap.FactsFrom(m), Candidates: []folderlook.Candidate{{ID: "home:old", Kind: folderlook.KindHomeProvider}}}
	if err := store.Create(rec); err != nil {
		t.Fatal(err)
	}
	service := folderagent.NewService(folderagent.Deps{Store: store, Workspaces: inboxWorkspace{root: root}, KnownFolderRoot: func(string) (string, error) { return root, nil }})
	h := New(service, func(context.Context) (string, error) { return "u1", nil })
	before, _ := store.Read("ws")
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.SetPathValue("workspaceID", "ws")
	response := httptest.NewRecorder()
	h.Get(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Downloads stays an inbox") || strings.Contains(response.Body.String(), "home:old") {
		t.Fatalf("get=%d %s", response.Code, response.Body.String())
	}
	after, _ := store.Read("ws")
	if before.Revision != after.Revision {
		t.Fatal("GET wrote")
	}
	req = httptest.NewRequest(http.MethodPost, "/ws/setup", strings.NewReader(`{"plan_digest":"old","selected":["group","home:old"]}`))
	req.SetPathValue("workspaceID", "ws")
	response = httptest.NewRecorder()
	h.SetUp(response, req)
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"plan_changed":true`) || !strings.Contains(response.Body.String(), "file-curator") {
		t.Fatalf("setup=%d %s", response.Code, response.Body.String())
	}
	other := New(service, func(context.Context) (string, error) { return "other", nil })
	response = httptest.NewRecorder()
	other.Get(response, req)
	if response.Code != 404 {
		t.Fatalf("owner=%d", response.Code)
	}
}
