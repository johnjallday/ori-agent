package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

type fakeJanitorActions struct {
	actions  map[string][]filejanitor.FileAction
	failing  map[string]bool
	roots    map[string]string
	statuses int
}

func (f *fakeJanitorActions) ListActions(workspaceID string) ([]filejanitor.FileAction, error) {
	if f.failing[workspaceID] {
		return nil, errors.New("journal unreadable")
	}
	return f.actions[workspaceID], nil
}

func (f *fakeJanitorActions) Status(workspaceID string) (filejanitor.Status, error) {
	f.statuses++
	return filejanitor.Status{Settings: filejanitor.JanitorSettings{RootPath: f.roots[workspaceID]}}, nil
}

func applied(id string, op filejanitor.Operation, at time.Time) filejanitor.FileAction {
	return filejanitor.FileAction{ID: id, Operation: op, Result: filejanitor.ResultApplied, ApprovedAt: at, CompletedAt: at}
}

func TestSummarizeJanitorActions_CountsRecentAppliedNotUndone(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	undone := applied("undone", filejanitor.OperationMove, now.Add(-time.Minute))
	undone.UndoneAt = now
	failed := applied("failed", filejanitor.OperationMove, now.Add(-time.Minute))
	failed.Result = filejanitor.ResultFailed

	result, recent := summarizeJanitorActions([]filejanitor.FileAction{
		applied("move-new", filejanitor.OperationMove, now.Add(-time.Hour)),
		applied("move-old", filejanitor.OperationMove, now.Add(-2*time.Hour)),
		applied("trash", filejanitor.OperationTrash, now.Add(-3*time.Hour)),
		applied("yesterday", filejanitor.OperationMove, now.Add(-25*time.Hour)),
		undone, failed,
	}, since)
	if !recent || result.Moved != 2 || result.Trashed != 1 || result.NewestActionID != "move-new" ||
		!result.NewestAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("summary = %+v recent=%t", result, recent)
	}

	if _, recent := summarizeJanitorActions([]filejanitor.FileAction{
		applied("yesterday", filejanitor.OperationMove, now.Add(-25*time.Hour)), undone, failed,
	}, since); recent {
		t.Fatal("nothing recent and applied still reported a result")
	}
}

func TestTodayJanitorResults_ReadsEachOwnedJanitorWorkspace(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s := newStarterStore(t)
	downloads := s.add("ws-downloads", "Tidy Downloads", "file-janitor", "", nil)
	desktop := s.add("ws-desktop", "Tidy Desktop", "downloads-janitor", "", nil)
	quiet := s.add("ws-quiet", "Quiet Janitor", "file-janitor", "", nil)
	broken := s.add("ws-broken", "Broken Janitor", "file-janitor", "", nil)
	s.add("ws-email", "Email Ops", "email-ops", "", nil)
	s.add("ws-foreign", "Their Janitor", "file-janitor", "another-user", nil)

	source := &fakeJanitorActions{
		actions: map[string][]filejanitor.FileAction{
			downloads.ID: {applied("d1", filejanitor.OperationMove, now.Add(-time.Hour))},
			desktop.ID:   {applied("k1", filejanitor.OperationTrash, now.Add(-2*time.Hour))},
			quiet.ID:     {applied("q1", filejanitor.OperationMove, now.Add(-48*time.Hour))},
			"ws-email":   {applied("e1", filejanitor.OperationMove, now.Add(-time.Hour))},
			"ws-foreign": {applied("f1", filejanitor.OperationMove, now.Add(-time.Hour))},
		},
		failing: map[string]bool{broken.ID: true},
		roots: map[string]string{
			downloads.ID: "/Users/someone/Downloads/", desktop.ID: "/Users/someone/Desktop",
		},
	}
	reader := todayJanitorResults{workspaces: s.store, janitor: source, now: func() time.Time { return now }}

	results, err := reader.JanitorResults(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, result := range results {
		byID[result.WorkspaceID] = i
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want the two recent owned janitors", results)
	}
	got := results[byID[downloads.ID]]
	if got.WorkspaceName != "Tidy Downloads" || got.Slug != downloads.FolderSlug || got.FolderName != "Downloads" ||
		got.Moved != 1 || got.NewestActionID != "d1" {
		t.Fatalf("downloads result = %+v", got)
	}
	if other := results[byID[desktop.ID]]; other.FolderName != "Desktop" || other.Trashed != 1 {
		t.Fatalf("retired downloads-janitor result = %+v", other)
	}
	// Settings are read only for a workspace with something to report.
	if source.statuses != 2 {
		t.Fatalf("settings reads = %d, want 2", source.statuses)
	}

	if results, err := (todayJanitorResults{}).JanitorResults(context.Background(), "local"); err != nil || results != nil {
		t.Fatalf("an unwired reader returned %+v, %v", results, err)
	}
}

type fakeCurrentBrief struct {
	revision *dailybrief.Revision
	err      error
	asked    []string
}

func (f *fakeCurrentBrief) GetCurrent(_ context.Context, workspaceID string) (*dailybrief.Revision, error) {
	f.asked = append(f.asked, workspaceID)
	return f.revision, f.err
}

func TestBriefRevisionExists(t *testing.T) {
	withBrief := &fakeCurrentBrief{revision: &dailybrief.Revision{ID: "brief-1"}}
	if !briefRevisionExists(withBrief, "hq-1") || len(withBrief.asked) != 1 || withBrief.asked[0] != "hq-1" {
		t.Fatalf("an existing brief was not found: asked %v", withBrief.asked)
	}
	if briefRevisionExists(&fakeCurrentBrief{err: dailybrief.ErrRevisionNotFound}, "hq-1") {
		t.Fatal("no revision counted as a brief")
	}
	noHQ := &fakeCurrentBrief{revision: &dailybrief.Revision{ID: "brief-1"}}
	if briefRevisionExists(noHQ, "") || len(noHQ.asked) != 0 {
		t.Fatal("a missing HQ still read a brief")
	}
	if briefRevisionExists(nil, "hq-1") {
		t.Fatal("a nil reader reported a brief")
	}
}
