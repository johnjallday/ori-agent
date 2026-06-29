package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
)

type directorySyncTestStore struct {
	workspaces map[string]*Workspace
}

func (s *directorySyncTestStore) Save(ws *Workspace) error {
	s.workspaces[ws.ID] = ws
	return nil
}

func (s *directorySyncTestStore) Get(id string) (*Workspace, error) {
	ws, ok := s.workspaces[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return ws, nil
}

func (s *directorySyncTestStore) List() ([]string, error) {
	ids := make([]string, 0, len(s.workspaces))
	for id := range s.workspaces {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *directorySyncTestStore) Delete(id string) error {
	delete(s.workspaces, id)
	return nil
}

func (s *directorySyncTestStore) ListActive() ([]*Workspace, error) {
	var active []*Workspace
	for _, ws := range s.workspaces {
		if ws.Status == StatusActive || ws.Status == "" {
			active = append(active, ws)
		}
	}
	return active, nil
}

func (s *directorySyncTestStore) GetFilesPath(workspaceID string) string {
	return filepath.Join("workspaces", workspaceID, "files")
}

func (s *directorySyncTestStore) GetOutputsPath(workspaceID string) string {
	return filepath.Join("workspaces", workspaceID, "outputs")
}

func (s *directorySyncTestStore) GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error) {
	return nil, false, nil
}

func (s *directorySyncTestStore) SaveWorkspaceAgent(workspaceID, agentName string, ag *agent.Agent) error {
	return nil
}

func (s *directorySyncTestStore) Lock(wsID string) func() { return func() {} }

func (s *directorySyncTestStore) Update(wsID string, fn func(*Workspace) error) error {
	return CanonicalUpdate(s, wsID, fn)
}

func TestDirectorySyncManagerEmitsWorkspaceUpdatedEvent(t *testing.T) {
	dir := t.TempDir()
	ws := &Workspace{
		ID:     "ws-sync-test",
		Name:   "Sync Test",
		Status: StatusActive,
		DirectoryReferences: []DirectoryReference{
			{
				ID:          "dir-1",
				WorkspaceID: "ws-sync-test",
				Name:        "Repo",
				Path:        dir,
			},
		},
	}

	store := &directorySyncTestStore{
		workspaces: map[string]*Workspace{
			ws.ID: ws,
		},
	}

	eventBus := DefaultEventBus()
	eventCh := make(chan Event, 8)
	subID := eventBus.Subscribe(func(event Event) {
		if event.Type != EventWorkspaceUpdated {
			return
		}
		if event.WorkspaceID != ws.ID {
			return
		}
		if action, _ := event.Data["action"].(string); action == "directory_synced" {
			eventCh <- event
		}
	}, nil)
	defer eventBus.Unsubscribe(subID)

	cfg := DefaultDirectorySyncConfig()
	cfg.PollInterval = 40 * time.Millisecond
	cfg.WatcherConfig.DebounceDuration = 25 * time.Millisecond
	cfg.WatcherConfig.EventBufferSize = 16

	manager, err := NewDirectorySyncManager(store, eventBus, cfg)
	if err != nil {
		t.Fatalf("failed to create directory sync manager: %v", err)
	}
	manager.Start()
	defer manager.Stop()

	if result, err := manager.WatchWorkspace(ws.ID); err != nil {
		t.Fatalf("WatchWorkspace: %v", err)
	} else if result.Watched != 1 {
		t.Fatalf("expected 1 watched directory, got %+v", result)
	}

	targetFile := filepath.Join(dir, "sync-event.txt")
	if err := os.WriteFile(targetFile, []byte("hello sync"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	select {
	case evt := <-eventCh:
		if evt.Source != "directory-sync" {
			t.Fatalf("expected source directory-sync, got %q", evt.Source)
		}
		if action, _ := evt.Data["action"].(string); action != "directory_synced" {
			t.Fatalf("expected action directory_synced, got %q", action)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for directory sync event")
	}
}

func TestDirectorySyncManagerSkipsTrashedWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws := &Workspace{
		ID:     "ws-trashed-sync",
		Name:   "Trashed Sync",
		Status: StatusTrashed,
		DirectoryReferences: []DirectoryReference{
			{
				ID:          "dir-trashed",
				WorkspaceID: "ws-trashed-sync",
				Name:        "Trashed",
				Path:        dir,
			},
		},
	}

	store := &directorySyncTestStore{
		workspaces: map[string]*Workspace{
			ws.ID: ws,
		},
	}

	manager, err := NewDirectorySyncManager(store, DefaultEventBus(), DefaultDirectorySyncConfig())
	if err != nil {
		t.Fatalf("failed to create directory sync manager: %v", err)
	}
	defer func() { _ = manager.watcher.Close() }()

	if result, err := manager.WatchWorkspace(ws.ID); err != nil {
		t.Fatalf("WatchWorkspace: %v", err)
	} else if result.Watched != 0 {
		t.Fatalf("expected no watched directories for trashed workspace, got %+v", result)
	}

	if len(manager.watched) != 0 {
		t.Fatalf("expected no watched directories for trashed workspace, got %d", len(manager.watched))
	}
}

func TestDirectorySyncManagerStartsIdle(t *testing.T) {
	dir := t.TempDir()
	ws := &Workspace{
		ID:     "ws-idle-sync",
		Name:   "Idle Sync",
		Status: StatusActive,
		DirectoryReferences: []DirectoryReference{{
			ID:          "dir-idle",
			WorkspaceID: "ws-idle-sync",
			Name:        "Repo",
			Path:        dir,
		}},
	}

	store := &directorySyncTestStore{
		workspaces: map[string]*Workspace{ws.ID: ws},
	}

	cfg := DefaultDirectorySyncConfig()
	cfg.PollInterval = 20 * time.Millisecond
	manager, err := NewDirectorySyncManager(store, DefaultEventBus(), cfg)
	if err != nil {
		t.Fatalf("failed to create directory sync manager: %v", err)
	}
	manager.Start()
	defer manager.Stop()

	time.Sleep(80 * time.Millisecond)

	if len(manager.watched) != 0 {
		t.Fatalf("expected manager to start idle, got %d watched directories", len(manager.watched))
	}
}

func TestDirectorySyncManagerRespectsWatchLimit(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	ws := &Workspace{
		ID:     "ws-limit-sync",
		Name:   "Limit Sync",
		Status: StatusActive,
		DirectoryReferences: []DirectoryReference{
			{ID: "dir-a", WorkspaceID: "ws-limit-sync", Name: "A", Path: dirA},
			{ID: "dir-b", WorkspaceID: "ws-limit-sync", Name: "B", Path: dirB},
		},
	}

	store := &directorySyncTestStore{
		workspaces: map[string]*Workspace{ws.ID: ws},
	}

	cfg := DefaultDirectorySyncConfig()
	cfg.MaxWatchedDirectories = 1
	manager, err := NewDirectorySyncManager(store, DefaultEventBus(), cfg)
	if err != nil {
		t.Fatalf("failed to create directory sync manager: %v", err)
	}
	defer func() { _ = manager.watcher.Close() }()

	result, err := manager.WatchWorkspace(ws.ID)
	if err != nil {
		t.Fatalf("WatchWorkspace: %v", err)
	}
	if result.Watched != 1 || result.SkippedOverLimit != 1 {
		t.Fatalf("expected one watch and one limit skip, got %+v", result)
	}
	if len(manager.watched) != 1 {
		t.Fatalf("expected one watched directory, got %d", len(manager.watched))
	}
}

func TestDirectorySyncManagerEvictsLeastRecentlyUsedWorkspaceAtLimit(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	wsA := &Workspace{
		ID:     "ws-lru-a",
		Name:   "LRU A",
		Status: StatusActive,
		DirectoryReferences: []DirectoryReference{
			{ID: "dir-a", WorkspaceID: "ws-lru-a", Name: "A", Path: dirA},
		},
	}
	wsB := &Workspace{
		ID:     "ws-lru-b",
		Name:   "LRU B",
		Status: StatusActive,
		DirectoryReferences: []DirectoryReference{
			{ID: "dir-b", WorkspaceID: "ws-lru-b", Name: "B", Path: dirB},
		},
	}

	store := &directorySyncTestStore{
		workspaces: map[string]*Workspace{
			wsA.ID: wsA,
			wsB.ID: wsB,
		},
	}

	cfg := DefaultDirectorySyncConfig()
	cfg.MaxWatchedDirectories = 1
	manager, err := NewDirectorySyncManager(store, DefaultEventBus(), cfg)
	if err != nil {
		t.Fatalf("failed to create directory sync manager: %v", err)
	}
	defer func() { _ = manager.watcher.Close() }()

	if result, err := manager.WatchWorkspace(wsA.ID); err != nil {
		t.Fatalf("WatchWorkspace A: %v", err)
	} else if result.Watched != 1 || result.SkippedOverLimit != 0 {
		t.Fatalf("expected workspace A to be watched, got %+v", result)
	}
	firstAccessed := manager.workspaceAccessed[wsA.ID]
	time.Sleep(time.Millisecond)

	if _, err := manager.watchWorkspace(wsA.ID, false); err != nil {
		t.Fatalf("refresh workspace A: %v", err)
	}
	if refreshedAccessed := manager.workspaceAccessed[wsA.ID]; !refreshedAccessed.Equal(firstAccessed) {
		t.Fatalf("refresh should not update LRU access time: before=%s after=%s", firstAccessed, refreshedAccessed)
	}

	if result, err := manager.WatchWorkspace(wsB.ID); err != nil {
		t.Fatalf("WatchWorkspace B: %v", err)
	} else if result.Watched != 1 || result.SkippedOverLimit != 0 {
		t.Fatalf("expected workspace B to evict A and be watched, got %+v", result)
	}

	if len(manager.watched) != 1 {
		t.Fatalf("expected one watched directory after eviction, got %d", len(manager.watched))
	}
	if _, ok := manager.watched[buildDirectoryWatchKey(wsB.ID, "dir-b")]; !ok {
		t.Fatalf("expected workspace B watch to remain, got %+v", manager.watched)
	}
	if _, ok := manager.watched[buildDirectoryWatchKey(wsA.ID, "dir-a")]; ok {
		t.Fatalf("expected workspace A watch to be evicted, got %+v", manager.watched)
	}
}
