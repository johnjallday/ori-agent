package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// All knowledge fixtures use an in-memory relationship/profile DB and a
// temporary folder. They must never resolve the user's actual HOME/workspaces.
type knowledgeFolder struct{ path string }

func (f knowledgeFolder) GetFolderPath(id string) (string, error) {
	if id != "hq-local" {
		return "", ErrRepairNeeded
	}
	return f.path, nil
}

type knowledgeProfileReader struct{ provenance ProfileProvenance }

func (p *knowledgeProfileReader) PersonalAssistantProfileProvenance(name string) (ProfileProvenance, bool) {
	return p.provenance, name == p.provenance.Name
}

type knowledgeFixture struct {
	relationships *SQLiteStore
	hq            *fakeHQReader
	profiles      *knowledgeProfileReader
	profileStore  *userprofile.SQLiteStore
	folder        knowledgeFolder
	memory        *workspace.MemoryStore
}

func newKnowledgeFixture(t *testing.T) *knowledgeFixture {
	t.Helper()
	stateStore, db := newTestStore(t)
	state := activeTestState("local", "assistant-a")
	if _, err := stateStore.CreateState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	folder := knowledgeFolder{path: t.TempDir()}
	ws := &session.Workspace{
		ID: "hq-local", FolderSlug: "personal-hq", OwnerUserID: "local",
		Designation:    session.WorkspaceDesignationPersonalHQ,
		AgentInstances: []session.AgentInstance{{ID: "instance-local", Name: "Ada", EntryPoint: true}},
	}
	return &knowledgeFixture{
		relationships: stateStore,
		hq:            &fakeHQReader{status: &personalhq.Status{UserID: "local", WorkspaceID: ws.ID, Workspace: ws, Valid: true}},
		profiles:      &knowledgeProfileReader{provenance: ProfileProvenance{Name: "Ada", AssistantID: "assistant-a"}},
		profileStore:  userprofile.NewSQLiteStore(db),
		folder:        folder,
		memory:        workspace.NewMemoryStore(folder),
	}
}

func (f *knowledgeFixture) resolver() *KnowledgeResolver {
	return NewKnowledgeResolver(f.relationships, f.hq, f.profiles)
}

func TestKnowledgeResolverRequiresCurrentOwnedHQAndEntry(t *testing.T) {
	ctx := context.Background()
	for _, status := range []RelationshipStatus{StatusActive, StatusPaused} {
		t.Run(string(status), func(t *testing.T) {
			f := newKnowledgeFixture(t)
			state, err := f.relationships.GetState(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			state.Status = status
			if _, err = f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
				t.Fatal(err)
			}
			binding, err := f.resolver().Resolve(ctx, "local")
			if err != nil || binding.UserID != "local" || binding.HQWorkspaceID != "hq-local" || binding.EntryAgentInstanceID != "instance-local" || binding.Paused != (status == StatusPaused) {
				t.Fatalf("binding=%+v err=%v", binding, err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		change func(*knowledgeFixture, *State)
	}{
		{"not hired", func(_ *knowledgeFixture, s *State) { s.Status = StatusNotHired }},
		{"needs HQ", func(_ *knowledgeFixture, s *State) { s.Status = StatusAwaitingHQ }},
		{"repair", func(_ *knowledgeFixture, s *State) { s.Status = StatusRepairNeeded }},
		{"foreign user", func(f *knowledgeFixture, _ *State) { f.hq.status.UserID = "someone-else" }},
		{"foreign workspace", func(f *knowledgeFixture, _ *State) { f.hq.status.Workspace.OwnerUserID = "someone-else" }},
		{"replaced HQ", func(f *knowledgeFixture, _ *State) { f.hq.status.WorkspaceID = "other" }},
		{"mismatched entry", func(f *knowledgeFixture, _ *State) { f.hq.status.Workspace.AgentInstances[0].ID = "different" }},
		{"non-entry instance", func(f *knowledgeFixture, _ *State) { f.hq.status.Workspace.AgentInstances[0].EntryPoint = false }},
		{"name-only impersonation", func(f *knowledgeFixture, _ *State) {
			f.profiles.provenance = ProfileProvenance{Name: "Ada", AssistantID: "other-assistant"}
		}},
		{"renaming", func(_ *knowledgeFixture, s *State) { s.RenameStep = RenameProfilePending }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newKnowledgeFixture(t)
			state, err := f.relationships.GetState(ctx, "local")
			if err != nil {
				t.Fatal(err)
			}
			test.change(f, state)
			// This fixture is deliberately capable of presenting invalid or
			// intermediate states; it never writes those states to the DB.
			failing := &readTrackingStore{state: state}
			resolver := NewKnowledgeResolver(failing, f.hq, f.profiles)
			if _, err := resolver.Resolve(ctx, "local"); err == nil {
				t.Fatal("unverified binding granted HQ knowledge")
			}
		})
	}
	f := newKnowledgeFixture(t)
	if _, err := f.resolver().Resolve(ctx, "foreign-user"); err == nil {
		t.Fatal("foreign user resolved local relationship")
	}
}

func TestKnowledgeStoreRestartAndFailedWriteKeepCanonicalOwners(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	const pending = "PENDING-SENTINEL: never inject this proposal"
	const legacy = "Legacy HQ fact, kept byte-for-byte"
	memoryBytes := []byte("# Workspace Memory\r\n\r\n- [fact, 2026-09-01, user] " + legacy) // no terminal newline
	path := filepath.Join(f.folder.path, workspace.MemoryFileName)
	if err := os.WriteFile(path, memoryBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.profileStore.SetFields(ctx, "local", map[string]any{"preferences.response_style": "brief"}); err != nil {
		t.Fatal(err)
	}
	beforeProfile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}

	store := NewKnowledgeStore(f.resolver(), f.folder)
	initial, err := store.Read(ctx, "local")
	if err != nil || initial.SchemaVersion != KnowledgeSchemaVersion || initial.Version != 0 {
		t.Fatalf("initial sidecar=%+v err=%v", initial, err)
	}
	created, err := store.Update(ctx, "local", 0, func(doc *KnowledgeDocument) error {
		doc.Items = append(doc.Items, KnowledgeItem{
			ID: "item-1", Version: 1, State: KnowledgeCandidate,
			SemanticKey: "saved-app/v1/example", Category: "how_you_work",
			Revisions: []KnowledgeRevision{{ID: "rev-1", Text: pending}},
		})
		return nil
	})
	if err != nil || created.Version != 1 {
		t.Fatalf("create candidate=%+v err=%v", created, err)
	}
	// A newly constructed store must read the persisted candidate rather than
	// silently treating absence/corruption as an empty document.
	restarted := NewKnowledgeStore(f.resolver(), f.folder)
	reloaded, err := restarted.Read(ctx, "local")
	if err != nil || reloaded.Version != 1 || len(reloaded.Items) != 1 || reloaded.Items[0].Revisions[0].Text != pending {
		t.Fatalf("restart sidecar=%+v err=%v", reloaded, err)
	}
	if _, err := store.Update(ctx, "local", 0, func(*KnowledgeDocument) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error=%v", err)
	}

	// Inject the failure before the atomic replace; no partial revision may
	// appear on restart and neither canonical store may have been modified.
	store.beforeRename = func() error { return errors.New("injected sidecar write failure") }
	if _, err := store.Update(ctx, "local", 1, func(doc *KnowledgeDocument) error {
		doc.Items[0].Version++
		doc.Items[0].Revisions = append(doc.Items[0].Revisions, KnowledgeRevision{ID: "rev-2", Text: "edited candidate"})
		return nil
	}); err == nil || !strings.Contains(err.Error(), "injected sidecar write failure") {
		t.Fatalf("write failure was not propagated: %v", err)
	}
	after, err := restarted.Read(ctx, "local")
	if err != nil || after.Version != 1 || after.Items[0].State != KnowledgeCandidate || len(after.Items[0].Revisions) != 1 {
		t.Fatalf("failed write leaked revision: %+v, %v", after, err)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- test-only path under t.TempDir
	if err != nil || string(raw) != string(memoryBytes) {
		t.Fatalf("canonical memory changed: %q, %v", raw, err)
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.DisplayName != beforeProfile.DisplayName || profile.Preferences["response_style"] != beforeProfile.Preferences["response_style"] {
		t.Fatalf("canonical profile changed: %+v, %v", profile, err)
	}
	if strings.Contains(string(raw), pending) {
		t.Fatal("candidate reached canonical memory")
	}
}

// This deliberately red regression demonstrates the current prompt bypass:
// RenderMemoryPromptSection sees structured managed lines but has no lifecycle
// reader. Once the shared eligibility boundary is connected, the same sentinel
// must be absent from every structured/raw/native/tool path, not only here.
func TestKnowledgeManagedMemoryPromptFailsClosedWithoutLifecycle(t *testing.T) {
	const suspended = "NEEDS-REVIEW-SENTINEL: no longer supported"
	const approved = "APPROVED-SENTINEL: confirmed by user"
	for _, text := range []string{
		"PENDING-SENTINEL", "REJECTED-SENTINEL", "FORGOTTEN-SENTINEL", suspended,
	} {
		doc := workspace.ParseMemoryDocument("- [fact, 2026-09-01, ori-hq:item-1:rev-1] " + text + "\n")
		if got := workspace.RenderMemoryPromptSection(doc, false); strings.Contains(got, text) {
			t.Errorf("managed %q was injected without any lifecycle proof", text)
		}
	}
	_ = approved // Later tests must prove only a current, canonical, approved revision is included.
}
