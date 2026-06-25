package templateonboarding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistLoadResume(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	session := mustStoreSession(t, StatusCollecting)
	if _, err := session.MergeValues(map[string]any{"bpm": 128, "song_name": "Night Drive"}); err != nil {
		t.Fatalf("MergeValues: %v", err)
	}
	if _, err := session.MarkReadyToComplete(); err != nil {
		t.Fatalf("MarkReadyToComplete: %v", err)
	}

	if err := store.Save(ctx, session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(ctx, session.WorkspaceID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.WorkspaceID != session.WorkspaceID {
		t.Fatalf("workspace_id=%q, want %q", loaded.WorkspaceID, session.WorkspaceID)
	}
	if loaded.Status != StatusReadyToComplete {
		t.Fatalf("status=%q, want ready_to_complete", loaded.Status)
	}
	if loaded.Values["bpm"].(float64) != 128 || loaded.Values["song_name"] != "Night Drive" {
		t.Fatalf("values not reloaded: %#v", loaded.Values)
	}
	if loaded.Spec.Fields[0].ID != "bpm" {
		t.Fatalf("spec snapshot missing after reload: %+v", loaded.Spec)
	}
}

func TestStoreWritesExpectedSidecar(t *testing.T) {
	ctx := context.Background()
	store, folder := newTestStore(t)
	session := mustStoreSession(t, StatusPendingEntryAgent)
	if err := store.Save(ctx, session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(folder, SessionFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected sidecar at %s: %v", path, err)
	}
	gotPath, err := store.Path(session.WorkspaceID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if gotPath != path {
		t.Fatalf("Path=%q, want %q", gotPath, path)
	}
}

func TestStoreMissingSession(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Load(context.Background(), "ws-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Load missing err=%v, want ErrSessionNotFound", err)
	}
}

func TestStoreSnapshotImmutabilityAcrossTemplateEdits(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	spec := testSpec()
	session, err := NewSession("ws-1", spec, StatusCollecting)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	spec.Fields[0].Label = "Template edit after session creation"
	spec.Fields = append(spec.Fields, Field{ID: "genre", Label: "Genre", Type: FieldString})
	spec.Completion.Inputs["genre"] = "${fields.genre}"

	if err := store.Save(ctx, session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Spec.Fields[0].Label; got != "BPM" {
		t.Fatalf("loaded snapshot label=%q, want original BPM", got)
	}
	if len(loaded.Spec.Fields) != 2 {
		t.Fatalf("loaded snapshot field count=%d, want 2", len(loaded.Spec.Fields))
	}
	if _, ok := loaded.Spec.Completion.Inputs["genre"]; ok {
		t.Fatalf("loaded snapshot picked up later template input: %#v", loaded.Spec.Completion.Inputs)
	}
}

func TestStoreBestEffortMirror(t *testing.T) {
	ctx := context.Background()
	mirror := &recordingMirror{err: errors.New("mirror unavailable")}
	resolver := testResolver{"ws-1": t.TempDir()}
	store := NewStore(resolver, WithMirror(mirror))
	session := mustStoreSession(t, StatusCollecting)

	if err := store.Save(ctx, session); err != nil {
		t.Fatalf("Save should ignore mirror error after sidecar write: %v", err)
	}
	if mirror.saved == nil {
		t.Fatal("expected mirror to receive a cloned session")
	}
	mirror.saved.WorkspaceID = "mutated"
	if session.WorkspaceID != "ws-1" {
		t.Fatalf("mirror received original session pointer")
	}
}

func TestStoreInvalidLoadedStatus(t *testing.T) {
	store, folder := newTestStore(t)
	path := filepath.Join(folder, SessionFileName)
	if err := os.WriteFile(path, []byte(`{"workspace_id":"ws-1","status":"bogus","spec":{"version":"1","completion":{"type":"none"}}}`), 0o644); err != nil {
		t.Fatalf("write invalid sidecar: %v", err)
	}
	if _, err := store.Load(context.Background(), "ws-1"); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("Load invalid status err=%v, want ErrInvalidStatus", err)
	}
}

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	folder := t.TempDir()
	return NewStore(testResolver{"ws-1": folder}), folder
}

func mustStoreSession(t *testing.T, status Status) *Session {
	t.Helper()
	session, err := NewSession("ws-1", testSpec(), status)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

type testResolver map[string]string

func (r testResolver) GetFolderPath(workspaceID string) (string, error) {
	folder, ok := r[workspaceID]
	if !ok {
		return "", errors.New("not found")
	}
	return folder, nil
}

type recordingMirror struct {
	saved *Session
	err   error
}

func (m *recordingMirror) SaveTemplateOnboardingSession(_ context.Context, session *Session) error {
	m.saved = session
	return m.err
}
