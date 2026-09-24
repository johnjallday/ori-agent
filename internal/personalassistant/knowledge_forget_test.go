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

func approvedKnowledgeItem(t *testing.T, f *knowledgeFixture) (*KnowledgeLearningService, KnowledgeItem) {
	t.Helper()
	s := approvedTestService(f)
	item, _, err := s.Propose(context.Background(), "local", testProposal("app-a", "Reviewed source fact"))
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.ApproveCandidate(context.Background(), "local", item.ID, item.Version, "req-approve")
	if err != nil {
		t.Fatal(err)
	}
	return s, item
}

func TestKnowledgeForgetScrubsAndSuppressesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	forgotten, err := s.ForgetApproved(ctx, "local", item.ID, item.Version, "req-forget")
	if err != nil || forgotten.State != KnowledgeForgotten || len(forgotten.Revisions) != 0 || forgotten.CurrentRevisionID != "" {
		t.Fatalf("forget=%+v err=%v", forgotten, err)
	}
	memory, err := f.memory.ReadRaw("hq-local")
	if err != nil || strings.Contains(memory, "Reviewed source fact") {
		t.Fatalf("forgotten canonical value remains: %q %v", memory, err)
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || len(doc.Tombstones) != 2 || len(doc.Receipts) != 2 || !keySuppressed(doc, knowledgeTextKey("Reviewed source fact")) {
		t.Fatalf("suppression/receipts missing: %+v %v", doc, err)
	}
	restarted := approvedTestService(f)
	if _, created, err := restarted.Propose(ctx, "local", testProposal("app-a", "Automatic replay after forget")); !errors.Is(err, ErrKnowledgeSuppressed) || created {
		t.Fatalf("automatic reproposal after forget: created=%v err=%v", created, err)
	}
	if _, err := restarted.ForgetApproved(ctx, "local", item.ID, item.Version, "req-forget"); err != nil {
		t.Fatalf("durable forget replay: %v", err)
	}
}

func TestKnowledgeForgetPreparedExclusionSurvivesCanonicalWriteFailure(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	// A symlink is not a trusted MEMORY.md target. Refuse the delete even
	// though the separate sidecar already excludes and suppresses the fact.
	path := filepath.Join(f.folder.path, workspace.MemoryFileName)
	outside := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(outside, []byte("external data"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.store.beforeRename = func() error {
		s.store.beforeRename = nil
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Symlink(outside, path)
	}
	if _, err := s.ForgetApproved(ctx, "local", item.ID, item.Version, "req-forget"); err == nil {
		t.Fatal("unsafe canonical path reported durable deletion")
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || doc.Items[0].Prepared == nil || len(doc.Items[0].Revisions) != 0 || len(doc.Tombstones) == 0 {
		t.Fatalf("failed delete not excluded/suppressed: %+v %v", doc, err)
	}
	data, err := os.ReadFile(outside) // #nosec G304 -- test-only temporary folder
	if err != nil || string(data) != "external data" {
		t.Fatalf("outside symlink target was mutated: %q %v", data, err)
	}
}

func TestKnowledgeForgetServerOwnedRecoveryActionAfterInterruptedResponse(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	writes := 0
	s.store.beforeRename = func() error {
		writes++
		if writes == 2 {
			return errors.New("injected interrupted finalization")
		}
		return nil
	}
	if _, err := s.ForgetItem(ctx, "local", item.ID, item.Version, "req-forget-disconnected"); err == nil {
		t.Fatal("interrupted Forget falsely reported success")
	}
	views, err := s.ReviewItems(ctx, "local")
	if err != nil || len(views) != 1 || !views[0].CanResumeForget || views[0].Text != "" {
		t.Fatalf("prepared Forget was not safely reviewable: %+v %v", views, err)
	}
	other := approvedTestService(f) // new store instance simulates process restart
	completed, err := other.ResumePreparedForget(ctx, "local", item.ID)
	if err != nil || completed.State != KnowledgeForgotten || len(completed.Revisions) != 0 {
		t.Fatalf("server-owned retry failed: %+v %v", completed, err)
	}
	raw, err := f.memory.ReadRaw("hq-local")
	if err != nil || strings.Contains(raw, "Reviewed source fact") {
		t.Fatalf("interrupted response retained canonical plaintext: %q %v", raw, err)
	}
	if views, err := other.ReviewItems(ctx, "local"); err != nil || len(views) != 0 {
		t.Fatalf("forgotten fact still visible: %+v %v", views, err)
	}
}

func TestKnowledgeForgetRecoveryAfterCanonicalDeletionBeforeFinalize(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	writes := 0
	s.store.beforeRename = func() error {
		writes++
		if writes == 2 {
			return errors.New("injected forget finalize failure")
		}
		return nil
	}
	if _, err := s.ForgetApproved(ctx, "local", item.ID, item.Version, "req-forget"); err == nil {
		t.Fatal("partial forget reported success")
	}
	doc, err := s.store.Read(ctx, "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || doc.Items[0].Prepared == nil || len(doc.Items[0].Revisions) != 0 {
		t.Fatalf("partial forget retained active plaintext: %+v %v", doc, err)
	}
	memory, err := f.memory.ReadRaw("hq-local")
	if err != nil || strings.Contains(memory, "Reviewed source fact") {
		t.Fatalf("canonical deletion did not persist: %q %v", memory, err)
	}
	restarted := approvedTestService(f)
	forgotten, err := restarted.ForgetApproved(ctx, "local", item.ID, item.Version, "req-forget")
	if err != nil || forgotten.State != KnowledgeForgotten {
		t.Fatalf("forget recovery=%+v err=%v", forgotten, err)
	}
}
