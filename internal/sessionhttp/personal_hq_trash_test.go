package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func TestPersonalHQTrashReviewsAndMovesOnlyTheCurrentManagedFolder(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("isolated path-based system Trash is tested on macOS and Linux")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	f, cleanup := newPersonalHQImportHarness(t)
	defer cleanup()
	h := f.handler
	h.SetPersonalHQTrashLookup(func(ctx context.Context) (*personalhq.Status, error) {
		return f.service.Status(ctx, userprofile.LocalUserID)
	})
	ctx := context.Background()
	hqID := createTestWorkspace(t, h, "My HQ")
	otherID := createTestWorkspace(t, h, "My project")
	if _, err := f.service.Designate(ctx, userprofile.LocalUserID, hqID); err != nil {
		t.Fatal(err)
	}
	folder, err := f.fileStore.GetFolderPath(hqID)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(folder, "linked-project")); err != nil {
		t.Fatal(err)
	}
	request := func(id, query, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/workspaces/"+id+"?trash_personal_hq=true"+query, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		h.HandleWorkspaces(w, r)
		if w.Code != want {
			t.Fatalf("%q got %d want %d: %s", query, w.Code, want, w.Body.String())
		}
		return w
	}
	request(otherID, "", "", http.StatusConflict)
	w := request(hqID, "", "", http.StatusOK)
	var review struct {
		FolderPath string `json:"folder_path"`
	}
	canonical, err := filepath.EvalSymlinks(folder)
	if err != nil || json.Unmarshal(w.Body.Bytes(), &review) != nil || review.FolderPath != canonical {
		t.Fatalf("expected reviewed folder %q, got %s (%v)", canonical, w.Body.String(), err)
	}
	request(hqID, "&confirm=true", `{"folder_path":"/not-reviewed"}`, http.StatusConflict)
	if _, err := os.Stat(folder); err != nil {
		t.Fatalf("review or stale confirmation moved folder: %v", err)
	}
	payload, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	request(hqID, "&confirm=true&delete_sessions=true", string(payload), http.StatusConflict)
	request(hqID, "&confirm=true", string(payload), http.StatusOK)
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("original folder must be gone: %v", err)
	}
	row, err := h.store.GetWorkspace(ctx, hqID)
	if err != nil || row.Status != session.WorkspaceStatusTrashed {
		t.Fatalf("workspace must be restorable from Trash: %+v %v", row, err)
	}
	_, trashed := workspaceTrashPaths(row)
	if target, err := os.Readlink(filepath.Join(trashed, "linked-project")); err != nil || target != outside {
		t.Fatalf("only the link should have moved: %q %v", target, err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("linked target must still exist: %v", err)
	}
	if status := f.status(t); status.Valid {
		t.Fatalf("trashed HQ is still active: %+v", status)
	}
	request(hqID, "&confirm=true", string(payload), http.StatusConflict)
	w = httptest.NewRecorder()
	h.HandleWorkspaces(w, httptest.NewRequest(http.MethodPost, "/api/workspaces/"+hqID+"/restore", nil))
	if w.Code != http.StatusOK || !f.status(t).Valid {
		t.Fatalf("existing workspace restore should make Personal HQ active again: %d %s", w.Code, w.Body.String())
	}
}
