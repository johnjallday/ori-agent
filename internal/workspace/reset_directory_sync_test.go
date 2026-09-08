package workspace

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

type blockingDirectorySyncStore struct {
	*directorySyncTestStore
	started chan struct{}
	finish  chan struct{}
	once    sync.Once
}

func (s *blockingDirectorySyncStore) Get(id string) (*Workspace, error) {
	s.once.Do(func() { close(s.started) })
	<-s.finish
	return s.directorySyncTestStore.Get(id)
}

func TestResetAdmissionDirectorySyncOwnsStoreAndWatcherUpdate(t *testing.T) {
	dir := t.TempDir()
	workspace := &Workspace{ID: "owned", Status: StatusActive, DirectoryReferences: []DirectoryReference{{ID: "dir", Path: dir}}}
	store := &blockingDirectorySyncStore{
		directorySyncTestStore: &directorySyncTestStore{workspaces: map[string]*Workspace{"owned": workspace}},
		started:                make(chan struct{}), finish: make(chan struct{}),
	}
	manager, err := NewDirectorySyncManager(store, DefaultEventBus(), DefaultDirectorySyncConfig())
	if err != nil {
		t.Fatal(err)
	}
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	manager.Start()
	finish := sync.OnceFunc(func() { close(store.finish) })
	t.Cleanup(func() { finish(); manager.Stop() })
	done := make(chan error, 1)
	go func() { _, err := manager.WatchWorkspace("owned"); done <- err }()
	select {
	case <-store.started:
	case <-time.After(5 * time.Second):
		t.Fatal("directory sync did not enter store")
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatalf("directory sync not tracked: %v", err)
	}
	release, err := gate.Enter()
	if err != nil {
		t.Fatalf("busy refusal fenced new work: %v", err)
	}
	release()
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("directory sync did not finish")
	}
	manager.Stop()
	if !manager.watcher.IsWatching("owned::dir") {
		t.Fatal("completed watcher registration was lost")
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdmissionDirectorySyncRefusesBeforeOwnersOrPublish(t *testing.T) {
	store := &directorySyncTestStore{workspaces: map[string]*Workspace{"owned": {ID: "owned"}}}
	bus := DefaultEventBus()
	var calls atomic.Int32
	bus.Subscribe(func(Event) { calls.Add(1) }, nil)
	manager, err := NewDirectorySyncManager(store, bus, DefaultDirectorySyncConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.watcher.Close() }()
	gate := &resetstate.WorkGate{}
	manager.SetAdmissionGate(gate)
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	manager.Start()
	if manager.started {
		t.Fatal("fenced manager started OS watcher loops")
	}
	if _, err := manager.WatchWorkspace("owned"); !errors.Is(err, resetstate.ErrWorkFenced) {
		t.Fatal(err)
	}
	manager.watched["watch"] = directoryWatchTarget{WorkspaceID: "owned"}
	manager.handleWatchEvent(filewatcher.WatchEvent{SessionID: "watch"})
	if calls.Load() != 0 {
		t.Fatal("fenced watcher event was published")
	}
}
