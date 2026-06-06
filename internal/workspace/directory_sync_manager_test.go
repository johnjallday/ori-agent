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

	// Let initial watcher registration complete.
	time.Sleep(150 * time.Millisecond)

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

	manager.syncWatchedDirectories()

	if len(manager.watched) != 0 {
		t.Fatalf("expected no watched directories for trashed workspace, got %d", len(manager.watched))
	}
}
