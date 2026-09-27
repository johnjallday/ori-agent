package projectlibrary

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestForget_RevokedUnlinkedHomeRecordRequiresReviewAndPreservesExternalFiles(t *testing.T) {
	inspector, scope, roots, file, tree, _ := activationFixture(t)
	library := inspector.library
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.ReviewForget(scope, "single", doc.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("active source could be forgotten before disconnect: %v", err)
	}
	goal := GoalInput{Goal: "Write a melody"}
	goalReview, err := library.ReviewGoal(scope, "single", 1, goal, scope.OwnerUserID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.CommitGoal(scope, "single", goalReview.Token, "forget-goal", 1, goal, scope.OwnerUserID); err != nil {
		t.Fatal(err)
	}
	song := filepath.Join(tree.single, "Song.rpp")
	before := fileDigest(t, song)
	doc, err = library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := roots.ReviewRevoke(scope, doc.Roots[0].ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := roots.CommitRevoke(scope, doc.Roots[0].ID, revoke.Token, "forget-disconnect"); err != nil {
		t.Fatal(err)
	}
	doc, err = library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	at := library.now().UTC()
	_, _, err = library.mutate(scope, doc.Revision, operation{key: "forget-fixture-proposal", action: "fixture", digest: "fixture"},
		func(current *Document) (string, error) {
			current.Proposals = append(current.Proposals, ManagerProposal{ID: "suggestion", EntryID: "single",
				BindingRevision: 1, AgentInstanceID: "manager", AgentName: "Manager", NextAction: "Old suggestion",
				Digest: strings.Repeat("a", 64), CreatedAt: at, ExpiresAt: at.Add(time.Hour)})
			return "suggestion", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	queue, _, err := library.StartActivationQueue(scope, []string{"alternates", "single"}, "forget-queue")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := library.ProgressActivationQueue(scope, queue.ID, "alternates", "skip", "forget-queue-skip-first", 1); err != nil {
		t.Fatal(err)
	}
	doc, err = library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	library.WithProviderEvidence(func(_ Scope, _ *workspace.Workspace) bool { return false })
	review, err := library.ReviewForget(scope, "single", doc.Revision)
	if err != nil || review.ProjectName != "Single" || review.SourceCount != 1 || review.SessionCount != 1 || review.QueuedAt != 2 {
		t.Fatalf("review omitted destructive impact: %+v %v", review, err)
	}
	current, err := library.Read(scope)
	if err != nil || len(current.Entries) != len(doc.Entries) || len(current.Sessions) != 1 {
		t.Fatalf("inert review erased user notes: %+v %v", current, err)
	}
	if _, err := library.CommitForget(scope, "single", review.Token, "wrong-confirm"); err != nil {
		t.Fatal(err)
	}
	replayed, err := library.CommitForget(scope, "single", review.Token, "wrong-confirm")
	if err != nil || !replayed {
		t.Fatalf("forget replay was not idempotent: replay=%v err=%v", replayed, err)
	}
	path, err := file.GetFolderPath(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := NewStore(reopened).Read(scope)
	if err != nil || len(persisted.Roots) != 1 || len(persisted.Entries) != len(doc.Entries)-1 ||
		len(persisted.Sessions) != 0 || len(persisted.Proposals) != 0 ||
		sessionEntry(persisted, "single") != nil || fileDigest(t, song) != before {
		t.Fatalf("forget modified source, grant or other Home history: %+v %v", persisted, err)
	}
	if _, err := NewStore(reopened).GetSession(scope, "single", goalReview.Session.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("forgotten session remained readable: %v", err)
	}
	queueStore := NewStore(reopened)
	if view, err := queueStore.CurrentActivationQueue(scope); err != nil || view.Queue == nil || view.Queue.ID != queue.ID || view.Queue.Index != 1 {
		t.Fatalf("forget silently removed the saved queue or changed skip order: %+v %v", view, err)
	}
	if completed, _, err := queueStore.ProgressActivationQueue(scope, queue.ID, "single", "skip", "forget-queue-skip-gone", 2); err != nil || completed.Status != "complete" ||
		fileDigest(t, song) != before {
		t.Fatalf("explicitly skipping forgotten item changed source or failed: %+v %v", completed, err)
	}
}

func TestForget_RefusesLinkedProjectEvenAfterDiscoveryRevoked(t *testing.T) {
	inspector, scope, roots, file, tree, plugins := activationFixture(t)
	childID := connectExistingSong(t, scope, file, plugins, tree.single)
	doc, err := inspector.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	linkReview, err := inspector.library.ReviewLinkedProject(scope, childID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.library.CommitLinkedProject(scope, childID, linkReview.Token, "forget-linked-association"); err != nil {
		t.Fatal(err)
	}
	doc, err = inspector.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := roots.ReviewRevoke(scope, doc.Roots[0].ID, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := roots.CommitRevoke(scope, doc.Roots[0].ID, revoke.Token, "forget-linked-disconnect"); err != nil {
		t.Fatal(err)
	}
	doc, err = inspector.library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.library.ReviewForget(scope, "single", doc.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("linked project could be forgotten and strand child: %v", err)
	}
	if _, err := inspector.library.ReviewForget(scope, "foreign", doc.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign ID yielded a forget review: %v", err)
	}
}
