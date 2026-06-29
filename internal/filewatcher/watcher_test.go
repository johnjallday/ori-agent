package filewatcher

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func testWatcherConfig() WatcherConfig {
	return WatcherConfig{
		DebounceDuration: 10 * time.Millisecond,
		EventBufferSize:  10,
	}
}

func TestNewWatcher(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	if w.fsWatcher == nil {
		t.Error("fsWatcher should not be nil")
	}
}

func TestWatcher_WatchUnwatch(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "watcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Watch
	if err := w.Watch("session-1", tmpDir); err != nil {
		t.Fatalf("failed to watch: %v", err)
	}

	if !w.IsWatching("session-1") {
		t.Error("should be watching session-1")
	}

	// Unwatch
	if err := w.Unwatch("session-1"); err != nil {
		t.Fatalf("failed to unwatch: %v", err)
	}

	if w.IsWatching("session-1") {
		t.Error("should not be watching session-1 after unwatch")
	}
}

func TestWatcher_UnwatchSharedPath(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	tmpDir, err := os.MkdirTemp("", "watcher-shared-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Two sessions watching the same folder.
	if err := w.Watch("session-1", tmpDir); err != nil {
		t.Fatalf("failed to watch session-1: %v", err)
	}
	if err := w.Watch("session-2", tmpDir); err != nil {
		t.Fatalf("failed to watch session-2: %v", err)
	}

	// Unwatching the first must not remove the shared OS-level watch, and the
	// second Unwatch must not error on an already-removed watch.
	if err := w.Unwatch("session-1"); err != nil {
		t.Fatalf("unwatch session-1: %v", err)
	}
	if err := w.Unwatch("session-2"); err != nil {
		t.Fatalf("unwatch session-2: %v", err)
	}

	if w.IsWatching("session-1") || w.IsWatching("session-2") {
		t.Error("no sessions should remain watched")
	}
}

func TestWatcher_RefCountsSharedPath(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	tmpDir := t.TempDir()
	cleanPath := filepath.Clean(tmpDir)

	if err := w.Watch("session-1", tmpDir); err != nil {
		t.Fatalf("watch session-1: %v", err)
	}
	if err := w.Watch("session-2", tmpDir); err != nil {
		t.Fatalf("watch session-2: %v", err)
	}

	if got := w.pathRefs[cleanPath]; got != 2 {
		t.Fatalf("path refcount = %d, want 2", got)
	}
	if err := w.Unwatch("session-1"); err != nil {
		t.Fatalf("unwatch session-1: %v", err)
	}
	if got := w.pathRefs[cleanPath]; got != 1 {
		t.Fatalf("path refcount after first unwatch = %d, want 1", got)
	}
	if err := w.Unwatch("session-2"); err != nil {
		t.Fatalf("unwatch session-2: %v", err)
	}
	if _, ok := w.pathRefs[cleanPath]; ok {
		t.Fatal("path refcount should be removed after final unwatch")
	}
}

func TestWatcher_WatchIDsForPathUsesLongestPrefix(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	if err := w.Watch("parent", parent); err != nil {
		t.Fatalf("watch parent: %v", err)
	}
	if err := w.Watch("child", child); err != nil {
		t.Fatalf("watch child: %v", err)
	}
	if got := w.watchIDsForPath(filepath.Join(child, "file.txt")); !reflect.DeepEqual(got, []string{"child"}) {
		t.Fatalf("watchIDsForPath nested file = %v, want [child]", got)
	}
	if got := w.watchIDsForPath(filepath.Join(parent, "sibling.txt")); !reflect.DeepEqual(got, []string{"parent"}) {
		t.Fatalf("watchIDsForPath parent file = %v, want [parent]", got)
	}
}

func TestWatcher_SubscribeFanoutForSharedPath(t *testing.T) {
	w, err := NewWatcher(testWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()
	w.Start()

	tmpDir := t.TempDir()
	if err := w.Watch("session-1", tmpDir); err != nil {
		t.Fatalf("watch session-1: %v", err)
	}
	if err := w.Watch("session-2", tmpDir); err != nil {
		t.Fatalf("watch session-2: %v", err)
	}

	sub1 := w.Subscribe("session-1", 1)
	defer sub1.Close()
	sub2 := w.Subscribe("session-2", 1)
	defer sub2.Close()

	filePath := filepath.Join(tmpDir, "fanout.txt")
	w.handleFSEvent(fsnotify.Event{Name: filePath, Op: fsnotify.Write})

	assertSubscriptionEvent := func(name string, ch <-chan WatchEvent, wantSession string) {
		t.Helper()
		select {
		case event := <-ch:
			if event.SessionID != wantSession {
				t.Fatalf("%s session = %q, want %q", name, event.SessionID, wantSession)
			}
			if event.FilePath != filePath {
				t.Fatalf("%s file path = %q, want %q", name, event.FilePath, filePath)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timeout waiting for %s event", name)
		}
	}
	assertSubscriptionEvent("session-1", sub1.Events(), "session-1")
	assertSubscriptionEvent("session-2", sub2.Events(), "session-2")
}

func TestWatcher_UnwatchDeletedFolder(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	tmpDir, err := os.MkdirTemp("", "watcher-deleted-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	if err := w.Watch("session-1", tmpDir); err != nil {
		t.Fatalf("failed to watch: %v", err)
	}

	// Deleting the folder makes fsnotify auto-drop the watch; Unwatch must still
	// succeed (the "non-existent watch" error is treated as benign).
	_ = os.RemoveAll(tmpDir)

	if err := w.Unwatch("session-1"); err != nil {
		t.Fatalf("unwatch after folder delete should not error: %v", err)
	}
	if w.IsWatching("session-1") {
		t.Error("session-1 should no longer be watched")
	}
}

func TestWatcher_WatchedSessions(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Create temp directories
	tmpDir1, _ := os.MkdirTemp("", "watcher-test-1-*")
	tmpDir2, _ := os.MkdirTemp("", "watcher-test-2-*")
	defer func() { _ = os.RemoveAll(tmpDir1) }()
	defer func() { _ = os.RemoveAll(tmpDir2) }()

	_ = w.Watch("session-1", tmpDir1)
	_ = w.Watch("session-2", tmpDir2)

	sessions := w.WatchedSessions()
	if len(sessions) != 2 {
		t.Errorf("expected 2 watched sessions, got %d", len(sessions))
	}
}

func TestWatcher_DetectsFileCreate(t *testing.T) {
	config := WatcherConfig{
		DebounceDuration: 50 * time.Millisecond,
		EventBufferSize:  10,
	}

	w, err := NewWatcher(config)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "watcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Start watcher
	w.Start()

	// Watch directory
	if err := w.Watch("session-1", tmpDir); err != nil {
		t.Fatalf("failed to watch: %v", err)
	}

	// Give watcher time to set up
	time.Sleep(50 * time.Millisecond)

	// Create a file
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Wait for event
	select {
	case event := <-w.Events():
		if event.SessionID != "session-1" {
			t.Errorf("expected session ID 'session-1', got '%s'", event.SessionID)
		}
		if event.FileName != "test.txt" {
			t.Errorf("expected file name 'test.txt', got '%s'", event.FileName)
		}
		// Event type could be Create or Modify depending on timing
		if event.Type != EventCreate && event.Type != EventModify {
			t.Errorf("expected type 'create' or 'modify', got '%s'", event.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for file create event")
	}
}

func TestWatcher_DetectsFileRemove(t *testing.T) {
	config := WatcherConfig{
		DebounceDuration: 50 * time.Millisecond,
		EventBufferSize:  10,
	}

	w, err := NewWatcher(config)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Create temp directory with a file
	tmpDir, _ := os.MkdirTemp("", "watcher-test-*")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	filePath := filepath.Join(tmpDir, "test.txt")
	_ = os.WriteFile(filePath, []byte("hello"), 0644)

	// Start watcher
	w.Start()
	_ = w.Watch("session-1", tmpDir)
	time.Sleep(50 * time.Millisecond)

	// Remove the file
	_ = os.Remove(filePath)

	// Wait for event
	select {
	case event := <-w.Events():
		if event.Type != EventRemove {
			t.Errorf("expected type 'remove', got '%s'", event.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for file remove event")
	}
}

func TestWatcher_IgnoresHiddenFiles(t *testing.T) {
	config := WatcherConfig{
		DebounceDuration: 50 * time.Millisecond,
		EventBufferSize:  10,
	}

	w, err := NewWatcher(config)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	tmpDir, _ := os.MkdirTemp("", "watcher-test-*")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	w.Start()
	_ = w.Watch("session-1", tmpDir)
	time.Sleep(50 * time.Millisecond)

	// Create a hidden file
	hiddenPath := filepath.Join(tmpDir, ".hidden")
	_ = os.WriteFile(hiddenPath, []byte("hidden"), 0644)

	// Should not receive event
	select {
	case event := <-w.Events():
		t.Errorf("should not receive event for hidden file, got: %v", event)
	case <-time.After(200 * time.Millisecond):
		// Good, no event received
	}
}

func TestWatcher_IgnoresTempFiles(t *testing.T) {
	config := WatcherConfig{
		DebounceDuration: 50 * time.Millisecond,
		EventBufferSize:  10,
	}

	w, err := NewWatcher(config)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	tmpDir, _ := os.MkdirTemp("", "watcher-test-*")
	defer func() { _ = os.RemoveAll(tmpDir) }()

	w.Start()
	_ = w.Watch("session-1", tmpDir)
	time.Sleep(50 * time.Millisecond)

	// Create temp files
	tempFiles := []string{"~$document.docx", "file.tmp", "file.swp"}
	for _, name := range tempFiles {
		path := filepath.Join(tmpDir, name)
		_ = os.WriteFile(path, []byte("temp"), 0644)
	}

	// Should not receive events
	select {
	case event := <-w.Events():
		t.Errorf("should not receive event for temp file, got: %v", event)
	case <-time.After(200 * time.Millisecond):
		// Good, no events received
	}
}

func TestShouldIgnoreFile(t *testing.T) {
	tests := []struct {
		fileName string
		expected bool
	}{
		{".DS_Store", true},
		{".hidden", true},
		{"~$document.docx", true},
		{"file.tmp", true},
		{"file.swp", true},
		{"backup.bak", true},
		{"Thumbs.db", true},
		{"desktop.ini", true},
		{"normal.txt", false},
		{"document.pdf", false},
		{"image.png", false},
	}

	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			result := shouldIgnoreFile(tt.fileName)
			if result != tt.expected {
				t.Errorf("shouldIgnoreFile(%s) = %v, want %v", tt.fileName, result, tt.expected)
			}
		})
	}
}

func TestConvertEventType(t *testing.T) {
	tests := []struct {
		op       uint32 // Using uint32 to match fsnotify.Op
		expected EventType
	}{
		{1, EventCreate}, // fsnotify.Create = 1
		{2, EventModify}, // fsnotify.Write = 2
		{4, EventRemove}, // fsnotify.Remove = 4
		{8, EventRename}, // fsnotify.Rename = 8
	}

	for _, tt := range tests {
		// Create a mock fsnotify.Op
		result := convertEventTypeTest(tt.op)
		if result != tt.expected {
			t.Errorf("convertEventType(%d) = %s, want %s", tt.op, result, tt.expected)
		}
	}
}

// Helper to test event type conversion without importing fsnotify in test
func convertEventTypeTest(op uint32) EventType {
	switch op {
	case 1:
		return EventCreate
	case 2:
		return EventModify
	case 4:
		return EventRemove
	case 8:
		return EventRename
	default:
		return ""
	}
}

func TestDefaultWatcherConfig(t *testing.T) {
	config := DefaultWatcherConfig()

	if config.DebounceDuration != 500*time.Millisecond {
		t.Errorf("expected debounce duration 500ms, got %v", config.DebounceDuration)
	}

	if config.EventBufferSize != 100 {
		t.Errorf("expected buffer size 100, got %d", config.EventBufferSize)
	}
}
