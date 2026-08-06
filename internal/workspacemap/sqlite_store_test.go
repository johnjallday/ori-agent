package workspacemap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

// seedWorkspace inserts the minimum workspace row a position can point at. The
// positions table has a real foreign key, so a test that skips this is testing
// a constraint violation rather than the store.
func seedWorkspace(t *testing.T, db *database.DB, ids ...string) {
	t.Helper()
	now := time.Now().UTC()
	for _, id := range ids {
		if _, err := db.ExecContext(context.Background(), `
			INSERT INTO workspaces (id, name, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(id) DO NOTHING
		`, id, id, now, now); err != nil {
			t.Fatalf("seed workspace %q: %v", id, err)
		}
	}
}

func TestLoadReturnsDefaultLayoutWithoutWriting(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %d, want %d", layout.SchemaVersion, SchemaVersion)
	}
	if !layout.SnapToGrid {
		t.Error("a new layout must default to snapping enabled (FR-57)")
	}
	if layout.Viewport != nil {
		t.Errorf("viewport = %+v, want nil so the map opens on Fit All (FR-45)", layout.Viewport)
	}
	if len(layout.Positions) != 0 {
		t.Errorf("positions = %v, want empty", layout.Positions)
	}

	// FR-23: viewing deterministic fallback placement must not create a record.
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspace_map_layouts`).Scan(&rows); err != nil {
		t.Fatalf("count layouts: %v", err)
	}
	if rows != 0 {
		t.Errorf("layout rows after read = %d, want 0; reading must never write", rows)
	}
}

func TestApplySetPositionsRoundTrips(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-a", "ws-b")

	result, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 38, Y: -76}}),
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Revision != 1 {
		t.Errorf("revision = %d, want 1", result.Revision)
	}
	if got := result.Positions["ws-a"]; got != (Point{X: 38, Y: -76}) {
		t.Errorf("committed position = %+v, want {38 -76}", got)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["ws-a"]; got != (Point{X: 38, Y: -76}) {
		t.Errorf("reloaded position = %+v, want {38 -76}", got)
	}
	if layout.Revision != 1 {
		t.Errorf("reloaded revision = %d, want 1", layout.Revision)
	}
}

func TestApplyMergesAgainstLatestRecord(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-a", "ws-b")

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 10, Y: 10}, "ws-b": {X: 20, Y: 20}}),
	}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}
	// A second tab moves only ws-a. FR-101: ws-b must survive untouched.
	result, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 99, Y: 99}}),
	}})
	if err != nil {
		t.Fatalf("Apply move: %v", err)
	}
	if len(result.Positions) != 1 {
		t.Errorf("result positions = %v, want only the committed node", result.Positions)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["ws-a"]; got != (Point{X: 99, Y: 99}) {
		t.Errorf("ws-a = %+v, want {99 99}", got)
	}
	if got := layout.Positions["ws-b"]; got != (Point{X: 20, Y: 20}) {
		t.Errorf("ws-b = %+v, want {20 20}; a partial update must not erase unrelated anchors", got)
	}
	if layout.Revision != 2 {
		t.Errorf("revision = %d, want 2", layout.Revision)
	}
}

func TestSameNodeLastAcceptedDropWins(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-a")

	// Two tabs drop the same building in different places. The later accepted
	// drop is the committed one, and each caller learns the revision its own
	// write produced (FR-102).
	first, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 10, Y: 10}}),
	}})
	if err != nil {
		t.Fatalf("Apply first: %v", err)
	}
	second, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 20, Y: 20}}),
	}})
	if err != nil {
		t.Fatalf("Apply second: %v", err)
	}
	if second.Revision <= first.Revision {
		t.Errorf("revisions = %d then %d, want strictly increasing", first.Revision, second.Revision)
	}
	if second.Positions["ws-a"] != (Point{X: 20, Y: 20}) {
		t.Errorf("committed position = %+v, want the later drop", second.Positions["ws-a"])
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.Positions["ws-a"] != (Point{X: 20, Y: 20}) {
		t.Errorf("stored position = %+v, want the later drop", layout.Positions["ws-a"])
	}
}

func TestApplyIsAtomicAcrossOperations(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-a")

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 5, Y: 5}}),
	}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}

	// "ws-missing" has no workspace row, so its insert violates the foreign key
	// mid-patch. The valid sibling in the same patch must roll back with it.
	_, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 500, Y: 500}, "ws-missing": {X: 1, Y: 1}}),
	}})
	if err == nil {
		t.Fatal("Apply with an unknown workspace should fail")
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["ws-a"]; got != (Point{X: 5, Y: 5}) {
		t.Errorf("ws-a = %+v, want the pre-patch {5 5}; a failed patch must roll back whole", got)
	}
	if layout.Revision != 1 {
		t.Errorf("revision = %d, want 1; a rolled-back patch must not consume a revision", layout.Revision)
	}
}

func TestApplyViewportAndPreferences(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetViewport(Viewport{CenterX: 120.5, CenterY: -40.25, Zoom: 1.5}),
		SetSnapToGrid(false),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.Viewport == nil || *layout.Viewport != (Viewport{CenterX: 120.5, CenterY: -40.25, Zoom: 1.5}) {
		t.Errorf("viewport = %+v, want {120.5 -40.25 1.5}", layout.Viewport)
	}
	if layout.SnapToGrid {
		t.Error("snap preference did not persist as disabled (FR-57)")
	}
}

func TestLoadDropsOnlyCorruptEntries(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-good", "ws-bad")

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-good": {X: 7, Y: 7}, "ws-bad": {X: 1, Y: 1}}),
		SetViewport(Viewport{CenterX: 0, CenterY: 0, Zoom: 1}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Corrupt one anchor and one camera axis the way a hand-edited or
	// partially-written database would.
	if _, err := db.ExecContext(ctx, `UPDATE workspace_map_positions SET x = 'not-a-number' WHERE workspace_id = 'ws-bad'`); err != nil {
		t.Fatalf("corrupt position: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE workspace_map_layouts SET viewport_zoom = 'wat' WHERE user_id = 'local'`); err != nil {
		t.Fatalf("corrupt viewport: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := layout.Positions["ws-good"]; got != (Point{X: 7, Y: 7}) {
		t.Errorf("ws-good = %+v, want {7 7}; one corrupt row must not discard valid siblings (FR-22)", got)
	}
	if _, present := layout.Positions["ws-bad"]; present {
		t.Error("corrupt anchor should degrade to fallback placement, not be returned")
	}
	if layout.Viewport != nil {
		t.Errorf("viewport = %+v, want nil so the map opens on a sensible default (FR-45)", layout.Viewport)
	}
}

func TestLoadRefusesUnsupportedSchemaVersion(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{SetSnapToGrid(true)}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE workspace_map_layouts SET schema_version = 99 WHERE user_id = 'local'`); err != nil {
		t.Fatalf("bump schema version: %v", err)
	}

	if _, err := store.Load(ctx, "local"); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Load error = %v, want ErrUnsupportedSchemaVersion", err)
	}
	// The refusal must also block writes, so this build never overwrites a
	// newer format with its own.
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{SetSnapToGrid(false)}}); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Apply error = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

func TestLayoutsAreIsolatedPerUser(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-shared")

	if _, err := store.Apply(ctx, "alice", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-shared": {X: 100, Y: 0}}),
	}}); err != nil {
		t.Fatalf("Apply alice: %v", err)
	}
	if _, err := store.Apply(ctx, "bob", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-shared": {X: -100, Y: 0}}),
	}}); err != nil {
		t.Fatalf("Apply bob: %v", err)
	}

	alice, err := store.Load(ctx, "alice")
	if err != nil {
		t.Fatalf("Load alice: %v", err)
	}
	bob, err := store.Load(ctx, "bob")
	if err != nil {
		t.Fatalf("Load bob: %v", err)
	}
	if alice.Positions["ws-shared"].X != 100 || bob.Positions["ws-shared"].X != -100 {
		t.Errorf("layouts leaked between users: alice=%+v bob=%+v", alice.Positions, bob.Positions)
	}
}

func TestResetAndRestorePositions(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-a", "ws-b")

	before := map[string]Point{"ws-a": {X: 1, Y: 2}, "ws-b": {X: 3, Y: 4}}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(before),
		SetSnapToGrid(false),
	}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{Reset()}}); err != nil {
		t.Fatalf("Apply reset: %v", err)
	}
	afterReset, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load after reset: %v", err)
	}
	if len(afterReset.Positions) != 0 {
		t.Errorf("positions after reset = %v, want none", afterReset.Positions)
	}
	if afterReset.SnapToGrid {
		t.Error("reset must preserve the snap preference (FR-110)")
	}

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{RestorePositions(before)}}); err != nil {
		t.Fatalf("Apply undo: %v", err)
	}
	restored, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load after undo: %v", err)
	}
	if len(restored.Positions) != 2 || restored.Positions["ws-a"] != (Point{X: 1, Y: 2}) || restored.Positions["ws-b"] != (Point{X: 3, Y: 4}) {
		t.Errorf("restored positions = %v, want the exact pre-reset set (FR-112)", restored.Positions)
	}
}

// TestMapOperationsLeaveWorkspaceRowsByteIdentical is success metric 5, asserted
// against the real database rather than a fake.
//
// Every map operation the feature has runs against a seeded workspace, and then
// every column of that workspace row is compared with what it was. The map is
// allowed to remember where a building sits; it is not allowed to touch a single
// thing about the workspace itself (FR-6).
func TestMapOperationsLeaveWorkspaceRowsByteIdentical(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "grp", "ws-a", "ws-b")
	store.SetDescendantResolver(staticResolver{"grp": {"grp", "ws-a"}})

	if _, err := db.ExecContext(ctx, `
		UPDATE workspaces
		SET parent_id = 'grp', layout = '{"pan":{"x":9}}', status = 'active', description = 'unchanged'
		WHERE id = 'ws-a'
	`); err != nil {
		t.Fatalf("seed workspace fields: %v", err)
	}
	before := snapshotWorkspaceRows(t, db)

	steps := []Patch{
		{Operations: []Operation{SetPositions(map[string]Point{"ws-a": {X: 38, Y: 38}, "ws-b": {X: 380, Y: 38}, "grp": {X: 10, Y: 10}})}},
		{Operations: []Operation{SetViewport(Viewport{CenterX: 100, CenterY: 100, Zoom: 1.5})}},
		{Operations: []Operation{SetSnapToGrid(false)}},
		{Operations: []Operation{TranslateGroup("grp", Point{X: 76, Y: 0})}},
		{Operations: []Operation{RestorePositions(map[string]Point{"ws-a": {X: 1, Y: 1}})}},
		{Operations: []Operation{Reset()}},
	}
	for i, patch := range steps {
		if _, err := store.Apply(ctx, "local", patch); err != nil {
			t.Fatalf("step %d (%s): %v", i, patch.Operations[0].Kind, err)
		}
	}

	if after := snapshotWorkspaceRows(t, db); after != before {
		t.Errorf("map operations changed workspace data.\nbefore: %s\nafter:  %s", before, after)
	}
}

type staticResolver map[string][]string

func (s staticResolver) GroupNodeIDs(_ context.Context, groupID string) ([]string, error) {
	return s[groupID], nil
}

// snapshotWorkspaceRows serializes every column of every workspace row, so a
// change to any field — hierarchy, order, status, timestamps, content, the
// per-workspace CanvasLayout — shows up as a difference.
func snapshotWorkspaceRows(t *testing.T, db *database.DB) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT * FROM workspaces ORDER BY id`)
	if err != nil {
		t.Fatalf("read workspaces: %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read columns: %v", err)
	}
	var out strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan workspace row: %v", err)
		}
		for i, column := range columns {
			fmt.Fprintf(&out, "%s=%v;", column, values[i])
		}
		out.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate workspaces: %v", err)
	}
	return out.String()
}

func TestPositionsSurviveTrashAndVanishOnPermanentDelete(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-a", "ws-b")

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"ws-a": {X: 11, Y: 11}, "ws-b": {X: 22, Y: 22}}),
	}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Soft delete keeps the workspace row, so the anchor must survive for
	// restore (FR-26, FR-27).
	if _, err := db.ExecContext(ctx, `UPDATE workspaces SET status = 'trashed' WHERE id = 'ws-a'`); err != nil {
		t.Fatalf("trash workspace: %v", err)
	}
	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load after trash: %v", err)
	}
	if got := layout.Positions["ws-a"]; got != (Point{X: 11, Y: 11}) {
		t.Errorf("trashed workspace anchor = %+v, want it retained for restore", got)
	}

	// Permanent deletion cascades away exactly that one anchor (FR-28).
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces WHERE id = 'ws-a'`); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	layout, err = store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load after delete: %v", err)
	}
	if _, present := layout.Positions["ws-a"]; present {
		t.Error("permanently deleted workspace kept its anchor")
	}
	if got := layout.Positions["ws-b"]; got != (Point{X: 22, Y: 22}) {
		t.Errorf("sibling anchor = %+v, want {22 22} undisturbed", got)
	}
}
