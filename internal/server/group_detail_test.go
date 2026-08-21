package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGroupWorkspaceServesWorkspaceDetailPage locks in the page-merge behavior:
// a group's /workspaces/{id} URL renders the standard workspace-detail page
// (group-specific UI is handled client-side from the loaded workspace's kind),
// not a separate group page.
func TestGroupWorkspaceServesWorkspaceDetailPage(t *testing.T) {
	// Sandbox HOME so the workspace root (~/Ori Workspaces) — where group
	// creation writes its folder — resolves inside the test directory instead
	// of the developer's real workspace root.
	t.Setenv("HOME", t.TempDir())
	handler := newRoutesTestHandler(t)

	body := bytes.NewBufferString(`{"name":"Routing Group","kind":"group"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create group: got %d, want 201: %s", createRec.Code, createRec.Body.String())
	}
	var resp struct {
		Folder struct {
			ID         string `json:"id"`
			FolderSlug string `json:"folder_slug"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if resp.Folder.ID == "" || resp.Folder.FolderSlug == "" {
		t.Fatalf("expected created group route identity in response: %s", createRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+resp.Folder.FolderSlug, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected group page route to return 200, got %d", rec.Code)
	}
	page := rec.Body.String()
	if !strings.Contains(page, "js/modules/workspace-detail.js") {
		t.Fatalf("expected group URL to render the workspace-detail page")
	}
	if strings.Contains(page, "group-detail.css") {
		t.Fatalf("group URL still renders the retired group-detail page")
	}
}
