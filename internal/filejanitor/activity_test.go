package filejanitor

import (
	"os"
	"strings"
	"sync"
	"testing"
)

type recordingJanitorActivity struct {
	mu         sync.Mutex
	activities []Activity
}

func (r *recordingJanitorActivity) PublishJanitorActivity(activity Activity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activities = append(r.activities, activity)
}

func (r *recordingJanitorActivity) all() []Activity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Activity(nil), r.activities...)
}

func scanFinish(t *testing.T, activities []Activity) Activity {
	t.Helper()
	if len(activities) != 2 {
		t.Fatalf("expected a start and a finish and nothing between, got %#v", activities)
	}
	start, finish := activities[0], activities[1]
	if start.Phase != ActivityStarted || finish.Phase != ActivityFinished {
		t.Fatalf("phases = %q, %q", start.Phase, finish.Phase)
	}
	if start.ActivityID == "" || start.ActivityID != finish.ActivityID {
		t.Fatalf("one scan has one activity id: %q vs %q", start.ActivityID, finish.ActivityID)
	}
	if start.WorkspaceID != "ws-1" || start.Source != ScanSourceWatcher {
		t.Fatalf("start = %#v", start)
	}
	return finish
}

func TestActivity_AScanWithNewProposalsFinishesWithItsBatchAndCount(t *testing.T) {
	automation, _, root, _, _ := automationFixture(t)
	publisher := &recordingJanitorActivity{}
	automation.SetActivityPublisher(publisher)
	agedFile(t, root, "a.pdf", 10)
	agedFile(t, root, "b.pdf", 10)

	automation.runOnce("ws-1", ScanSourceWatcher)

	finish := scanFinish(t, publisher.all())
	if finish.Outcome != "succeeded" || finish.Count != 2 || finish.BatchID == "" {
		t.Fatalf("finish = %#v", finish)
	}
	for _, activity := range publisher.all() {
		if strings.Contains(activity.ActivityID, "a.pdf") || strings.Contains(activity.BatchID, root) {
			t.Fatalf("an activity names no file or path: %#v", activity)
		}
	}
}

func TestActivity_AScanThatFindsNothingNewFinishesWithZero(t *testing.T) {
	automation, _, root, _, _ := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)
	automation.runOnce("ws-1", ScanSourceDaily)

	publisher := &recordingJanitorActivity{}
	automation.SetActivityPublisher(publisher)
	automation.runOnce("ws-1", ScanSourceWatcher)

	finish := scanFinish(t, publisher.all())
	if finish.Outcome != "succeeded" || finish.Count != 0 || finish.BatchID != "" {
		t.Fatalf("finish = %#v", finish)
	}
}

func TestActivity_AFailedScanFinishesAsFailed(t *testing.T) {
	automation, _, root, _, _ := automationFixture(t)
	publisher := &recordingJanitorActivity{}
	automation.SetActivityPublisher(publisher)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	automation.runOnce("ws-1", ScanSourceWatcher)

	finish := scanFinish(t, publisher.all())
	if finish.Outcome != "failed" || finish.Count != 0 || finish.BatchID != "" {
		t.Fatalf("finish = %#v", finish)
	}
}

func TestActivity_AScanThatNeverRunsIsNotAnActivity(t *testing.T) {
	automation, service, root, _, _ := automationFixture(t)
	publisher := &recordingJanitorActivity{}
	automation.SetActivityPublisher(publisher)
	agedFile(t, root, "a.pdf", 10)
	if _, err := service.store.UpdateSettings("ws-1", func(s *JanitorSettings) error {
		s.Paused = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	automation.runOnce("ws-1", ScanSourceWatcher)
	automation.runOnce("ws-unconfigured", ScanSourceWatcher)
	if got := publisher.all(); len(got) != 0 {
		t.Fatalf("paused or unconfigured workspaces start nothing: %#v", got)
	}
}

func TestActivity_NilPublisherIsSafe(t *testing.T) {
	automation, _, root, _, _ := automationFixture(t)
	agedFile(t, root, "a.pdf", 10)
	automation.SetActivityPublisher(nil)
	automation.runOnce("ws-1", ScanSourceWatcher)
}
