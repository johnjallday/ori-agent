package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// drainProcess is an inert stand-in for a plugin's Workspace Surface service.
// It executes nothing and starts no operating-system process.
type drainProcess struct {
	mu      sync.Mutex
	started bool
	stopped bool
}

func (p *drainProcess) Start(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = true
	return nil
}

func (p *drainProcess) Stop(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	return nil
}

func (p *drainProcess) Call(context.Context, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (p *drainProcess) Healthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started && !p.stopped
}

func (p *drainProcess) state() (bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started, p.stopped
}

// TestResetDrainStopsPluginServicesBeforeStagingIsAccepted proves the fence and
// drain contract the plugin categories depend on: after a confirmed reset is
// staged, no plugin-backed Workspace Surface service is still running and no
// new plugin update check can start. Destructive effects happen only in the
// relaunched process, so nothing may still be holding a plugin's files here.
func TestResetDrainStopsPluginServicesBeforeStagingIsAccepted(t *testing.T) {
	process := &drainProcess{}
	services := workspacesurface.NewServiceManager(func(workspacesurface.ServiceSpec) workspacesurface.ServiceProcess {
		return process
	})
	gate := &resetstate.WorkGate{}
	services.SetAdmissionGate(gate)
	spec := workspacesurface.ServiceSpec{
		PluginID: "drain-demo", PluginGeneration: 1, ServiceID: "svc",
		Command: "never-executed-by-this-fake", MaxConcurrency: 1,
		StartupTimeout: time.Second, ShutdownTimeout: time.Second,
	}
	if err := services.Probe(t.Context(), spec); err != nil {
		t.Fatalf("start the fake plugin service: %v", err)
	}
	if started, stopped := process.state(); !started || stopped {
		t.Fatalf("fake plugin service did not start: started=%v stopped=%v", started, stopped)
	}

	server := &Server{resetWork: gate, workspaceSurfaceServices: services}
	lifecycle := newServerResetLifecycle(server)
	if err := lifecycle.TryFence(t.Context()); err != nil {
		t.Fatalf("fence an idle runtime: %v", err)
	}
	if err := lifecycle.Drain(t.Context()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if _, stopped := process.state(); !stopped {
		t.Fatal("drain accepted a reset while a plugin Workspace Surface service was still running")
	}

	// The fence is what keeps a plugin update check or a service call from
	// starting again between admission and the relaunch.
	if _, err := gate.Enter(); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("new plugin work was admitted after the fence: %v", err)
	}
	if err := services.Probe(t.Context(), spec); err == nil {
		t.Fatal("a fenced runtime restarted a plugin service")
	}
}

// TestResetDrainRefusesWhileAPluginUpdateCheckIsRunning states the other half:
// admission never cancels work in flight. An update check that already holds
// the gate blocks the fence until it finishes.
func TestResetDrainRefusesWhileAPluginUpdateCheckIsRunning(t *testing.T) {
	gate := &resetstate.WorkGate{}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("enter as a running plugin update check: %v", err)
	}
	lifecycle := newServerResetLifecycle(&Server{resetWork: gate})
	if err := lifecycle.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("reset fenced a runtime with an active plugin check: %v", err)
	}
	release()
	if err := lifecycle.TryFence(t.Context()); err != nil {
		t.Fatalf("fence after the check finished: %v", err)
	}
}

// TestProductionPluginResetPathsMatchTheLiveHandlerLayout guards the one thing
// two independent resolvers must agree on: the live plugin handler and the
// pre-store recovery resolver have to name the same directories, or reset would
// describe one installation and delete from another.
func TestProductionPluginResetPathsMatchTheLiveHandlerLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dataDir := t.TempDir()

	paths := resetPluginPaths(dataDir)
	if !paths.Resolved() {
		t.Fatal("production plugin reset paths did not resolve")
	}
	// plugin.NewManager is constructed over <data>/plugins with <data>/plugins/src
	// as its clone directory, and its Start Fresh paths are the authority for the
	// managed layout. Compare against those rather than restating the joins.
	manager := plugin.NewManager(nil, paths.PluginsDir, paths.CloneDir)
	fresh := manager.FreshPersistencePaths()
	for kind, want := range map[string]string{
		"plugin_registry":     paths.RegistryPath(),
		"plugin_marketplaces": paths.MarketplacesPath(),
		"plugin_clones":       paths.CloneDir,
		"plugin_state":        paths.StateRoot(),
		"plugin_artifacts":    paths.ArtifactsRoot(),
		"plugin_preview":      paths.PreviewRoot(),
	} {
		if fresh[kind] != want {
			t.Errorf("%s: reset resolves %q, the live manager uses %q", kind, want, fresh[kind])
		}
	}
}
