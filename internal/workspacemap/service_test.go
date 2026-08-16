package workspacemap

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeLookup struct {
	records map[string]*workspace.Workspace
	gets    []string
}

func (f *fakeLookup) Get(id string) (*workspace.Workspace, error) {
	f.gets = append(f.gets, id)
	record, ok := f.records[id]
	if !ok {
		return nil, fmt.Errorf("workspace %q not found", id)
	}
	return record, nil
}

func newTestService(t *testing.T, records map[string]*workspace.Workspace) (*Service, *fakeLookup) {
	t.Helper()
	store := NewSQLiteStore(newTestDB(t))
	lookup := &fakeLookup{records: records}
	return NewService(store, lookup), lookup
}

func TestServiceRejectsUnknownWorkspace(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{})

	_, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-ghost": {X: 0, Y: 0}}),
	}})
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Apply error = %v, want ErrNodeNotFound", err)
	}
}

func TestServiceRejectsNodeOwnedByAnotherUser(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{
		"ws-a": {ID: "ws-a", OwnerUserID: "alice"},
	})

	_, err := service.Apply(context.Background(), "bob", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 0, Y: 0}}),
	}})
	if !errors.Is(err, ErrNodeNotOwned) {
		t.Fatalf("Apply error = %v, want ErrNodeNotOwned", err)
	}
}

func TestServiceRejectsReservedHQSite(t *testing.T) {
	service, lookup := newTestService(t, map[string]*workspace.Workspace{})

	_, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{ReservedHQSiteID: {X: 0, Y: 0}}),
	}})
	if !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("Apply error = %v, want ErrInvalidNodeID", err)
	}
	if len(lookup.gets) != 0 {
		t.Errorf("workspace lookups = %v, want none; the reserved site is not a workspace (FR-30)", lookup.gets)
	}
}

func TestServiceTreatsEmptyOwnerAsLocalUser(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{
		"ws-legacy": {ID: "ws-legacy"},
	})
	seedServiceWorkspace(t, service, "ws-legacy")

	if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-legacy": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestServiceAllowsTrashedWorkspaceToKeepItsAnchor(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{
		"ws-trashed": {ID: "ws-trashed", OwnerUserID: "local", Status: workspace.StatusTrashed},
	})
	seedServiceWorkspace(t, service, "ws-trashed")

	// FR-26/FR-27: a trashed record is hidden from the active map but keeps its
	// coordinate, so an Undo restore that names it must not be refused.
	if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		RestorePositions(map[string]Point{"ws-trashed": {X: 5, Y: 5}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestServiceLeavesWorkspaceRecordsUnchanged(t *testing.T) {
	updatedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	record := &workspace.Workspace{
		ID:          "ws-a",
		Name:        "Alpha",
		OwnerUserID: "local",
		ParentID:    "grp-1",
		OrderIndex:  7,
		Status:      workspace.StatusActive,
		UpdatedAt:   updatedAt,
	}
	before := semanticFields(record)
	service, _ := newTestService(t, map[string]*workspace.Workspace{"ws-a": record})
	seedServiceWorkspace(t, service, "ws-a")

	if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 300, Y: -300}}),
		SetViewport(Viewport{CenterX: 10, CenterY: 10, Zoom: 2}),
		SetSnapToGrid(false),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// FR-6 / success metric 5: the map has no vocabulary for workspace
	// semantics, so a placement cannot have moved a parent, an order, a status,
	// a name, or a timestamp.
	if after := semanticFields(record); !reflect.DeepEqual(after, before) {
		t.Errorf("workspace record changed: got %+v, want %+v", after, before)
	}
}

// semanticFields captures the workspace data a map operation must never touch.
// It reads individual fields rather than copying the record, which carries a
// mutex.
func semanticFields(record *workspace.Workspace) map[string]any {
	return map[string]any{
		"name":        record.Name,
		"parent_id":   record.ParentID,
		"order_index": record.OrderIndex,
		"status":      record.Status,
		"owner":       record.OwnerUserID,
		"updated_at":  record.UpdatedAt,
		"designation": record.Designation,
	}
}

func TestServiceResetClearsOnlyPositions(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{
		"ws-a": {ID: "ws-a", OwnerUserID: "local"},
	})
	seedServiceWorkspace(t, service, "ws-a")
	ctx := context.Background()

	if _, err := service.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 9, Y: 9}}),
		SetSnapToGrid(false),
		SetViewport(Viewport{CenterX: 4, CenterY: 4, Zoom: 1.25}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := service.Reset(ctx, "local"); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	layout, err := service.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(layout.Positions) != 0 {
		t.Errorf("positions after reset = %v, want none", layout.Positions)
	}
	if layout.SnapToGrid {
		t.Error("reset must preserve the snap preference (FR-110)")
	}
	if layout.Viewport == nil {
		t.Error("reset must not clear the camera; that is Reset View's job (FR-109)")
	}
}

// ---------------------------------------------------------------------------
// District eligibility (#346 FR-181)
// ---------------------------------------------------------------------------

func groupRecord(id, parentID string) *workspace.Workspace {
	return &workspace.Workspace{ID: id, Name: id, Kind: "group", OwnerUserID: "local", ParentID: parentID}
}

func districtOps(groupID string) []Operation {
	return []Operation{
		SetGroupFrame(groupID, Frame{X: 0, Y: 0, Width: 400, Height: 400}),
		FitGroupToContents(groupID),
		SetGroupCollapsed(groupID, true),
		SetGroupAppearance(groupID, "moss", "blueprint"),
		ResetGroupAppearance(groupID),
	}
}

func TestServiceAcceptsTopLevelGroupDistrict(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{
		"grp-top": groupRecord("grp-top", ""),
	})
	seedServiceWorkspace(t, service, "grp-top")

	for _, op := range districtOps("grp-top") {
		if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{op}}); err != nil {
			t.Fatalf("Apply %s: %v", op.Kind, err)
		}
	}
}

// A group whose parent is an ordinary workspace still renders as its own
// top-level district on the Map, so it stays eligible.
func TestServiceAcceptsGroupUnderOrdinaryWorkspace(t *testing.T) {
	service, _ := newTestService(t, map[string]*workspace.Workspace{
		"grp-a":     groupRecord("grp-a", "ws-parent"),
		"ws-parent": {ID: "ws-parent", Name: "Parent", OwnerUserID: "local"},
	})
	seedServiceWorkspace(t, service, "grp-a")

	if _, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{
		SetGroupCollapsed("grp-a", true),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestServiceRejectsIneligibleDistrictTargets(t *testing.T) {
	records := map[string]*workspace.Workspace{
		"ws-plain":  {ID: "ws-plain", Name: "Plain", OwnerUserID: "local"},
		"grp-outer": groupRecord("grp-outer", ""),
		"grp-inner": groupRecord("grp-inner", "grp-outer"),
		"grp-alice": {ID: "grp-alice", Name: "Alice", Kind: "group", OwnerUserID: "alice"},
	}

	for _, tc := range []struct {
		name    string
		groupID string
		want    error
	}{
		// V1 draws no nested district frames (FR-10).
		{"nested group", "grp-inner", ErrGroupNotEligible},
		// An ordinary workspace is a building, not a district (FR-9).
		{"ordinary workspace", "ws-plain", ErrGroupNotEligible},
		{"missing group", "grp-ghost", ErrNodeNotFound},
		// Reported as missing rather than forbidden, so the API never confirms
		// another user's group exists.
		{"another user's group", "grp-alice", ErrNodeNotOwned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := newTestService(t, records)
			seedServiceWorkspace(t, service, "ws-plain", "grp-outer", "grp-inner")

			for _, op := range districtOps(tc.groupID) {
				_, err := service.Apply(context.Background(), "local", Patch{Operations: []Operation{op}})
				if !errors.Is(err, tc.want) {
					t.Fatalf("Apply %s: error = %v, want %v", op.Kind, err, tc.want)
				}
			}

			// Nothing was stored, and no workspace row was touched.
			layout, err := service.Load(context.Background(), "local")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(layout.Groups) != 0 {
				t.Errorf("districts after rejected operations = %v, want none", layout.Groups)
			}
		})
	}
}

// A district operation must leave every workspace semantic field byte-identical:
// resizing, collapsing, or recolouring is presentation, never hierarchy
// (FR-5, FR-62).
func TestServiceDistrictOperationsLeaveHierarchyUnchanged(t *testing.T) {
	record := groupRecord("grp-a", "")
	record.OrderIndex = 3
	record.UpdatedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	before := semanticFields(record)

	service, _ := newTestService(t, map[string]*workspace.Workspace{"grp-a": record})
	seedServiceWorkspace(t, service, "grp-a")

	if _, err := service.Apply(context.Background(), "local", Patch{Operations: districtOps("grp-a")}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if after := semanticFields(record); !reflect.DeepEqual(after, before) {
		t.Errorf("district operations changed the group record: got %+v, want %+v", after, before)
	}
}

// seedServiceWorkspace inserts the workspace rows the position foreign key
// requires for a service built on the real SQLite store.
func seedServiceWorkspace(t *testing.T, service *Service, ids ...string) {
	t.Helper()
	store, ok := service.store.(*SQLiteStore)
	if !ok {
		t.Fatalf("service store is %T, want *SQLiteStore", service.store)
	}
	seedWorkspace(t, store.db, ids...)
}
