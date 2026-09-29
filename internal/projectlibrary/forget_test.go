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

func TestForget_RedactsHandledQueueIDsAndCompactsTerminalOutcome(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "active_skipped", true: "terminal"}[terminal], func(t *testing.T) {
			inspector, scope, roots, _, tree, _ := activationFixture(t)
			s := inspector.library
			before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
			doc, err := s.Read(scope)
			if err != nil {
				t.Fatal(err)
			}
			revoke, err := roots.ReviewRevoke(scope, doc.Roots[0].ID, doc.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := roots.CommitRevoke(scope, doc.Roots[0].ID, revoke.Token, "redact-root"); err != nil {
				t.Fatal(err)
			}
			q, _, err := s.StartActivationQueue(scope, []string{"single", "alternates"}, "redact-queue")
			if err != nil {
				t.Fatal(err)
			}
			skipped, _, err := s.ProgressActivationQueue(scope, q.ID, "single", "skip", "redact-skip", 1)
			if err != nil {
				t.Fatal(err)
			}
			if terminal {
				if _, _, err := s.ProgressActivationQueue(scope, q.ID, "alternates", "skip", "redact-last", skipped.Revision); err != nil {
					t.Fatal(err)
				}
			}
			doc, err = s.Read(scope)
			if err != nil {
				t.Fatal(err)
			}
			review, err := s.ReviewForget(scope, "single", doc.Revision)
			if err != nil || review.QueuedAt != 0 {
				t.Fatalf("already handled item misreported as pending: %+v %v", review, err)
			}
			if _, err := s.CommitForget(scope, "single", review.Token, "redact-forget"); err != nil {
				t.Fatal(err)
			}
			doc, err = s.Read(scope)
			if err != nil {
				t.Fatal(err)
			}
			if terminal {
				if doc.Queue != nil || len(doc.QueueHistory) != 1 || doc.QueueHistory[0].SkippedCount != 2 || doc.QueueHistory[0].SelectedCount != 2 {
					t.Fatalf("terminal queue retained forgotten catalog IDs: %+v", doc)
				}
			} else {
				if doc.Queue == nil || doc.Queue.Index != 1 || doc.Queue.IDs[0] == "single" ||
					doc.Queue.Skipped[0] != doc.Queue.IDs[0] || doc.Queue.IDs[1] != "alternates" || doc.Queue.Revision <= skipped.Revision {
					t.Fatalf("active queue retained forgotten skipped ID or lost remaining item: %+v", doc.Queue)
				}
				if _, _, err := s.ProgressActivationQueue(scope, q.ID, "alternates", "skip", "after-redact", doc.Queue.Revision); err != nil {
					t.Fatalf("redacting a handled ID prevented later explicit skip: %v", err)
				}
			}
			if fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
				t.Fatal("forget/redaction modified external project")
			}
		})
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
