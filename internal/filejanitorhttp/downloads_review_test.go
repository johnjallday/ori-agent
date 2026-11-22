package filejanitorhttp

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const downloadsReviewAPI = "/api/workspaces/ws-1/file-janitor"

func downloadsReviewFixture(t *testing.T) (*Handler, *fakeStore, string) {
	t.Helper()
	h, store, root, reference := setupReviewFixture(t)
	ws := store.workspaces["ws-1"]
	ws.SharedData = map[string]any{workspace.AnchorDirectoryReferenceIDKey: reference}
	h.SetDownloadsRoot(func(owner, id string) (string, error) {
		if owner != "local" || id != "ws-1" {
			return "", errors.New("unverified")
		}
		return root, nil
	})
	return h, store, root
}

func TestDownloadsReviewRequiresSeparateAccessAndScan(t *testing.T) {
	h, store, root := downloadsReviewFixture(t)
	get := func(want string) {
		t.Helper()
		rec, body := serve(t, h, http.MethodGet, downloadsReviewAPI+"?review=downloads", "")
		if rec.Code != http.StatusOK || body["status"].(map[string]any)["downloads_review"] != want {
			t.Fatalf("got %d %s, want %q", rec.Code, rec.Body.String(), want)
		}
	}
	get("access")
	if _, err := os.Stat(filepath.Join(root, "Filed")); !os.IsNotExist(err) || len(store.workspaces["ws-1"].MCPBindings) != 0 {
		t.Fatal("opening review granted access")
	}
	rec, _ := serve(t, h, http.MethodPost, downloadsReviewAPI+"/setup?review=downloads", fmt.Sprintf(`{"path":%q}`, root))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("accepted a client path: %d %s", rec.Code, rec.Body.String())
	}
	for range 2 {
		rec, _ = serve(t, h, http.MethodPost, downloadsReviewAPI+"/setup?review=downloads", `{}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("grant failed: %d %s", rec.Code, rec.Body.String())
		}
	}
	get("ready")
	status, err := h.service.Status("ws-1")
	if err != nil || !status.Settings.ManualScansOnly() || !status.Settings.Paused || len(store.workspaces["ws-1"].MCPBindings) == 0 {
		t.Fatal("grant was not manual", status, err)
	}
	_, batch := serve(t, h, http.MethodGet, downloadsReviewAPI+"/batches/latest", "")
	if batch["batch"] != nil {
		t.Fatal("grant also scanned")
	}
	if _, err := h.service.RevokeAccess(nil, "ws-1"); err != nil {
		t.Fatal(err)
	}
	get("access")
}

func TestDownloadsReviewDoesNotReadOrRelinkOtherGrant(t *testing.T) {
	h, _, _ := downloadsReviewFixture(t)
	other, err := filepath.EvalSymlinks(inboxFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	configured, _ := serve(t, h, http.MethodPost, downloadsReviewAPI+"/setup", fmt.Sprintf(`{"path":%q,"paused":true}`, other))
	if configured.Code != http.StatusOK {
		t.Fatalf("fixture setup: %d %s", configured.Code, configured.Body.String())
	}
	for _, r := range []struct{ method, endpoint string }{
		{http.MethodGet, downloadsReviewAPI}, {http.MethodPost, downloadsReviewAPI + "/setup"},
	} {
		rec, _ := serve(t, h, r.method, r.endpoint+"?review=downloads", `{}`)
		if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), other) || !strings.Contains(rec.Body.String(), "different folder") {
			t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
		}
	}
	status, err := h.service.Status("ws-1")
	if err != nil || status.Settings.RootPath != other {
		t.Fatal("wrong grant changed", status, err)
	}
}
