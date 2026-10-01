package workspace_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func completenessSeed(t *testing.T, store workspace.Store) *workspace.Workspace {
	t.Helper()
	ws := contractSeed(t, store)
	ws.Description = "original description"
	ws.Mission = "Watch release notes"
	ws.MissionEnabled = true
	ws.Cadence = &workspace.ScheduleConfig{Type: workspace.ScheduleDaily, TimeOfDay: "09:00"}
	ws.MarkMissionLoaded()
	ws.MCPBindings = []workspace.MCPBinding{{ID: "binding-1", ServerName: "fixture", Enabled: true}}
	ws.SetInstalledCapabilities([]workspace.InstalledCapability{{
		ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now().UTC(),
	}})
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestWorkspaceSummaryContract(t *testing.T) {
	// Separate types prevent passing a listing to Save by accident, even when
	// the summary contains all the metadata the consumer currently needs.
	if reflect.TypeFor[workspace.WorkspaceSummary]().ConvertibleTo(reflect.TypeFor[workspace.Workspace]()) {
		t.Fatal("a summary can be converted into a writable Workspace")
	}
	for name, create := range contractStores() {
		t.Run(name, func(t *testing.T) {
			store := create(t)
			seed := completenessSeed(t, store)
			for _, status := range []workspace.WorkspaceStatus{workspace.StatusCompleted, workspace.StatusFailed, workspace.StatusCancelled, workspace.StatusTrashed, workspace.StatusMissing} {
				inactive := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Inactive " + string(status)})
				inactive.Status = status
				if err := store.Save(inactive); err != nil {
					t.Fatal(err)
				}
			}
			lister, ok := store.(workspace.SummaryLister)
			if !ok {
				t.Fatal("store has no summary-only listing API")
			}
			listed, err := lister.ListActiveSummaries()
			if err != nil || len(listed) != 1 {
				t.Fatalf("ListActiveSummaries: count=%d, err=%v", len(listed), err)
			}
			persisted := contractGet(t, store, seed.ID)
			summary := listed[0]
			if summary.ID != persisted.ID || summary.Name != persisted.Name || summary.FolderSlug != persisted.FolderSlug ||
				summary.Description != persisted.Description || summary.Status != persisted.Status || summary.Version != persisted.Version ||
				!summary.CreatedAt.Equal(persisted.CreatedAt) || !summary.UpdatedAt.Equal(persisted.UpdatedAt) {
				t.Fatalf("summary lost metadata: %+v", summary)
			}
			encoded, err := json.Marshal(summary)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for _, payload := range []string{"tasks", "shared_data", "messages", "mcp_bindings", "mission", "installed_capabilities"} {
				if _, exists := fields[payload]; exists {
					t.Errorf("summary exposes writable payload %q", payload)
				}
			}
			listed[0].Name = "uncommitted summary edit"
			if got := contractGet(t, store, seed.ID); got.Name != seed.Name || len(got.Tasks) != 1 || got.Mission != seed.Mission {
				t.Fatal("summary edit changed stored state")
			}
		})
	}
}

func TestWorkspaceCompletenessContract(t *testing.T) {
	for name, create := range contractStores() {
		t.Run(name, func(t *testing.T) {
			t.Run("metadata_update_preserves_payload", func(t *testing.T) {
				store := create(t)
				seed := completenessSeed(t, store)
				if err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
					ws.Description = "metadata edit"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				got := contractGet(t, store, seed.ID)
				if got.Description != "metadata edit" || len(got.Tasks) != 1 || got.Tasks[0].Context["label"] != "original" ||
					got.SharedData["label"] != "original" || len(got.MCPBindings) != 1 || got.MCPBindings[0].ID != "binding-1" ||
					got.Mission != seed.Mission || !got.MissionEnabled || got.Cadence == nil || len(got.InstalledCapabilities) != 1 {
					t.Fatalf("metadata update lost unedited payload: %+v", got)
				}
			})
			t.Run("explicit_clear_survives_later_metadata_update", func(t *testing.T) {
				store := create(t)
				seed := completenessSeed(t, store)
				if err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
					ws.Tasks = []workspace.Task{}
					ws.SharedData = map[string]any{}
					ws.MCPBindings = []workspace.MCPBinding{}
					ws.Mission = ""
					ws.MissionEnabled = false
					ws.Cadence = nil
					ws.SetInstalledCapabilities([]workspace.InstalledCapability{})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					got := contractGet(t, store, seed.ID)
					if len(got.Tasks) != 0 || len(got.SharedData) != 0 || len(got.MCPBindings) != 0 ||
						got.Mission != "" || got.MissionEnabled || got.Cadence != nil || len(got.InstalledCapabilities) != 0 {
						t.Fatalf("cleared payload was retained or resurrected: %+v", got)
					}
					if err := store.Update(seed.ID, func(ws *workspace.Workspace) error {
						ws.Description = "metadata edit after clearing"
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
