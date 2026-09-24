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

type approvingTestAuthority struct{ err error }

func (a approvingTestAuthority) Revalidate(context.Context, KnowledgeBinding, KnowledgeItem) error {
	return a.err
}

func approvedTestService(f *knowledgeFixture) *KnowledgeLearningService {
	return NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, approvingTestAuthority{})
}

func TestKnowledgeApprovalRequiresServerAuthorityAndExactEditedRevision(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	service := NewKnowledgeLearningService(NewKnowledgeStore(f.resolver(), f.folder))
	item, _, err := service.Propose(ctx, "local", testProposal("app-a", "Unreviewed suggestion"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveCandidate(ctx, "local", item.ID, item.Version, "req-1"); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("approval without evidence authority succeeded: %v", err)
	}
	service = approvedTestService(f)
	item, err = service.EditCandidate(ctx, "local", item.ID, item.Version, "Exactly approved wording")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveCandidate(ctx, "local", item.ID, item.Version, "req-1")
	if err != nil || approved.State != KnowledgeApproved || approved.Target == nil || approved.Prepared != nil {
		t.Fatalf("approval=%+v err=%v", approved, err)
	}
	snapshot, err := f.memory.SnapshotExact("hq-local")
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Entry.Text != "Exactly approved wording" ||
		snapshot.Entries[0].Target.LineHash != approved.Target.CanonicalHash ||
		snapshot.Entries[0].Target.Provenance != approved.Target.Marker {
		t.Fatalf("canonical revision mismatch: %+v %v", snapshot, err)
	}
	// A durable request replay may return only the reviewed committed result.
	replayed := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, approvingTestAuthority{})
	again, err := replayed.ApproveCandidate(ctx, "local", item.ID, item.Version, "req-1")
	if err != nil || again.Version != approved.Version {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
	snapshot, err = f.memory.SnapshotExact("hq-local")
	if err != nil || len(snapshot.Entries) != 1 {
		t.Fatalf("approval replay duplicated memory: %+v %v", snapshot, err)
	}
}

func TestKnowledgeApprovalRestartAfterCanonicalWriteBeforeSidecarFinalization(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	service := approvedTestService(f)
	item, _, err := service.Propose(ctx, "local", testProposal("app-a", "Confirmed on review"))
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	service.store.beforeRename = func() error {
		writes++
		if writes == 2 {
			return errors.New("injected finalize failure")
		}
		return nil
	}
	if _, err := service.ApproveCandidate(ctx, "local", item.ID, item.Version, "req-1"); err == nil {
		t.Fatal("sidecar finalization failure reported success")
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || doc.Items[0].State != KnowledgeCandidate || doc.Items[0].Prepared == nil {
		t.Fatalf("unsafe partial approval state: %+v %v", doc, err)
	}
	views, err := service.ReviewItems(ctx, "local")
	if err != nil || len(views) != 1 || !views[0].CanResumeOperation || views[0].Text != "" {
		t.Fatalf("prepared approval leaked intermediate content: %+v %v", views, err)
	}
	memory, err := f.memory.SnapshotExact("hq-local")
	if err != nil || len(memory.Entries) != 1 {
		t.Fatalf("canonical write did not persist: %+v %v", memory, err)
	}
	recovered := approvedTestService(f)
	if _, err := recovered.ResumePreparedForget(ctx, "local", item.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong recovery kind changed prepared approval: %v", err)
	}
	approved, err := recovered.ResumePreparedOperation(ctx, "local", item.ID)
	if err != nil || approved.State != KnowledgeApproved {
		t.Fatalf("recovery did not finalize: %+v %v", approved, err)
	}
	memory, err = f.memory.SnapshotExact("hq-local")
	if err != nil || len(memory.Entries) != 1 {
		t.Fatalf("recovery duplicated canonical write: %+v %v", memory, err)
	}
}

func TestKnowledgeApprovalExternalEditConflictsWithoutResurrection(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	service := approvedTestService(f)
	item, _, err := service.Propose(ctx, "local", testProposal("app-a", "Suggested fact"))
	if err != nil {
		t.Fatal(err)
	}
	service.store.beforeRename = func() error {
		// Interpose an outside MEMORY.md edit between the prepared sidecar
		// write and canonical append. This is an ordinary hand edit, not a
		// reason to restore the old candidate value.
		service.store.beforeRename = nil
		return os.WriteFile(filepath.Join(f.folder.path, workspace.MemoryFileName), []byte("# hand-edited\n"), 0o600)
	}
	if _, err := service.ApproveCandidate(ctx, "local", item.ID, item.Version, "req-1"); err == nil {
		t.Fatal("outside edit was silently overwritten")
	}
	doc, err := service.store.Read(ctx, "local")
	if err != nil || doc.Items[0].State == KnowledgeApproved {
		t.Fatalf("outside edit produced active fact: %+v %v", doc, err)
	}
	raw, err := f.memory.ReadRaw("hq-local")
	if err != nil || strings.Contains(raw, "Suggested fact") || raw != "# hand-edited\n" {
		t.Fatalf("outside edit was overwritten: %q %v", raw, err)
	}
}
