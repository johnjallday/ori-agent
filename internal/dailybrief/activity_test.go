package dailybrief

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type recordingActivityPublisher struct {
	mu         sync.Mutex
	activities []Activity
}

func (p *recordingActivityPublisher) PublishBriefActivity(activity Activity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activities = append(p.activities, activity)
}

func (p *recordingActivityPublisher) all() []Activity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Activity(nil), p.activities...)
}

// failingRevisionStore persists everything except the revision itself.
type failingRevisionStore struct {
	*SQLiteStore
}

func (s failingRevisionStore) CreateRevision(context.Context, *Revision) error {
	return errors.New("disk full")
}

// startAndFinish asserts a generation told the map exactly two things — that
// it started and how it finished — and returns the finish.
func startAndFinish(t *testing.T, publisher *recordingActivityPublisher, workspaceID string, trigger Trigger) Activity {
	t.Helper()
	got := publisher.all()
	if len(got) != 2 {
		t.Fatalf("expected a start and a finish and nothing between, got %#v", got)
	}
	start, finish := got[0], got[1]
	if start.Phase != ActivityStarted || finish.Phase != ActivityFinished {
		t.Fatalf("phases = %q, %q", start.Phase, finish.Phase)
	}
	if start.ActivityID == "" || start.ActivityID != finish.ActivityID {
		t.Fatalf("one run has one activity id: %q vs %q", start.ActivityID, finish.ActivityID)
	}
	if start.WorkspaceID != workspaceID || finish.WorkspaceID != workspaceID {
		t.Fatalf("workspace = %q, %q", start.WorkspaceID, finish.WorkspaceID)
	}
	if start.Trigger != trigger || finish.Trigger != trigger || finish.LocalDate == "" {
		t.Fatalf("trigger/date = %#v", finish)
	}
	if start.Outcome != "" || start.RevisionID != "" {
		t.Fatalf("a start claims no outcome: %#v", start)
	}
	return finish
}

func TestActivity_SucceededAndPartialGenerationsReportTheirRevision(t *testing.T) {
	for _, status := range []GenerationStatus{GenerationSucceeded, GenerationPartial} {
		t.Run(string(status), func(t *testing.T) {
			store := newServiceTestStore(t)
			svc := NewService(store, &fakeGenerator{result: GenerationResult{Status: status, ContentJSON: `{"opening_summary":"ok"}`}})
			publisher := &recordingActivityPublisher{}
			svc.SetActivityPublisher(publisher)
			seedConfig(t, store, "hq")

			rev, err := svc.RequestGenerationNow(context.Background(), "hq", "local", TriggerScheduled)
			if err != nil {
				t.Fatalf("RequestGenerationNow: %v", err)
			}
			finish := startAndFinish(t, publisher, "hq", TriggerScheduled)
			if finish.Outcome != string(status) || finish.RevisionID != rev.ID {
				t.Fatalf("finish = %#v, revision %s", finish, rev.ID)
			}
		})
	}
}

func TestActivity_FailedGenerationsFinishAsFailed(t *testing.T) {
	ctx := context.Background()

	t.Run("generator error", func(t *testing.T) {
		store := newServiceTestStore(t)
		svc := NewService(store, &fakeGenerator{err: errors.New("model unavailable")})
		publisher := &recordingActivityPublisher{}
		svc.SetActivityPublisher(publisher)
		seedConfig(t, store, "hq")
		rev, _ := svc.RequestGenerationNow(ctx, "hq", "local", TriggerManual)
		finish := startAndFinish(t, publisher, "hq", TriggerManual)
		if finish.Outcome != "failed" || rev == nil || finish.RevisionID != rev.ID {
			t.Fatalf("a failed revision is still a revision: %#v", finish)
		}
	})

	t.Run("no generator", func(t *testing.T) {
		store := newServiceTestStore(t)
		svc := NewService(store, nil)
		publisher := &recordingActivityPublisher{}
		svc.SetActivityPublisher(publisher)
		seedConfig(t, store, "hq")
		_, _ = svc.RequestGenerationNow(ctx, "hq", "local", TriggerScheduled)
		finish := startAndFinish(t, publisher, "hq", TriggerScheduled)
		if finish.Outcome != "failed" || finish.RevisionID != "" {
			t.Fatalf("finish = %#v", finish)
		}
	})

	t.Run("store failure", func(t *testing.T) {
		base := newServiceTestStore(t)
		store := failingRevisionStore{SQLiteStore: base}
		svc := NewService(store, &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded}})
		publisher := &recordingActivityPublisher{}
		svc.SetActivityPublisher(publisher)
		seedConfig(t, base, "hq")
		if _, err := svc.RequestGenerationNow(ctx, "hq", "local", TriggerScheduled); err == nil {
			t.Fatal("expected the revision write to fail")
		}
		finish := startAndFinish(t, publisher, "hq", TriggerScheduled)
		if finish.Outcome != "failed" || finish.RevisionID != "" {
			t.Fatalf("an unsaved brief finished %#v", finish)
		}
	})
}

func TestActivity_NilPublisherIsSafe(t *testing.T) {
	store := newServiceTestStore(t)
	svc := NewService(store, &fakeGenerator{result: GenerationResult{Status: GenerationSucceeded}})
	seedConfig(t, store, "hq")
	if _, err := svc.RequestGenerationNow(context.Background(), "hq", "local", TriggerScheduled); err != nil {
		t.Fatalf("RequestGenerationNow: %v", err)
	}
}
