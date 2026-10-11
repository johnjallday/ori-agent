package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
)

func TestPersonalAssistantDiscoveryReusesActualUIOwnersWithoutStartupOrMutation(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Skills", "telegram")
	if err := os.MkdirAll(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("---\nname: telegram\ndescription: community assistance\n---\nPRIVATE_PROMPT_SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := skills.NewManager(skills.ManagerConfig{AgentStorePath: filepath.Join(root, "agents.json"), PersonalSkillsDir: filepath.Join(root, "Skills")})
	registry := mcp.NewRegistry()
	if err := registry.AddServer(mcp.ServerConfig{Name: "telegram", Command: "never-run-this-test-server", Enabled: true, Env: map[string]string{"TOKEN": "PRIVATE_CONFIG_SENTINEL"}}); err != nil {
		t.Fatal(err)
	}
	catalog := mcpregistry.NewStoreAt(root)
	if err := catalog.AddSource(mcpregistry.RegistrySource{ID: "fixture", Name: "Example Catalog", URL: "http://127.0.0.1:1/must-not-fetch", Enabled: true, SourceType: "url"}); err != nil {
		t.Fatal(err)
	}
	mcpHandler := mcphttp.NewHandler(registry, mcp.NewConfigManager(root))
	mcpHandler.SetRegistryStore(catalog)
	srv := &Server{Handlers: &HandlerFacade{Skills: skillshttp.New(manager, nil, nil, nil), MCP: mcpHandler}, Integration: &IntegrationSystemFacade{MCPRegistry: registry}}
	handler := srv.newHomeAssistantAskHandler()
	if handler.Discovery == nil {
		t.Fatal("production Ask has no discovery seam")
	}
	// Fill the exact cache after constructing the adapter. A copied/private
	// cache would fail to see it; no local HTTP recursion or network is used.
	if err := catalog.SetCache([]mcpregistry.RegistryEntry{{Name: "telegram", Description: "Example cached listing", SourceID: "fixture", Source: "Example Catalog", Command: "never-run-this-test-server"}}); err != nil {
		t.Fatal(err)
	}
	result := handler.Discovery.MCPCatalog(context.Background(), "telegram")
	if result.Availability != assistantdiscovery.Available || len(result.Candidates) != 1 || result.Candidates[0].Receipt.Freshness != "cached" {
		t.Fatalf("production catalog: %+v", result)
	}
	inventory := handler.Discovery.Installed(context.Background(), "telegram")
	if inventory.Availability != assistantdiscovery.Available || len(inventory.Candidates) != 2 {
		t.Fatalf("production inventory: %+v", inventory)
	}
	encoded, _ := json.Marshal(inventory)
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "PRIVATE_PROMPT_SENTINEL") || strings.Contains(string(encoded), "PRIVATE_CONFIG_SENTINEL") {
		t.Fatalf("production adapter leaked private data: %s", encoded)
	}
	if status, err := registry.GetServerStatus("telegram"); err != nil || status != mcp.StatusStopped {
		t.Fatalf("discovery started MCP: %s %v", status, err)
	}
	// The existing browser path retains its installation metadata schema, and
	// sees the same post-construction cache update without an implicit refresh.
	rr := httptest.NewRecorder()
	mcpHandler.SearchServersHandler(rr, httptest.NewRequest(http.MethodGet, "/api/mcp/search?q=telegram&source=Example%20Catalog", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "never-run-this-test-server") {
		t.Fatalf("browser owner diverged: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(err) {
		t.Fatal("discovery wrote hired-agent state")
	}
}

func TestPersonalAssistantOptionalSearchRequiresExplicitEnabledSelectionAndIgnoresEnvironment(t *testing.T) {
	t.Setenv("BRAVE_API_KEY", "PRIVATE_ENVIRONMENT_KEY_SENTINEL")
	root := t.TempDir()
	manager := config.NewManager(filepath.Join(root, "settings.json"))
	registry := mcp.NewRegistry()
	srv := &Server{Core: &CoreSystemFacade{ConfigManager: manager}, Handlers: &HandlerFacade{}, Integration: &IntegrationSystemFacade{MCPRegistry: registry}}
	service := srv.newPersonalAssistantDiscovery().(*assistantdiscovery.Service)
	for _, fixture := range []struct {
		enabled  bool
		provider string
		want     bool
	}{{true, "auto", false}, {true, "brave", false}, {false, "duckduckgo", false}, {true, "duckduckgo", true}, {false, "duckduckgo", false}} {
		settings := config.Settings{Utility: config.UtilitySettings{Enabled: fixture.enabled, SearchProvider: fixture.provider}}
		if err := manager.Update(settings); err != nil {
			t.Fatal(err)
		}
		if service.PublicSearchConfigured() != fixture.want {
			t.Fatalf("provider selection ignored: %+v", fixture)
		}
	}
}

func TestPersonalAssistantDiscoveryMissingOwnersRemainUnavailable(t *testing.T) {
	for _, srv := range []*Server{{}, {Handlers: &HandlerFacade{}}} {
		if srv.newPersonalAssistantDiscovery() != nil {
			t.Fatal("missing owners advertised a usable discovery service")
		}
	}
}
