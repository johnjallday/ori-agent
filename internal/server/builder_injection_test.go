package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Full builds still create services besides the injected dependencies. Keep
// their relative paths, data directory, and HOME inside one disposable root.
func prepareBuilderInjectionRoot(t *testing.T) string {
	t.Helper()
	root := prepareBuilderDataRoot(t)
	t.Setenv("AGENT_STORE_PATH", filepath.Join(root, "default-agents", "agents.json"))
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "ORI_TEMPLATES_DIR", "ORI_VAULT_DIR", "ORI_DEV_SCRIPTED_TASK_RUNS"} {
		t.Setenv(key, "")
	}
	return root
}

func closeInjectionTestServer(t *testing.T, srv *Server) {
	t.Helper()
	t.Cleanup(func() {
		srv.Shutdown()
		if srv.Integration.MCPRegistry != nil {
			if err := srv.Integration.MCPRegistry.StopAll(); err != nil {
				t.Errorf("stop MCP registry: %v", err)
			}
		}
		if srv.workspaceFileStore != nil {
			if err := srv.workspaceFileStore.Close(); err != nil {
				t.Errorf("close folder store: %v", err)
			}
		}
		if srv.Core.CostTracker != nil {
			srv.Core.CostTracker.Close()
		}
		if srv.Storage.SessionStore != nil {
			if err := srv.Storage.SessionStore.Close(); err != nil {
				t.Errorf("close session store: %v", err)
			}
		}
	})
}

func TestServerBuilder_BuildPreservesInjectedDependencies(t *testing.T) {
	for _, name := range []string{"in_memory_workspace", "folder_workspace", "composed_workspace"} {
		t.Run(name, func(t *testing.T) {
			root := prepareBuilderInjectionRoot(t)
			cfg := config.NewManagerWithSecretStore(filepath.Join(root, "injected-settings.json"), vault.NewMemorySecretStore())
			factory := llm.NewFactory()
			provider := llm.NewOpenAIProvider(llm.ProviderConfig{})
			factory.Register("injected", provider)
			agents, err := store.NewFileStore(filepath.Join(root, "injected-agents", "agents.json"), loadDefaultSettings())
			if err != nil {
				t.Fatalf("create injected agent store: %v", err)
			}
			var workspaces workspace.Store = workspace.NewInMemoryStore()
			var folders *workspace.FileStore
			if name != "in_memory_workspace" {
				folders, err = workspace.NewFileStore(filepath.Join(root, "injected-workspaces"))
				if err != nil {
					t.Fatalf("create injected folder store: %v", err)
				}
				workspaces = folders
				if name == "composed_workspace" {
					workspaces = workspace.NewSyncStore(workspace.NewInMemoryStore(), folders)
				}
			}
			seed := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Injected Workspace"})
			if err := workspaces.Save(seed); err != nil {
				t.Fatalf("seed injected workspace: %v", err)
			}
			builder, err := NewServerBuilder()
			if err != nil {
				t.Fatalf("create builder: %v", err)
			}
			builder.WithConfigManager(cfg).WithLLMFactory(factory).WithStore(agents).WithWorkspaceStore(workspaces)
			builder.WithDesktopOpener(discardDesktopOpener{})
			srv, err := builder.Build()
			if err != nil {
				t.Fatalf("build with injected dependencies: %v", err)
			}
			closeInjectionTestServer(t, srv)

			if srv.Core.ConfigManager != cfg || builder.configManager != cfg {
				t.Error("Build replaced the injected config manager")
			}
			if srv.Core.LLMFactory != factory || builder.llmFactory != factory {
				t.Error("Build replaced the injected LLM factory")
			}
			if got, err := srv.Core.LLMFactory.GetProvider("injected"); err != nil || got != provider {
				t.Errorf("injected provider was not retained: provider=%v, error=%v", got, err)
			}
			if srv.Storage.AgentStore != agents || builder.st != agents {
				t.Error("Build replaced the injected agent store")
			}
			if srv.Storage.WorkspaceStore != workspaces || builder.workspaceStore != workspaces {
				t.Error("Build replaced or wrapped the injected workspace store")
			}
			if srv.workspaceFileStore != folders {
				t.Error("Build did not use the injected store's folder capability")
			}
			if srv.Handlers.Chat == nil || srv.Handlers.Workspace == nil || srv.Workflow.TaskExecutor == nil {
				t.Error("Build skipped downstream wiring for injected dependencies")
			}
			if srv.Storage.AgentStorePath != resolveAgentStorePath() {
				t.Error("Build did not resolve the data path used by agent sidecar services")
			}
			// Exercise a consumer, not just the final facade's pointers. No
			// listener or model call is needed to read the injected workspace.
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+seed.ID, nil)
			srv.Handlers.Workspace.GetWorkspace(rec, req)
			var got workspace.Workspace
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode injected workspace response: %v; body=%s", err, rec.Body.String())
			}
			if rec.Code != http.StatusOK || got.ID != seed.ID || got.Name != seed.Name {
				t.Errorf("workspace handler did not read the supplied store: status=%d, body=%s", rec.Code, rec.Body.String())
			}
			for _, path := range []string{"settings.json", filepath.Join("default-agents", "agents.json")} {
				if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Errorf("Build opened a default dependency at %s: %v", path, err)
				}
			}
		})
	}
}

func TestServerBuilder_BuildCreatesDefaultsForNilDependencies(t *testing.T) {
	root := prepareBuilderInjectionRoot(t)
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	// Nil clears an override; it must not leave startup without a dependency.
	builder.WithConfigManager(config.NewManagerWithSecretStore("unused.json", vault.NewMemorySecretStore())).WithConfigManager(nil)
	builder.WithLLMFactory(llm.NewFactory()).WithLLMFactory(nil)
	builder.WithWorkspaceStore(workspace.NewInMemoryStore()).WithWorkspaceStore(nil)
	agents, err := store.NewFileStore(filepath.Join(root, "unused-agents", "agents.json"), loadDefaultSettings())
	if err != nil {
		t.Fatalf("create overridden agent store: %v", err)
	}
	builder.WithStore(agents).WithStore(nil)
	builder.WithDesktopOpener(discardDesktopOpener{})
	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("build default dependencies: %v", err)
	}
	closeInjectionTestServer(t, srv)
	if srv.Core.ConfigManager == nil || srv.Core.LLMFactory == nil || srv.Storage.AgentStore == nil || srv.Storage.WorkspaceStore == nil {
		t.Fatal("Build left a default dependency unset")
	}
	if srv.workspaceFileStore == nil || srv.Handlers.SetupWizard == nil || srv.Handlers.RuntimeCapabilities == nil {
		t.Error("default workspace composition lost its folder-backed services")
	}
	if srv.Storage.AgentStore == agents {
		t.Error("nil did not clear the injected agent store")
	}
}

func TestServerBuilder_BuildWithOnlyLLMFactoryOverride(t *testing.T) {
	prepareBuilderInjectionRoot(t)
	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	factory := llm.NewFactory()
	builder.WithLLMFactory(factory).WithDesktopOpener(discardDesktopOpener{})
	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("build with one override: %v", err)
	}
	closeInjectionTestServer(t, srv)
	if srv.Core.LLMFactory != factory {
		t.Error("Build replaced the only injected dependency")
	}
	if srv.Core.ConfigManager == nil || srv.Storage.AgentStore == nil || srv.Storage.WorkspaceStore == nil || srv.workspaceFileStore == nil {
		t.Error("an injected factory prevented default initialization of other dependencies")
	}
}
