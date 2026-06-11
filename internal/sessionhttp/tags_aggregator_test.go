package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// setupUnifiedTagsHandler seeds every tag source: a session, a workspace
// (SQLite row + folder store with a disk-only tag), a note, a folder-store
// task, and a templates library manifest.
func setupUnifiedTagsHandler(t *testing.T) (*Handler, func()) {
	t.Helper()

	handler, cleanup := createTestHandler(t)
	ctx := t.Context()

	// Folder-based workspace store.
	fileStore, err := agentworkspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	handler.SetWorkspaceStore(fileStore)

	// Templates library with one manifest.
	templatesRoot := t.TempDir()
	tplDir := filepath.Join(templatesRoot, "demo-template")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := `{"name": "Demo", "tags": ["Music", "template-only"]}`
	if err := os.WriteFile(filepath.Join(tplDir, "template.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
	handler.SetTemplatesRootResolver(func() string { return templatesRoot })

	// Workspace: SQLite row without tags; folder store carries a disk-only
	// tag plus a tagged task. The aggregator must hydrate tags from disk.
	now := time.Now()
	ws := &session.Workspace{ID: "ws-tags", Name: "Tagged WS"}
	if err := handler.store.CreateWorkspace(ctx, ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	folderWS := &agentworkspace.Workspace{
		ID:         "ws-tags",
		Name:       "Tagged WS",
		Status:     agentworkspace.StatusActive,
		Tags:       []string{"music", "disk-only"},
		SharedData: make(map[string]any),
		Tasks: []agentworkspace.Task{
			{
				ID:          "task-1",
				WorkspaceID: "ws-tags",
				Description: "Tagged task",
				Tags:        []string{"music", "task-only"},
				Status:      agentworkspace.TaskStatusPending,
				CreatedAt:   now,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := fileStore.Save(folderWS); err != nil {
		t.Fatalf("fileStore.Save: %v", err)
	}

	// Session tags.
	sess := &session.Session{Title: "S", AgentName: "a", Tags: []string{"music", "session-only"}}
	if err := handler.store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Note tags.
	note := &session.WorkspaceNote{
		ID: "note-1", WorkspaceID: "ws-tags", Name: "N", Content: "c",
		Tags: []string{"music", "note-only"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := handler.store.CreateNote(ctx, note); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	return handler, func() {
		_ = fileStore.Close()
		cleanup()
	}
}

func TestHandleTags_ScopeAllAggregatesAllSources(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/tags?scope=all", nil)
	w := httptest.NewRecorder()
	handler.HandleTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Tags []UnifiedTag `json:"tags"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	byName := map[string]UnifiedTag{}
	for _, tag := range resp.Tags {
		byName[tag.Name] = tag
	}

	music, ok := byName["music"]
	if !ok {
		t.Fatalf("expected music tag in pool, got %#v", byName)
	}
	want := UnifiedTagCounts{Workspaces: 1, Sessions: 1, Notes: 1, Tasks: 1, Templates: 1}
	if music.Counts != want {
		t.Fatalf("music counts mismatch: got %+v want %+v", music.Counts, want)
	}
	if music.Total != 5 {
		t.Fatalf("music total mismatch: got %d", music.Total)
	}
	if resp.Tags[0].Name != "music" {
		t.Fatalf("expected music first (highest total), got %q", resp.Tags[0].Name)
	}

	// One representative per single-source tag, including the disk-only
	// workspace tag that exists in workspace.json but not in SQLite, and the
	// template manifest tag (normalized to lowercase).
	for name, want := range map[string]UnifiedTagCounts{
		"disk-only":     {Workspaces: 1},
		"session-only":  {Sessions: 1},
		"note-only":     {Notes: 1},
		"task-only":     {Tasks: 1},
		"template-only": {Templates: 1},
	} {
		got, ok := byName[name]
		if !ok {
			t.Errorf("expected %s in pool", name)
			continue
		}
		if got.Counts != want {
			t.Errorf("%s counts mismatch: got %+v want %+v", name, got.Counts, want)
		}
		if got.Total != 1 {
			t.Errorf("%s total mismatch: got %d", name, got.Total)
		}
	}
}

func TestHandleTags_DefaultScopeStaysSessionsOnly(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	w := httptest.NewRecorder()
	handler.HandleTags(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The legacy shape: {"tags": [{"name", "usage_count"}]} with session tags
	// only — no workspace/note/task/template tags, no counts object.
	var resp struct {
		Tags []session.Tag `json:"tags"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	names := map[string]int{}
	for _, tag := range resp.Tags {
		names[tag.Name] = tag.UsageCount
	}
	if len(names) != 2 || names["music"] != 1 || names["session-only"] != 1 {
		t.Fatalf("expected sessions-only tags, got %#v", names)
	}
	if strings.Contains(w.Body.String(), "counts") {
		t.Fatalf("default response must not contain unified counts: %s", w.Body.String())
	}
}
