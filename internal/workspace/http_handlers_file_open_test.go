package workspace

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These tests intentionally exercise only the validation/error paths: the
// success path shells out to the OS (open/Finder), which must not run in tests.

func TestOpenWorkspaceFileRejectsTraversal(t *testing.T) {
	_, ws, handler := newFolderHandlerTest(t, "ws-open-traversal", "Open Traversal")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/files/open",
		bytes.NewBufferString(`{"relative_path":"../escape.txt"}`))
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceFile(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestOpenWorkspaceFileMissingReturns404(t *testing.T) {
	_, ws, handler := newFolderHandlerTest(t, "ws-open-missing", "Open Missing")

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+ws.ID+"/files/open",
		bytes.NewBufferString(`{"relative_path":"nope.txt"}`))
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceFile(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing file, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRevealWorkspaceFileRejectsWrongMethod(t *testing.T) {
	_, ws, handler := newFolderHandlerTest(t, "ws-reveal-method", "Reveal Method")

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/files/reveal", nil)
	rr := httptest.NewRecorder()
	handler.RevealWorkspaceFile(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestNearestExistingDirWalksUpToRoot(t *testing.T) {
	root := t.TempDir()

	// A path several levels below root, none of which exist, resolves to root.
	deep := root + "/a/b/c"
	if got := nearestExistingDir(deep, root); got != root {
		t.Fatalf("expected nearest existing dir to be root %q, got %q", root, got)
	}

	// A path escaping the root must not resolve outside it.
	if got := nearestExistingDir("/etc/somewhere", root); got != root && got != "" {
		t.Fatalf("expected bounded result (root or empty), got %q", got)
	}
}
