package agentmap

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/database"
)

func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestStore(t *testing.T) (*SQLiteStore, *database.DB) {
	t.Helper()
	db := newTestDB(t)
	return NewSQLiteStore(db), db
}

// stubLister is the read-only "which agents exist" seam. Nothing about it can
// change an agent, which is the point of the interface being one method.
type stubLister struct{ names []string }

func (s stubLister) ListAgents() []string { return s.names }

func TestLoadReturnsDefaultLayoutWithoutWriting(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.Revision != 0 || len(layout.Positions) != 0 || layout.Viewport != nil {
		t.Fatalf("layout = %+v, want an empty default", layout)
	}
	if !layout.SnapToGrid {
		t.Fatal("a fresh layout should have snapping on")
	}

	// Reading must not create a record. A user who has only ever looked at the
	// map has nothing stored.
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_map_layouts`).Scan(&rows); err != nil {
		t.Fatalf("count layouts: %v", err)
	}
	if rows != 0 {
		t.Fatalf("layout rows after a read = %d, want 0", rows)
	}
}

func TestApplyPersistsPositionsAndBumpsRevision(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	result, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 10, Y: 20}}),
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Layout.Revision != 1 {
		t.Fatalf("revision = %d, want 1", result.Layout.Revision)
	}
	if got := result.Layout.Positions["Atlas"]; got != (Point{X: 10, Y: 20}) {
		t.Fatalf("Atlas = %v, want (10, 20)", got)
	}

	// A second write bumps again and merges rather than replacing.
	result, err = store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Beacon": {X: -5, Y: 5}}),
	}})
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if result.Layout.Revision != 2 {
		t.Fatalf("revision = %d, want 2", result.Layout.Revision)
	}
	if len(result.Layout.Positions) != 2 {
		t.Fatalf("positions = %v, want both agents — a partial write must not erase the other", result.Layout.Positions)
	}
}

// The revision is what lets a tab that has been open across someone else's
// change be recognised instead of silently clobbering it (FR-53).
func TestApplyRejectsAStaleRevision(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}

	// Revision is now 1. A client echoing 0 means "I did not check" and is
	// allowed; a client echoing a specific wrong number is refused.
	_, err := store.Apply(ctx, "local", Patch{
		ExpectedRevision: 99,
		Operations:       []Operation{SetPositions(map[string]Point{"Atlas": {X: 2, Y: 2}})},
	})
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("error = %v, want ErrStaleRevision", err)
	}

	// The refused write changed nothing.
	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["Atlas"]; got != (Point{X: 1, Y: 1}) {
		t.Fatalf("Atlas = %v, want the pre-conflict (1, 1)", got)
	}
	if layout.Revision != 1 {
		t.Fatalf("revision = %d, want 1 — a rejected write must not consume one", layout.Revision)
	}

	// Echoing the correct revision succeeds.
	if _, err := store.Apply(ctx, "local", Patch{
		ExpectedRevision: 1,
		Operations:       []Operation{SetPositions(map[string]Point{"Atlas": {X: 3, Y: 3}})},
	}); err != nil {
		t.Fatalf("Apply with the current revision: %v", err)
	}
}

// A position for an agent that no longer exists is ignored on read, so an
// orphan from any path is harmless rather than corrupting (FR-57).
func TestLoadIgnoresPositionsForAgentsThatNoLongerExist(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Ghost": {X: 2, Y: 2}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// With no lister every stored row is returned — the safe direction.
	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load without a lister: %v", err)
	}
	if len(layout.Positions) != 2 {
		t.Fatalf("positions = %v, want both while no lister is wired", layout.Positions)
	}

	store.SetAgentLister(stubLister{names: []string{"Atlas"}})
	layout, err = store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load with a lister: %v", err)
	}
	if _, ok := layout.Positions["Ghost"]; ok {
		t.Fatalf("positions = %v, want the orphan dropped", layout.Positions)
	}
	if _, ok := layout.Positions["Atlas"]; !ok {
		t.Fatalf("positions = %v, want the live agent kept", layout.Positions)
	}
}

// A write response is what the client adopts, so it must report exactly what
// its next read would return. Reporting an orphan as stored made the client
// echo a dead name back on its next patch, where it was refused as an unknown
// agent — a save failure caused by the response before it.
func TestApplyResponseHidesOrphansTheSameWayLoadDoes(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Ghost": {X: 2, Y: 2}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	store.SetAgentLister(stubLister{names: []string{"Atlas"}})

	result, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 3, Y: 3}}),
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := result.Layout.Positions["Ghost"]; ok {
		t.Fatalf("write response = %v, want the orphan hidden as Load hides it", result.Layout.Positions)
	}

	loaded, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(result.Layout.Positions) != len(loaded.Positions) {
		t.Fatalf("write response %v disagrees with the next read %v", result.Layout.Positions, loaded.Positions)
	}
}

// The lister's names are compared case-insensitively, matching how the agent
// API resolves a name elsewhere. Without this a stored "Atlas" would be dropped
// by a lister reporting "atlas" — a live agent losing its saved tile.
func TestLoadMatchesAgentNamesCaseInsensitively(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	store.SetAgentLister(stubLister{names: []string{"atlas"}})

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := layout.Positions["Atlas"]; !ok {
		t.Fatalf("positions = %v, want Atlas kept despite the lister's lowercase name", layout.Positions)
	}
}

func TestViewportAndPreferencesRoundTrip(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	want := Viewport{CenterX: 120.5, CenterY: -80.25, Zoom: 0.5}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetViewport(want),
		SetPreferences(false),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.Viewport == nil || *layout.Viewport != want {
		t.Fatalf("viewport = %v, want %v", layout.Viewport, want)
	}
	if layout.SnapToGrid {
		t.Fatal("snap preference should have been stored as false")
	}
}

// Reset clears the arrangement and nothing else: the camera and the snap
// preference survive, because neither is part of where the tiles are.
func TestResetClearsPositionsButKeepsCameraAndPreference(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	camera := Viewport{CenterX: 40, CenterY: 40, Zoom: 1.5}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Beacon": {X: 2, Y: 2}}),
		SetViewport(camera),
		SetPreferences(false),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{Reset()}}); err != nil {
		t.Fatalf("reset Apply: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(layout.Positions) != 0 {
		t.Fatalf("positions = %v, want empty after a reset", layout.Positions)
	}
	if layout.Viewport == nil || *layout.Viewport != camera {
		t.Fatalf("viewport = %v, want the camera preserved across a reset", layout.Viewport)
	}
	if layout.SnapToGrid {
		t.Fatal("the snap preference should survive a reset")
	}
}

// Undo restores the exact prior arrangement, which is what makes the reset
// action safe to offer (FR-72).
func TestRestorePositionsReplacesTheWholeSet(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Beacon": {X: 2, Y: 2}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{Reset()}}); err != nil {
		t.Fatalf("reset: %v", err)
	}

	result, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		RestorePositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Beacon": {X: 2, Y: 2}}),
	}})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(result.Layout.Positions) != 2 {
		t.Fatalf("positions = %v, want both restored", result.Layout.Positions)
	}

	// A restore is a whole-set replacement, so an anchor absent from it is gone.
	result, err = store.Apply(ctx, "local", Patch{Operations: []Operation{
		RestorePositions(map[string]Point{"Atlas": {X: 9, Y: 9}}),
	}})
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if len(result.Layout.Positions) != 1 {
		t.Fatalf("positions = %v, want only Atlas", result.Layout.Positions)
	}
}

func TestDeletePositionsRemovesOnlyTheNamedAgents(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Beacon": {X: 2, Y: 2}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}

	if err := store.DeletePositions(ctx, "Atlas"); err != nil {
		t.Fatalf("DeletePositions: %v", err)
	}
	// Naming an agent with no stored position is not an error.
	if err := store.DeletePositions(ctx, "Nobody"); err != nil {
		t.Fatalf("DeletePositions for an unknown agent: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := layout.Positions["Atlas"]; ok {
		t.Fatalf("positions = %v, want Atlas removed", layout.Positions)
	}
	if _, ok := layout.Positions["Beacon"]; !ok {
		t.Fatalf("positions = %v, want Beacon untouched", layout.Positions)
	}
}

// The rename path saves the new record and then deletes the old one, which is
// the exact shape that has twice destroyed per-agent data here. The position is
// carried explicitly (FR-58).
func TestRenamePositionCarriesTheAnchor(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 42, Y: -13}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}

	if err := store.RenamePosition(ctx, "Atlas", "Atlas Prime"); err != nil {
		t.Fatalf("RenamePosition: %v", err)
	}

	store.SetAgentLister(stubLister{names: []string{"Atlas Prime"}})
	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := layout.Positions["Atlas Prime"]
	if !ok {
		t.Fatalf("positions = %v, want the anchor under the new name", layout.Positions)
	}
	if got != (Point{X: 42, Y: -13}) {
		t.Fatalf("carried point = %v, want the exact original coordinates", got)
	}
	if _, ok := layout.Positions["Atlas"]; ok {
		t.Fatalf("positions = %v, want nothing left under the old name", layout.Positions)
	}
}

func TestRenamePositionIsANoOpForTheSameName(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 5, Y: 5}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	if err := store.RenamePosition(ctx, "Atlas", "Atlas"); err != nil {
		t.Fatalf("RenamePosition: %v", err)
	}
	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["Atlas"]; got != (Point{X: 5, Y: 5}) {
		t.Fatalf("Atlas = %v, want it untouched", got)
	}
}

// A rename onto a name that already holds a row must not abort on the primary
// key and strand the position under a name that is about to disappear.
func TestRenamePositionOverwritesAnExistingDestination(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Beacon": {X: 2, Y: 2}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	if err := store.RenamePosition(ctx, "Atlas", "Beacon"); err != nil {
		t.Fatalf("RenamePosition onto an occupied name: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["Beacon"]; got != (Point{X: 1, Y: 1}) {
		t.Fatalf("Beacon = %v, want the carried anchor to win", got)
	}
	if _, ok := layout.Positions["Atlas"]; ok {
		t.Fatalf("positions = %v, want the old name gone", layout.Positions)
	}
}

// A layout written by a newer build is refused outright rather than read as
// empty, because reading it as empty would invite the next write to overwrite
// it (FR-54).
func TestLoadRefusesAnUnsupportedSchemaVersion(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_map_layouts SET schema_version = 99`); err != nil {
		t.Fatalf("bump schema version: %v", err)
	}

	if _, err := store.Load(ctx, "local"); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("error = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

// A record predating versioning reads as a fresh layout rather than being
// refused, so an older install is not locked out of its own map.
func TestLoadTreatsSchemaVersionZeroAsUnwritten(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_map_layouts SET schema_version = 0`); err != nil {
		t.Fatalf("zero the schema version: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := layout.Positions["Atlas"]; !ok {
		t.Fatalf("positions = %v, want the anchors still readable", layout.Positions)
	}
}

// One unreadable row costs that agent its saved position and nothing else. A
// corrupt coordinate must never take the whole map down with it.
func TestLoadDropsOnlyTheUnreadableRow(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}, "Beacon": {X: 2, Y: 2}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}
	// A coordinate outside the safe world, as a hand-edited database or an
	// older build could leave behind.
	if _, err := db.ExecContext(ctx, `
		UPDATE agent_map_positions SET x = ? WHERE agent_name = 'Beacon'
	`, MaxCoordinate*10); err != nil {
		t.Fatalf("corrupt a coordinate: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := layout.Positions["Beacon"]; ok {
		t.Fatalf("positions = %v, want the out-of-range anchor dropped", layout.Positions)
	}
	if _, ok := layout.Positions["Atlas"]; !ok {
		t.Fatalf("positions = %v, want the valid sibling kept", layout.Positions)
	}
}

// Layouts are per user: one user's arrangement is invisible to another's.
func TestLayoutsAreScopedPerUser(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "alice", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("alice Apply: %v", err)
	}

	layout, err := store.Load(ctx, "bob")
	if err != nil {
		t.Fatalf("bob Load: %v", err)
	}
	if len(layout.Positions) != 0 {
		t.Fatalf("bob sees %v, want an empty layout of his own", layout.Positions)
	}
}

// An empty user id is the single-user install, which must resolve to one
// consistent identity rather than to a separate blank-named layout.
func TestEmptyUserIDResolvesToTheLocalUser(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 7, Y: 7}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	layout, err := store.Load(ctx, LocalUserID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["Atlas"]; got != (Point{X: 7, Y: 7}) {
		t.Fatalf("Atlas = %v, want the anchor written under the blank id", got)
	}
}

// A rejected patch leaves the stored layout byte-identical: no revision
// consumed, no partial move committed.
func TestARejectedPatchCommitsNothing(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 1, Y: 1}}),
	}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}

	// The second operation is invalid, so the first must not land either.
	_, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"Atlas": {X: 50, Y: 50}}),
		SetViewport(Viewport{CenterX: 0, CenterY: 0, Zoom: 900}),
	}})
	if !errors.Is(err, ErrInvalidZoom) {
		t.Fatalf("error = %v, want ErrInvalidZoom", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["Atlas"]; got != (Point{X: 1, Y: 1}) {
		t.Fatalf("Atlas = %v, want the original anchor", got)
	}
	if layout.Revision != 1 {
		t.Fatalf("revision = %d, want 1", layout.Revision)
	}
}
