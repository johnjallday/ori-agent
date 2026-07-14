package progression

import (
	"errors"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
	ws "github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeStore is an in-memory StateStore for tests.
type fakeStore struct {
	state  types.ProgressionState
	writes int
}

func (f *fakeStore) GetProgression() types.ProgressionState { return f.state }

func (f *fakeStore) SetProgression(p types.ProgressionState) error {
	f.state = p
	f.writes++
	return nil
}

func completed(e *Engine, id string) bool {
	for _, tv := range e.Status().Tiers {
		for _, q := range tv.Quests {
			if q.ID == id {
				return q.Status == StatusCompleted
			}
		}
	}
	return false
}

func TestHandleEvent_CompletesMatchingQuest(t *testing.T) {
	e := New(&fakeStore{})

	if completed(e, "t1-first-message") {
		t.Fatal("quest should start incomplete")
	}
	e.HandleEvent(ws.Event{Type: ws.EventMessageSent})
	if !completed(e, "t1-first-message") {
		t.Fatal("message.sent should complete t1-first-message")
	}
}

func TestHandleEvent_Idempotent(t *testing.T) {
	store := &fakeStore{}
	fires := 0
	e := New(store, WithOnComplete(func(Quest) { fires++ }))

	for i := 0; i < 3; i++ {
		e.HandleEvent(ws.Event{Type: ws.EventWorkspaceCreated})
	}
	if fires != 1 {
		t.Fatalf("onComplete should fire exactly once, got %d", fires)
	}
	if !completed(e, "t2-create-workspace") {
		t.Fatal("workspace.created should complete t2-create-workspace")
	}
}

func TestHandleEvent_JumpAheadGrantsCredit(t *testing.T) {
	e := New(&fakeStore{})

	// User skips ahead to a Tier-5 action while still on Tier 1.
	e.HandleEvent(ws.Event{Type: ws.EventScheduledTaskTriggered})
	if !completed(e, "t5-unattended-run") {
		t.Fatal("detection must run for all tiers, not just the current one")
	}
	// Current tier stays 1 because Tier 1 is still incomplete.
	if got := e.Status().CurrentTier; got != 1 {
		t.Fatalf("current tier = %d, want 1", got)
	}
}

func TestAgentTaskCompleteRequiresAgent(t *testing.T) {
	e := New(&fakeStore{})

	// A task completing with no agent must NOT satisfy the agent-task quest.
	e.HandleEvent(ws.Event{Type: ws.EventTaskCompleted, Data: map[string]any{}})
	if completed(e, "t3-agent-task-done") {
		t.Fatal("task.completed without an agent should not complete the agent quest")
	}
	e.HandleEvent(ws.Event{Type: ws.EventTaskCompleted, Data: map[string]any{"agent": "researcher"}})
	if !completed(e, "t3-agent-task-done") {
		t.Fatal("task.completed with an agent should complete the agent quest")
	}
}

func TestHandleEvent_NoteCreatedCompletesQuest(t *testing.T) {
	e := New(&fakeStore{})
	e.HandleEvent(ws.Event{Type: ws.EventNoteCreated, WorkspaceID: "w1"})
	if !completed(e, "t2-create-note") {
		t.Fatal("note.created should complete t2-create-note")
	}
}

func TestHandleEvent_SkillAndMCPBindings(t *testing.T) {
	e := New(&fakeStore{})

	// A generic workspace.updated must NOT complete the equip quests.
	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceUpdated, Data: map[string]any{"action": "renamed"}})
	if completed(e, "t4-enable-skill") || completed(e, "t4-connect-mcp") {
		t.Fatal("unrelated workspace.updated should not complete equip quests")
	}

	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceUpdated, Data: map[string]any{"action": "skill_binding_created"}})
	if !completed(e, "t4-enable-skill") {
		t.Fatal("skill_binding_created action should complete t4-enable-skill")
	}

	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceUpdated, Data: map[string]any{"action": "mcp_binding_created"}})
	if !completed(e, "t4-connect-mcp") {
		t.Fatal("mcp_binding_created action should complete t4-connect-mcp")
	}
}

func TestComplete_DirectAndIdempotent(t *testing.T) {
	e := New(&fakeStore{})

	if !e.Complete("t1-personalize") {
		t.Fatal("first Complete should report newly-completed")
	}
	if e.Complete("t1-personalize") {
		t.Fatal("second Complete should be a no-op")
	}
	if e.Complete("does-not-exist") {
		t.Fatal("unknown quest should not complete")
	}
}

func TestBackfill_FreshInstall_CompletesNothing(t *testing.T) {
	store := &fakeStore{}
	e := New(store)

	if err := e.Backfill(ScannerFunc(func() Snapshot { return Snapshot{} })); err != nil {
		t.Fatal(err)
	}
	if got := e.Status().CompletedCount; got != 0 {
		t.Fatalf("fresh install should backfill 0 quests, got %d", got)
	}
	if e.Status().CurrentTier != 1 {
		t.Fatal("fresh install should be on Tier 1")
	}
}

func TestBackfill_EstablishedInstall_GrandfathersSilently(t *testing.T) {
	store := &fakeStore{}
	fires := 0
	e := New(store, WithOnComplete(func(Quest) { fires++ }))

	snap := Snapshot{
		Workspaces:        2,
		Notes:             1,
		TasksStarted:      3,
		Agents:            2,
		AgentTasksDone:    1,
		SkillsBound:       1,
		MCPServers:        1,
		Triggers:          1,
		OrchestrationRuns: 1,
		MemoryWrites:      1,
		Personalized:      true,
	}
	if err := e.Backfill(ScannerFunc(func() Snapshot { return snap })); err != nil {
		t.Fatal(err)
	}

	// Backfill must be silent — no toasts for established users.
	if fires != 0 {
		t.Fatalf("backfill must not fire onComplete, got %d", fires)
	}
	// Every backfillable quest should be complete; the two live-only quests
	// (tool-task, unattended-run) have no Satisfied predicate and remain
	// open, and the optional Personal HQ quest's Satisfied predicate is
	// false because this snapshot has no HasPersonalHQ.
	st := e.Status()
	if st.CompletedCount != st.TotalCount-3 {
		t.Fatalf("expected all but the 2 live-only quests and the unmet Personal HQ quest complete, got %d/%d", st.CompletedCount, st.TotalCount)
	}
	if completed(e, "t4-tool-task") || completed(e, "t5-unattended-run") || completed(e, "t2-build-hq") {
		t.Fatal("live-only and unmet-Personal-HQ quests must not be grandfathered")
	}
}

func TestBackfill_RunsOnce(t *testing.T) {
	store := &fakeStore{}
	e := New(store)

	if err := e.Backfill(ScannerFunc(func() Snapshot { return Snapshot{Workspaces: 1} })); err != nil {
		t.Fatal(err)
	}
	// A second scan with richer state must be ignored (backfill is one-shot).
	if err := e.Backfill(ScannerFunc(func() Snapshot { return Snapshot{Agents: 5, Workspaces: 5} })); err != nil {
		t.Fatal(err)
	}
	if completed(e, "t3-second-agent") {
		t.Fatal("second backfill should be a no-op after BackfilledAt is set")
	}
}

func TestReset_SurvivesBackfill(t *testing.T) {
	store := &fakeStore{}
	e := New(store)
	e.HandleEvent(ws.Event{Type: ws.EventMessageSent})
	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	if e.Status().CompletedCount != 0 {
		t.Fatal("reset should clear completions")
	}

	// A later backfill (e.g. on the next restart) must NOT re-grandfather from
	// existing state — the explicit reset is a persistent blank slate.
	if err := e.Backfill(ScannerFunc(func() Snapshot { return Snapshot{Workspaces: 3, Personalized: true} })); err != nil {
		t.Fatal(err)
	}
	if got := e.Status().CompletedCount; got != 0 {
		t.Fatalf("backfill after reset should complete nothing, got %d", got)
	}
}

func TestState_PersistsAndReloads(t *testing.T) {
	store := &fakeStore{}
	e1 := New(store)
	e1.HandleEvent(ws.Event{Type: ws.EventMessageSent})

	// A new engine over the same store should see the completion.
	e2 := New(store)
	if !completed(e2, "t1-first-message") {
		t.Fatal("completion should persist across engine instances")
	}
}

func TestStatus_TierCompletionAndProgress(t *testing.T) {
	e := New(&fakeStore{})
	// Complete all of Tier 1.
	e.Complete("t1-first-message")
	e.Complete("t1-personalize")

	st := e.Status()
	if st.CurrentTier != 2 {
		t.Fatalf("current tier should advance to 2, got %d", st.CurrentTier)
	}
	if st.Tiers[0].Tier != 1 || !st.Tiers[0].Complete {
		t.Fatal("tier 1 should be marked complete")
	}
	if st.NextQuest == nil || st.NextQuest.Tier != 2 {
		t.Fatalf("next quest should be in tier 2, got %+v", st.NextQuest)
	}
}

func TestDismissPersists(t *testing.T) {
	store := &fakeStore{}
	e := New(store)
	if err := e.SetDismissed(true); err != nil {
		t.Fatal(err)
	}
	if !New(store).Status().Dismissed {
		t.Fatal("dismissed flag should persist")
	}
}

// questView returns the QuestView for id, or nil.
func questView(e *Engine, id string) *QuestView {
	for _, tv := range e.Status().Tiers {
		for i := range tv.Quests {
			if tv.Quests[i].ID == id {
				return &tv.Quests[i]
			}
		}
	}
	return nil
}

func TestSkip_RejectsUnknownQuest(t *testing.T) {
	e := New(&fakeStore{})
	if err := e.Skip("does-not-exist"); !errors.Is(err, ErrQuestNotFound) {
		t.Fatalf("expected ErrQuestNotFound, got %v", err)
	}
}

func TestSkip_RejectsNonOptionalQuest(t *testing.T) {
	e := New(&fakeStore{})
	if err := e.Skip("t1-first-message"); !errors.Is(err, ErrQuestNotOptional) {
		t.Fatalf("expected ErrQuestNotOptional, got %v", err)
	}
}

func TestSkip_MarksSkippedAndIsIdempotent(t *testing.T) {
	e := New(&fakeStore{})
	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatalf("Skip: %v", err)
	}
	qv := questView(e, "t2-build-hq")
	if qv == nil || qv.Status != StatusSkipped || qv.SkippedAt == nil {
		t.Fatalf("expected skipped status with SkippedAt set, got %+v", qv)
	}
	firstSkippedAt := *qv.SkippedAt

	// Skipping again must not error and must not disturb the timestamp.
	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatalf("second Skip should be a no-op success, got %v", err)
	}
	qv = questView(e, "t2-build-hq")
	if qv.SkippedAt == nil || !qv.SkippedAt.Equal(firstSkippedAt) {
		t.Fatalf("re-skipping should not change SkippedAt: before=%v after=%v", firstSkippedAt, qv.SkippedAt)
	}
}

func TestSkip_NeverFiresOnComplete(t *testing.T) {
	fires := 0
	e := New(&fakeStore{}, WithOnComplete(func(Quest) { fires++ }))
	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatal(err)
	}
	if fires != 0 {
		t.Fatalf("Skip must never fire onComplete, got %d calls", fires)
	}
}

func TestSkip_DoesNotIncreaseCompletedCountButAdvancesResolvedCount(t *testing.T) {
	e := New(&fakeStore{})
	before := e.Status()
	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatal(err)
	}
	after := e.Status()

	if after.CompletedCount != before.CompletedCount {
		t.Fatalf("skip must not change CompletedCount: before=%d after=%d", before.CompletedCount, after.CompletedCount)
	}
	if after.ResolvedCount != before.ResolvedCount+1 {
		t.Fatalf("skip must advance ResolvedCount by 1: before=%d after=%d", before.ResolvedCount, after.ResolvedCount)
	}
}

// TestSkip_AdvancesCurrentTierPastLockedTier covers the core non-gating
// requirement: skipping the optional Personal HQ quest must not keep tier 3
// (or the widget's locked-tier display) stuck behind it once every other
// tier-2 quest resolves.
func TestSkip_AdvancesCurrentTierPastLockedTier(t *testing.T) {
	e := New(&fakeStore{})
	e.Complete("t1-first-message")
	e.Complete("t1-personalize")
	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceCreated}) // t2-create-workspace
	e.HandleEvent(ws.Event{Type: ws.EventNoteCreated})      // t2-create-note
	e.HandleEvent(ws.Event{Type: ws.EventTaskStarted})      // t2-run-task

	// Before skipping, tier 2 is not yet complete and tier 3 is locked.
	st := e.Status()
	if st.CurrentTier != 2 {
		t.Fatalf("precondition: expected current tier 2 before skip, got %d", st.CurrentTier)
	}
	if q := questView(e, "t3-second-agent"); q == nil || q.Status != StatusLocked {
		t.Fatalf("precondition: tier 3 quest should be locked, got %+v", q)
	}

	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatal(err)
	}

	st = e.Status()
	if st.CurrentTier != 3 {
		t.Fatalf("expected current tier to advance to 3 after skip, got %d", st.CurrentTier)
	}
	var tier2 *TierView
	for i := range st.Tiers {
		if st.Tiers[i].Tier == 2 {
			tier2 = &st.Tiers[i]
		}
	}
	if tier2 == nil || !tier2.Complete {
		t.Fatalf("tier 2 should be marked complete once its optional quest is skipped, got %+v", tier2)
	}
	if q := questView(e, "t3-second-agent"); q == nil || q.Status != StatusAvailable {
		t.Fatalf("tier 3 quest should now be available (unlocked), got %+v", q)
	}
}

// TestSkip_LaterCompletionReplacesSkip covers requirement 3.5: a later real
// completion must replace a skip outcome, remove it from SkippedQuests, and
// fire the completion callback for that newly observed action.
func TestSkip_LaterCompletionReplacesSkip(t *testing.T) {
	fires := 0
	e := New(&fakeStore{}, WithOnComplete(func(Quest) { fires++ }))

	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatal(err)
	}
	if fires != 0 {
		t.Fatalf("skip must not fire onComplete, got %d", fires)
	}

	if ok := e.Complete("t2-build-hq"); !ok {
		t.Fatal("expected Complete to report a newly observed completion")
	}
	if fires != 1 {
		t.Fatalf("the later real completion should fire onComplete exactly once, got %d", fires)
	}

	qv := questView(e, "t2-build-hq")
	if qv == nil || qv.Status != StatusCompleted || qv.CompletedAt == nil {
		t.Fatalf("expected completed status after later completion, got %+v", qv)
	}
	if qv.SkippedAt != nil {
		t.Fatalf("SkippedAt must be cleared once the quest is actually completed, got %v", qv.SkippedAt)
	}
}

// TestSkip_NoOpWhenAlreadyCompleted covers the reverse direction: a real
// completion must never be downgraded back to skipped.
func TestSkip_NoOpWhenAlreadyCompleted(t *testing.T) {
	e := New(&fakeStore{})
	e.Complete("t2-build-hq")

	if err := e.Skip("t2-build-hq"); err != nil {
		t.Fatalf("Skip on an already-completed quest should be a no-op success, got %v", err)
	}
	qv := questView(e, "t2-build-hq")
	if qv == nil || qv.Status != StatusCompleted {
		t.Fatalf("quest must remain completed, not regress to skipped: %+v", qv)
	}
}

// TestBuildHQQuestDoesNotAffectCreateWorkspaceQuest covers requirement 3.6:
// the new optional Personal HQ quest must be independent of the existing
// t2-create-workspace quest in both directions.
func TestBuildHQQuestDoesNotAffectCreateWorkspaceQuest(t *testing.T) {
	e := New(&fakeStore{})

	// A normal workspace-created event still only completes
	// t2-create-workspace, matching FR48 (new-HQ creation goes through this
	// same event and separately calls Complete("t2-build-hq"), which callers
	// wire outside the engine).
	e.HandleEvent(ws.Event{Type: ws.EventWorkspaceCreated})
	if !completed(e, "t2-create-workspace") {
		t.Fatal("workspace.created should still complete t2-create-workspace")
	}
	if completed(e, "t2-build-hq") {
		t.Fatal("workspace.created must not implicitly complete t2-build-hq")
	}

	// Completing the HQ objective directly (as the designate-existing-
	// workspace path will do, FR49) must not replay t2-create-workspace.
	e2 := New(&fakeStore{})
	e2.Complete("t2-build-hq")
	if completed(e2, "t2-create-workspace") {
		t.Fatal("completing t2-build-hq directly must not complete t2-create-workspace")
	}
}

// TestAllComplete_TreatsSkipAsResolved proves a skipped optional quest can
// still reach the "all complete" celebratory state without ever being
// recorded as an actual completion.
func TestAllComplete_TreatsSkipAsResolved(t *testing.T) {
	e := New(&fakeStore{})
	for _, q := range BuiltinQuests() {
		if q.Optional {
			if err := e.Skip(q.ID); err != nil {
				t.Fatalf("Skip(%s): %v", q.ID, err)
			}
			continue
		}
		e.Complete(q.ID)
	}

	st := e.Status()
	if !st.AllComplete {
		t.Fatalf("expected AllComplete once every quest is completed or skipped, got %+v", st)
	}
	if st.ResolvedCount != st.TotalCount {
		t.Fatalf("ResolvedCount should equal TotalCount, got %d/%d", st.ResolvedCount, st.TotalCount)
	}
	if st.CompletedCount >= st.TotalCount {
		t.Fatalf("CompletedCount must stay below TotalCount when a quest was skipped, got %d/%d", st.CompletedCount, st.TotalCount)
	}
}

// TestSkip_PersistsAcrossRestart covers requirement 3.2/3.9: a skip must
// survive an engine reload from the same store, distinctly from completion.
func TestSkip_PersistsAcrossRestart(t *testing.T) {
	store := &fakeStore{}
	e1 := New(store)
	if err := e1.Skip("t2-build-hq"); err != nil {
		t.Fatal(err)
	}

	e2 := New(store)
	qv := questView(e2, "t2-build-hq")
	if qv == nil || qv.Status != StatusSkipped {
		t.Fatalf("skip should survive reload from the same store, got %+v", qv)
	}
}
