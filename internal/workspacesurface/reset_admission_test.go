package workspacesurface

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionWorkspaceSurfaceOwnsIdleProcessUntilShutdown(t *testing.T) {
	stats := &fakeProcessStats{behaviors: []fakeProcessBehavior{{}}}
	manager := NewServiceManager(stats.factory)
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	if err := manager.Probe(t.Context(), testServiceSpec()); err != nil {
		t.Fatal(err)
	}
	if snapshot := gate.Snapshot(); snapshot.Active != 0 || snapshot.Owners != 1 {
		t.Fatalf("idle service process not registered: %+v", snapshot)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("work before fence: %v", err)
	}
	release()
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatalf("idle service process prevented fence: %v", err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 || !snapshot.Fenced {
		t.Fatalf("service process not drained: %+v", snapshot)
	}
}

type resetSurfaceProcess struct {
	mu      sync.Mutex
	healthy bool
	stopErr error
}

func (p *resetSurfaceProcess) Start(context.Context) error {
	p.mu.Lock()
	p.healthy = true
	p.mu.Unlock()
	return nil
}
func (p *resetSurfaceProcess) Stop(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopErr != nil {
		return p.stopErr
	}
	p.healthy = false
	return nil
}
func (p *resetSurfaceProcess) Call(context.Context, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (p *resetSurfaceProcess) Healthy() bool          { p.mu.Lock(); defer p.mu.Unlock(); return p.healthy }
func (p *resetSurfaceProcess) setStopError(err error) { p.mu.Lock(); p.stopErr = err; p.mu.Unlock() }

func TestResetAdmissionWorkspaceSurfaceAmbiguousStopRetainsRetryableOwnership(t *testing.T) {
	process := &resetSurfaceProcess{stopErr: errors.New("stop ownership unknown")}
	manager := NewServiceManager(func(ServiceSpec) ServiceProcess { return process })
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	if err := manager.Probe(t.Context(), testServiceSpec()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("ambiguous shutdown = %v", err)
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 1 {
		t.Fatalf("ambiguous stop released process: %+v", snapshot)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatalf("ambiguous idle process prevented fence: %v", err)
	}
	process.setStopError(nil)
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 || !snapshot.Fenced {
		t.Fatalf("retried service stop did not drain owner: %+v", snapshot)
	}
}

func TestResetAdmissionWorkspaceSurfaceRefusesBeforeFactory(t *testing.T) {
	var calls int
	manager := NewServiceManager(func(ServiceSpec) ServiceProcess { calls++; return &resetSurfaceProcess{} })
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Probe(t.Context(), testServiceSpec()); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced probe = %v", err)
	}
	if _, err := manager.Call(t.Context(), testServiceSpec(), ServiceCall{Operation: "status.read"}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced call = %v", err)
	}
	if calls != 0 {
		t.Fatal("fenced service reached process factory")
	}
}
