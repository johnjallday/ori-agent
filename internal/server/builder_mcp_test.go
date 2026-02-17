package server

import (
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/store"
)

type mcpBuilderTestStore struct {
	names []string
}

func (s *mcpBuilderTestStore) ListAgents() (names []string, current string) {
	return append([]string{}, s.names...), ""
}

func (s *mcpBuilderTestStore) SetCurrentAgent(string) error { return nil }
func (s *mcpBuilderTestStore) CreateAgent(string, *store.CreateAgentConfig) error {
	return nil
}
func (s *mcpBuilderTestStore) DeleteAgent(string) error { return nil }
func (s *mcpBuilderTestStore) GetAgent(string) (*agent.Agent, bool) {
	return nil, false
}
func (s *mcpBuilderTestStore) SetAgent(string, *agent.Agent) error { return nil }
func (s *mcpBuilderTestStore) ClearAgents() error                  { return nil }
func (s *mcpBuilderTestStore) Save() error                         { return nil }

type fakeMCPStarter struct {
	statuses  map[string]mcp.ServerStatus
	statusErr map[string]error
	startErr  map[string]error
	started   []string
	stopped   []string
}

func (f *fakeMCPStarter) GetServerStatus(name string) (mcp.ServerStatus, error) {
	if err, ok := f.statusErr[name]; ok {
		return mcp.StatusStopped, err
	}
	status, ok := f.statuses[name]
	if !ok {
		return mcp.StatusStopped, errors.New("missing server")
	}
	return status, nil
}

func (f *fakeMCPStarter) StartServer(name string) error {
	f.started = append(f.started, name)
	if err, ok := f.startErr[name]; ok {
		return err
	}
	if f.statuses == nil {
		f.statuses = map[string]mcp.ServerStatus{}
	}
	f.statuses[name] = mcp.StatusRunning
	return nil
}

func (f *fakeMCPStarter) StopServer(name string) error {
	f.stopped = append(f.stopped, name)
	if f.statuses != nil {
		f.statuses[name] = mcp.StatusStopped
	}
	return nil
}

func TestCollectEnabledMCPServerNames(t *testing.T) {
	tempDir := t.TempDir()
	configManager := mcp.NewConfigManager(tempDir)
	if err := configManager.SaveGlobalConfig(&mcp.GlobalConfig{
		Servers: []mcp.ServerConfig{
			{Name: "filesystem"},
			{Name: "ori-reaper"},
			{Name: "sqlite"},
		},
	}); err != nil {
		t.Fatalf("failed to save global config: %v", err)
	}

	if err := configManager.SaveAgentConfig("alpha", &mcp.AgentMCPConfig{
		EnabledServers: []string{"filesystem", "ori-reaper"},
	}); err != nil {
		t.Fatalf("failed to save alpha config: %v", err)
	}

	if err := configManager.SaveAgentConfig("beta", &mcp.AgentMCPConfig{
		EnabledServers: []string{"ori-reaper", "missing"},
	}); err != nil {
		t.Fatalf("failed to save beta config: %v", err)
	}

	builder := &ServerBuilder{
		st:               &mcpBuilderTestStore{names: []string{"alpha", "beta"}},
		mcpConfigManager: configManager,
	}

	got := builder.collectEnabledMCPServerNames()
	want := []string{"filesystem", "ori-reaper"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected enabled server names %v, got %v", want, got)
	}
}

func TestStartEnabledMCPServers(t *testing.T) {
	starter := &fakeMCPStarter{
		statuses: map[string]mcp.ServerStatus{
			"running":    mcp.StatusRunning,
			"starting":   mcp.StatusStarting,
			"restarting": mcp.StatusRestarting,
			"stopped":    mcp.StatusStopped,
			"erroring":   mcp.StatusError,
			"broken":     mcp.StatusStopped,
		},
		statusErr: map[string]error{
			"missing": errors.New("not found"),
		},
		startErr: map[string]error{
			"broken": errors.New("failed to start"),
		},
	}

	started, failed := startEnabledMCPServers(starter, []string{
		"running",
		"starting",
		"restarting",
		"stopped",
		"erroring",
		"missing",
		"broken",
	})

	if started != 2 {
		t.Fatalf("expected 2 servers to start successfully, got %d", started)
	}
	if failed != 2 {
		t.Fatalf("expected 2 servers to fail startup, got %d", failed)
	}

	wantStarted := []string{"stopped", "erroring", "broken"}
	if !reflect.DeepEqual(starter.started, wantStarted) {
		t.Fatalf("expected start calls %v, got %v", wantStarted, starter.started)
	}

	wantStopped := []string{"erroring"}
	if !reflect.DeepEqual(starter.stopped, wantStopped) {
		t.Fatalf("expected stop calls %v, got %v", wantStopped, starter.stopped)
	}
}
