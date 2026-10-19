package filejanitorhttp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func setupReviewFixture(t *testing.T) (*Handler, *fakeStore, string, string) {
	t.Helper()
	h, store := newTestHandler(t, map[string]string{"ws-1": "local", "foreign": "other"})
	root := inboxFixture(t)
	ws := store.workspaces["ws-1"]
	if _, err := ws.AddInstalledCapability(workspace.InstalledCapability{
		ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace,
	}); err != nil {
		t.Fatal(err)
	}
	if err := ws.AddDirectoryReference(workspace.DirectoryReference{Name: "Inbox", Path: root}); err != nil {
		t.Fatal(err)
	}
	return h, store, root, ws.DirectoryReferences[0].ID
}

func TestSetupReview_OwnerOnlyReadAndSelectionGrantNothing(t *testing.T) {
	h, store, root, reference := setupReviewFixture(t)
	for _, prefix := range []string{"file-janitor", "downloads-janitor"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rec, body := serve(t, h, method, "/api/workspaces/ws-1/"+prefix+"/setup-review", fmt.Sprintf(`{"reference_id":%q,"monitoring":true}`, reference))
			if rec.Code != http.StatusOK {
				t.Fatalf("review: %d %s", rec.Code, rec.Body.String())
			}
			review := body["review"].(map[string]any)
			if review["state"] != "review" || review["review_revision"] == "" || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("review = %+v", review)
			}
			if review["monitoring"] != (method == http.MethodPost) {
				t.Fatal("GET must default monitoring off")
			}
		}
	}
	for _, path := range []string{filepath.Join(root, "Filed"), filepath.Join(store.root, "ws-1", filejanitor.LegacyStateDirName)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("review wrote %s: %v", path, err)
		}
	}
	if len(store.workspaces["ws-1"].MCPBindings) != 0 {
		t.Fatal("review granted tools")
	}
}

func TestSetupReview_RefusesForeignOwnerAndUnknownMutationFields(t *testing.T) {
	h, _, root, reference := setupReviewFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec, _ := serve(t, h, method, "/api/workspaces/foreign/file-janitor/setup-review", `{}`)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), root) {
			t.Fatalf("foreign response = %d %s", rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{
		`{"reference_id":"unknown"}`, `{"path":"/tmp/arbitrary"}`, `{"owner_user_id":"local"}`,
		`{"create_now":true}`, `{"setup_decision":{"mode":"manual"}}`, `{}` + `{}`,
		fmt.Sprintf(`{"reference_id":%q,"paused":false}`, reference),
	} {
		rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/setup-review", body)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusConflict {
			t.Fatalf("accepted unknown authority %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestPrepareReview_ExplicitConfirmationAndResponseLossReplay(t *testing.T) {
	h, _, root, _ := setupReviewFixture(t)
	file := filepath.Join(root, "invoice.pdf")
	if err := os.WriteFile(file, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	_, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/setup-review", "")
	review := body["review"].(map[string]any)
	request := fmt.Sprintf(`{"reference_id":%q,"monitoring":false,"review_revision":%q}`, review["reference_id"], review["review_revision"])
	first, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/prepare-review", request)
	if first.Code != http.StatusOK {
		t.Fatalf("prepare = %d %s", first.Code, first.Body.String())
	}
	prepared := body["preparation"].(map[string]any)
	for range 2 {
		replayed, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/prepare-review", request)
		if replayed.Code != http.StatusOK {
			t.Fatalf("replay = %d %s", replayed.Code, replayed.Body.String())
		}
		got := body["preparation"].(map[string]any)
		if got["operation_id"] != prepared["operation_id"] || got["result"].(map[string]any)["batch_id"] != prepared["result"].(map[string]any)["batch_id"] {
			t.Fatal("lost response created a different operation/result")
		}
	}
	_, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/setup-review", "")
	if body["review"].(map[string]any)["state"] != "prepared" {
		t.Fatalf("refresh lost result: %+v", body)
	}
}

func TestStopPreparation_ExactOwnerOperationKeepsSavedSetup(t *testing.T) {
	h, _, root, _ := setupReviewFixture(t)
	_, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/setup-review", "")
	review := body["review"].(map[string]any)
	request := fmt.Sprintf(`{"reference_id":%q,"monitoring":false,"review_revision":%q}`, review["reference_id"], review["review_revision"])
	first, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/prepare-review", request)
	if first.Code != http.StatusOK {
		t.Fatalf("prepare: %s", first.Body.String())
	}
	operationID := body["preparation"].(map[string]any)["operation_id"]
	stop, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/stop-preparation", fmt.Sprintf(`{"operation_id":%q}`, operationID))
	if stop.Code != http.StatusOK {
		t.Fatalf("stop: %s", stop.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); err != nil {
		t.Fatal("stop removed Filed")
	}
	replay, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/prepare-review", request)
	if replay.Code != http.StatusConflict {
		t.Fatalf("stop resumed: %d %s", replay.Code, replay.Body.String())
	}
}

func TestPrepareReview_OmittedConsentAndModelFieldsGrantNothing(t *testing.T) {
	h, _, root, _ := setupReviewFixture(t)
	for _, request := range []string{`{}`, `{"create_now":true}`, `{"ready":true}`, `{"path":"/tmp/inbox"}`} {
		rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/file-janitor/prepare-review", request)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("manufactured consent: %d %s", rec.Code, rec.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) {
		t.Fatal("refusal created Filed")
	}
}

func TestReviewedSetupErrors_DoNotExposeUnexpectedPathsOrAnotherOwnerName(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("read /private/another-owner/secret-folder: denied"),
		&filejanitor.SetupError{Code: filejanitor.CodeFolderConflict, Message: "Private Other Workspace owns the overlapping folder"},
	} {
		w := httptest.NewRecorder()
		respondReviewedSetupError(w, err, "setup_unavailable", "The current review cannot be read. Keep this workspace and retry.")
		if strings.Contains(w.Body.String(), "secret-folder") || strings.Contains(w.Body.String(), "Private Other Workspace") {
			t.Fatalf("private error escaped: %s", w.Body.String())
		}
	}
}

func TestSetupReview_RefusesRemovedCapabilityOrInactiveWorkspace(t *testing.T) {
	for _, change := range []string{"removed", "archived"} {
		t.Run(change, func(t *testing.T) {
			h, store, _, _ := setupReviewFixture(t)
			ws := store.workspaces["ws-1"]
			if change == "archived" {
				ws.Status = workspace.StatusCompleted
			} else {
				ws.RemoveInstalledCapability(workspace.CapabilityFileJanitor)
			}
			rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/file-janitor/setup-review", "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("inactive review = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
