package agenthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeMapPositions records what the agent lifecycle asked of the Agent Map's
// coordinate store, without needing a database.
type fakeMapPositions struct {
	deleted []string
	renames [][2]string
	err     error
}

func (f *fakeMapPositions) DeletePositions(_ context.Context, names ...string) error {
	f.deleted = append(f.deleted, names...)
	return f.err
}

func (f *fakeMapPositions) RenamePosition(_ context.Context, oldName, newName string) error {
	f.renames = append(f.renames, [2]string{oldName, newName})
	return f.err
}

// Renaming an agent must carry its saved tile across (agents-page-ux FR-58).
// The rename path saves the new record and then deletes the old one, which is
// the exact shape that has twice destroyed per-agent data in this repository.
func TestRenameCarriesTheAgentMapPosition(t *testing.T) {
	h := guardTestHandler(t, []string{"Loose"}, nil)
	positions := &fakeMapPositions{}
	h.SetMapPositionStore(positions)

	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Loose", strings.NewReader(`{"name":"Loose Renamed"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", rr.Code, rr.Body.String())
	}

	if len(positions.renames) != 1 {
		t.Fatalf("renames = %v, want exactly one", positions.renames)
	}
	if positions.renames[0] != [2]string{"Loose", "Loose Renamed"} {
		t.Fatalf("rename = %v, want Loose -> Loose Renamed", positions.renames[0])
	}
	// The old name must not also be deleted: that would race the carry and
	// leave the agent back on automatic placement.
	if len(positions.deleted) != 0 {
		t.Fatalf("deleted = %v, want nothing deleted during a rename", positions.deleted)
	}
}

// Deleting an agent removes its anchor rather than leaving an orphan row
// (FR-57). The read path also ignores orphans, but that is the safety net, not
// the mechanism.
func TestDeleteRemovesTheAgentMapPosition(t *testing.T) {
	h := guardTestHandler(t, []string{"Doomed"}, nil)
	positions := &fakeMapPositions{}
	h.SetMapPositionStore(positions)

	req := httptest.NewRequest(http.MethodDelete, "/api/agents?name=Doomed", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rr.Code, rr.Body.String())
	}

	if len(positions.deleted) != 1 || positions.deleted[0] != "Doomed" {
		t.Fatalf("deleted = %v, want [Doomed]", positions.deleted)
	}
}

// An agent whose rename is refused must not have its position moved: the
// attachment guard runs before the carry, so a blocked rename leaves the map
// exactly as it was.
func TestABlockedRenameDoesNotMoveThePosition(t *testing.T) {
	h := guardTestHandler(t,
		[]string{"Attached"},
		map[string][]string{"ws-a": {"Attached"}},
	)
	positions := &fakeMapPositions{}
	h.SetMapPositionStore(positions)

	req := httptest.NewRequest(http.MethodPatch, "/api/agents/Attached", strings.NewReader(`{"name":"Renamed"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	if len(positions.renames) != 0 {
		t.Fatalf("renames = %v, want none for a blocked rename", positions.renames)
	}
}

// The map store is optional. Unwired, an agent's lifecycle still works — the
// map's read path ignores orphans, so the cost is a stale row rather than a
// broken delete.
func TestAgentLifecycleWorksWithoutAMapStore(t *testing.T) {
	h := guardTestHandler(t, []string{"Solo"}, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/agents?name=Solo", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete without a map store: status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

// A map-store failure must not fail the delete: the agent is already gone by
// then, and reporting failure afterwards would be a lie.
func TestAMapStoreFailureDoesNotFailTheDelete(t *testing.T) {
	h := guardTestHandler(t, []string{"Solo"}, nil)
	h.SetMapPositionStore(&fakeMapPositions{err: context.DeadlineExceeded})

	req := httptest.NewRequest(http.MethodDelete, "/api/agents?name=Solo", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 despite the map-store failure: %s", rr.Code, rr.Body.String())
	}
}
