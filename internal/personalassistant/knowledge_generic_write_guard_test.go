package personalassistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestKnowledgeTombstoneCapacityCountsOnlyNewSuppressionHashes(t *testing.T) {
	doc := KnowledgeDocument{Tombstones: make([]KnowledgeTombstone, knowledgeMaxTombstone)}
	for i := range doc.Tombstones {
		doc.Tombstones[i].SemanticKey = knowledgeTextKey(string(rune('A' + i)))
	}
	if !hasKnowledgeTombstoneCapacity(doc, []string{doc.Tombstones[0].SemanticKey}) {
		t.Fatal("an already suppressed revision must not block Forget at the tombstone limit")
	}
	if hasKnowledgeTombstoneCapacity(doc, []string{"text:new-suppression"}) {
		t.Fatal("a new suppression hash exceeded the bound")
	}
}

func TestGenericHQToolCannotDuplicateOrResurrectRejectedForgottenOrSuspendedText(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s := approvedTestService(f)
	candidate, _, err := s.Propose(ctx, "local", testProposal("app-a", "Pending work preference"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GuardGenericMemoryWrite(ctx, "local", "hq-local", "pending work preference"); !errors.Is(err, workspace.ErrMemoryManaged) {
		t.Fatalf("generic tool promoted a pending candidate: %v", err)
	}
	candidate, err = s.EditCandidate(ctx, "local", candidate.ID, candidate.Version, "Edited pending preference")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SuppressCandidate(ctx, "local", candidate.ID, candidate.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"pending work preference", "edited pending preference"} {
		if err := s.GuardGenericMemoryWrite(ctx, "local", "hq-local", text); !errors.Is(err, workspace.ErrMemoryManaged) {
			t.Fatalf("generic tool resurrected rejected wording %q: %v", text, err)
		}
	}
	approved, _, err := s.Propose(ctx, "local", testProposal("app-b", "Another reviewed preference"))
	if err != nil {
		t.Fatal(err)
	}
	approved, err = s.ApproveCandidate(ctx, "local", approved.ID, approved.Version, "generic-approve")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GuardGenericMemoryWrite(ctx, "local", "hq-local", "Another reviewed preference"); !errors.Is(err, workspace.ErrMemoryManaged) {
		t.Fatalf("generic tool duplicated approved value: %v", err)
	}
	if _, err := s.ForgetItem(ctx, "local", approved.ID, approved.Version, "generic-forget"); err != nil {
		t.Fatal(err)
	}
	restarted := approvedTestService(f)
	if err := restarted.GuardGenericMemoryWrite(ctx, "local", "hq-local", "another reviewed preference"); !errors.Is(err, workspace.ErrMemoryManaged) {
		t.Fatalf("generic tool resurrected forgotten wording after restart: %v", err)
	}
	if err := restarted.GuardGenericMemoryWrite(ctx, "local", "hq-local", "Unrelated operational fact"); err != nil {
		t.Fatalf("unrelated HQ tool memory was blocked: %v", err)
	}
	if err := restarted.GuardGenericMemoryWrite(ctx, "local", "other", "Unrelated operational fact"); err == nil {
		t.Fatal("guard applied under another workspace binding")
	}
	doc, err := restarted.store.Read(ctx, "local")
	if err != nil || len(doc.Tombstones) < 4 {
		t.Fatalf("suppression hashes lost on restart: %+v %v", doc.Tombstones, err)
	}
	for _, tombstone := range doc.Tombstones {
		if strings.Contains(tombstone.SemanticKey, "preference") {
			t.Fatal("plaintext persisted in suppression key")
		}
	}
}
