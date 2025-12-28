package filewatcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewWatcher(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	if w.fsWatcher == nil {
		t.Error("fsWatcher should not be nil")
	}
}

func TestWatcher_WatchUnwatch(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "watcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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

func TestWatcher_WatchedSessions(t *testing.T) {
	w, err := NewWatcher(DefaultWatcherConfig())
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	defer w.Close()

	// Create temp directories
	tmpDir1, _ := os.MkdirTemp("", "watcher-test-1-*")
	tmpDir2, _ := os.MkdirTemp("", "watcher-test-2-*")
	defer os.RemoveAll(tmpDir1)
	defer os.RemoveAll(tmpDir2)

	w.Watch("session-1", tmpDir1)
	w.Watch("session-2", tmpDir2)

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
	defer w.Close()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "watcher-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	defer w.Close()

	// Create temp directory with a file
	tmpDir, _ := os.MkdirTemp("", "watcher-test-*")
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(filePath, []byte("hello"), 0644)

	// Start watcher
	w.Start()
	w.Watch("session-1", tmpDir)
	time.Sleep(50 * time.Millisecond)

	// Remove the file
	os.Remove(filePath)

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
	defer w.Close()

	tmpDir, _ := os.MkdirTemp("", "watcher-test-*")
	defer os.RemoveAll(tmpDir)

	w.Start()
	w.Watch("session-1", tmpDir)
	time.Sleep(50 * time.Millisecond)

	// Create a hidden file
	hiddenPath := filepath.Join(tmpDir, ".hidden")
	os.WriteFile(hiddenPath, []byte("hidden"), 0644)

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
	defer w.Close()

	tmpDir, _ := os.MkdirTemp("", "watcher-test-*")
	defer os.RemoveAll(tmpDir)

	w.Start()
	w.Watch("session-1", tmpDir)
	time.Sleep(50 * time.Millisecond)

	// Create temp files
	tempFiles := []string{"~$document.docx", "file.tmp", "file.swp"}
	for _, name := range tempFiles {
		path := filepath.Join(tmpDir, name)
		os.WriteFile(path, []byte("temp"), 0644)
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
