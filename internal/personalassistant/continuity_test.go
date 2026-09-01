package personalassistant

import (
	"context"
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/session"
)

type continuityBriefs struct {
	config     dailybrief.Config
	updateErr  error
	updateHits int
}

func (b *continuityBriefs) GetConfig(context.Context, string) (*dailybrief.Config, error) {
	copy := b.config
	copy.ScheduleDays = append([]string(nil), b.config.ScheduleDays...)
	copy.SelectedWorkspaceIDs = append([]string(nil), b.config.SelectedWorkspaceIDs...)
	return &copy, nil
}

func (b *continuityBriefs) UpdateConfig(_ context.Context, cfg dailybrief.Config) (*dailybrief.Config, error) {
	b.updateHits++
	if b.updateErr != nil {
		return nil, b.updateErr
	}
	normalized, err := dailybrief.NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	normalized.ConfigRevision = b.config.ConfigRevision + 1
	b.config = normalized
	return b.GetConfig(context.Background(), cfg.WorkspaceID)
}

func newContinuityFixture(t *testing.T) (*ContinuityService, *SQLiteStore, *continuityBriefs) {
	t.Helper()
	ctx := context.Background()
	store, _ := newTestStore(t)
	state := activeTestState("local", "assistant-a")
	state.FirstAssignmentStatus = FirstAssignmentCompleted
	if _, err := store.CreateState(ctx, state); err != nil {
		t.Fatal(err)
	}
	ws := &session.Workspace{
		ID: "hq-local", OwnerUserID: "local", FolderSlug: "personal-hq",
		AgentInstances: []session.AgentInstance{{ID: "instance-local", Name: "Ada", EntryPoint: true}},
	}
	hq := &fakeHQReader{status: &personalhq.Status{UserID: "local", WorkspaceID: ws.ID, Workspace: ws, Valid: true}}
	briefs := &continuityBriefs{config: dailybrief.Config{
		WorkspaceID: ws.ID, UserID: "local", Timezone: "UTC", ScheduleDays: []string{"mon", "tue"},
		ScheduleTime: "08:00", ScheduleEnabled: true, Scope: dailybrief.ScopeAll,
		IncludeFutureWorkspaces: true, ConfigRevision: 4,
	}}
	read := NewService(store, hq, briefs,
		fakeModelReader{availability: SourceAvailability{Available: true, Status: AvailabilityAvailable}})
	return NewContinuityService(store, hq, briefs, read), store, briefs
}

func TestContinuity_UpdateWorkingAgreementUsesCASAndCanonicalBriefConfig(t *testing.T) {
	service, store, briefs := newContinuityFixture(t)
	mandate := "Keep launches and commitments visible."
	focus := []string{"plan_my_day", "track_commitments_and_follow_ups"}
	tz, scheduleTime := "America/New_York", "09:15"
	days := []string{"mon", "wed", "fri"}
	notify := true
	projection, err := service.UpdateWorkingAgreement(context.Background(), "local", WorkingAgreementUpdate{
		IfVersion: 1, IfConfigRevision: 4, Mandate: &mandate, FocusAreas: &focus,
		Timezone: &tz, ScheduleDays: &days, ScheduleTime: &scheduleTime, NotifyOnReady: &notify,
	})
	if err != nil {
		t.Fatalf("UpdateWorkingAgreement: %v", err)
	}
	if projection.Mandate != mandate || projection.DailyBrief.Timezone != tz || projection.DailyBrief.ScheduleTime != scheduleTime || !projection.DailyBrief.NotifyOnReady {
		t.Fatalf("projection=%+v", projection)
	}
	if briefs.updateHits != 1 || briefs.config.ConfigRevision != 5 {
		t.Fatalf("canonical config not updated once: %+v", briefs)
	}
	persisted, _ := store.GetState(context.Background(), "local")
	if persisted.StateVersion != 2 || persisted.Mandate != mandate {
		t.Fatalf("relationship=%+v", persisted)
	}
	if _, err := service.UpdateWorkingAgreement(context.Background(), "local", WorkingAgreementUpdate{IfVersion: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error=%v", err)
	}
}

func TestContinuity_InvalidTimezoneAndPartialConfigFailureDoNotLeaveMandateChanged(t *testing.T) {
	service, store, briefs := newContinuityFixture(t)
	invalid := "Mars/Olympus"
	if _, err := service.UpdateWorkingAgreement(context.Background(), "local", WorkingAgreementUpdate{IfVersion: 1, Timezone: &invalid}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid timezone error=%v", err)
	}
	state, _ := store.GetState(context.Background(), "local")
	if state.StateVersion != 1 {
		t.Fatalf("invalid config mutated relationship: %+v", state)
	}

	briefs.updateErr = errors.New("brief store unavailable")
	mandate := "A new mandate"
	if _, err := service.UpdateWorkingAgreement(context.Background(), "local", WorkingAgreementUpdate{IfVersion: 1, Mandate: &mandate}); err == nil {
		t.Fatal("expected brief update failure")
	}
	state, _ = store.GetState(context.Background(), "local")
	if state.Mandate == mandate || state.Status != StatusActive {
		t.Fatalf("safe rollback did not restore relationship: %+v", state)
	}
}

func TestContinuity_PauseResumePreservesRoutineAndRecordsAcrossRestart(t *testing.T) {
	service, store, briefs := newContinuityFixture(t)
	paused, err := service.Pause(context.Background(), "local", 1)
	if err != nil || paused.State != APIStatePaused {
		t.Fatalf("pause=%+v err=%v", paused, err)
	}
	if !briefs.config.ScheduleEnabled || briefs.config.ConfigRevision != 4 || briefs.updateHits != 0 {
		t.Fatalf("pause changed canonical config: %+v", briefs.config)
	}
	persisted, _ := store.GetState(context.Background(), "local")
	if persisted.FirstAssignmentStatus != FirstAssignmentCompleted || persisted.AssistantID != "assistant-a" {
		t.Fatalf("pause changed relationship history: %+v", persisted)
	}
	resumed, err := service.Resume(context.Background(), "local", persisted.StateVersion)
	if err != nil || resumed.State != APIStateActive || !resumed.DailyBrief.ScheduleEnabled {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
}
