package chathttp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newMemoryToolProvider(t *testing.T) (*WorkspaceToolProvider, *workspace.FileStore) {
	t.Helper()
	fs, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ws := &workspace.Workspace{ID: "ws-mem-1", Name: "Mem Test"}
	if err := fs.Save(ws); err != nil {
		t.Fatalf("Save workspace: %v", err)
	}
	p := NewWorkspaceToolProvider(nil, nil, ws.ID)
	p.SetFileStore(fs)
	p.SetExecutingAgent("Scout")
	return p, fs
}

func readMemoryFile(t *testing.T, fs *workspace.FileStore) string {
	t.Helper()
	folder, err := fs.GetFolderPath("ws-mem-1")
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(folder, workspace.MemoryFileName))
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	return string(data)
}

func TestMemoryWriteTool_AppendsEntry(t *testing.T) {
	p, fs := newMemoryToolProvider(t)

	out, err := p.memoryWriteTool().Call(context.Background(), `{"text":"build baseline is ~7 min","type":"watch"}`)
	if err != nil {
		t.Fatalf("memory_write: %v", err)
	}
	if !strings.Contains(out, "workspace memory") {
		t.Errorf("response should confirm the save, got: %s", out)
	}

	content := readMemoryFile(t, fs)
	want := "- [watch, " + time.Now().Format("2006-01-02") + ", agent:Scout] build baseline is ~7 min"
	if !strings.Contains(content, want) {
		t.Errorf("MEMORY.md missing entry %q, got:\n%s", want, content)
	}
}

func TestMemoryWriteTool_CollapsesWhitespaceAndDefaultsType(t *testing.T) {
	p, fs := newMemoryToolProvider(t)

	if _, err := p.memoryWriteTool().Call(context.Background(), `{"text":"line one\nline   two"}`); err != nil {
		t.Fatalf("memory_write: %v", err)
	}
	content := readMemoryFile(t, fs)
	if !strings.Contains(content, "- [fact, ") || !strings.Contains(content, "] line one line two") {
		t.Errorf("expected single-line fact entry, got:\n%s", content)
	}
}

func TestMemoryWriteTool_Validation(t *testing.T) {
	p, _ := newMemoryToolProvider(t)
	tool := p.memoryWriteTool()

	if _, err := tool.Call(context.Background(), `{"text":"   "}`); err == nil {
		t.Error("empty text should be rejected")
	}

	long := strings.Repeat("x", workspace.MemoryEntryMaxLen+1)
	if _, err := tool.Call(context.Background(), `{"text":"`+long+`"}`); err == nil || !strings.Contains(err.Error(), "note") {
		t.Errorf("over-length text should point the agent to notes, got: %v", err)
	}

	if _, err := tool.Call(context.Background(), `{"text":"the api key is sk-abc1234567890def"}`); err == nil || !strings.Contains(err.Error(), "Vault") {
		t.Errorf("secret-looking text should point the agent to the Vault, got: %v", err)
	}
	if _, err := tool.Call(context.Background(), `{"text":"-----BEGIN RSA PRIVATE KEY----- stuff"}`); err == nil {
		t.Error("PEM header should be refused")
	}
}

func TestMemoryWriteTool_TaskProvenance(t *testing.T) {
	p, fs := newMemoryToolProvider(t)
	p.SetTaskID("task-7")

	if _, err := p.memoryWriteTool().Call(context.Background(), `{"text":"checked through commit abc123"}`); err != nil {
		t.Fatalf("memory_write: %v", err)
	}
	if content := readMemoryFile(t, fs); !strings.Contains(content, "task:task-7 (Scout)") {
		t.Errorf("task-scoped writes should carry task provenance, got:\n%s", content)
	}
}

func TestMemoryForgetTool(t *testing.T) {
	p, fs := newMemoryToolProvider(t)
	write := p.memoryWriteTool()
	for _, text := range []string{"alpha beta", "beta gamma"} {
		if _, err := write.Call(context.Background(), `{"text":"`+text+`"}`); err != nil {
			t.Fatalf("seed write: %v", err)
		}
	}
	forget := p.memoryForgetTool()

	if _, err := forget.Call(context.Background(), `{"match":"beta"}`); err == nil || !strings.Contains(err.Error(), "alpha beta") {
		t.Errorf("ambiguous match should list candidates, got: %v", err)
	}
	out, err := forget.Call(context.Background(), `{"match":"gamma"}`)
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if !strings.Contains(out, "beta gamma") {
		t.Errorf("response should name the removed entry, got: %s", out)
	}
	if content := readMemoryFile(t, fs); strings.Contains(content, "beta gamma") {
		t.Errorf("entry should be gone from MEMORY.md:\n%s", content)
	}
}

func TestMemoryTools_RequireFileStore(t *testing.T) {
	p := NewWorkspaceToolProvider(nil, nil, "ws-x")
	p.SetExecutingAgent("Scout")

	if _, err := p.memoryWriteTool().Call(context.Background(), `{"text":"hi"}`); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("missing file store should be an instructive error, got: %v", err)
	}

	names := func(p *WorkspaceToolProvider) map[string]bool {
		out := map[string]bool{}
		for _, tool := range p.Tools() {
			out[tool.Definition().Name] = true
		}
		return out
	}
	if got := names(p); got[workspace.MemoryWriteToolName] || got[workspace.MemoryForgetToolName] {
		t.Error("memory tools should not register without a file store")
	}

	withFS, _ := newMemoryToolProvider(t)
	if got := names(withFS); !got[workspace.MemoryWriteToolName] || !got[workspace.MemoryForgetToolName] {
		t.Error("memory tools should register when the file store is wired")
	}
}
