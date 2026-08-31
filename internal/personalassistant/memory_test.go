package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type memoryAppender struct {
	entries []workspace.MemoryEntry
}

func (m *memoryAppender) AppendUnique(_ string, entry workspace.MemoryEntry) (bool, error) {
	for _, existing := range m.entries {
		if existing.Text == entry.Text && existing.Provenance == entry.Provenance && existing.Type == entry.Type {
			return false, nil
		}
	}
	m.entries = append(m.entries, entry)
	return true, nil
}

func newMemoryFixture(t *testing.T) (*MemoryService, *SQLiteStore, *userprofile.SQLiteStore, *memoryAppender, *fakeHQReader) {
	t.Helper()
	store, db := newTestStore(t)
	if _, err := store.CreateState(context.Background(), activeTestState("local", "assistant-a")); err != nil {
		t.Fatal(err)
	}
	ws := &session.Workspace{
		ID: "hq-local", FolderSlug: "personal-hq", OwnerUserID: "local",
		AgentInstances: []session.AgentInstance{{ID: "instance-local", Name: "Ada", EntryPoint: true}},
	}
	hq := &fakeHQReader{status: &personalhq.Status{UserID: "local", WorkspaceID: ws.ID, Workspace: ws, Valid: true}}
	profiles := userprofile.NewSQLiteStore(db)
	memory := &memoryAppender{}
	service := NewMemoryService(store, hq, profiles, memory)
	service.now = func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) }
	return service, store, profiles, memory, hq
}

func TestMemoryService_ExplicitPreferenceAndHQFactUseCanonicalStores(t *testing.T) {
	service, _, profiles, memory, _ := newMemoryFixture(t)
	profileResult, err := service.Remember(context.Background(), "local", RememberRequest{
		IfVersion: 1, Destination: MemoryDestinationProfile,
		Text: "I prefer concise responses", Preference: "response_style", Value: "concise",
	})
	if err != nil || profileResult.Href != "/profile" {
		t.Fatalf("profile remember=%+v err=%v", profileResult, err)
	}
	profile, _ := profiles.Get(context.Background(), "local")
	if profile.Preferences["response_style"] != "concise" {
		t.Fatalf("preference was not saved canonically: %+v", profile)
	}

	request := RememberRequest{IfVersion: 1, Destination: MemoryDestinationPersonalHQ, Text: "Maya owns the launch review"}
	first, err := service.Remember(context.Background(), "local", request)
	if err != nil || !first.Created || first.Href != "/workspaces/personal-hq#memory" {
		t.Fatalf("HQ remember=%+v err=%v", first, err)
	}
	second, err := service.Remember(context.Background(), "local", request)
	if err != nil || second.Created || len(memory.entries) != 1 {
		t.Fatalf("confirmation replay duplicated memory: second=%+v entries=%+v err=%v", second, memory.entries, err)
	}
}

func TestMemoryService_RefusesSecretStaleVersionAndReplacedHQ(t *testing.T) {
	service, store, _, memory, hq := newMemoryFixture(t)
	for name, request := range map[string]RememberRequest{
		"secret": {IfVersion: 1, Destination: MemoryDestinationPersonalHQ, Text: "token sk-abcdefghijklmnopqrstuv"},
		"stale":  {IfVersion: 99, Destination: MemoryDestinationPersonalHQ, Text: "Safe fact"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Remember(context.Background(), "local", request); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
	hq.status.WorkspaceID = "replacement"
	if _, err := service.Remember(context.Background(), "local", RememberRequest{IfVersion: 1, Destination: MemoryDestinationPersonalHQ, Text: "Safe fact"}); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("replaced HQ error=%v", err)
	}
	if len(memory.entries) != 0 {
		t.Fatalf("refused memory was written: %+v", memory.entries)
	}
	state, _ := store.GetState(context.Background(), "local")
	if state.StateVersion != 1 {
		t.Fatalf("memory refusal changed relationship: %+v", state)
	}
}
