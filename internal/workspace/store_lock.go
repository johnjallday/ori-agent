package workspace

import "sync"

// LockTable hands out a per-workspace mutex used by Store implementations to
// serialize cross-instance mutations on the same workspace.
//
// The race it prevents: two goroutines call Get to obtain independent deep
// clones, mutate disjoint fields, and then Save. Without serialization the
// second Save overwrites the cache with a clone that never observed the first
// goroutine's mutation, silently dropping it. Holding a workspace's lock
// across the canonical Get + mutate + Save sequence eliminates that window.
//
// The zero value is ready to use. The table itself is concurrency-safe and
// lazily allocates per-workspace mutexes; existing entries are never
// garbage-collected since workspaces are long-lived in practice.
type LockTable struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Lock acquires the per-workspace mutex and returns an unlock function. The
// unlock function is safe to call exactly once.
func (t *LockTable) Lock(wsID string) func() {
	t.mu.Lock()
	if t.locks == nil {
		t.locks = make(map[string]*sync.Mutex)
	}
	m, ok := t.locks[wsID]
	if !ok {
		m = &sync.Mutex{}
		t.locks[wsID] = m
	}
	t.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// CanonicalUpdate is the standard implementation of Store.Update. It serializes
// against other Update calls for the same workspace via s.Lock, then runs the
// canonical Get → fn → Save sequence under that lock. Save is dispatched on s
// (not on a wrapped inner), so wrapper stores see their own Save override
// invoked even when the lock is held by the inner.
func CanonicalUpdate(s Store, wsID string, fn func(*Workspace) error) error {
	unlock := s.Lock(wsID)
	defer unlock()

	ws, err := s.Get(wsID)
	if err != nil {
		return err
	}
	if err := fn(ws); err != nil {
		return err
	}
	return s.Save(ws)
}
