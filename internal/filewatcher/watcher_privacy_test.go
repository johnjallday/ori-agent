package filewatcher

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestWatchLifecycleLogsKeepFolderPathsPrivate(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	w, err := NewWatcher(testWatcherConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	root := t.TempDir()
	if err := w.Watch("opaque-watch-id", root); err != nil {
		t.Fatal(err)
	}
	if err := w.Unwatch("opaque-watch-id"); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "opaque-watch-id") {
		t.Fatal("watch identity was lost from diagnostics")
	}
	if strings.Contains(text, root) {
		t.Fatal("watch lifecycle diagnostics exposed the selected folder")
	}
}
