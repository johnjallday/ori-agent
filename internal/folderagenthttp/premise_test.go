package folderagenthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// Set it up is refused with a typed 409 while the premise is owed, and the
// premise route records the answer (confirm-and-visible-installs FR5, FR10).
func TestPremiseHTTP_RefusesSetUpUntilAnsweredAndRecordsTheAnswer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Songs")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	m, err := foldermap.Walk(root, foldermap.Options{})
	if err != nil {
		t.Fatal(err)
	}
	facts := foldermap.FactsFrom(m)
	facts.Shapes = []foldermap.ShapeCount{{Shape: "audio", Count: 6}}
	facts.Portfolio = &foldermap.Portfolio{Shape: "audio", Count: 6, HomeProviderKey: "music_project_management", HomeName: "Music Production Home"}
	sidecar := t.TempDir()
	store := folderagent.NewStore(func(string) (string, error) { return sidecar, nil })
	rec := &folderagent.Record{WorkspaceID: "ws", OwnerUserID: "u1", FolderName: "Songs", WorkspaceName: "Songs",
		State: folderagent.StateReported, Map: m, Facts: facts, Stage: foldermap.StageHome, AsksPremise: true}
	if err := store.Create(rec); err != nil {
		t.Fatal(err)
	}
	downloads := t.TempDir()
	service := folderagent.NewService(folderagent.Deps{Store: store, Workspaces: inboxWorkspace{root: root},
		KnownFolderRoot: func(string) (string, error) { return downloads, nil }})
	h := New(service, func(context.Context) (string, error) { return "u1", nil })

	view, err := service.View(context.Background(), "u1", "ws")
	if err != nil || view.Premise == nil || view.Premise.State != folderagent.PremiseAsked {
		t.Fatalf("view = %+v %v", view.Premise, err)
	}
	post := func(handler func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/ws", strings.NewReader(body))
		req.SetPathValue("workspaceID", "ws")
		response := httptest.NewRecorder()
		handler(response, req)
		return response
	}
	setup := post(h.SetUp, `{"plan_digest":"`+view.Plan.Digest+`"}`)
	if setup.Code != http.StatusConflict || !strings.Contains(setup.Body.String(), `"code":"premise_owed"`) ||
		!strings.Contains(setup.Body.String(), `"state":"asked"`) {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	if bad := post(h.Premise, `{"answer":"maybe"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad answer = %d", bad.Code)
	}
	answered := post(h.Premise, `{"answer":"confirmed"}`)
	var payload struct {
		FolderAgent folderagent.View `json:"folder_agent"`
	}
	if answered.Code != http.StatusOK || json.Unmarshal(answered.Body.Bytes(), &payload) != nil ||
		payload.FolderAgent.Premise == nil || payload.FolderAgent.Premise.State != folderagent.PremiseConfirmed {
		t.Fatalf("premise = %d %s", answered.Code, answered.Body.String())
	}
	if stored, _ := store.Read("ws"); stored.Premise == nil || stored.Premise.Answer != folderagent.PremiseConfirmed {
		t.Fatalf("stored = %+v", stored.Premise)
	}
	// Choose refuses unknown fields but accepts the page the folder is fed from.
	if strings.Contains(post(h.Choose, `{"picker":true,"from_workspace_id":"home-1"}`).Body.String(), "not valid") {
		t.Fatal("from_workspace_id must be accepted")
	}
}
