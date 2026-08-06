package workspacemap

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeLister struct {
	workspaces []*workspace.Workspace
	err        error
	calls      int
}

func (f *fakeLister) ListActive() ([]*workspace.Workspace, error) {
	f.calls++
	return f.workspaces, f.err
}

func ws(id, parent string) *workspace.Workspace {
	return &workspace.Workspace{ID: id, ParentID: parent, OwnerUserID: "local"}
}

func TestGroupNodeIDsFlattensEveryDepth(t *testing.T) {
	// V1 draws one district per top-level group with deeper members inside it,
	// so a cluster move has to carry them all (FR-92).
	resolver := NewDescendantResolver(&fakeLister{workspaces: []*workspace.Workspace{
		ws("grp", ""),
		ws("child-a", "grp"),
		ws("child-b", "grp"),
		ws("grandchild", "child-a"),
		ws("outsider", ""),
	}})

	ids, err := resolver.GroupNodeIDs(context.Background(), "grp")
	if err != nil {
		t.Fatalf("GroupNodeIDs: %v", err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{"grp", "child-a", "child-b", "grandchild"} {
		if !got[want] {
			t.Errorf("cluster is missing %q: %v", want, ids)
		}
	}
	if got["outsider"] {
		t.Errorf("cluster swallowed a workspace outside the group: %v", ids)
	}
	if ids[0] != "grp" {
		t.Errorf("first id = %q, want the group itself", ids[0])
	}
}

func TestGroupNodeIDsRejectsUnknownGroup(t *testing.T) {
	resolver := NewDescendantResolver(&fakeLister{workspaces: []*workspace.Workspace{ws("grp", "")}})
	if _, err := resolver.GroupNodeIDs(context.Background(), "ghost"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("error = %v, want ErrNodeNotFound", err)
	}
}

func TestGroupNodeIDsSurvivesACyclicHierarchy(t *testing.T) {
	// A hierarchy that has somehow become cyclic must fail to hang, not fail to
	// answer.
	resolver := NewDescendantResolver(&fakeLister{workspaces: []*workspace.Workspace{
		ws("grp", "child"),
		ws("child", "grp"),
	}})
	ids, err := resolver.GroupNodeIDs(context.Background(), "grp")
	if err != nil {
		t.Fatalf("GroupNodeIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("ids = %v, want each node once", ids)
	}
}

func TestTranslateGroupMovesEveryMemberByTheSameDelta(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "grp", "child-a", "child-b", "outsider")
	store.SetDescendantResolver(NewDescendantResolver(&fakeLister{workspaces: []*workspace.Workspace{
		ws("grp", ""),
		ws("child-a", "grp"),
		ws("child-b", "grp"),
		ws("outsider", ""),
	}}))

	before := map[string]Point{
		"grp":      {X: 100, Y: 100},
		"child-a":  {X: 140, Y: 140},
		"child-b":  {X: 300, Y: 220},
		"outsider": {X: 900, Y: 900},
	}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{SetPositions(before)}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		TranslateGroup("grp", Point{X: 38, Y: -76}),
	}}); err != nil {
		t.Fatalf("Apply translate: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, id := range []string{"grp", "child-a", "child-b"} {
		want := Point{X: before[id].X + 38, Y: before[id].Y - 76}
		if layout.Positions[id] != want {
			t.Errorf("%s = %+v, want %+v", id, layout.Positions[id], want)
		}
	}
	// Relative spacing is preserved exactly, which is what "moves as one
	// cluster" means (FR-86).
	if layout.Positions["child-b"].X-layout.Positions["grp"].X != 200 {
		t.Errorf("relative spacing changed: %+v", layout.Positions)
	}
	if layout.Positions["outsider"] != (Point{X: 900, Y: 900}) {
		t.Errorf("a workspace outside the group moved: %+v", layout.Positions["outsider"])
	}
}

func TestTranslateGroupIsAllOrNothing(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "grp", "child")
	store.SetDescendantResolver(NewDescendantResolver(&fakeLister{workspaces: []*workspace.Workspace{
		ws("grp", ""),
		ws("child", "grp"),
	}}))

	before := map[string]Point{"grp": {X: 0, Y: 0}, "child": {X: MaxCoordinate - 10, Y: 0}}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{SetPositions(before)}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}

	// The delta would push the child outside the safe world. Shearing the
	// cluster to fit would be worse than refusing, so the whole move is refused.
	_, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		TranslateGroup("grp", Point{X: 1000, Y: 0}),
	}})
	if err == nil {
		t.Fatal("translating a member out of the safe world should fail")
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.Positions["grp"] != (Point{X: 0, Y: 0}) || layout.Positions["child"] != before["child"] {
		t.Errorf("a refused cluster move left members moved: %+v", layout.Positions)
	}
	if layout.Revision != 1 {
		t.Errorf("revision = %d, want 1; a rolled-back move must not consume one", layout.Revision)
	}
}

func TestTranslateGroupSkipsMembersWithNoSavedAnchor(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	seedWorkspace(t, db, "grp", "placed", "unplaced")
	store.SetDescendantResolver(NewDescendantResolver(&fakeLister{workspaces: []*workspace.Workspace{
		ws("grp", ""),
		ws("placed", "grp"),
		ws("unplaced", "grp"),
	}}))

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"grp": {X: 50, Y: 50}, "placed": {X: 90, Y: 90}}),
	}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}
	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		TranslateGroup("grp", Point{X: 10, Y: 10}),
	}}); err != nil {
		t.Fatalf("Apply translate: %v", err)
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, present := layout.Positions["unplaced"]; present {
		t.Error("a member on fallback placement was silently promoted to a saved anchor")
	}
	if layout.Positions["placed"] != (Point{X: 100, Y: 100}) {
		t.Errorf("placed = %+v, want it translated", layout.Positions["placed"])
	}
}

// dbReadingResolver resolves membership by reading the same database the store
// writes to — exactly what the production resolver does through the workspace
// store.
type dbReadingResolver struct {
	db  *database.DB
	ids []string
}

func (r *dbReadingResolver) GroupNodeIDs(ctx context.Context, _ string) ([]string, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspaces`).Scan(&count); err != nil {
		return nil, err
	}
	return r.ids, nil
}

// TestTranslateGroupResolvesMembersOutsideTheWriteTransaction pins the ordering
// that makes cluster moves work at all.
//
// Resolving membership from inside the write transaction takes a second
// connection to the same file, which then waits on the write lock the
// transaction is holding. That is a deadlock: the request hangs until the busy
// timeout expires. A fake in-memory resolver cannot catch it, so this one reads
// the real database.
func TestTranslateGroupResolvesMembersOutsideTheWriteTransaction(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{
		Path:    filepath.Join(t.TempDir(), "map.db"),
		WALMode: false,
	})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLiteStore(db)
	seedWorkspace(t, db, "grp", "child")
	store.SetDescendantResolver(&dbReadingResolver{db: db, ids: []string{"grp", "child"}})

	if _, err := store.Apply(ctx, "local", Patch{Operations: []Operation{
		SetPositions(map[string]Point{"grp": {X: 10, Y: 10}, "child": {X: 50, Y: 50}}),
	}}); err != nil {
		t.Fatalf("Apply seed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, applyErr := store.Apply(ctx, "local", Patch{Operations: []Operation{
			TranslateGroup("grp", Point{X: 38, Y: 0}),
		}})
		done <- applyErr
	}()

	select {
	case applyErr := <-done:
		if applyErr != nil {
			t.Fatalf("Apply translate: %v", applyErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cluster move deadlocked: membership must be resolved before the write transaction opens")
	}

	layout, err := store.Load(ctx, "local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if layout.Positions["grp"] != (Point{X: 48, Y: 10}) {
		t.Errorf("grp = %+v, want it translated", layout.Positions["grp"])
	}
}

func TestTranslateGroupWithoutAResolverIsRefused(t *testing.T) {
	store, db := newTestStore(t)
	seedWorkspace(t, db, "grp")
	_, err := store.Apply(context.Background(), "local", Patch{Operations: []Operation{
		TranslateGroup("grp", Point{X: 10, Y: 10}),
	}})
	if !errors.Is(err, ErrGroupResolverUnavailable) {
		t.Fatalf("error = %v, want ErrGroupResolverUnavailable", err)
	}
}
