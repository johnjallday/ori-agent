package projectlibrary

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestActivationQueue_DurableOrderSkipNoCreatorAndReplay(t *testing.T) {
	a, scope, _, file, tree, _ := activationFixture(t)
	s := a.library
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	queue, replay, err := s.StartActivationQueue(scope, []string{"alternates", "single"}, "queue-start")
	if err != nil || replay || queue.Index != 0 || queue.Status != "active" || queue.Revision != 1 {
		t.Fatalf("start: %+v %t %v", queue, replay, err)
	}
	if same, replay, err := s.StartActivationQueue(scope, []string{"alternates", "single"}, "queue-start"); err != nil || !replay || same.ID != queue.ID {
		t.Fatalf("start replay: %+v %t %v", same, replay, err)
	}
	if _, _, err := s.StartActivationQueue(scope, []string{"single", "alternates"}, "queue-start"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reordered replay: %v", err)
	}
	if _, _, err := s.StartActivationQueue(scope, []string{"single", "single"}, "other-start"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate selection: %v", err)
	}
	if _, _, err := s.ProgressActivationQueue(scope, queue.ID, "single", "skip", "wrong-order", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("out of order skip: %v", err)
	}
	if _, _, err := s.ProgressActivationQueue(scope, queue.ID, "alternates", "connected", "fabricated-link", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("browser asserted nonexistent child: %v", err)
	}
	skipped, replay, err := s.ProgressActivationQueue(scope, queue.ID, "alternates", "skip", "skip-one", 1)
	if err != nil || replay || skipped.Index != 1 || skipped.Revision != 2 || len(skipped.Skipped) != 1 || skipped.Skipped[0] != "alternates" {
		t.Fatalf("skip receipt: %+v %t %v", skipped, replay, err)
	}
	if _, _, err := s.ProgressActivationQueue(scope, queue.ID, "single", "skip", "stale-skip", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale queue revision: %v", err)
	}
	folder := filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID)))
	reopened, err := workspace.NewFileStore(folder)
	if err != nil {
		t.Fatal(err)
	}
	other := NewStore(reopened)
	view, err := other.CurrentActivationQueue(scope)
	if err != nil || view.Queue == nil || view.Queue.Index != 1 || view.Queue.ID != queue.ID || view.Queue.Skipped[0] != "alternates" ||
		fileDigest(t, filepath.Join(tree.single, "Song.rpp")) != before {
		t.Fatalf("lost durable skip or changed source: %+v %v", view, err)
	}
	if again, replay, err := other.ProgressActivationQueue(scope, queue.ID, "alternates", "skip", "skip-one", 1); err != nil || !replay || again.Index != 1 {
		t.Fatalf("skip replay across restart: %+v %t %v", again, replay, err)
	}
	if _, _, err := other.ProgressActivationQueue(scope, queue.ID, "alternates", "skip", "skip-one", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("key reused for changed revision: %v", err)
	}
	if _, _, err := other.ProgressActivationQueue(scope, queue.ID, "single", "skip", "skip-two", 2); err != nil {
		t.Fatal(err)
	}
	if view, err := other.CurrentActivationQueue(scope); err != nil || view.Queue != nil {
		t.Fatalf("completed queue still active: %+v %v", view, err)
	}
	if _, _, err := other.StartActivationQueue(scope, []string{"single", "alternates"}, "new-queue"); err != nil {
		t.Fatal(err)
	}
}

func TestActivationQueue_ExpiryAndDiscardRequireExplicitOwnerAction(t *testing.T) {
	a, scope, _, file, _, _ := activationFixture(t)
	s := a.library
	queue, _, err := s.StartActivationQueue(scope, []string{"single", "alternates"}, "queue-start")
	if err != nil {
		t.Fatal(err)
	}
	clock := s.now()
	s.now = func() time.Time { return clock.Add(8 * 24 * time.Hour) }
	if view, err := s.CurrentActivationQueue(scope); err != nil || view.Queue == nil || view.Queue.Status != "expired" {
		t.Fatalf("expiry projection: %+v %v", view, err)
	}
	if _, _, err := s.ProgressActivationQueue(scope, queue.ID, "single", "skip", "late-skip", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired queue progressed: %v", err)
	}
	if _, _, err := s.StartActivationQueue(scope, []string{"single", "alternates"}, "replacement"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired queue silently replaced: %v", err)
	}
	if _, err := s.DiscardActivationQueue(scope, queue.ID, "wrong-revision", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("discard without current queue revision: %v", err)
	}
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.PluginAvailable = false
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.DiscardActivationQueue(scope, queue.ID, "discard", 1); err != nil || replay {
		t.Fatalf("discard: %t %v", replay, err)
	}
	if replay, err := s.DiscardActivationQueue(scope, queue.ID, "discard", 1); err != nil || !replay {
		t.Fatalf("discard replay: %t %v", replay, err)
	}
	if view, err := s.CurrentActivationQueue(scope); err != nil || view.Queue != nil {
		t.Fatalf("discarded queue visible: %+v %v", view, err)
	}
}
