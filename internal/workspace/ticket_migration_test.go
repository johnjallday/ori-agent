package workspace

import (
	"strings"
	"testing"
	"time"
)

// These tests run against real user data in production, so they assert the
// three things that matter most: nothing is lost, nothing is guessed at
// silently, and running twice changes nothing.

func migrationFixtureWorkspace(tasks ...Task) *Workspace {
	ws := NewWorkspace(CreateWorkspaceParams{Name: "Legacy"})
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range tasks {
		if tasks[i].ID == "" {
			tasks[i].ID = "task-" + string(rune('a'+i))
		}
		if tasks[i].CreatedAt.IsZero() {
			tasks[i].CreatedAt = base.Add(time.Duration(i) * time.Minute)
		}
		tasks[i].WorkspaceID = ws.ID
	}
	ws.Tasks = tasks
	return ws
}

func findTask(t *testing.T, ws *Workspace, id string) *Task {
	t.Helper()
	for i := range ws.Tasks {
		if ws.Tasks[i].ID == id {
			return &ws.Tasks[i]
		}
	}
	t.Fatalf("task %s missing after migration — records must never be dropped", id)
	return nil
}

// FR-107 through FR-112: every legacy status maps to a defensible canonical
// state.
func TestMigrateWorkspaceTickets_StateMapping(t *testing.T) {
	cases := []struct {
		name   string
		task   Task
		want   TicketState
		legacy TaskStatus
	}{
		{"backlog stays backlog", Task{ID: "t1", Status: TaskStatusBacklog, Description: "idea"}, TicketStateBacklog, TaskStatusBacklog},
		{"pending becomes ready", Task{ID: "t2", Status: TaskStatusPending}, TicketStateReady, TaskStatusPending},
		// FR-108: assignment does not imply the work started.
		{"assigned becomes ready", Task{ID: "t3", Status: TaskStatusAssigned, To: "builder"}, TicketStateReady, TaskStatusAssigned},
		{"in progress stays in progress", Task{ID: "t4", Status: TaskStatusInProgress}, TicketStateInProgress, TaskStatusInProgress},
		{"waiting becomes in progress", Task{ID: "t5", Status: TaskStatusWaitingForChoice}, TicketStateInProgress, TaskStatusInProgress},
		// FR-110: a failed attempt leaves the work open, not closed.
		{"failed becomes in progress", Task{ID: "t6", Status: TaskStatusFailed, Error: "boom"}, TicketStateInProgress, TaskStatusFailed},
		{"timeout becomes in progress", Task{ID: "t7", Status: TaskStatusTimeout}, TicketStateInProgress, TaskStatusTimeout},
		// FR-111: completed preserves its prior meaning.
		{"completed becomes done", Task{ID: "t8", Status: TaskStatusCompleted}, TicketStateDone, TaskStatusCompleted},
		{"cancelled stays cancelled", Task{ID: "t9", Status: TaskStatusCancelled}, TicketStateCancelled, TaskStatusCancelled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := migrationFixtureWorkspace(tc.task)
			MigrateWorkspaceTickets(ws)

			task := findTask(t, ws, tc.task.ID)
			if task.TicketState != tc.want {
				t.Fatalf("TicketState = %q, want %q", task.TicketState, tc.want)
			}
			if task.CanonicalState() != tc.want {
				t.Fatalf("CanonicalState = %q, want %q", task.CanonicalState(), tc.want)
			}
			if task.Status != tc.legacy {
				t.Fatalf("legacy Status = %q, want %q", task.Status, tc.legacy)
			}
		})
	}
}

// FR-110/FR-32: a migrated failure keeps its needs-attention signal.
func TestMigrateWorkspaceTickets_FailedRecordsStillNeedAttention(t *testing.T) {
	ws := migrationFixtureWorkspace(Task{ID: "t1", Status: TaskStatusFailed, Error: "boom"})
	MigrateWorkspaceTickets(ws)

	task := findTask(t, ws, "t1")
	if task.CanonicalState() != TicketStateInProgress {
		t.Fatalf("state = %q, want in_progress", task.CanonicalState())
	}
	if !task.NeedsAttention() {
		t.Fatalf("a migrated failed record must still raise needs-attention")
	}
}

// FR-111: Review requires positive evidence. A custom column the migration
// cannot interpret is NOT evidence, and finished work must not be dragged back
// into a queue on a guess.
func TestMigrateWorkspaceTickets_CompletedToReviewNeedsRecognizedPlacement(t *testing.T) {
	cases := map[string]struct {
		column string
		want   TicketState
	}{
		"review column":         {"col-review", TicketStateReview},
		"approval column":       {"awaiting-approval", TicketStateReview},
		"done column":           {"col-done", TicketStateDone},
		"unrecognized column":   {"col-xyzzy", TicketStateDone},
		"no column at all":      {"", TicketStateDone},
		"custom column by name": {"shipped", TicketStateDone},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			task := Task{ID: "t1", Status: TaskStatusCompleted}
			if tc.column != "" {
				task.Context = map[string]any{"kanban_column_id": tc.column}
			}
			ws := migrationFixtureWorkspace(task)
			MigrateWorkspaceTickets(ws)

			if got := findTask(t, ws, "t1").TicketState; got != tc.want {
				t.Fatalf("column %q migrated to %q, want %q", tc.column, got, tc.want)
			}
		})
	}
}

// FR-3/FR-105/FR-140: numbering is deterministic, never changes IDs, and never
// reuses or renumbers.
func TestMigrateWorkspaceTickets_Numbering(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ws := migrationFixtureWorkspace(
		Task{ID: "third", Status: TaskStatusPending, CreatedAt: base.Add(2 * time.Hour)},
		Task{ID: "first", Status: TaskStatusPending, CreatedAt: base},
		Task{ID: "second", Status: TaskStatusPending, CreatedAt: base.Add(time.Hour)},
	)

	result := MigrateWorkspaceTickets(ws)
	if result.Numbered != 3 {
		t.Fatalf("numbered %d records, want 3", result.Numbered)
	}

	// Numbers follow creation order, not slice order.
	if findTask(t, ws, "first").TicketNumber != 1 ||
		findTask(t, ws, "second").TicketNumber != 2 ||
		findTask(t, ws, "third").TicketNumber != 3 {
		t.Fatalf("numbering is not deterministic by creation time: %d/%d/%d",
			findTask(t, ws, "first").TicketNumber,
			findTask(t, ws, "second").TicketNumber,
			findTask(t, ws, "third").TicketNumber)
	}
	if ws.TicketSequence != 3 {
		t.Fatalf("TicketSequence = %d, want 3 so new tickets continue the sequence", ws.TicketSequence)
	}
	// Stable IDs are untouched.
	for _, id := range []string{"first", "second", "third"} {
		findTask(t, ws, id)
	}
}

// FR-106: running twice is a no-op. This is the assertion that protects
// against a restart mid-upgrade renumbering everything.
func TestMigrateWorkspaceTickets_IsIdempotent(t *testing.T) {
	ws := migrationFixtureWorkspace(
		Task{ID: "t1", Status: TaskStatusPending},
		Task{ID: "t2", Status: TaskStatusCompleted},
	)

	first := MigrateWorkspaceTickets(ws)
	if first.Skipped {
		t.Fatalf("the first run must not be skipped")
	}
	numbers := map[string]int64{}
	states := map[string]TicketState{}
	for i := range ws.Tasks {
		numbers[ws.Tasks[i].ID] = ws.Tasks[i].TicketNumber
		states[ws.Tasks[i].ID] = ws.Tasks[i].TicketState
	}

	second := MigrateWorkspaceTickets(ws)
	if !second.Skipped {
		t.Fatalf("a second run must be skipped")
	}
	if second.Numbered != 0 {
		t.Fatalf("a second run numbered %d records", second.Numbered)
	}
	for i := range ws.Tasks {
		task := &ws.Tasks[i]
		if task.TicketNumber != numbers[task.ID] {
			t.Fatalf("ticket %s was renumbered %d → %d", task.ID, numbers[task.ID], task.TicketNumber)
		}
		if task.TicketState != states[task.ID] {
			t.Fatalf("ticket %s changed state on a second run", task.ID)
		}
	}
}

// A workspace where some records were already migrated (an interrupted run, or
// a mix of old and new records) must not collide or renumber.
func TestMigrateWorkspaceTickets_MixedRecords(t *testing.T) {
	ws := migrationFixtureWorkspace(
		Task{ID: "already", Status: TaskStatusPending, TicketState: TicketStateReview, TicketNumber: 7},
		Task{ID: "legacy", Status: TaskStatusPending},
	)

	MigrateWorkspaceTickets(ws)

	already := findTask(t, ws, "already")
	// An already-canonical state is left alone: re-deriving it from the legacy
	// status would undo any transition made since.
	if already.TicketState != TicketStateReview {
		t.Fatalf("an already-migrated record was re-derived to %q", already.TicketState)
	}
	if already.TicketNumber != 7 {
		t.Fatalf("an already-numbered record was renumbered to %d", already.TicketNumber)
	}

	legacy := findTask(t, ws, "legacy")
	if legacy.TicketNumber == 0 || legacy.TicketNumber == 7 {
		t.Fatalf("new number = %d; must be assigned and must not collide", legacy.TicketNumber)
	}
	if ws.TicketSequence < 8 {
		t.Fatalf("TicketSequence = %d, want past the highest existing number", ws.TicketSequence)
	}
}

// FR-115: every advanced field survives, including data the migration does not
// understand.
func TestMigrateWorkspaceTickets_PreservesEverything(t *testing.T) {
	next := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	ws := migrationFixtureWorkspace(Task{
		ID:                   "t1",
		Status:               TaskStatusAssigned,
		To:                   "builder",
		AssignedNodeID:       "builder-node-1",
		Description:          "important work",
		Details:              "body",
		Schedule:             &ScheduleConfig{Type: ScheduleDaily, TimeOfDay: "09:00"},
		ScheduleEnabled:      true,
		NextRun:              &next,
		RequiredCapabilities: []string{"email"},
		OutputSpec:           &TaskOutputSpec{},
		ExecutionHistory:     []TaskExecution{{}},
		CurrentRunID:         "run-1",
		ParentTaskID:         "parent-1",
		InputTaskIDs:         []string{"dep-1"},
		ReferenceURL:         "https://example.com/spec",
		Result:               "prior result",
		Context: map[string]any{
			"kanban_column_id":               "col-custom",
			"something_we_do_not_know_about": map[string]any{"nested": true},
		},
	})

	MigrateWorkspaceTickets(ws)
	task := findTask(t, ws, "t1")

	if task.To != "builder" || task.AssignedNodeID != "builder-node-1" {
		t.Fatalf("assignment lost")
	}
	if task.Schedule == nil || !task.ScheduleEnabled || task.NextRun == nil {
		t.Fatalf("schedule lost")
	}
	if len(task.RequiredCapabilities) != 1 || task.OutputSpec == nil || len(task.ExecutionHistory) != 1 {
		t.Fatalf("execution configuration lost")
	}
	if task.CurrentRunID != "run-1" || task.ParentTaskID != "parent-1" || len(task.InputTaskIDs) != 1 {
		t.Fatalf("run/hierarchy/dependency references lost")
	}
	if task.ReferenceURL == "" || task.Result != "prior result" {
		t.Fatalf("reference url or result lost")
	}
	// FR-118: legacy Kanban data is RETAINED for diagnostics and rollback.
	if task.Context["kanban_column_id"] != "col-custom" {
		t.Fatalf("legacy board column was not retained")
	}
	// Unknown context survives untouched — rollback depends on it.
	if _, ok := task.Context["something_we_do_not_know_about"]; !ok {
		t.Fatalf("unrecognized context was dropped")
	}
}

// FR-13/FR-14/FR-56/FR-57/FR-116/FR-117: legacy board labels and due dates
// become canonical fields, and the originals stay put.
func TestMigrateWorkspaceTickets_KanbanLabelsAndDueDate(t *testing.T) {
	t.Run("labels merge into tags and de-duplicate", func(t *testing.T) {
		ws := migrationFixtureWorkspace(Task{
			ID: "t1", Status: TaskStatusPending, Tags: []string{"infra"},
			Context: map[string]any{"kanban_labels": []string{"Infra", "urgent"}},
		})
		MigrateWorkspaceTickets(ws)
		task := findTask(t, ws, "t1")

		if len(task.Tags) != 2 {
			t.Fatalf("Tags = %v, want infra and urgent de-duplicated", task.Tags)
		}
		if task.Context["kanban_labels"] == nil {
			t.Fatalf("legacy labels must be retained for rollback")
		}
	})

	t.Run("a valid due date is promoted", func(t *testing.T) {
		ws := migrationFixtureWorkspace(Task{
			ID: "t1", Status: TaskStatusPending,
			Context: map[string]any{"kanban_due_date": "2026-09-01"},
		})
		MigrateWorkspaceTickets(ws)
		task := findTask(t, ws, "t1")

		if task.DueDate == nil || task.DueDate.Format("2006-01-02") != "2026-09-01" {
			t.Fatalf("due date not promoted: %v", task.DueDate)
		}
	})

	t.Run("an unreadable due date is reported, not dropped or guessed", func(t *testing.T) {
		ws := migrationFixtureWorkspace(Task{
			ID: "t1", Status: TaskStatusPending,
			Context: map[string]any{"kanban_due_date": "next tuesday"},
		})
		result := MigrateWorkspaceTickets(ws)
		task := findTask(t, ws, "t1")

		if task.DueDate != nil {
			t.Fatalf("an unreadable date must not become a due date, got %v", task.DueDate)
		}
		if task.Context["kanban_due_date"] != "next tuesday" {
			t.Fatalf("the original value must be preserved for repair")
		}
		if len(result.Findings) == 0 {
			t.Fatalf("an unreadable date must produce a repair finding")
		}
	})
}

// FR-113/FR-126: anything ambiguous produces a finding rather than a guess,
// and the record is always preserved.
func TestMigrateWorkspaceTickets_RepairFindings(t *testing.T) {
	t.Run("an unknown status lands safely and is reported", func(t *testing.T) {
		ws := migrationFixtureWorkspace(Task{ID: "t1", Status: TaskStatus("teleported")})
		result := MigrateWorkspaceTickets(ws)

		task := findTask(t, ws, "t1")
		// Ready is the least destructive landing place: it claims neither that
		// the work finished nor that it is running.
		if task.TicketState != TicketStateReady {
			t.Fatalf("unknown status landed in %q", task.TicketState)
		}
		if len(result.Findings) != 1 || result.Findings[0].Severity != TicketRepairWarning {
			t.Fatalf("expected one warning finding, got %+v", result.Findings)
		}
		if result.Findings[0].TicketID != "t1" {
			t.Fatalf("finding does not identify the record: %+v", result.Findings[0])
		}
	})

	t.Run("an unsafe backlog record is reported, not silently stripped", func(t *testing.T) {
		ws := migrationFixtureWorkspace(Task{
			ID: "t1", Status: TaskStatusBacklog, Description: "captured",
			To: "builder", ScheduleEnabled: true, Schedule: &ScheduleConfig{Type: ScheduleDaily},
		})
		result := MigrateWorkspaceTickets(ws)
		task := findTask(t, ws, "t1")

		// Nothing removed — the user needs to see how it got this way.
		if task.To != "builder" || !task.ScheduleEnabled {
			t.Fatalf("migration stripped execution details instead of reporting them")
		}
		if len(result.Findings) == 0 {
			t.Fatalf("an unsafe backlog record must produce a finding")
		}
	})

	t.Run("findings never carry record content", func(t *testing.T) {
		ws := migrationFixtureWorkspace(Task{
			ID: "t1", Status: TaskStatus("weird"),
			Description: "SECRET TITLE", Details: "SECRET BODY",
		})
		result := MigrateWorkspaceTickets(ws)

		for _, finding := range result.Findings {
			if strings.Contains(finding.Summary, "SECRET") {
				t.Fatalf("a repair finding leaked record content: %q", finding.Summary)
			}
		}
	})
}

// FR-105/FR-2: migration creates no duplicate records and the canonical API
// reads exactly what was migrated.
func TestMigrateWorkspaceTickets_NoDuplicatesAndCanonicalReads(t *testing.T) {
	store := newBacklogTestStore(t)
	svc := NewTicketService(store)

	ws := migrationFixtureWorkspace(
		Task{ID: "t1", Status: TaskStatusBacklog, Description: "captured"},
		Task{ID: "t2", Status: TaskStatusCompleted, Description: "finished"},
	)
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	results, err := MigrateAllWorkspaceTickets(store)
	if err != nil {
		t.Fatalf("MigrateAllWorkspaceTickets: %v", err)
	}
	if len(results) != 1 || results[0].Migrated != 2 {
		t.Fatalf("migration result = %+v", results)
	}

	page, err := svc.Search(TicketQuery{WorkspaceID: ws.ID, Archive: TicketArchiveAll})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("workspace holds %d tickets after migration, want 2 — no duplicates", page.Total)
	}

	captured, err := svc.Get(ws.ID, "t1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if captured.State != TicketStateBacklog || captured.Number == 0 {
		t.Fatalf("migrated ticket is not canonical: %+v", captured)
	}
	// And a second full pass changes nothing.
	again, err := MigrateAllWorkspaceTickets(store)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("a second full migration pass reported %d migrated workspaces, want 0", len(again))
	}
}

// FR-106: the migration must be restart-safe, which means the version gate has
// to survive a round-trip through persistence — not just live in memory.
//
// FileStore.Get deserializes fresh from disk on every read, so reading the
// workspace back is a genuine reload rather than a cache hit. That is the
// closest available proxy for "the server restarted".
func TestMigrateWorkspaceTickets_VersionGateSurvivesReload(t *testing.T) {
	store := newBacklogTestStore(t)

	ws := migrationFixtureWorkspace(
		Task{ID: "t1", Status: TaskStatusPending},
		Task{ID: "t2", Status: TaskStatusBacklog, Description: "captured"},
	)
	if err := store.Save(ws); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := MigrateAllWorkspaceTickets(store); err != nil {
		t.Fatalf("first migration: %v", err)
	}

	reloaded, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TicketMigrationVersion != TicketMigrationVersion {
		t.Fatalf("migration version did not persist: %d", reloaded.TicketMigrationVersion)
	}
	if reloaded.TicketSequence != 2 {
		t.Fatalf("TicketSequence did not persist: %d", reloaded.TicketSequence)
	}
	if NeedsTicketMigration(reloaded) {
		t.Fatalf("a reloaded migrated workspace still reports as needing migration")
	}

	numbers := map[string]int64{}
	for i := range reloaded.Tasks {
		numbers[reloaded.Tasks[i].ID] = reloaded.Tasks[i].TicketNumber
		if reloaded.Tasks[i].TicketNumber == 0 {
			t.Fatalf("ticket %s lost its number across a reload", reloaded.Tasks[i].ID)
		}
	}

	// Simulate the next boot: migrate again against the persisted state.
	results, err := MigrateAllWorkspaceTickets(store)
	if err != nil {
		t.Fatalf("second boot migration: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("the next boot re-migrated %d workspaces, want 0", len(results))
	}

	final, err := store.Get(ws.ID)
	if err != nil {
		t.Fatalf("final reload: %v", err)
	}
	for i := range final.Tasks {
		task := &final.Tasks[i]
		if task.TicketNumber != numbers[task.ID] {
			t.Fatalf("ticket %s was renumbered across a restart: %d → %d",
				task.ID, numbers[task.ID], task.TicketNumber)
		}
	}
}
