package personalassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/backuprecovery"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestWorkspaceProtectionIsStateThatNoDeclineHides(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	relationship := baseTodayProjection()
	relationship.DailyBrief = &BriefConfigProjection{Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true}
	digest := newFolderDigestFixture(t)
	service := NewTodayService(stubTodayRelationship{projection: relationship}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.now = func() time.Time { return now }
	service.SetBackupCoverage(&todayTestCoverage{state: backuprecovery.CoverageNotCovered}, digest.service)
	saveAdoptedTodayWorkspace(t, store, "song-1", "Song 1")

	fact, err := service.WorkspaceProtection(context.Background(), "local", "song-1")
	if err != nil || fact == nil || fact.Sentence != "Not in any backup Ori observes" || fact.Route != backupChooserRoute || fact.RouteLabel != "Check protection" {
		t.Fatal(fact, err)
	}
	if err := service.DeclineBackupCoverage(context.Background(), "local"); err != nil {
		t.Fatal(err)
	}
	if after, err := service.WorkspaceProtection(context.Background(), "local", "song-1"); err != nil || after == nil || after.State != backuprecovery.CoverageNotCovered {
		t.Fatal("a decline hid the workspace fact", after, err)
	}
	if none, err := service.WorkspaceProtection(context.Background(), "local", "hq-1"); err != nil || none != nil {
		t.Fatal("a workspace without an adopted folder got a fact", none, err)
	}
}

func TestWorkspaceProtectionHidesOtherOwnersAndMissingWorkspaces(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	store, _ := newTodayWorkspace(t, now)
	service := NewTodayService(stubTodayRelationship{projection: baseTodayProjection()}, stubTodayBrief{}, store, stubTodayFollowUps{})
	service.SetBackupCoverage(&todayTestCoverage{state: backuprecovery.CoverageNotCovered}, failingCoverageDeclines{})
	saveAdoptedTodayWorkspace(t, store, "theirs", "Their Song")
	theirs, _ := store.Get("theirs")
	theirs.OwnerUserID = "someone-else"
	if err := store.Save(theirs); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"theirs", "missing", ""} {
		if fact, err := service.WorkspaceProtection(context.Background(), "local", id); !errors.Is(err, ErrProtectionWorkspaceNotFound) || fact != nil {
			t.Fatal(id, fact, err)
		}
	}
	// Before any brief scope is saved, the fact still reads every owned workspace.
	saveAdoptedTodayWorkspace(t, store, "mine", "My Song")
	if fact, err := service.WorkspaceProtection(context.Background(), "local", "mine"); err != nil || fact == nil || fact.State != backuprecovery.CoverageNotCovered {
		t.Fatal(fact, err)
	}
	unwired := NewTodayService(stubTodayRelationship{}, stubTodayBrief{}, workspace.NewInMemoryStore(), stubTodayFollowUps{})
	if _, err := unwired.WorkspaceProtection(context.Background(), "local", "mine"); !errors.Is(err, ErrRepairNeeded) {
		t.Fatal(err)
	}
}
