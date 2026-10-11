package mcphttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
)

func TestMCPBrowserReusesRegistryFilterAndPreservesSetupFields(t *testing.T) {
	store := mcpregistry.NewStoreAt(t.TempDir())
	if err := store.AddSource(mcpregistry.RegistrySource{ID: "example", Name: "Example", SourceType: "url", URL: "http://127.0.0.1:1/must-not-fetch", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	entry := mcpregistry.RegistryEntry{Name: "telegram", Description: "community helper", Category: "communication", Tags: []string{"messages"}, Source: "Example", SourceID: "example", Command: "never-run", Args: []string{"setup-arg"}}
	if err := store.SetCache([]mcpregistry.RegistryEntry{entry}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(mcp.NewRegistry(), mcp.NewConfigManager(t.TempDir()))
	handler.SetRegistryStore(store)
	rr := httptest.NewRecorder()
	handler.SearchServersHandler(rr, httptest.NewRequest(http.MethodGet, "/api/mcp/search?q=MESSAGES&category=COMMUNICATION&source=EXAMPLE", nil))
	if rr.Code != http.StatusOK || rr.Header().Get("X-Ori-Catalog-Availability") != "available" {
		t.Fatalf("search: %d %s", rr.Code, rr.Body.String())
	}
	var entries []mcpregistry.RegistryEntry
	if err := json.Unmarshal(rr.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "telegram" || entries[0].Command != "never-run" || entries[0].Args[0] != "setup-arg" {
		t.Fatalf("UI setup metadata changed: %+v", entries)
	}
}

func TestMCPBrowserFreshEmptyCacheDoesNotAutomaticallyRetryFailedSources(t *testing.T) {
	store := mcpregistry.NewStoreAt(t.TempDir())
	if err := store.AddSource(mcpregistry.RegistrySource{ID: "example", Name: "Example", SourceType: "url", URL: "http://127.0.0.1:1/must-not-fetch", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCache(nil); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(mcp.NewRegistry(), mcp.NewConfigManager(t.TempDir()))
	handler.SetRegistryStore(store)
	rr := httptest.NewRecorder()
	handler.SearchServersHandler(rr, httptest.NewRequest(http.MethodGet, "/api/mcp/search?source=Example", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "[]\n" || rr.Header().Get("X-Ori-Catalog-Availability") != "unavailable" {
		t.Fatalf("empty remote: %d %s %v", rr.Code, rr.Body.String(), rr.Header())
	}
}
