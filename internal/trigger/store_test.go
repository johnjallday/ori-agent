package trigger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSource maps workspace IDs to temp folders, standing in for the
// workspace FileStore.
type fakeSource struct {
	folders map[string]string
}

func (f *fakeSource) List() ([]string, error) {
	ids := make([]string, 0, len(f.folders))
	for id := range f.folders {
		ids = append(ids, id)
	}
	return ids, nil
}

func (f *fakeSource) GetFolderPath(workspaceID string) (string, error) {
	p, ok := f.folders[workspaceID]
	if !ok {
		return "", os.ErrNotExist
	}
	return p, nil
}

func newTestStore(t *testing.T, wsIDs ...string) (*Store, *fakeSource) {
	t.Helper()
	src := &fakeSource{folders: make(map[string]string)}
	for _, id := range wsIDs {
		src.folders[id] = t.TempDir()
	}
	s := NewStore(src)
	if err := s.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return s, src
}

func webhookTrigger(wsID string) Trigger {
	return Trigger{
		WorkspaceID: wsID,
		Name:        "pr-opened",
		Type:        TypeWebhook,
		Enabled:     true,
		Action:      Action{Kind: ActionMissionRun},
	}
}

func TestStoreCreatePersistsAndReloads(t *testing.T) {
	s, src := newTestStore(t, "ws1")

	created, err := s.Create(webhookTrigger("ws1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.Webhook.Token == "" {
		t.Fatalf("Create did not fill server-side fields: %+v", created)
	}

	if _, err := os.Stat(filepath.Join(src.folders["ws1"], TriggersFileName)); err != nil {
		t.Fatalf("triggers.json not written: %v", err)
	}

	// A fresh store over the same folders must see the trigger and index its token.
	s2 := NewStore(src)
	if err := s2.LoadAll(); err != nil {
		t.Fatalf("LoadAll on fresh store: %v", err)
	}
	got, err := s2.Get("ws1", created.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.Name != "pr-opened" {
		t.Errorf("reloaded trigger name = %q, want pr-opened", got.Name)
	}
	if _, ok := s2.GetByToken(created.Webhook.Token); !ok {
		t.Error("token not indexed after reload")
	}
}

func TestStoreUpdateRollsBackOnValidationError(t *testing.T) {
	s, _ := newTestStore(t, "ws1")
	created, err := s.Create(webhookTrigger("ws1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.Update("ws1", created.ID, func(tr *Trigger) error {
		tr.Name = "" // structurally invalid
		return nil
	})
	if err == nil {
		t.Fatal("Update with invalid mutation should fail")
	}
	got, _ := s.Get("ws1", created.ID)
	if got.Name != "pr-opened" {
		t.Errorf("trigger mutated despite failed update: name=%q", got.Name)
	}
}

func TestStoreDeleteRemovesTokenIndex(t *testing.T) {
	s, _ := newTestStore(t, "ws1")
	created, _ := s.Create(webhookTrigger("ws1"))

	if err := s.Delete("ws1", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("ws1", created.ID); err != ErrNotFound {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if _, ok := s.GetByToken(created.Webhook.Token); ok {
		t.Error("token still resolvable after delete")
	}
}

func TestStoreTokenRegenerationReindexes(t *testing.T) {
	s, _ := newTestStore(t, "ws1")
	created, _ := s.Create(webhookTrigger("ws1"))
	oldToken := created.Webhook.Token

	newToken, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := s.Update("ws1", created.ID, func(tr *Trigger) error {
		tr.Webhook.Token = newToken
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, ok := s.GetByToken(oldToken); ok {
		t.Error("old token still resolves after regeneration")
	}
	if _, ok := s.GetByToken(newToken); !ok {
		t.Error("new token does not resolve")
	}
}

func TestRecordFireCapsHistory(t *testing.T) {
	s, _ := newTestStore(t, "ws1")
	created, _ := s.Create(webhookTrigger("ws1"))

	for i := 0; i < maxFireHistory+5; i++ {
		if err := s.RecordFire("ws1", created.ID, FireRecord{
			FireID: "fire", FiredAt: time.Now(), EventCount: 1, Summary: "x",
		}); err != nil {
			t.Fatalf("RecordFire #%d: %v", i, err)
		}
	}
	got, _ := s.Get("ws1", created.ID)
	if len(got.FireHistory) != maxFireHistory {
		t.Errorf("history length = %d, want %d", len(got.FireHistory), maxFireHistory)
	}
	if got.FireCount != maxFireHistory+5 {
		t.Errorf("fire count = %d, want %d", got.FireCount, maxFireHistory+5)
	}
}

func TestPendingFireSurvivesReload(t *testing.T) {
	s, src := newTestStore(t, "ws1")
	created, _ := s.Create(webhookTrigger("ws1"))

	pf := &PendingFire{
		FireID:    "fire-restart",
		Events:    []Event{{Kind: "webhook", Body: "hello", Timestamp: time.Now()}},
		CreatedAt: time.Now(),
	}
	if err := s.SetPendingFire("ws1", created.ID, pf); err != nil {
		t.Fatalf("SetPendingFire: %v", err)
	}

	s2 := NewStore(src)
	if err := s2.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got, _ := s2.Get("ws1", created.ID)
	if got.PendingFire == nil || got.PendingFire.FireID != "fire-restart" {
		t.Fatalf("pending fire not restored: %+v", got.PendingFire)
	}
	if len(got.PendingFire.Events) != 1 || got.PendingFire.Events[0].Body != "hello" {
		t.Errorf("pending fire events not restored: %+v", got.PendingFire.Events)
	}
}

func TestStoreSkipsCorruptFile(t *testing.T) {
	s, src := newTestStore(t, "ws1", "ws2")
	if _, err := s.Create(webhookTrigger("ws1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Corrupt ws2's file; ws1 must still load.
	if err := os.WriteFile(filepath.Join(src.folders["ws2"], TriggersFileName), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(src)
	if err := s2.LoadAll(); err != nil {
		t.Fatalf("LoadAll should tolerate one corrupt file: %v", err)
	}
	if got := s2.List("ws1"); len(got) != 1 {
		t.Errorf("ws1 triggers lost when ws2 file corrupt: %d", len(got))
	}
}
