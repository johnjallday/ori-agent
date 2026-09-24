package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

type fakeSavedAppSnapshot struct {
	profile *types.InferredProfile
	reads   int
}

func (f *fakeSavedAppSnapshot) GetUserProfile() *types.InferredProfile {
	f.reads++
	return f.profile
}

func TestSavedAppProducerOnlyProposesFromPersistedAllowlistedObservation(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	reader := &fakeSavedAppSnapshot{}
	service := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, NewSavedAppAuthority(reader))
	producer := NewSavedAppProducer(service, reader, f.profileStore)
	if items, err := producer.Check(ctx, "local"); err != nil || len(items) != 0 {
		t.Fatalf("missing saved profile produced proposals: %+v %v", items, err)
	}
	reader.profile = &types.InferredProfile{DetectedApps: []string{"Visual Studio Code"}}
	if items, err := producer.Check(ctx, "local"); err != nil || len(items) != 0 {
		t.Fatalf("zero observation time produced proposals: %+v %v", items, err)
	}
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	reader.profile.InferredAt = at
	reader.profile.DetectedApps = []string{"Unknown", "Visual Studio Code", "Visual Studio Code", "Sensitive Personal App"}
	items, err := producer.Check(ctx, "local")
	if err != nil || len(items) != 1 || items[0].State != KnowledgeCandidate || items[0].Revisions[0].Evidence[0].ObservedAt != at {
		t.Fatalf("unexpected safe candidates: %+v %v", items, err)
	}
	if reader.reads != 3 {
		t.Fatalf("unexpected saved-snapshot reads: %d", reader.reads)
	}
	if _, err := service.ApproveCandidate(ctx, "local", items[0].ID, items[0].Version, "approve-saved-app"); err != nil {
		t.Fatalf("current saved evidence blocked review: %v", err)
	}
	if replay, err := producer.Check(ctx, "local"); err != nil || len(replay) != 0 {
		t.Fatalf("reconciliation duplicated an approved fact: %+v %v", replay, err)
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || len(doc.Items) != 1 {
		t.Fatalf("reconciliation changed approved history: %+v %v", doc, err)
	}
}

func TestSavedAppOldObservationKeepsItsDateAndEditedRejectionSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	observed := time.Date(2020, time.March, 2, 9, 0, 0, 0, time.UTC)
	reader := &fakeSavedAppSnapshot{profile: &types.InferredProfile{
		InferredAt: observed, DetectedApps: []string{"Obsidian", "Obsidian", "Unknown Private App"},
	}}
	service := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, NewSavedAppAuthority(reader))
	producer := NewSavedAppProducer(service, reader, f.profileStore)
	items, err := producer.Check(ctx, "local")
	if err != nil || len(items) != 1 || !items[0].Revisions[0].Evidence[0].ObservedAt.Equal(observed) {
		t.Fatalf("old saved observation was relabelled recent or duplicated: %+v %v", items, err)
	}
	edited, err := service.EditCandidate(ctx, "local", items[0].ID, items[0].Version, "Obsidian may help me keep work notes.")
	if err != nil || edited.State != KnowledgeCandidate {
		t.Fatalf("candidate edit became an approval: %+v %v", edited, err)
	}
	if _, err := service.SuppressCandidate(ctx, "local", edited.ID, edited.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	restartStore := NewKnowledgeStore(f.resolver(), f.folder)
	restarted := NewSavedAppProducer(NewKnowledgeLifecycleService(restartStore, f.memory, NewSavedAppAuthority(reader)), reader, f.profileStore)
	if again, err := restarted.Check(ctx, "local"); err != nil || len(again) != 0 {
		t.Fatalf("rejected edited tool hypothesis returned after restart: %+v %v", again, err)
	}
	doc, err := restartStore.Read(ctx, "local")
	if err != nil || len(doc.Items) != 1 || doc.Items[0].State != KnowledgeRejected || len(doc.Items[0].Revisions) != 0 || len(doc.Admissions) != 1 {
		t.Fatalf("suppressed old observation was re-admitted or retained plaintext: %+v %v", doc, err)
	}
}

func TestSavedAppAuthorityRefusesChangedSnapshotAndForeignUser(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	reader := &fakeSavedAppSnapshot{profile: &types.InferredProfile{InferredAt: at, DetectedApps: []string{"Obsidian"}}}
	service := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, NewSavedAppAuthority(reader))
	producer := NewSavedAppProducer(service, reader, f.profileStore)
	items, err := producer.Check(ctx, "local")
	if err != nil || len(items) != 1 {
		t.Fatalf("saved evidence=%+v %v", items, err)
	}
	reader.profile.InferredAt = at.Add(time.Hour)
	if _, err := service.ApproveCandidate(ctx, "local", items[0].ID, items[0].Version, "approve-stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed observation was approved: %v", err)
	}
	reader.profile.InferredAt = at
	if _, err := producer.Check(ctx, "foreign-user"); err == nil {
		t.Fatal("foreign user read local app profile")
	}
}

func TestSavedAppProducerSkipsExistingCanonicalProfileSpecialization(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	reader := &fakeSavedAppSnapshot{profile: &types.InferredProfile{
		InferredAt: time.Now().UTC(), DetectedApps: []string{"Obsidian"},
	}}
	if err := f.profileStore.Upsert(ctx, &userprofile.UserProfile{ID: "local", Specializations: []string{"Obsidian"}}); err != nil {
		t.Fatal(err)
	}
	service := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, NewSavedAppAuthority(reader))
	items, err := NewSavedAppProducer(service, reader, f.profileStore).Check(ctx, "local")
	if err != nil || len(items) != 0 {
		t.Fatalf("already canonical specialization was re-proposed: %+v %v", items, err)
	}
}
