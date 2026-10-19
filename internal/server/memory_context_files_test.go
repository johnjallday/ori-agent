package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// FR23–FR26 through the server as wired: an external-memory sync refreshes
// every workspace's .ori/context files; a memory write re-renders once the
// server's observer is on (Server.Start registers it); the Memory station's
// switch writes and removes Ori's block in the folder-root files.
func TestMemoryContextFilesFollowSyncsWritesAndTheRootSwitch(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	if builder.server.memoryContext == nil {
		t.Fatal("the context-file writer is not wired")
	}
	if err := builder.workspaceStore.Save(&workspace.Workspace{ID: "chart-lab", Name: "Chart Lab", Status: workspace.StatusActive}); err != nil {
		t.Fatal(err)
	}
	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	if err := memory.Append("chart-lab", workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-10-19", Provenance: "user", Text: "Studies use 0.6% per-side fees."}); err != nil {
		t.Fatal(err)
	}
	folder, err := builder.workspaceFileStore.GetFolderPath("chart-lab")
	if err != nil {
		t.Fatal(err)
	}
	contextFile := filepath.Join(folder, ".ori", "context", "AGENTS.md")
	if _, err := os.Stat(contextFile); err == nil {
		t.Fatal("an unserved server wrote context files on a memory write")
	}

	if _, err := builder.externalMemoryRunner.Run(ctx, externalmemory.ReasonManual); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(contextFile) // #nosec G304 -- test workspace folder
	if err != nil || !strings.Contains(string(data), "0.6% per-side fees") {
		t.Fatalf("a sync should refresh the context files: %v\n%s", err, data)
	}

	// What Server.Start does: later memory writes re-render.
	workspace.SetMemoryWriteObserver(builder.server.memoryContext.Render)
	t.Cleanup(func() { workspace.SetMemoryWriteObserver(nil) })
	if err := memory.Append("chart-lab", workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-10-19", Provenance: "user", Text: "Exits on EMA20 below EMA50."}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(contextFile); !strings.Contains(string(data), "EMA20 below EMA50") { // #nosec G304 -- test workspace folder
		t.Fatalf("a memory write did not re-render:\n%s", data)
	}

	patch := func(body string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPatch, "/api/workspaces/chart-lab/memory/settings", strings.NewReader(body))
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"context_files"`) {
			t.Fatalf("PATCH %s: %d %s", body, result.Code, result.Body.String())
		}
	}
	// A plain workspace's folder root is its own folder.
	rootFile := filepath.Join(folder, "AGENTS.md")
	patch(`{"root_files":true}`)
	if data, err := os.ReadFile(rootFile); err != nil || !strings.Contains(string(data), workspace.MemoryBlockStart) { // #nosec G304 -- test workspace folder
		t.Fatalf("switching on did not write the root block: %v\n%s", err, data)
	}
	patch(`{"root_files":false}`)
	if _, err := os.Stat(rootFile); !os.IsNotExist(err) {
		t.Fatalf("switching off should delete a root file holding only Ori's block (err=%v)", err)
	}
	if _, err := os.Stat(contextFile); err != nil {
		t.Fatal("the .ori/context files stay when the root switch goes off")
	}
}
