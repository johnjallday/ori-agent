package mcp

import (
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
)

func TestResetAdmissionMCPServerOwnsRuntimeUntilSuccessfulStop(t *testing.T) {
	server := NewServer(ServerConfig{Name: "owned", Command: "fixture", Transport: TransportStdio})
	gate := &resetstate.WorkGate{}
	server.SetAdmissionGate(gate)
	server.startRuntime = func() error { server.setStatus(StatusRunning); return nil }
	stopStarted, stopFinish := make(chan struct{}), make(chan struct{})
	server.stopRuntime = func() error { close(stopStarted); <-stopFinish; return nil }
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	if snapshot := gate.Snapshot(); snapshot.Active != 0 || snapshot.Owners != 1 {
		t.Fatalf("MCP runtime not registered: %+v", snapshot)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatalf("idle MCP runtime prevented fence: %v", err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Stop() }()
	select {
	case <-stopStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP stop did not start")
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 1 || !snapshot.Fenced {
		t.Fatalf("MCP stop released early: %+v", snapshot)
	}
	close(stopFinish)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP stop did not finish")
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 || !snapshot.Fenced {
		t.Fatalf("MCP runtime not drained: %+v", snapshot)
	}
}

func TestResetAdmissionMCPAmbiguousStopRetainsOwnershipForRetry(t *testing.T) {
	server := NewServer(ServerConfig{Name: "owned", Command: "fixture", Transport: TransportStdio})
	gate := &resetstate.WorkGate{}
	server.SetAdmissionGate(gate)
	server.startRuntime = func() error { server.setStatus(StatusRunning); return nil }
	server.stopRuntime = func() error { return errors.New("transport ownership unknown") }
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Stop(); err == nil {
		t.Fatal("ambiguous stop unexpectedly succeeded")
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 1 {
		t.Fatalf("ambiguous stop released ownership: %+v", snapshot)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatalf("ambiguous idle owner prevented fence: %v", err)
	}
	server.stopRuntime = func() error { return nil }
	if err := server.Stop(); err != nil {
		t.Fatal(err)
	}
	if snapshot := gate.Snapshot(); snapshot.Owners != 0 || !snapshot.Fenced {
		t.Fatalf("retried MCP stop did not drain owner: %+v", snapshot)
	}
}

func TestResetAdmissionMCPRegistryRefusesBeforeConfiguration(t *testing.T) {
	registry := NewRegistry()
	gate := &resetstate.WorkGate{}
	registry.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := registry.AddServer(ServerConfig{}); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced add reached validation: %v", err)
	}
	if err := registry.StartServer("missing"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced start reached registry: %v", err)
	}
	if _, err := registry.CallTool(t.Context(), "missing", "tool", nil); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatalf("fenced call reached registry: %v", err)
	}
}
