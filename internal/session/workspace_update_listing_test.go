package session

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestWorkspaceUpdateListingBoundedOwnedKeysetAndFinalAuthority(t *testing.T) {
	store, cleanup := setupHybridStore(t, 5)
	defer cleanup()
	h := store.(*hybridStore)
	if _, err := h.DB().ExecContext(t.Context(), `INSERT INTO users (id,created_at,updated_at) VALUES ('other',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for i := range 108 {
		ws := &Workspace{ID: fmt.Sprintf("w-%03d", i), Name: "Same display name", FolderSlug: fmt.Sprintf("desk-%03d", i), OwnerUserID: "local", Status: WorkspaceStatusActive}
		switch i {
		case 105:
			ws.OwnerUserID = "other"
		case 106:
			ws.Status = WorkspaceStatusTrashed
		case 107:
			ws.Status = WorkspaceStatusMissing
		}
		if err := h.CreateWorkspace(t.Context(), ws); err != nil {
			t.Fatal(err)
		}
	}
	first, err := h.ListWorkspaceUpdateIDs(t.Context(), "local", "", 100)
	if err != nil || len(first) != 101 || first[0] != "w-000" || first[100] != "w-100" {
		t.Fatalf("first: %v %v", first, err)
	}
	// The extra row is only a continuation hint. The next page starts after
	// the last projected row, not the unprojected look-ahead.
	last, err := h.ListWorkspaceUpdateIDs(t.Context(), "local", first[99], 100)
	if err != nil || !reflect.DeepEqual(last, []string{"w-100", "w-101", "w-102", "w-103", "w-104"}) {
		t.Fatal(last, err)
	}
	if _, err := h.DB().ExecContext(t.Context(), `UPDATE workspaces SET name='Renamed', messages_json='not JSON' WHERE id='w-000'`); err != nil {
		t.Fatal(err)
	}
	again, err := h.ListWorkspaceUpdateIDs(t.Context(), "local", "", 100)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("labels/heavy state changed navigation order", err)
	}
	if _, err := h.DB().ExecContext(t.Context(), `UPDATE workspaces SET owner_user_id='other' WHERE id='w-001'`); err != nil {
		t.Fatal(err)
	}
	targets, err := h.ReadWorkspaceUpdateTargets(t.Context(), "local", []string{"w-000", "w-001", "w-105", "w-106", "w-107", "w-000", "missing"})
	if err != nil || len(targets) != 1 || targets[0].Name != "Renamed" || targets[0].FolderSlug != "desk-000" {
		t.Fatal(targets, err)
	}
	for _, owner := range []string{"other", "local' OR 1=1 --"} {
		got, err := h.ListWorkspaceUpdateIDs(t.Context(), owner, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		if owner == "other" && !reflect.DeepEqual(got, []string{"w-001", "w-105"}) {
			t.Fatal(got)
		}
		if owner != "other" && len(got) != 0 {
			t.Fatal("owner predicate bypassed", got)
		}
	}
	var planID, parent, unused int
	var plan string
	if err := h.DB().QueryRowContext(t.Context(), `EXPLAIN QUERY PLAN SELECT id FROM workspaces
		WHERE COALESCE(NULLIF(TRIM(owner_user_id), ''), 'local')=? AND id>?
		AND COALESCE(status, '') NOT IN ('trashed','missing') ORDER BY id LIMIT ?`, "local", "w-001", 101).Scan(&planID, &parent, &unused, &plan); err != nil || !strings.Contains(plan, "idx_workspaces_updates_owner_id") {
		t.Fatal("keyset lost its bounded live-owner index", plan, err)
	}
	var mutations int
	if err := h.DB().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM sessions)+(SELECT COUNT(*) FROM workspace_welcome_enrollments)+(SELECT COUNT(*) FROM workspace_welcome_messages)`).Scan(&mutations); err != nil || mutations != 0 {
		t.Fatal("listing wrote host state", mutations, err)
	}
}

func TestWorkspaceUpdateListingRejectsUnboundedOrMalformedPages(t *testing.T) {
	store, cleanup := setupHybridStore(t, 5)
	defer cleanup()
	h := store.(*hybridStore)
	for _, tc := range []struct {
		owner, after string
		limit        int
	}{
		{"", "", 1}, {"local", "", 0}, {"local", "", 101}, {"local", "", -1},
		{"local", strings.Repeat("x", 129), 1}, {"local", "x\n", 1}, {"local", " x", 1}, {"local", "\xff", 1},
	} {
		if _, err := h.ListWorkspaceUpdateIDs(t.Context(), tc.owner, tc.after, tc.limit); !errors.Is(err, ErrInvalidID) {
			t.Fatal(tc, err)
		}
	}
	if _, err := h.ReadWorkspaceUpdateTargets(t.Context(), "local", make([]string, 101)); !errors.Is(err, ErrInvalidID) {
		t.Fatal(err)
	}
}
