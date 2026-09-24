package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
)

func approvedProfileFact(t *testing.T, f *knowledgeFixture) KnowledgeItem {
	t.Helper()
	ctx := context.Background()
	if err := f.profileStore.Upsert(ctx, &userprofile.UserProfile{
		ID: "local", Preferences: map[string]string{"response_style": "brief", "language": "English"},
	}); err != nil {
		t.Fatal(err)
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	item := KnowledgeItem{
		ID: "profile-item", Version: 1, State: KnowledgeApproved,
		SemanticKey: "explicit-style-v1", Category: "how_you_work", SourceKind: "explicit",
		CurrentRevisionID: "profile-revision", Revisions: []KnowledgeRevision{{
			ID: "profile-revision", Text: "brief", ApprovedAt: &at,
		}},
		Target: &KnowledgeTarget{
			Kind: "profile", Field: "preferences.response_style", CanonicalHash: hashKnowledgeLine("brief"),
			Version: profile.UpdatedAt.Format(time.RFC3339Nano),
		},
	}
	store := NewKnowledgeStore(f.resolver(), f.folder)
	if _, err := store.Update(ctx, "local", 0, func(doc *KnowledgeDocument) error {
		doc.Items = append(doc.Items, item)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestKnowledgeForgetApprovedProfilePreservesOtherFieldsAndRestartSuppression(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	item := approvedProfileFact(t, f)
	s := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	s.SetProfileCAS(f.profileStore)
	forgotten, err := s.ForgetApproved(ctx, "local", item.ID, item.Version, "req-profile-forget")
	if err != nil || forgotten.State != KnowledgeForgotten || len(forgotten.Revisions) != 0 {
		t.Fatalf("profile forget=%+v %v", forgotten, err)
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "" || profile.Preferences["language"] != "English" {
		t.Fatalf("profile field clear clobbered unrelated value: %+v %v", profile, err)
	}
	restarted := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	restarted.SetProfileCAS(f.profileStore)
	again, err := restarted.ForgetApproved(ctx, "local", item.ID, item.Version, "req-profile-forget")
	if err != nil || again.Version != forgotten.Version {
		t.Fatalf("profile forget replay=%+v %v", again, err)
	}
}

func TestKnowledgeForgetProfileRestartAfterSQLClearBeforeFinalization(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	item := approvedProfileFact(t, f)
	s := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	s.SetProfileCAS(f.profileStore)
	writes := 0
	s.store.beforeRename = func() error {
		writes++
		if writes == 2 {
			return errors.New("injected profile finalize failure")
		}
		return nil
	}
	if _, err := s.ForgetApproved(ctx, "local", item.ID, item.Version, "req-profile-forget"); err == nil {
		t.Fatal("partial profile clear reported success")
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || doc.Items[0].Prepared == nil || len(doc.Items[0].Revisions) != 0 || len(doc.Tombstones) == 0 {
		t.Fatalf("partial profile clear leaked plaintext: %+v %v", doc, err)
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "" {
		t.Fatalf("SQL clear did not persist: %+v %v", profile, err)
	}
	restart := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	restart.SetProfileCAS(f.profileStore)
	forgotten, err := restart.ForgetApproved(ctx, "local", item.ID, item.Version, "req-profile-forget")
	if err != nil || forgotten.State != KnowledgeForgotten {
		t.Fatalf("profile clear recovery=%+v %v", forgotten, err)
	}
}

func TestKnowledgeForgetProfileStaleValueDoesNotOverwriteOutsideEdit(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	item := approvedProfileFact(t, f)
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.profileStore.UpdateFieldCAS(ctx, "local", "preferences.response_style", profile.UpdatedAt, "brief", "detailed"); err != nil {
		t.Fatal(err)
	}
	s := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	s.SetProfileCAS(f.profileStore)
	if _, err := s.ForgetApproved(ctx, "local", item.ID, item.Version, "req-stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale value was cleared: %v", err)
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || len(doc.Items[0].Revisions) != 0 || len(doc.Tombstones) == 0 {
		t.Fatalf("stale profile fact remained active/unsuppressed: %+v %v", doc, err)
	}
	profile, err = f.profileStore.Get(ctx, "local")
	if err != nil || profile.Preferences["response_style"] != "detailed" {
		t.Fatalf("outside edit was lost: %+v %v", profile, err)
	}
}
