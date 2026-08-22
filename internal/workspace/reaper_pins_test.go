package workspace

import (
	"fmt"
	"sync"
	"testing"
)

func newReaperPinTestService(t *testing.T) (*ReaperPinService, Store) {
	t.Helper()
	// FileStore, not InMemoryStore: FileStore.Get deserializes a fresh copy
	// on every read, which is what makes store.Update genuinely atomic — the
	// concurrency assertion below depends on that.
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewReaperPinService(store), store
}

func newReaperPinTestWorkspace(t *testing.T, store Store) *Workspace {
	t.Helper()
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Alpha"})
	if err := store.Save(ws); err != nil {
		t.Fatalf("save workspace: %v", err)
	}
	return ws
}

func TestReaperPinService_PinAppendsInOrder(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	if err := svc.Pin(ws.ID, "custom:a.lua"); err != nil {
		t.Fatalf("Pin a: %v", err)
	}
	if err := svc.Pin(ws.ID, "custom:b.lua"); err != nil {
		t.Fatalf("Pin b: %v", err)
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"custom:a.lua", "custom:b.lua"}
	if !equalStringSlices(got.PinnedReaperScripts, want) {
		t.Fatalf("PinnedReaperScripts = %v, want %v", got.PinnedReaperScripts, want)
	}
}

func TestReaperPinService_PinTwiceIsNoop(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	if err := svc.Pin(ws.ID, "custom:a.lua"); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := svc.Pin(ws.ID, "custom:a.lua"); err != nil {
		t.Fatalf("Pin again: %v", err)
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PinnedReaperScripts) != 1 {
		t.Fatalf("PinnedReaperScripts = %v, want exactly one entry", got.PinnedReaperScripts)
	}
}

func TestReaperPinService_UnpinRemovesAndPreservesOrder(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	for _, id := range []string{"custom:a.lua", "custom:b.lua", "custom:c.lua"} {
		if err := svc.Pin(ws.ID, id); err != nil {
			t.Fatalf("Pin %s: %v", id, err)
		}
	}
	if err := svc.Unpin(ws.ID, "custom:b.lua"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"custom:a.lua", "custom:c.lua"}
	if !equalStringSlices(got.PinnedReaperScripts, want) {
		t.Fatalf("PinnedReaperScripts = %v, want %v", got.PinnedReaperScripts, want)
	}
}

func TestReaperPinService_UnpinNotPinnedIsNoop(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	if err := svc.Pin(ws.ID, "custom:a.lua"); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := svc.Unpin(ws.ID, "custom:never-pinned.lua"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !equalStringSlices(got.PinnedReaperScripts, []string{"custom:a.lua"}) {
		t.Fatalf("PinnedReaperScripts = %v, want unchanged", got.PinnedReaperScripts)
	}
}

func TestReaperPinService_ReorderPermutesExistingPins(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	for _, id := range []string{"custom:a.lua", "custom:b.lua", "custom:c.lua"} {
		if err := svc.Pin(ws.ID, id); err != nil {
			t.Fatalf("Pin %s: %v", id, err)
		}
	}
	reordered := []string{"custom:c.lua", "custom:a.lua", "custom:b.lua"}
	if err := svc.Reorder(ws.ID, reordered); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !equalStringSlices(got.PinnedReaperScripts, reordered) {
		t.Fatalf("PinnedReaperScripts = %v, want %v", got.PinnedReaperScripts, reordered)
	}
}

func TestReaperPinService_ReorderRejectsMismatchedSet(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	if err := svc.Pin(ws.ID, "custom:a.lua"); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	cases := [][]string{
		{"custom:a.lua", "custom:b.lua"}, // extra id not currently pinned
		{},                               // missing the currently pinned id
		{"custom:a.lua", "custom:a.lua"}, // duplicate
		{"custom:z.lua"},                 // wholesale substitution
	}
	for _, orderedIDs := range cases {
		if err := svc.Reorder(ws.ID, orderedIDs); err == nil {
			t.Fatalf("Reorder(%v) succeeded, want error", orderedIDs)
		}
	}

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !equalStringSlices(got.PinnedReaperScripts, []string{"custom:a.lua"}) {
		t.Fatalf("a rejected Reorder mutated state: %v", got.PinnedReaperScripts)
	}
}

// FR from tasks-reaper-station-discoverability.md 1.3: a pin surviving after
// its script is deleted from the shared library is expected — pruning is a
// read-time filter in internal/reaperhttp, not here. This test only asserts
// that the persisted field itself is untouched by anything in this package;
// it is not the pruning behavior itself.
func TestReaperPinService_PinSurvivesUntouchedWhenScriptGoneElsewhere(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	if err := svc.Pin(ws.ID, "custom:deleted-later.lua"); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !equalStringSlices(got.PinnedReaperScripts, []string{"custom:deleted-later.lua"}) {
		t.Fatalf("PinnedReaperScripts = %v, want the pin to remain persisted", got.PinnedReaperScripts)
	}
}

// Concurrency hardening, mirroring
// TestTicketService_ConcurrentReorders_LeaveConsistentRanks
// (ticket_concurrency_test.go:173): every operation runs against a real
// FileStore and the test asserts an INVARIANT about the surviving data, not
// a timing outcome — no ID is ever duplicated or silently lost.
func TestReaperPinService_ConcurrentPinsNeverLoseAWrite(t *testing.T) {
	svc, store := newReaperPinTestService(t)
	ws := newReaperPinTestWorkspace(t, store)

	const writers = 24
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func(n int) {
			defer wg.Done()
			_ = svc.Pin(ws.ID, fmt.Sprintf("custom:script-%d.lua", n))
		}(i)
	}
	wg.Wait()

	got, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	seen := make(map[string]bool, writers)
	for _, id := range got.PinnedReaperScripts {
		if seen[id] {
			t.Fatalf("script id %s pinned twice after concurrent Pin calls", id)
		}
		seen[id] = true
	}
	if len(seen) != writers {
		t.Fatalf("got %d pinned scripts, want %d — a concurrent Pin was lost", len(seen), writers)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
