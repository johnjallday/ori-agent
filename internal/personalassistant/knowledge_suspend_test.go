package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestKnowledgeSuspendRemovesCanonicalButRetainsInertRevision(t *testing.T) {
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	suspended, err := s.SuspendApproved(context.Background(), "local", item.ID, item.Version, "req-suspend")
	if err != nil || suspended.State != KnowledgeNeedsReview || suspended.Prepared != nil || suspended.Target != nil || len(suspended.Revisions) != 1 {
		t.Fatalf("suspension=%+v %v", suspended, err)
	}
	raw, err := f.memory.ReadRaw("hq-local")
	if err != nil || strings.Contains(raw, "Reviewed source fact") {
		t.Fatalf("suspended fact still active canonically: %q %v", raw, err)
	}
	restarted := approvedTestService(f)
	again, err := restarted.SuspendApproved(context.Background(), "local", item.ID, item.Version, "req-suspend")
	if err != nil || again.State != KnowledgeNeedsReview || again.Version != suspended.Version {
		t.Fatalf("suspension retry=%+v %v", again, err)
	}
}

func TestKnowledgeSuspensionRestartFinishesOnlyPreparedVerifiedSourceContradiction(t *testing.T) {
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	ctx := context.Background()
	doc, err := s.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Update(ctx, "local", doc.Version, func(doc *KnowledgeDocument) error {
		// The source adapter created this prepared contradiction after UndoDone.
		doc.Items[0].SourceKind = "file_janitor"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writes := 0
	s.store.beforeRename = func() error {
		writes++
		if writes == 2 {
			return errors.New("injected suspension finalization failure")
		}
		return nil
	}
	if _, err := s.SuspendApproved(ctx, "local", item.ID, item.Version, "req-source-undo"); err == nil {
		t.Fatal("interrupted suspension reported success")
	}
	views, err := s.ReviewItems(ctx, "local")
	if err != nil || len(views) != 1 || !views[0].CanResumeOperation || views[0].Text != "" {
		t.Fatalf("interrupted suspension exposed an old value: %+v %v", views, err)
	}
	restarted := approvedTestService(f)
	if _, err := restarted.ResumePreparedForget(ctx, "local", item.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("suspension was cast into Forget: %v", err)
	}
	got, err := restarted.ResumePreparedOperation(ctx, "local", item.ID)
	if err != nil || got.State != KnowledgeNeedsReview || got.Target != nil || got.Prepared != nil {
		t.Fatalf("resumed suspension: %+v %v", got, err)
	}
	raw, err := f.memory.ReadRaw("hq-local")
	if err != nil || strings.Contains(raw, "Reviewed source fact") {
		t.Fatalf("interrupted suspension left an active canonical fact: %q %v", raw, err)
	}
}

func TestKnowledgeSuspendOutsideEditDoesNotRemoveUserText(t *testing.T) {
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	path := filepath.Join(f.folder.path, workspace.MemoryFileName)
	s.store.beforeRename = func() error {
		s.store.beforeRename = nil
		return os.WriteFile(path, []byte("# external user change\n"), 0o600) // #nosec G304 -- disposable fixture
	}
	suspended, err := s.SuspendApproved(context.Background(), "local", item.ID, item.Version, "req-suspend")
	if err != nil || suspended.State != KnowledgeNeedsReview {
		t.Fatalf("outside-deleted marker was not reconciled safely: %+v %v", suspended, err)
	}
	doc, err := s.store.Read(context.Background(), "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || doc.Items[0].Prepared != nil {
		t.Fatalf("outside edit remained eligible: %+v %v", doc, err)
	}
	raw, err := f.memory.ReadRaw("hq-local")
	if err != nil || raw != "# external user change\n" {
		t.Fatalf("outside edit overwritten: %q %v", raw, err)
	}
}
