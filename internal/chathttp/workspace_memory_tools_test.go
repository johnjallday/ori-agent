package chathttp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/userprofile"
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

func TestMemoryWriteTool_RespectsServerOwnedHQSuppressionBeforeAppending(t *testing.T) {
	p, fs := newMemoryToolProvider(t)
	calls := 0
	p.SetHQVisibilityDeps(HQVisibilityDeps{MemoryWriteGuard: func(_ context.Context, workspaceID, text string) error {
		calls++
		if workspaceID != "ws-mem-1" || text != "forgotten personal fact" {
			return nil
		}
		return workspace.ErrMemoryManaged
	}})
	if _, err := p.memoryWriteTool().Call(context.Background(), `{"text":"forgotten personal fact"}`); err != workspace.ErrMemoryManaged {
		t.Fatalf("generic tool bypassed server-owned suppression: %v", err)
	}
	if _, err := p.memoryWriteTool().Call(context.Background(), `{"text":"unrelated task baseline"}`); err != nil {
		t.Fatalf("ordinary tool write was blocked: %v", err)
	}
	if calls != 2 || strings.Contains(readMemoryFile(t, fs), "forgotten personal fact") {
		t.Fatalf("memory guard did not cover each write: calls=%d", calls)
	}
}

func TestMemoryWriteTool_UsesAtomicHostWriteInsteadOfSeparateGuard(t *testing.T) {
	p, fs := newMemoryToolProvider(t)
	calls := 0
	p.SetHQVisibilityDeps(HQVisibilityDeps{
		MemoryWriteGuard: func(context.Context, string, string) error {
			t.Fatal("a non-atomic separate check must not run for a host-handled write")
			return nil
		},
		MemoryWrite: func(_ context.Context, workspaceID string, entry workspace.MemoryEntry) (bool, error) {
			calls++
			if workspaceID != "ws-mem-1" || entry.Text != "rejected reviewed wording" {
				t.Fatalf("wrong host write input: %s %+v", workspaceID, entry)
			}
			return true, workspace.ErrMemoryManaged
		},
	})
	out, err := p.memoryWriteTool().Call(context.Background(), `{"text":"rejected reviewed wording"}`)
	folder, pathErr := fs.GetFolderPath("ws-mem-1")
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	_, fileErr := os.Stat(filepath.Join(folder, workspace.MemoryFileName))
	if !errors.Is(err, workspace.ErrMemoryManaged) || out != "" || calls != 1 || !errors.Is(fileErr, os.ErrNotExist) {
		t.Fatalf("atomic host refusal leaked/changed canonical memory: %q %v calls=%d file=%v", out, err, calls, fileErr)
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

func TestProfileSetTool_UpdatesBehavioralFields(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := userprofile.NewSQLiteStore(db)

	p := NewWorkspaceToolProvider(nil, nil, "ws-profile")
	p.SetUserProfileDeps(store, userprofile.LocalUserProvider{})
	if _, err := p.profileSetTool().Call(ctx, `{"fields":{"preferences.response_style":"concise","about":"Prefers direct answers."}}`); err != nil {
		t.Fatalf("profile_set: %v", err)
	}
	got, err := store.Get(ctx, userprofile.LocalUserID)
	if err != nil {
		t.Fatalf("Get profile: %v", err)
	}
	if got.Preferences["response_style"] != "concise" || got.About != "Prefers direct answers." {
		t.Fatalf("profile_set did not update behavioral fields: %#v", got)
	}
}

func TestProfileSetTool_GuardedWriteCannotRaceUserForget(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := userprofile.NewSQLiteStore(db)
	if err := store.Upsert(ctx, &userprofile.UserProfile{
		ID: "local", Preferences: map[string]string{"response_style": "concise"},
	}); err != nil {
		t.Fatal(err)
	}
	p := NewWorkspaceToolProvider(nil, nil, "ordinary-workspace")
	p.SetUserProfileDeps(store, userprofile.LocalUserProvider{})
	p.SetHQVisibilityDeps(HQVisibilityDeps{ProfileWriteGuard: func(ctx context.Context, _ string, _ map[string]any) (time.Time, error) {
		before, err := store.Get(ctx, "local")
		if err != nil {
			return time.Time{}, err
		}
		_, err = store.UpdateFieldCAS(ctx, "local", "preferences.response_style", before.UpdatedAt, "concise", "")
		return before.UpdatedAt, err
	}})
	if result, err := p.profileSetTool().Call(ctx, `{"fields":{"preferences.response_style":"concise","about":"old tool result"}}`); !errors.Is(err, userprofile.ErrProfileConflict) || result != "" {
		t.Fatalf("interleaved Forget must conflict without echoing profile: %q %v", result, err)
	}
	got, err := store.Get(ctx, "local")
	if err != nil || got.Preferences["response_style"] != "" || got.About != "" {
		t.Fatalf("tool undid canonical Forget or wrote part of its bundle: %+v %v", got, err)
	}
	p.SetHQVisibilityDeps(HQVisibilityDeps{ProfileWriteGuard: func(context.Context, string, map[string]any) (time.Time, error) {
		return time.Time{}, workspace.ErrMemoryManaged
	}})
	if result, err := p.profileSetTool().Call(ctx, `{"field":"preferences.response_style","value":"concise"}`); !errors.Is(err, workspace.ErrMemoryManaged) || result != "" {
		t.Fatalf("prior reviewed text must not be returned or restored: %q %v", result, err)
	}
}

func TestProfileSetTool_RejectsIdentityFields(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := userprofile.NewSQLiteStore(db)

	p := NewWorkspaceToolProvider(nil, nil, "ws-profile")
	p.SetUserProfileDeps(store, userprofile.LocalUserProvider{})
	if _, err := p.profileSetTool().Call(ctx, `{"field":"timezone","value":"America/New_York"}`); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("identity field should be rejected, got %v", err)
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
