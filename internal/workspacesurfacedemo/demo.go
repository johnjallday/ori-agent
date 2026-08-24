// Package workspacesurfacedemo supplies the opt-in non-REAPER protocol fixture
// used by `wt demo` during Workspace Surface development. It is disabled unless
// ORI_WORKSPACE_SURFACE_DEMO=1 and is replaced by the installable example plugin
// before delivery.
package workspacesurfacedemo

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

const PluginID = "workspace-surface-demo"

type Runtime struct {
	mu          sync.Mutex
	statusReads int
	calls       int
}

func (r *Runtime) Status(_ context.Context, _ workspacesurface.WorkspaceContext) (workspacesurface.StationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusReads++
	return r.status(), nil
}

func (r *Runtime) Invoke(_ context.Context, invocation workspacesurface.Invocation) (workspacesurface.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	switch invocation.Operation {
	case "status.read":
		r.statusReads++
		output, err := json.Marshal(r.status())
		return workspacesurface.Result{Output: output}, err
	case "greeting.create":
		var input struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(invocation.Input, &input); err != nil {
			return workspacesurface.Result{}, err
		}
		output, err := json.Marshal(map[string]string{"message": "Hello, " + input.Name + ". The broker kept workspace authority in Ori."})
		return workspacesurface.Result{Output: output}, err
	default:
		return workspacesurface.Result{}, fmt.Errorf("operation is not implemented")
	}
}

func (r *Runtime) status() workspacesurface.StationStatus {
	return workspacesurface.StationStatus{
		State:       workspacesurface.StationReady,
		Value:       fmt.Sprintf("Ready · %d checks", r.statusReads),
		Description: "The non-REAPER Workspace Surface fixture is ready.",
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func Registration(assetRoot string, runtime *Runtime) workspacesurface.Registration {
	root := filepath.Clean(assetRoot)
	owner := workspacesurface.Owner{
		Kind: workspacesurface.OwnerPlugin, ID: PluginID, Version: "0.1.0",
		Generation: 1, ProtocolMin: 1, ProtocolMax: 1,
	}
	return workspacesurface.Registration{
		Owner: owner,
		Capabilities: []workspacesurface.Capability{{
			ID: "demo-tools", Version: 1,
			Display: workspacesurface.Display{Name: "Surface Demo", Description: "A harmless non-REAPER Workspace Surface."},
			Surfaces: []workspacesurface.Surface{{
				ID: "main", Label: "Surface Demo", Description: "Open the broker and sandbox reference surface.",
				Icon: workspacesurface.Icon{Kind: "host", Value: "puzzle"}, Placement: "map_modal",
				Modal:        workspacesurface.Modal{Width: 760, Height: 610},
				Polling:      workspacesurface.Polling{MapSeconds: 5, OpenSeconds: 1},
				OperationIDs: []string{"status.read", "greeting.create"}, StatusOperation: "status.read",
			}},
		}},
		Bindings: []workspacesurface.Binding{{
			CapabilityID: "demo-tools", SurfaceID: "main", AssetRoot: root, EntryAsset: "ui/index.html", Runtime: runtime,
			Operations: map[string]workspacesurface.Operation{
				"status.read": {
					ID:             "status.read",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{"state":{"type":"string","maxLength":32},"value":{"type":"string","maxLength":160},"description":{"type":"string","maxLength":500},"checked_at":{"type":"string","maxLength":64}},"required":["state","value","description","checked_at"],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyReadOnly,
				},
				"greeting.create": {
					ID:             "greeting.create",
					InputSchema:    json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":80}},"required":["name"],"additionalProperties":false}`),
					OutputSchema:   json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","maxLength":500}},"required":["message"],"additionalProperties":false}`),
					MaxOutputBytes: 4096, Timeout: workspacesurface.TimeoutFast, Policy: workspacesurface.PolicyReadOnly,
				},
			},
		}},
	}
}
