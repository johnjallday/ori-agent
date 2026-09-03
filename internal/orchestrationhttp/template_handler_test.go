package orchestrationhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
)

// Instantiating a template writes tasks, and tasks need a workspace to live in.
// A request without one used to fall through to a separate engine that spun up a
// throwaway "collab-*" workspace and returned before any task had run. That path
// is gone, so the missing field is now reported as the client error it always was
// rather than silently taking a different route.
func TestInstantiateTemplateRequiresWorkspaceID(t *testing.T) {
	th := NewTemplateHandler(nil, nil, templates.NewTemplateManager(t.TempDir()), nil)

	body := bytes.NewBufferString(`{"template_id":"tpl-1","agent_name":"Manager"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/templates/instantiate", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	th.InstantiateTemplateHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without workspace_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("workspace_id")) {
		t.Fatalf("error should name the missing field, got %s", rec.Body.String())
	}
}

// The guard runs before the template is looked up, so a blank workspace_id is
// rejected on its own terms rather than surfacing as a template error.
func TestInstantiateTemplateRejectsBlankWorkspaceID(t *testing.T) {
	th := NewTemplateHandler(nil, nil, templates.NewTemplateManager(t.TempDir()), nil)

	body := bytes.NewBufferString(`{"template_id":"tpl-1","workspace_id":"   "}`)
	req := httptest.NewRequest(http.MethodPost, "/api/orchestration/templates/instantiate", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	th.InstantiateTemplateHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a whitespace workspace_id, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}
