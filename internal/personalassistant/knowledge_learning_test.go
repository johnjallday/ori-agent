package personalassistant

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func testProposal(subject, text string) KnowledgeProposal {
	return KnowledgeProposal{
		SourceKind: "saved_app", ScopeID: "saved-onboarding", SubjectID: subject,
		Predicate: "uses tool", Value: "example", Category: "how_you_work", Text: text,
		Evidence: []KnowledgeEvidence{{SourceKind: "saved_app", SourceID: "snapshot-1", Summary: "Saved onboarding observation"}},
	}
}

func TestKnowledgeProposalMeaningSurvivesRewordingAndRestart(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	original, created, err := service.Propose(ctx, "local", testProposal("example-app", "Maybe you use Example for planning."))
	if err != nil || !created || original.State != KnowledgeCandidate || original.ID == "" || original.SemanticKey == "" {
		t.Fatalf("first proposal=%+v created=%v err=%v", original, created, err)
	}
	service = NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	service.now = func() time.Time { return now.Add(time.Hour) }
	replayed, created, err := service.Propose(ctx, "local", testProposal("  EXAMPLE-app ", "A different wording of the same hypothesis."))
	if err != nil || created || replayed.ID != original.ID || replayed.SemanticKey != original.SemanticKey {
		t.Fatalf("reworded replay=%+v created=%v err=%v", replayed, created, err)
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || len(doc.Items) != 1 || len(doc.Admissions) != 1 {
		t.Fatalf("duplicate consumed quota or created a revision: %+v, %v", doc, err)
	}
	if _, err := service.SuppressCandidate(ctx, "local", original.ID, original.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	service = NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	if _, created, err := service.Propose(ctx, "local", testProposal("example-app", "Same observation after restart.")); !errors.Is(err, ErrKnowledgeSuppressed) || created {
		t.Fatalf("rejected meaning was re-proposed: created=%v err=%v", created, err)
	}
	// A user-confirmed Remember is deliberately not routed through automatic
	// source proposal admission. A tombstone must not veto explicit re-entry.
	explicit := NewMemoryService(f.relationships, f.hq, f.profileStore, f.memory)
	result, err := explicit.Remember(ctx, "local", RememberRequest{
		IfVersion: 1, Destination: MemoryDestinationPersonalHQ, Text: "Maybe you use Example for planning.",
	})
	if err != nil || !result.Created {
		t.Fatalf("explicit re-entry was blocked by automatic suppression: %+v %v", result, err)
	}
	canonical, err := f.memory.Read("hq-local")
	if err != nil || len(canonical.Entries()) != 1 || canonical.Entries()[0].Type != workspace.MemoryTypeFact {
		t.Fatalf("explicit re-entry did not save canonically: %+v %v", canonical.Entries(), err)
	}
	if _, created, err := service.Propose(ctx, "local", testProposal("example-app", "Automatic replay again")); !errors.Is(err, ErrKnowledgeSuppressed) || created {
		t.Fatalf("explicit re-entry removed automatic suppression: created=%v err=%v", created, err)
	}
}

func TestKnowledgeProposalQuotaIsSharedAndRolling(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	for _, id := range []string{"app-a", "app-b", "app-c"} {
		proposal := testProposal(id, "Work-style hypothesis for "+id)
		if id == "app-c" {
			proposal.SourceKind = "file_janitor"
			proposal.ScopeID = "workspace-1/root-generation-1"
			proposal.Evidence[0].SourceKind = "file_janitor"
		}
		if _, created, err := service.Propose(ctx, "local", proposal); err != nil || !created {
			t.Fatalf("admit %s: created=%v err=%v", id, created, err)
		}
	}
	if _, _, err := service.Propose(ctx, "local", testProposal("app-d", "Fourth hypothesis")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("fourth in rolling 24 hours was admitted: %v", err)
	}
	// The exact cutoff is outside the rolling window, even after a restart.
	service = NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	service.now = func() time.Time { return now.Add(24 * time.Hour) }
	if _, created, err := service.Propose(ctx, "local", testProposal("app-d", "Fourth hypothesis")); err != nil || !created {
		t.Fatalf("24-hour cutoff: created=%v err=%v", created, err)
	}
	for _, id := range []string{"app-e", "app-f"} {
		if _, created, err := service.Propose(ctx, "local", testProposal(id, "Work-style hypothesis for "+id)); err != nil || !created {
			t.Fatalf("admit %s: created=%v err=%v", id, created, err)
		}
	}
	// Advance another full day: the pending cap still applies independently.
	service.now = func() time.Time { return now.Add(48 * time.Hour) }
	if _, _, err := service.Propose(ctx, "local", testProposal("app-g", "Seventh hypothesis")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("seventh pending proposal was admitted: %v", err)
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || len(doc.Items) != 6 || len(doc.Admissions) != 3 {
		t.Fatalf("quota state=%+v err=%v", doc, err)
	}
}

func TestKnowledgeCandidateEditIsNotApprovalAndStaleEditConflicts(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	original, _, err := service.Propose(ctx, "local", testProposal("app-a", "Original suggestion"))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := service.EditCandidate(ctx, "local", original.ID, original.Version, "Carefully reviewed wording")
	if err != nil || changed.State != KnowledgeCandidate || changed.Version != original.Version+1 ||
		len(changed.Revisions) != 2 || changed.Revisions[1].Text != "Carefully reviewed wording" ||
		changed.CurrentRevisionID != changed.Revisions[1].ID || changed.Target != nil {
		t.Fatalf("candidate edit incorrectly activated: %+v %v", changed, err)
	}
	if _, err := service.EditCandidate(ctx, "local", original.ID, original.Version, "Stale edit"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale candidate edit accepted: %v", err)
	}
	if _, err := service.EditCandidate(ctx, "local", original.ID, changed.Version, "token sk-abcdefghijklmnopqrstuv"); err == nil {
		t.Fatal("secret-bearing candidate edit accepted")
	}
	canonical, err := f.memory.Read("hq-local")
	if err != nil || len(canonical.Entries()) != 0 {
		t.Fatalf("candidate edit wrote canonical memory: %+v %v", canonical.Entries(), err)
	}
	profile, err := f.profileStore.Get(ctx, "local")
	if err != nil || profile.About != "" || len(profile.Preferences) != 0 {
		t.Fatalf("candidate edit wrote canonical profile: %+v %v", profile, err)
	}
	if _, err := service.SuppressCandidate(ctx, "local", original.ID, changed.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || len(doc.Items[0].Revisions) != 0 {
		t.Fatalf("rejected candidate retained edited plaintext: %+v %v", doc, err)
	}
}

func TestKnowledgeProposalEditedAliasesSuppressEquivalentMeaning(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	item, _, err := service.Propose(ctx, "local", testProposal("app-a", "Original suggestion"))
	if err != nil {
		t.Fatal(err)
	}
	// The canonical meaning is unchanged by a candidate wording edit; a
	// separate vetted alias can also be retained for source policy revisions.
	alias := proposalSemanticKey(KnowledgeBinding{UserID: "local", HQWorkspaceID: "hq-local"}, testProposal("app-renamed", "Reworded suggestion"))
	if _, err := service.store.Update(ctx, "local", 1, func(doc *KnowledgeDocument) error {
		doc.Items[0].Aliases = append(doc.Items[0].Aliases, alias)
		doc.Items[0].Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SuppressCandidate(ctx, "local", item.ID, item.Version+1, KnowledgeForgotten); err != nil {
		t.Fatal(err)
	}
	service = NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	for _, subject := range []string{"app-a", "app-renamed"} {
		if _, created, err := service.Propose(ctx, "local", testProposal(subject, "Source still sees the same thing")); !errors.Is(err, ErrKnowledgeSuppressed) || created {
			t.Fatalf("suppressed alias %s reappeared: created=%v err=%v", subject, created, err)
		}
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || len(doc.Tombstones) != 3 || !keySuppressed(doc, knowledgeTextKey("Original suggestion")) || doc.Items[0].State != KnowledgeForgotten || len(doc.Items[0].Revisions) != 0 {
		t.Fatalf("forgotten plaintext or tombstone missing: %+v, %v", doc, err)
	}
}

func TestKnowledgeProposalPausedDoesNotConsumeQuota(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	state, err := f.relationships.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	state.Status = StatusPaused
	if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	if _, created, err := service.Propose(ctx, "local", testProposal("app-a", "Safe text")); !errors.Is(err, ErrKnowledgePaused) || created {
		t.Fatalf("paused admission created=%v err=%v", created, err)
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || doc.Present || len(doc.Admissions) != 0 {
		t.Fatalf("paused admission wrote metadata: %+v, %v", doc, err)
	}
}

func TestKnowledgeProposalConcurrentAdmissionOnlyOneConsumesQuota(t *testing.T) {
	f := newKnowledgeFixture(t)
	const workers = 8
	start := make(chan struct{})
	ids := make(chan string, workers)
	created := make(chan bool, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
			<-start
			item, wasCreated, err := service.Propose(context.Background(), "local", testProposal("same-app", "Same safe hypothesis"))
			ids <- item.ID
			created <- wasCreated
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(ids)
	close(created)
	close(errs)
	var firstID string
	var wins int
	for id := range ids {
		if firstID == "" {
			firstID = id
		}
		if id != firstID || id == "" {
			t.Fatalf("concurrent proposal IDs differ: first=%q got=%q", firstID, id)
		}
	}
	for made := range created {
		if made {
			wins++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent proposal: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent admission consumed %d new slots", wins)
	}
}
