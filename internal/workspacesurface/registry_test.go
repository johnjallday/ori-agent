package workspacesurface

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type testRuntime struct {
	calls int
}

func (r *testRuntime) Status(context.Context, WorkspaceContext) (StationStatus, error) {
	return StationStatus{State: StationReady, Value: "Available", Description: "The test surface is ready."}, nil
}

func (r *testRuntime) Invoke(_ context.Context, invocation Invocation) (Result, error) {
	r.calls++
	return Result{Output: append(json.RawMessage(nil), invocation.Input...)}, nil
}

// registerTestSurface is deliberately test-only. Product registration goes
// through installed-plugin trust/lifecycle and then RegisterTrusted; tests can
// register a minimal owner without inventing router entries or plugin branches.
func registerTestSurface(t *testing.T, registry *Registry, ownerID string, generation uint64, runtime Runtime) RegisteredSurface {
	t.Helper()
	root := t.TempDir()
	registration := Registration{
		Owner: Owner{
			Kind: OwnerPlugin, ID: ownerID, Version: "0.1.0", Generation: generation,
			ProtocolMin: 1, ProtocolMax: 1,
		},
		Capabilities: []Capability{{
			ID: "demo-tools", Version: 1,
			Display: Display{Name: "Surface Demo", Description: "A harmless test surface."},
			Surfaces: []Surface{{
				ID: "main", Label: "Surface Demo", Description: "Open the test surface.",
				Icon: Icon{Kind: "host", Value: "puzzle"}, Placement: "map_modal",
				Modal: Modal{Width: 720, Height: 560}, Polling: Polling{MapSeconds: 5, OpenSeconds: 1},
				OperationIDs: []string{"status.read", "greeting.create"}, StatusOperation: "status.read",
			}},
		}},
		Bindings: []Binding{{
			CapabilityID: "demo-tools", SurfaceID: "main", AssetRoot: root, AssetVersion: "fixture-v1", EntryAsset: "ui/index.html",
			Operations: map[string]Operation{
				"status.read": {
					ID: "status.read", InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					OutputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`), MaxOutputBytes: 4096,
					Timeout: TimeoutFast, Policy: PolicyReadOnly,
				},
				"greeting.create": {
					ID: "greeting.create", InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","maxLength":80}},"required":["name"],"additionalProperties":false}`),
					OutputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","maxLength":160}},"required":["message"],"additionalProperties":false}`), MaxOutputBytes: 4096,
					Timeout: TimeoutFast, Policy: PolicyReadOnly,
				},
			},
			Runtime: runtime,
		}},
	}
	if err := registry.RegisterTrusted(registration); err != nil {
		t.Fatalf("RegisterTrusted() error = %v", err)
	}
	key := SurfaceKey(registration.Owner, "demo-tools", "main")
	surface, ok := registry.Surface(key)
	if !ok {
		t.Fatalf("Surface(%q) was not registered", key)
	}
	return surface
}

func TestRegistryTestRegistrationSeparatesInertDescriptorFromTrustedBinding(t *testing.T) {
	registry := NewRegistry()
	runtime := &testRuntime{}
	registered := registerTestSurface(t, registry, "workspace-surface-demo", 7, runtime)

	if registered.Key != "plugin:workspace-surface-demo:demo-tools:main" {
		t.Fatalf("surface key = %q", registered.Key)
	}
	publicJSON, err := json.Marshal(registered)
	if err != nil {
		t.Fatal(err)
	}
	public := string(publicJSON)
	for _, forbidden := range []string{"entry_asset", "asset_root", "ui/index.html", "input_schema", "output_schema"} {
		if strings.Contains(public, forbidden) {
			t.Fatalf("inert descriptor leaked trusted binding value %q: %s", forbidden, public)
		}
	}

	binding, ok := registry.Binding(registered.Key)
	if !ok {
		t.Fatal("trusted binding was not registered")
	}
	if binding.EntryAsset != "ui/index.html" || binding.AssetRoot == "" || binding.Runtime != runtime {
		t.Fatalf("binding = %+v", binding)
	}
	result, err := binding.Runtime.Invoke(context.Background(), Invocation{
		Workspace: WorkspaceContext{WorkspaceID: "workspace-1"},
		Operation: "greeting.create", Input: json.RawMessage(`{"name":"Ori"}`),
	})
	if err != nil || string(result.Output) != `{"name":"Ori"}` || runtime.calls != 1 {
		t.Fatalf("Invoke() = %s, %v; calls=%d", result.Output, err, runtime.calls)
	}
}

func TestRegistryOwnerQualificationAllowsSameLocalSurfaceIDs(t *testing.T) {
	registry := NewRegistry()
	first := registerTestSurface(t, registry, "first-plugin", 1, &testRuntime{})
	second := registerTestSurface(t, registry, "second-plugin", 1, &testRuntime{})

	if first.Key == second.Key || len(registry.Surfaces()) != 2 {
		t.Fatalf("qualified surfaces = %#v", registry.Surfaces())
	}
	if _, ok := registry.Surface(first.Key); !ok {
		t.Fatalf("first owner surface %q disappeared", first.Key)
	}
	if _, ok := registry.Surface(second.Key); !ok {
		t.Fatalf("second owner surface %q disappeared", second.Key)
	}
}

func TestRegistryRejectsOwnerCollisionAtomically(t *testing.T) {
	registry := NewRegistry()
	original := registerTestSurface(t, registry, "workspace-surface-demo", 1, &testRuntime{})

	collision := Registration{
		Owner: Owner{Kind: OwnerPlugin, ID: "workspace-surface-demo", Version: "0.2.0", Generation: 2, ProtocolMin: 1, ProtocolMax: 1},
		Capabilities: []Capability{{
			ID: "other", Version: 1, Display: Display{Name: "Other"},
		}},
	}
	if err := registry.RegisterTrusted(collision); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("collision error = %v", err)
	}
	if surfaces := registry.Surfaces(); len(surfaces) != 1 || surfaces[0].Key != original.Key || surfaces[0].Owner.Generation != 1 {
		t.Fatalf("collision partially changed registry: %#v", surfaces)
	}
}

func TestRegistryRequiresEverySurfaceBindingBeforePublishing(t *testing.T) {
	registry := NewRegistry()
	registration := Registration{
		Owner: Owner{Kind: OwnerPlugin, ID: "missing-binding", Version: "1.0.0", Generation: 1, ProtocolMin: 1, ProtocolMax: 1},
		Capabilities: []Capability{{
			ID: "demo", Version: 1, Display: Display{Name: "Demo"},
			Surfaces: []Surface{{
				ID: "main", Label: "Demo", Icon: Icon{Kind: "host", Value: "puzzle"}, Placement: "map_modal",
				Modal: Modal{Width: 640, Height: 480}, Polling: Polling{MapSeconds: 5, OpenSeconds: 1},
			}},
		}},
	}
	if err := registry.RegisterTrusted(registration); err == nil || !strings.Contains(err.Error(), "no trusted runtime binding") {
		t.Fatalf("missing binding error = %v", err)
	}
	if len(registry.Surfaces()) != 0 {
		t.Fatalf("invalid registration published surfaces: %#v", registry.Surfaces())
	}
}

func TestRegistryRejectsUnsupportedProtocolBeforePublishing(t *testing.T) {
	registry := NewRegistry()
	registration := Registration{
		Owner:        Owner{Kind: OwnerPlugin, ID: "future-plugin", Version: "1.0.0", Generation: 1, ProtocolMin: 2, ProtocolMax: 3},
		Capabilities: []Capability{{ID: "demo", Version: 1, Display: Display{Name: "Demo"}}},
	}
	if err := registry.RegisterTrusted(registration); err == nil || !strings.Contains(err.Error(), "does not support protocol 1") {
		t.Fatalf("protocol error = %v", err)
	}
	if len(registry.Surfaces()) != 0 {
		t.Fatal("incompatible registration changed the registry")
	}
}

func TestRegistryUnregisterRequiresExactGeneration(t *testing.T) {
	registry := NewRegistry()
	registered := registerTestSurface(t, registry, "workspace-surface-demo", 7, &testRuntime{})

	if err := registry.UnregisterOwner(OwnerPlugin, "workspace-surface-demo", 6); err == nil {
		t.Fatal("UnregisterOwner() accepted stale generation")
	}
	if _, ok := registry.Surface(registered.Key); !ok {
		t.Fatal("stale unregister removed the current surface")
	}
	if err := registry.UnregisterOwner(OwnerPlugin, "workspace-surface-demo", 7); err != nil {
		t.Fatalf("UnregisterOwner() error = %v", err)
	}
	if _, ok := registry.Surface(registered.Key); ok || len(registry.Surfaces()) != 0 {
		t.Fatal("exact unregister left a surface or binding behind")
	}
	if _, ok := registry.Binding(registered.Key); ok {
		t.Fatal("exact unregister left executable binding behind")
	}
}

func TestRegistryReturnsDefensiveCopies(t *testing.T) {
	registry := NewRegistry()
	registered := registerTestSurface(t, registry, "workspace-surface-demo", 1, &testRuntime{})

	registered.Surface.OperationIDs[0] = "mutated"
	registered.Capability.Surfaces[0].Label = "Mutated"
	binding, _ := registry.Binding(registered.Key)
	operation := binding.Operations["status.read"]
	operation.InputSchema[0] = '['
	binding.Operations["status.read"] = operation

	fresh, ok := registry.Surface(registered.Key)
	if !ok || fresh.Surface.OperationIDs[0] != "status.read" || fresh.Capability.Surfaces[0].Label != "Surface Demo" {
		t.Fatalf("public registry state was mutated: %#v", fresh)
	}
	freshBinding, ok := registry.Binding(registered.Key)
	if !ok || string(freshBinding.Operations["status.read"].InputSchema) != `{"type":"object","properties":{},"required":[],"additionalProperties":false}` {
		t.Fatalf("trusted binding was mutated: %#v", freshBinding.Operations)
	}
}
