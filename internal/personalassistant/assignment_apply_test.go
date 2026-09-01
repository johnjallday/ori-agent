package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type assignmentApplyEnv struct {
	ctx       context.Context
	store     *SQLiteStore
	service   *AssignmentService
	workspace workspace.Store
	followUps *followup.Service
	brief     *dailybrief.Service
	hqID      string
	preview   *AssignmentPreviewResult
	request   AssignmentApplyRequest
}

func newAssignmentApplyEnv(t *testing.T) *assignmentApplyEnv {
	t.Helper()
	return newAssignmentApplyEnvWithRows(t, []AssignmentInputRow{
		{Type: AssignmentRowPriority, Title: "Review launch"},
		{Type: AssignmentRowIOwe, Title: "Send Maya the draft", Counterparty: "Maya", Due: "2026-10-12"},
	})
}

func newAssignmentApplyEnvWithRows(t *testing.T, rows []AssignmentInputRow) *assignmentApplyEnv {
	t.Helper()
	ctx := context.Background()
	store, db := newTestStore(t)
	workspaceStore, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspaceStore.Close() })
	hq := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Personal HQ"})
	if err := workspaceStore.Save(hq); err != nil {
		t.Fatal(err)
	}
	stateInput := activeTestState("local", "assistant-1")
	stateInput.HQWorkspaceID = hq.ID
	state, err := store.CreateState(ctx, stateInput)
	if err != nil {
		t.Fatal(err)
	}
	followUps := followup.NewService(followup.NewSQLiteStore(db))
	writer := NewCanonicalWriter(workspace.NewTicketService(workspaceStore))
	writer.SetFollowUpService(followUps)
	briefStore := dailybrief.NewSQLiteStore(db)
	briefService := dailybrief.NewService(briefStore, &dailybrief.Synthesizer{
		Resolver: func(ctx context.Context, _ dailybrief.GenerationRequest, cfg dailybrief.Config) (dailybrief.Snapshot, *dailybrief.Revision, error) {
			return dailybrief.BuildSnapshot(ctx, dailybrief.SnapshotSources{
				Workspaces: workspaceStore, FollowUps: followUps,
			}, cfg, "local", time.Now()), nil, nil
		},
	})
	if _, err := briefService.UpdateConfig(ctx, dailybrief.Config{
		WorkspaceID: hq.ID, UserID: "local", Timezone: "UTC",
		Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewAssignmentService(store)
	service.SetCanonicalWriter(writer)
	service.SetBriefService(briefService)
	preview, err := service.Preview(ctx, "local", state.StateVersion, AssignmentInput{Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	return &assignmentApplyEnv{
		ctx: ctx, store: store, service: service, workspace: workspaceStore,
		followUps: followUps, brief: briefService, hqID: hq.ID, preview: preview,
		request: AssignmentApplyRequest{
			PreviewID: preview.Preview.PreviewID, PreviewVersion: preview.Preview.AssignmentVersion,
			PayloadHash: preview.Preview.PayloadHash, IfVersion: preview.StateVersion,
			ApplyRequestID: "apply-request-1",
		},
	}
}

func TestAssignmentService_ApplyCreatesCanonicalRecordsExactlyOnce(t *testing.T) {
	env := newAssignmentApplyEnv(t)
	result, err := env.service.Apply(env.ctx, "local", env.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != AssignmentCompleted || result.AppliedCount != 2 || result.TotalCount != 2 || result.Retryable {
		t.Fatalf("result = %#v", result)
	}
	assignment, err := env.store.GetAssignment(env.ctx, "local", env.request.PreviewID)
	if err != nil || assignment.BriefRequestID == "" || assignment.BriefRevisionID == "" ||
		(assignment.BriefStatus != string(dailybrief.GenerationSucceeded) && assignment.BriefStatus != string(dailybrief.GenerationPartial)) {
		t.Fatalf("persisted brief lifecycle = %#v, %v", assignment, err)
	}
	state, err := env.store.GetState(env.ctx, "local")
	if err != nil || state.FirstAssignmentStatus != FirstAssignmentCompleted || state.StateVersion != result.StateVersion {
		t.Fatalf("state = %#v, %v", state, err)
	}
	saved, err := env.workspace.Get(env.hqID)
	if err != nil || len(saved.Tasks) != 1 || saved.Tasks[0].TicketState != workspace.TicketStateReady || !saved.Tasks[0].AwaitingExecutionIntent {
		t.Fatalf("tickets = %#v, %v", saved.Tasks, err)
	}
	followUps, err := env.followUps.List(env.ctx, followup.Filter{UserID: "local", WorkspaceID: env.hqID})
	if err != nil || len(followUps) != 1 {
		t.Fatalf("follow-ups = %#v, %v", followUps, err)
	}

	replayed, err := env.service.Apply(env.ctx, "local", env.request)
	if err != nil || replayed.Status != AssignmentCompleted || replayed.AssignmentVersion != result.AssignmentVersion {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	saved, err = env.workspace.Get(env.hqID)
	if err != nil || len(saved.Tasks) != 1 {
		t.Fatalf("replay duplicated ticket: %d, %v", len(saved.Tasks), err)
	}
}

func TestAssignmentService_ApplyRejectsChangedPayloadAndRequestBinding(t *testing.T) {
	env := newAssignmentApplyEnv(t)
	changed := env.request
	changed.PayloadHash = "different-payload-hash"
	if _, err := env.service.Apply(env.ctx, "local", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed payload error = %v", err)
	}
	assignment, err := env.store.GetAssignment(env.ctx, "local", env.request.PreviewID)
	if err != nil || assignment.Status != AssignmentPreviewed || assignment.ApplyRequestID != "" {
		t.Fatalf("changed payload mutated assignment: %#v, %v", assignment, err)
	}

	env.service.SetFaultInjector(func(stage string, _ int) error {
		if stage == AssignmentFaultBeforeComplete {
			return errors.New("stop before completion")
		}
		return nil
	})
	if _, err := env.service.Apply(env.ctx, "local", env.request); err == nil {
		t.Fatal("expected partial apply")
	}
	env.service.SetFaultInjector(nil)
	differentRequest := env.request
	differentRequest.ApplyRequestID = "different-request"
	if _, err := env.service.Apply(env.ctx, "local", differentRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebound request error = %v", err)
	}
}

func TestAssignmentService_ApplyResumesAcrossEveryDurableBoundaryAndRestart(t *testing.T) {
	tests := []struct {
		stage string
		index int
	}{
		{AssignmentFaultAfterCanonical, 0},
		{AssignmentFaultAfterRef, 0},
		{AssignmentFaultAfterCanonical, 1},
		{AssignmentFaultAfterRef, 1},
		{AssignmentFaultBeforeComplete, 2},
	}
	for _, test := range tests {
		t.Run(test.stage+string(rune('0'+test.index)), func(t *testing.T) {
			env := newAssignmentApplyEnv(t)
			fired := false
			env.service.SetFaultInjector(func(stage string, index int) error {
				if !fired && stage == test.stage && index == test.index {
					fired = true
					return errors.New("injected boundary fault")
				}
				return nil
			})
			_, err := env.service.Apply(env.ctx, "local", env.request)
			var partial *PartialAssignmentError
			if !errors.As(err, &partial) || !partial.Result.Retryable || partial.Result.Status != AssignmentApplying {
				t.Fatalf("partial = %#v, err=%v", partial, err)
			}
			current, err := env.service.Current(env.ctx, "local")
			if err != nil || current.Status != AssignmentApplying || current.ApplyRequestID != env.request.ApplyRequestID || current.Preview == nil {
				t.Fatalf("durable current = %#v, %v", current, err)
			}

			// Simulate restart: orchestration state comes only from durable stores.
			writer := NewCanonicalWriter(workspace.NewTicketService(env.workspace))
			writer.SetFollowUpService(env.followUps)
			restarted := NewAssignmentService(env.store)
			restarted.SetCanonicalWriter(writer)
			restarted.SetBriefService(env.brief)
			result, err := restarted.Apply(env.ctx, "local", env.request)
			if err != nil || result.Status != AssignmentCompleted || result.AppliedCount != 2 {
				t.Fatalf("restart result = %#v, %v", result, err)
			}
			saved, err := env.workspace.Get(env.hqID)
			if err != nil || len(saved.Tasks) != 1 {
				t.Fatalf("restart duplicated ticket: %d, %v", len(saved.Tasks), err)
			}
		})
	}
}

func TestAssignmentService_ApplyFinalStateConflictIsRetryable(t *testing.T) {
	env := newAssignmentApplyEnv(t)
	env.service.SetFaultInjector(func(stage string, _ int) error {
		if stage != AssignmentFaultBeforeComplete {
			return nil
		}
		state, err := env.store.GetState(env.ctx, "local")
		if err != nil {
			return err
		}
		state.DisplayName = "Still the same assistant"
		_, err = env.store.UpdateState(env.ctx, state, state.StateVersion)
		return err
	})
	_, err := env.service.Apply(env.ctx, "local", env.request)
	var partial *PartialAssignmentError
	if !errors.As(err, &partial) || partial.Result.AppliedCount != 2 {
		t.Fatalf("final conflict = %+v result=%+v, %v", partial, partial.Result, err)
	}
	env.service.SetFaultInjector(nil)
	result, err := env.service.Apply(env.ctx, "local", env.request)
	if err != nil || result.Status != AssignmentCompleted {
		t.Fatalf("final retry = %#v, %v", result, err)
	}
}

type failingAssignmentBrief struct{ cfg dailybrief.Config }

func (f failingAssignmentBrief) GetConfig(context.Context, string) (*dailybrief.Config, error) {
	cfg := f.cfg
	return &cfg, nil
}
func (f failingAssignmentBrief) PlanFirstAssignmentBrief(context.Context, string) (*dailybrief.Config, dailybrief.Trigger, error) {
	cfg := f.cfg
	return &cfg, dailybrief.TriggerFirstOpen, nil
}
func (f failingAssignmentBrief) GenerateFirstAssignmentBrief(_ context.Context, _ dailybrief.Config, _ string, trigger dailybrief.Trigger, requestID string) (*dailybrief.GenerationRequest, *dailybrief.Revision, error) {
	return &dailybrief.GenerationRequest{ID: requestID, Trigger: trigger, Status: dailybrief.GenerationFailed}, nil, errors.New("provider credential super-secret")
}
func (f failingAssignmentBrief) GetRevision(context.Context, string) (*dailybrief.Revision, error) {
	return nil, dailybrief.ErrRevisionNotFound
}

func TestAssignmentService_BriefFailureKeepsRecordsAndReturnsDistinctRetryState(t *testing.T) {
	env := newAssignmentApplyEnv(t)
	cfg, err := env.brief.GetConfig(env.ctx, env.hqID)
	if err != nil {
		t.Fatal(err)
	}
	env.service.SetBriefService(failingAssignmentBrief{cfg: *cfg})
	_, err = env.service.Apply(env.ctx, "local", env.request)
	var partial *PartialAssignmentError
	if !errors.As(err, &partial) || partial.Result.Outcome != "records_saved_brief_failed" ||
		partial.Result.AppliedCount != 2 || partial.Result.Brief == nil || partial.Result.Brief.Status != string(dailybrief.GenerationFailed) {
		t.Fatalf("brief failure result=%+v err=%v", partial, err)
	}
	saved, err := env.workspace.Get(env.hqID)
	if err != nil || len(saved.Tasks) != 1 {
		t.Fatalf("brief failure rolled back records: count=%d err=%v", len(saved.Tasks), err)
	}
	state, err := env.store.GetState(env.ctx, "local")
	if err != nil || state.FirstAssignmentStatus != FirstAssignmentApplying {
		t.Fatalf("brief failure state=%#v err=%v", state, err)
	}

	env.service.SetBriefService(env.brief)
	result, err := env.service.Apply(env.ctx, "local", env.request)
	if err != nil || result.Status != AssignmentCompleted || result.Brief == nil || result.Brief.RevisionID == "" {
		t.Fatalf("brief retry result=%+v err=%v", result, err)
	}
}

func TestAssignmentService_EmptyAssignmentGeneratesHonestDeterministicBrief(t *testing.T) {
	env := newAssignmentApplyEnvWithRows(t, nil)
	result, err := env.service.Apply(env.ctx, "local", env.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "complete_empty" || result.AppliedCount != 0 || result.TotalCount != 0 ||
		result.Brief == nil || result.Brief.Status != string(dailybrief.GenerationSucceeded) || len(result.Brief.TopItems) != 0 {
		t.Fatalf("empty result=%+v", result)
	}
	saved, err := env.workspace.Get(env.hqID)
	if err != nil || len(saved.Tasks) != 0 {
		t.Fatalf("empty assignment fabricated records: %#v err=%v", saved.Tasks, err)
	}
	revision, err := env.brief.GetRevision(env.ctx, result.Brief.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var content dailybrief.BriefContent
	if err := json.Unmarshal([]byte(revision.ContentJSON), &content); err != nil {
		t.Fatal(err)
	}
	if len(content.NeedsAttention) != 0 || len(content.TodaysPlan) != 0 || len(content.SinceLastBrief) != 0 {
		t.Fatalf("empty brief fabricated priorities: %#v", content)
	}
}

func TestFirstValueCheckpoint_NoIntegrationsNoModelEditRetryAndGroundedBrief(t *testing.T) {
	rows := []AssignmentInputRow{
		{Type: AssignmentRowPriority, Title: "Original priority"},
		{Type: AssignmentRowIOwe, Title: "Send the signed form", Counterparty: "Maya", Due: "2000-01-01"},
		{Type: AssignmentRowWaitingOn, Title: "Receive design approval", Counterparty: "Lee", Due: "2000-01-01"},
		{Type: AssignmentRowFixedCommitment, Title: "Planning call", Action: "Prepare call notes", Due: "2000-01-01"},
		{Type: AssignmentRowFixedCommitment, Title: "Time-only appointment", Due: "2000-01-01"},
	}
	env := newAssignmentApplyEnvWithRows(t, rows)
	rows[0].Title = "Edited priority"
	edited, err := env.service.Preview(env.ctx, "local", env.preview.StateVersion, AssignmentInput{Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Preview.PreviewID == env.preview.Preview.PreviewID || edited.Preview.AssignmentVersion <= env.preview.Preview.AssignmentVersion {
		t.Fatalf("edited preview did not replace original: old=%#v new=%#v", env.preview.Preview, edited.Preview)
	}
	env.preview = edited
	env.request = AssignmentApplyRequest{
		PreviewID: edited.Preview.PreviewID, PreviewVersion: edited.Preview.AssignmentVersion,
		PayloadHash: edited.Preview.PayloadHash, IfVersion: edited.StateVersion,
		ApplyRequestID: "checkpoint-apply",
	}
	env.service.SetFaultInjector(func(stage string, index int) error {
		if stage == AssignmentFaultAfterRef && index == 2 {
			return errors.New("simulated reload")
		}
		return nil
	})
	if _, err := env.service.Apply(env.ctx, "local", env.request); err == nil {
		t.Fatal("expected durable partial checkpoint")
	}

	writer := NewCanonicalWriter(workspace.NewTicketService(env.workspace))
	writer.SetFollowUpService(env.followUps)
	restarted := NewAssignmentService(env.store)
	restarted.SetCanonicalWriter(writer)
	restarted.SetBriefService(env.brief)
	result, err := restarted.Apply(env.ctx, "local", env.request)
	if err != nil || result.Status != AssignmentCompleted || result.AppliedCount != 5 {
		t.Fatalf("checkpoint retry result=%+v err=%v", result, err)
	}
	saved, err := env.workspace.Get(env.hqID)
	if err != nil || len(saved.Tasks) != 2 || len(saved.AgentInstances) != 0 {
		t.Fatalf("checkpoint HQ tasks=%d agents=%d err=%v", len(saved.Tasks), len(saved.AgentInstances), err)
	}
	if saved.Tasks[0].Description != "Edited priority" && saved.Tasks[1].Description != "Edited priority" {
		t.Fatalf("edited canonical payload was not applied: %#v", saved.Tasks)
	}
	followUps, err := env.followUps.List(env.ctx, followup.Filter{UserID: "local", WorkspaceID: env.hqID})
	if err != nil || len(followUps) != 3 {
		t.Fatalf("checkpoint follow-ups=%#v err=%v", followUps, err)
	}
	followUpIDs := map[string]bool{}
	for _, item := range followUps {
		followUpIDs[item.ID] = true
	}
	revision, err := env.brief.GetRevision(env.ctx, result.Brief.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var content dailybrief.BriefContent
	if err := json.Unmarshal([]byte(revision.ContentJSON), &content); err != nil {
		t.Fatal(err)
	}
	groundedFollowUps := 0
	for _, item := range content.NeedsAttention {
		if item.Ref.EntityType == "follow_up" {
			groundedFollowUps++
			if !followUpIDs[item.Ref.EntityID] {
				t.Fatalf("brief used unknown follow-up ref: %#v", item.Ref)
			}
		}
	}
	if groundedFollowUps == 0 {
		t.Fatalf("first brief omitted due/stale follow-up refs: %#v", content)
	}
	if replay, err := restarted.Apply(env.ctx, "local", env.request); err != nil || replay.AppliedCount != 5 {
		t.Fatalf("checkpoint replay=%+v err=%v", replay, err)
	}
	saved, _ = env.workspace.Get(env.hqID)
	followUps, _ = env.followUps.List(env.ctx, followup.Filter{UserID: "local", WorkspaceID: env.hqID})
	if len(saved.Tasks) != 2 || len(followUps) != 3 {
		t.Fatalf("checkpoint replay duplicated records: tickets=%d followups=%d", len(saved.Tasks), len(followUps))
	}
}

func TestAssignmentService_ConcurrentApplyConverges(t *testing.T) {
	env := newAssignmentApplyEnv(t)
	const attempts = 8
	results := make(chan *AssignmentApplyResult, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := env.service.Apply(env.ctx, "local", env.request)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Apply: %v", err)
	}
	count := 0
	for result := range results {
		count++
		if result.Status != AssignmentCompleted || result.AppliedCount != 2 {
			t.Fatalf("concurrent result = %#v", result)
		}
	}
	if count != attempts {
		t.Fatalf("results = %d, want %d", count, attempts)
	}
	saved, err := env.workspace.Get(env.hqID)
	if err != nil || len(saved.Tasks) != 1 {
		t.Fatalf("concurrent tickets = %d, %v", len(saved.Tasks), err)
	}
}
