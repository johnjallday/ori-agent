package server

import (
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

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
	got := collectEnabledMCPServerNames([]mcp.ServerConfig{
		{Name: "filesystem", Enabled: true},
		{Name: "ori-reaper", Enabled: true},
		{Name: "ori-reaper", Enabled: true},
		{Name: "sqlite", Enabled: false},
		{Name: "", Enabled: true},
	})
	want := []string{"filesystem", "ori-reaper"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected enabled server names %v, got %v", want, got)
	}
}

func TestExternalMCPImportEnabled(t *testing.T) {
	t.Run("default enabled", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "")
		if !externalMCPImportEnabled() {
			t.Fatal("expected external MCP import to be enabled by default")
		}
	})

	t.Run("explicit disable", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "true")
		if externalMCPImportEnabled() {
			t.Fatal("expected external MCP import to be disabled when env is true")
		}
	})

	t.Run("explicit enable", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "false")
		if !externalMCPImportEnabled() {
			t.Fatal("expected external MCP import to remain enabled when env is false")
		}
	})

	t.Run("invalid value keeps default", func(t *testing.T) {
		t.Setenv(disableExternalMCPImportEnv, "not-a-bool")
		if !externalMCPImportEnabled() {
			t.Fatal("expected invalid env values to preserve default enabled behavior")
		}
	})
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
