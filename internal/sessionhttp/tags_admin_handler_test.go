package sessionhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

func postTagAdmin(t *testing.T, handlerFunc http.HandlerFunc, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handlerFunc(w, req)
	return w
}

func unifiedPoolByName(t *testing.T, handler *Handler) map[string]UnifiedTag {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tags?scope=all", nil)
	w := httptest.NewRecorder()
	handler.HandleTags(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pool fetch failed: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Tags []UnifiedTag `json:"tags"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal pool: %v", err)
	}
	byName := map[string]UnifiedTag{}
	for _, tag := range resp.Tags {
		byName[tag.Name] = tag
	}
	return byName
}

func tagsTestNoteFile(t *testing.T, handler *Handler) string {
	t.Helper()
	folderPath, err := handler.workspaceStore.GetFolderPath("ws-tags")
	if err != nil {
		t.Fatalf("GetFolderPath: %v", err)
	}
	return filepath.Join(folderPath, agentworkspace.NotesDir, agentworkspace.NoteFilename("N", "note-1"))
}

func TestHandleTagRename_PropagatesEverywhere(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()
	ctx := t.Context()

	// Materialize the note's markdown file so the rename has a file to fix.
	note, err := handler.store.GetNote(ctx, "note-1")
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	handler.syncNoteToFile(note)

	w := postTagAdmin(t, handler.HandleTagRename, "/api/tags/rename", `{"from":" Music ","to":"Audio"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rename failed: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Renamed tagMutationCounts `json:"renamed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := tagMutationCounts{Workspaces: 1, Sessions: 1, Notes: 1, Tasks: 1}
	if resp.Renamed != want {
		t.Fatalf("renamed counts mismatch: got %+v want %+v", resp.Renamed, want)
	}

	// Pool: music is gone from every entity source (the template still
	// declares it — manifests are read-only), audio carries the counts.
	pool := unifiedPoolByName(t, handler)
	if music, ok := pool["music"]; ok {
		entityTotal := music.Counts.Workspaces + music.Counts.Sessions + music.Counts.Notes + music.Counts.Tasks
		if entityTotal != 0 {
			t.Fatalf("music should be gone from entities, got %+v", music.Counts)
		}
		if music.Counts.Templates == 0 {
			t.Fatalf("music should still be declared by the template, got %+v", music.Counts)
		}
	}
	audio, ok := pool["audio"]
	if !ok {
		t.Fatalf("audio missing from pool: %v", pool)
	}
	if audio.Counts.Workspaces != 1 || audio.Counts.Sessions != 1 || audio.Counts.Notes != 1 || audio.Counts.Tasks != 1 {
		t.Fatalf("audio counts mismatch: %+v", audio.Counts)
	}

	// Folder store: workspace.json tags and the task both renamed.
	folderWS, err := handler.workspaceStore.Get("ws-tags")
	if err != nil {
		t.Fatalf("folder Get: %v", err)
	}
	if strings.Join(folderWS.Tags, ",") != "audio,disk-only" {
		t.Fatalf("workspace.json tags mismatch: %v", folderWS.Tags)
	}
	if strings.Join(folderWS.Tasks[0].Tags, ",") != "audio,task-only" {
		t.Fatalf("task tags mismatch: %v", folderWS.Tasks[0].Tags)
	}

	// Note markdown frontmatter rewritten on disk.
	data, err := os.ReadFile(tagsTestNoteFile(t, handler))
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}
	if !strings.Contains(string(data), `- "audio"`) || strings.Contains(string(data), `- "music"`) {
		t.Fatalf("note frontmatter not renamed:\n%s", string(data))
	}

	// Template manifest untouched.
	manifest, err := os.ReadFile(filepath.Join(handler.templatesRootResolver(), "demo-template", "template.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "Music") {
		t.Fatalf("template manifest must not be modified: %s", string(manifest))
	}
}

func TestHandleTagRename_MergesWithExistingTag(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()
	ctx := t.Context()

	// This session already carries both the old and the new name.
	sess := &session.Session{Title: "Merge", AgentName: "a", Tags: []string{"music", "audio"}}
	if err := handler.store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	w := postTagAdmin(t, handler.HandleTagRename, "/api/tags/rename", `{"from":"music","to":"audio"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rename failed: %d %s", w.Code, w.Body.String())
	}

	got, err := handler.store.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "audio" {
		t.Fatalf("expected merged tags [audio], got %v", got.Tags)
	}
}

func TestHandleTagDelete_RemovesEverywhere(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()
	ctx := t.Context()

	note, err := handler.store.GetNote(ctx, "note-1")
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	handler.syncNoteToFile(note)

	w := postTagAdmin(t, handler.HandleTagDelete, "/api/tags/delete", `{"tag":"music"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Removed tagMutationCounts `json:"removed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := tagMutationCounts{Workspaces: 1, Sessions: 1, Notes: 1, Tasks: 1}
	if resp.Removed != want {
		t.Fatalf("removed counts mismatch: got %+v want %+v", resp.Removed, want)
	}

	pool := unifiedPoolByName(t, handler)
	if music, ok := pool["music"]; ok {
		entityTotal := music.Counts.Workspaces + music.Counts.Sessions + music.Counts.Notes + music.Counts.Tasks
		if entityTotal != 0 {
			t.Fatalf("music should be removed from entities, got %+v", music.Counts)
		}
	}

	folderWS, err := handler.workspaceStore.Get("ws-tags")
	if err != nil {
		t.Fatalf("folder Get: %v", err)
	}
	if strings.Join(folderWS.Tags, ",") != "disk-only" {
		t.Fatalf("workspace.json tags mismatch after delete: %v", folderWS.Tags)
	}
	if strings.Join(folderWS.Tasks[0].Tags, ",") != "task-only" {
		t.Fatalf("task tags mismatch after delete: %v", folderWS.Tasks[0].Tags)
	}

	data, err := os.ReadFile(tagsTestNoteFile(t, handler))
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}
	if strings.Contains(string(data), `- "music"`) {
		t.Fatalf("note frontmatter should drop music:\n%s", string(data))
	}
}

func TestHandleTagUsage_ReportsCountsAndTemplates(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/tags/usage?tag=Music", nil)
	w := httptest.NewRecorder()
	handler.HandleTagUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("usage failed: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Tag       string           `json:"tag"`
		Counts    UnifiedTagCounts `json:"counts"`
		Total     int              `json:"total"`
		Templates []string         `json:"templates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Tag != "music" || resp.Total != 5 {
		t.Fatalf("unexpected usage payload: %+v", resp)
	}
	if len(resp.Templates) != 1 || resp.Templates[0] != "Demo" {
		t.Fatalf("expected template name Demo, got %v", resp.Templates)
	}
}

func TestHandleTagRename_Validation(t *testing.T) {
	handler, cleanup := setupUnifiedTagsHandler(t)
	defer cleanup()

	for name, body := range map[string]string{
		"missing to":   `{"from":"music"}`,
		"missing from": `{"to":"music"}`,
		"same tag":     `{"from":"Music","to":" music "}`,
	} {
		w := postTagAdmin(t, handler.HandleTagRename, "/api/tags/rename", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", name, w.Code, w.Body.String())
		}
	}

	w := postTagAdmin(t, handler.HandleTagDelete, "/api/tags/delete", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("delete without tag: expected 400, got %d", w.Code)
	}

	// Unknown tag is a no-op success with zero counts.
	w = postTagAdmin(t, handler.HandleTagDelete, "/api/tags/delete", `{"tag":"does-not-exist"}`)
	if w.Code != http.StatusOK {
		t.Errorf("unknown tag delete: expected 200, got %d", w.Code)
	}
	var resp struct {
		Removed tagMutationCounts `json:"removed"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Removed != (tagMutationCounts{}) {
		t.Errorf("unknown tag delete should touch nothing, got %+v", resp.Removed)
	}
}
