package progression

import (
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
	// (tool-task, unattended-run) have no Satisfied predicate and remain open.
	st := e.Status()
	if st.CompletedCount != st.TotalCount-2 {
		t.Fatalf("expected all but the 2 live-only quests complete, got %d/%d", st.CompletedCount, st.TotalCount)
	}
	if completed(e, "t4-tool-task") || completed(e, "t5-unattended-run") {
		t.Fatal("live-only quests must not be grandfathered")
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
