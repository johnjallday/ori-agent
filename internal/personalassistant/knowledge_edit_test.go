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

func TestKnowledgeApprovedEditChangesOnlyReviewedCanonicalValue(t *testing.T) {
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	path := filepath.Join(f.folder.path, workspace.MemoryFileName)
	before, err := os.ReadFile(path) // #nosec G304 -- disposable test HQ
	if err != nil {
		t.Fatal(err)
	}
	edited, err := s.EditApproved(context.Background(), "local", item.ID, item.Version, "req-edit", "Carefully corrected fact")
	if err != nil || edited.State != KnowledgeApproved || edited.Target == nil || edited.CurrentRevisionID == item.CurrentRevisionID || edited.Target.Marker == item.Target.Marker {
		t.Fatalf("edit=%+v err=%v", edited, err)
	}
	after, err := os.ReadFile(path) // #nosec G304 -- disposable test HQ
	if err != nil || !strings.Contains(string(after), "Carefully corrected fact") || strings.Contains(string(after), "Reviewed source fact") {
		t.Fatalf("canonical value not replaced: %q %v", after, err)
	}
	if !strings.HasPrefix(string(after), strings.Split(string(before), "- [fact,")[0]) {
		t.Fatalf("edit changed unrelated prelude: %q", after)
	}
	replay := approvedTestService(f)
	got, err := replay.EditApproved(context.Background(), "local", item.ID, item.Version, "req-edit", "Carefully corrected fact")
	if err != nil || got.Version != edited.Version {
		t.Fatalf("idempotent edit replay=%+v %v", got, err)
	}
	if _, err := replay.EditApproved(context.Background(), "local", item.ID, item.Version, "req-edit-2", "stale edit"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale edit overwrote reviewed value: %v", err)
	}
}

func TestKnowledgeApprovedEditRestartAfterCanonicalBeforeFinalization(t *testing.T) {
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	writes := 0
	s.store.beforeRename = func() error {
		writes++
		if writes == 2 {
			return errors.New("injected finalize failure")
		}
		return nil
	}
	if _, err := s.EditApproved(context.Background(), "local", item.ID, item.Version, "req-edit", "Only the new fact"); err == nil {
		t.Fatal("partial edit was reported as committed")
	}
	doc, err := s.store.Read(context.Background(), "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || doc.Items[0].Prepared == nil {
		t.Fatalf("partial edit still active: %+v %v", doc, err)
	}
	views, err := s.ReviewItems(context.Background(), "local")
	if err != nil || len(views) != 1 || !views[0].CanResumeOperation || views[0].Text != "" {
		t.Fatalf("prepared edit leaked intermediate content: %+v %v", views, err)
	}
	memory, err := f.memory.SnapshotExact("hq-local")
	if err != nil || len(memory.Entries) != 1 || memory.Entries[0].Entry.Text != "Only the new fact" {
		t.Fatalf("canonical edit missing or duplicated: %+v %v", memory, err)
	}
	restart := approvedTestService(f)
	got, err := restart.ResumePreparedOperation(context.Background(), "local", item.ID)
	if err != nil || got.State != KnowledgeApproved {
		t.Fatalf("restarted edit=%+v %v", got, err)
	}
	memory, err = f.memory.SnapshotExact("hq-local")
	if err != nil || len(memory.Entries) != 1 {
		t.Fatalf("edit replay duplicated canonical value: %+v %v", memory, err)
	}
}

func TestKnowledgeApprovedEditExternalChangeNeverRestoresOldRevision(t *testing.T) {
	f := newKnowledgeFixture(t)
	s, item := approvedKnowledgeItem(t, f)
	path := filepath.Join(f.folder.path, workspace.MemoryFileName)
	s.store.beforeRename = func() error {
		s.store.beforeRename = nil
		return os.WriteFile(path, []byte("# outside edit\n"), 0o600) // #nosec G304 -- test fixture
	}
	if _, err := s.EditApproved(context.Background(), "local", item.ID, item.Version, "req-edit", "Proposed correction"); !errors.Is(err, ErrConflict) {
		t.Fatalf("outside edit unexpectedly adopted: %v", err)
	}
	doc, err := s.store.Read(context.Background(), "local")
	if err != nil || doc.Items[0].State != KnowledgeNeedsReview || doc.Items[0].Prepared != nil {
		t.Fatalf("outside-edited fact remained active: %+v %v", doc, err)
	}
	raw, err := f.memory.ReadRaw("hq-local")
	if err != nil || raw != "# outside edit\n" {
		t.Fatalf("outside edit overwritten: %q %v", raw, err)
	}
}
