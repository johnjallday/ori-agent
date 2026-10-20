package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
)

type staticPluginLister []plugin.InstalledPlugin

func (l staticPluginLister) List() ([]plugin.InstalledPlugin, error) { return l, nil }

func catalogContractFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "resourcecatalog", "testdata", "contract", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// newCatalogHandler wires the catalog the way the server does, with a fake
// plugin call standing in for the REAPER service.
func newCatalogHandler(t *testing.T) (*Handler, *resourceSummaryFixture, string) {
	t.Helper()
	fixture := newResourceSummaryFixture(t)
	studio := fixture.linkStudio(t)
	reaper := &fixture.installed[1]
	reaper.WorkspaceSurfaces = &plugin.SurfaceContribution{ResourceCatalog: catalogContractFile(t, "declaration.valid.json")}
	reaper.ResolvedArtifacts = []plugin.ResolvedArtifact{{ServiceID: "reaper-service", Available: true}}
	reaper.ResolvedBlueprints[0].Template.AssistantProject.ID = "reaper-studio-team"
	h := &Handler{
		workspaceTaskStore:    fixture.store,
		installedPluginLister: staticPluginLister(fixture.installed),
		currentUserID:         func(context.Context) (string, error) { return "local", nil },
	}
	h.SetResourceCatalogDataDir(t.TempDir())
	h.SetResourceCatalogCall(func(_ context.Context, installed plugin.InstalledPlugin, which string, input json.RawMessage) (json.RawMessage, error) {
		if installed.Name != "reaper-plugin" {
			t.Fatalf("called %s", installed.Name)
		}
		if which == plugin.ResourceCatalogSources {
			return catalogContractFile(t, "sources.valid.json"), nil
		}
		var request map[string]any
		_ = json.Unmarshal(input, &request)
		empty := ""
		return json.Marshal(map[string]any{
			"schema_version": 1, "installation_id": request["installation_id"], "source_id": request["source_id"], "state": "ok",
			"entries":     []map[string]any{{"native_id": "One." + request["source_id"].(string), "kind": kindFor(request["source_id"].(string)), "name": "One", "facts": []any{}}},
			"next_cursor": empty, "coverage": map[string]any{"visited": 1, "skipped": []any{}},
		})
	})
	return h, fixture, studio.ID
}

func kindFor(sourceID string) string {
	switch sourceID {
	case "project_templates":
		return "project_template"
	case "track_templates":
		return "track_template"
	case "audio_plugins":
		return "effect"
	default:
		return "script"
	}
}

func catalogCall(t *testing.T, handler http.HandlerFunc, method, path string, values map[string]string, body string) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	for key, value := range values {
		request.SetPathValue(key, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	var decoded map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &decoded)
	return recorder.Code, decoded
}

func TestResourceCatalogHTTP_ReviewApproveInspectAndBrowse(t *testing.T) {
	h, fixture, studioID := newCatalogHandler(t)
	path := map[string]string{"workspaceID": studioID}

	code, view := catalogCall(t, h.GetResourceCatalog, http.MethodGet, "/api/workspaces/x/resource-catalog", path, "")
	if code != http.StatusOK || view["can_inspect"] != true || view["approved"] != false {
		t.Fatalf("overview = %d %v", code, view)
	}
	code, review := catalogCall(t, h.ReviewResourceCatalogSources, http.MethodPost, "/review", path, `{}`)
	if code != http.StatusOK || review["review_token"] == "" {
		t.Fatalf("review = %d %v", code, review)
	}
	commit, _ := json.Marshal(map[string]any{"review_token": review["review_token"], "idempotency_key": "approve-1", "source_ids": []string{"project_templates", "reaper_scripts"}})
	code, started := catalogCall(t, h.CommitResourceCatalogSources, http.MethodPost, "/commit", path, string(commit))
	if code != http.StatusOK {
		t.Fatalf("commit = %d %v", code, started)
	}
	operationID := started["operation"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, polled := catalogCall(t, h.GetResourceCatalogInspection, http.MethodGet, "/inspections", map[string]string{"workspaceID": studioID, "operationID": operationID}, "")
		if state := polled["operation"].(map[string]any)["state"]; state != "running" {
			if state != "published" {
				t.Fatalf("operation = %v", polled)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the inspection never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	code, page := catalogCall(t, h.ListResourceCatalogEntries, http.MethodGet, "/api/workspaces/x/resource-catalog/entries?category=templates", path, "")
	if code != http.StatusOK || page["total"] != float64(1) {
		t.Fatalf("entries = %d %v", code, page)
	}
	_ = fixture
}

func TestResourceCatalogHTTP_RefusesWhatIsNotTheOwnersCatalog(t *testing.T) {
	h, fixture, studioID := newCatalogHandler(t)
	if code, _ := catalogCall(t, h.GetResourceCatalog, http.MethodGet, "/", map[string]string{"workspaceID": fixture.home.ID}, ""); code != http.StatusNotFound {
		t.Fatalf("the Home's catalog = %d", code)
	}
	if code, _ := catalogCall(t, h.ReviewResourceCatalogSources, http.MethodPost, "/", map[string]string{"workspaceID": studioID}, `{"path":"/Users"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d", code)
	}
	if code, _ := catalogCall(t, h.ListResourceCatalogEntries, http.MethodGet, "/x?q=a&q=b", map[string]string{"workspaceID": studioID}, ""); code != http.StatusBadRequest {
		t.Fatalf("a repeated query key = %d", code)
	}
	h.currentUserID = func(context.Context) (string, error) { return "someone-else", nil }
	if code, _ := catalogCall(t, h.GetResourceCatalog, http.MethodGet, "/", map[string]string{"workspaceID": studioID}, ""); code != http.StatusNotFound {
		t.Fatalf("another owner = %d", code)
	}
}
