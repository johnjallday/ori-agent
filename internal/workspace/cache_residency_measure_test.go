package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMeasure_FileStoreCacheResidency quantifies the resident heap cost of the
// eager FileStore cache, which holds a full Workspace struct (including embedded
// chat history) for every workspace on disk. This is the cost item 2.0 targets:
// reads are served from SQLite via the adapter, so these resident structs are
// largely dead weight.
//
// Skipped by default (it writes hundreds of files and allocates tens of MB). Run:
//
//	ORI_MEASURE=1 go test ./internal/workspace/ \
//	  -run TestMeasure_FileStoreCacheResidency -v -count=1
func TestMeasure_FileStoreCacheResidency(t *testing.T) {
	if os.Getenv("ORI_MEASURE") == "" {
		t.Skip("set ORI_MEASURE=1 to run the cache-residency measurement")
	}

	const (
		numWorkspaces = 500
		msgsPerWS     = 200
		contentBytes  = 300
	)

	dir := t.TempDir()
	content := strings.Repeat("x", contentBytes)
	now := time.Now()

	for i := range numWorkspaces {
		slug := fmt.Sprintf("ws-%04d", i)
		ws := newTestWorkspace(slug, fmt.Sprintf("Workspace %04d", i))
		ws.FolderSlug = slug
		ws.Messages = make([]AgentMessage, msgsPerWS)
		for j := range ws.Messages {
			ws.Messages[j] = AgentMessage{
				ID:        fmt.Sprintf("m-%d-%d", i, j),
				From:      "agent",
				To:        "user",
				Content:   content,
				Timestamp: now,
			}
		}
		folder := filepath.Join(dir, slug)
		if err := os.MkdirAll(folder, 0755); err != nil {
			t.Fatal(err)
		}
		data, err := ws.ToJSON()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, WorkspaceConfigFile), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	onDisk := dirSizeBytes(t, dir)

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	runtime.GC()
	runtime.ReadMemStats(&m1)

	cached := len(store.cache)
	indexed := len(store.idToPath)
	runtime.KeepAlive(store) // keep the cache resident across the measurement

	heapDelta := int64(m1.HeapAlloc) - int64(m0.HeapAlloc)
	t.Logf("workspaces=%d  msgs/ws=%d  content=%dB", numWorkspaces, msgsPerWS, contentBytes)
	t.Logf("on-disk workspace.json total: %s", humanBytes(onDisk))
	t.Logf("cache entries=%d  idToPath entries=%d", cached, indexed)
	t.Logf("resident heap delta after NewFileStore: %s (%.1f KB/workspace)",
		humanBytes(heapDelta), float64(heapDelta)/float64(numWorkspaces)/1024)

	_ = store.Close()
}

func dirSizeBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return total
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
