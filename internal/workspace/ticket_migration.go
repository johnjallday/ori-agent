package workspace

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// In-place migration of persisted Tasks into canonical Tickets
// (tasks/prd-workspace-ticket-management.md FR-105 through FR-118, FR-126).
//
// The design constraint that shapes everything here: this runs against real
// user data that has no backup. So the migration is
//
//   - IN PLACE. Every record keeps its stable ID. No parallel Ticket row, no
//     mirror file, no duplicate (FR-105).
//   - IDEMPOTENT and restart-safe. A per-workspace version marks completion;
//     running twice is a no-op and never renumbers (FR-106).
//   - LOSSLESS. Nothing is deleted or overwritten, including data the
//     migration does not understand. Legacy Kanban values are retained for
//     diagnostics and rollback (FR-115, FR-118).
//   - HONEST. Anything ambiguous produces a repair finding rather than a
//     guess. Guessing silently is how a user's completed work becomes
//     "cancelled" with no way to tell it happened (FR-113, FR-114, FR-126).

// TicketMigrationVersion is the current in-place migration version. A
// workspace whose TicketMigrationVersion equals this has already been
// migrated and is skipped.
const TicketMigrationVersion = 1

// Repair finding severities. Neither aborts the migration: the record is
// always preserved, and the finding tells the user what to look at.
const (
	// TicketRepairInfo records a decision worth knowing about that was
	// nonetheless unambiguous.
	TicketRepairInfo = "info"
	// TicketRepairWarning records data the migration could not interpret and
	// therefore left alone.
	TicketRepairWarning = "warning"
)

// TicketRepairFinding describes one record the migration could not fully
// interpret (FR-126).
//
// It deliberately carries IDs and field names, never field values: a finding
// is surfaced in the UI and must not become a channel for leaking note bodies,
// credentials pasted into a description, or anything else the record holds.
type TicketRepairFinding struct {
	TicketID     string `json:"ticket_id"`
	TicketNumber int64  `json:"ticket_number,omitempty"`
	Severity     string `json:"severity"`
	Field        string `json:"field"`
	// Summary is non-sensitive prose naming what could not be interpreted and
	// what the migration did instead.
	Summary string `json:"summary"`
}

// TicketMigrationResult reports what one workspace's migration did.
type TicketMigrationResult struct {
	WorkspaceID string                `json:"workspace_id"`
	Migrated    int                   `json:"migrated"`
	Numbered    int                   `json:"numbered"`
	Skipped     bool                  `json:"skipped"`
	Findings    []TicketRepairFinding `json:"findings,omitempty"`
}

// NeedsTicketMigration reports whether ws still has to be migrated.
func NeedsTicketMigration(ws *Workspace) bool {
	return ws != nil && ws.TicketMigrationVersion < TicketMigrationVersion
}

// MigrateWorkspaceTickets converts every Task in ws into a canonical Ticket
// in place.
//
// It mutates ws and returns what it did. It performs NO persistence — the
// caller wraps it in store.Update so the whole workspace is written atomically
// or not at all, which is what makes a crash mid-migration leave the original
// data intact (FR-106, FR-113).
func MigrateWorkspaceTickets(ws *Workspace) TicketMigrationResult {
	result := TicketMigrationResult{WorkspaceID: ""}
	if ws == nil {
		return result
	}
	result.WorkspaceID = ws.ID

	if !NeedsTicketMigration(ws) {
		// Already migrated. Returning early is what makes a second run a
		// no-op rather than a renumbering.
		result.Skipped = true
		return result
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Numbering runs over a deterministic ordering so the same workspace
	// always produces the same numbers, on any machine and in any run.
	order := make([]int, 0, len(ws.Tasks))
	for i := range ws.Tasks {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := &ws.Tasks[order[a]], &ws.Tasks[order[b]]
		if !x.CreatedAt.Equal(y.CreatedAt) {
			return x.CreatedAt.Before(y.CreatedAt)
		}
		return x.ID < y.ID
	})

	// Seed the sequence past any number that already exists, so a partially
	// numbered workspace (an interrupted earlier run, or records created by a
	// newer build) never reuses a value (FR-140).
	highest := ws.TicketSequence
	for i := range ws.Tasks {
		if n := ws.Tasks[i].TicketNumber; n > highest {
			highest = n
		}
	}

	for _, idx := range order {
		task := &ws.Tasks[idx]
		findings := migrateTaskToTicket(task)
		result.Findings = append(result.Findings, findings...)

		if task.TicketNumber == 0 {
			highest++
			task.TicketNumber = highest
			result.Numbered++
		}
		result.Migrated++
	}

	ws.TicketSequence = highest
	ws.TicketMigrationVersion = TicketMigrationVersion
	return result
}

// migrateTaskToTicket converts one record, returning any repair findings.
//
// Every branch preserves the source data. The migration adds canonical fields
// and never removes legacy ones.
func migrateTaskToTicket(task *Task) []TicketRepairFinding {
	var findings []TicketRepairFinding

	// A record that already carries canonical state was migrated by an earlier
	// run or created by a newer build. Leave its state alone — re-deriving it
	// from the legacy status would undo any transition made since.
	if !task.TicketState.Valid() {
		state, stateFindings := migrateTicketState(task)
		task.TicketState = state
		findings = append(findings, stateFindings...)
	}

	// Legacy Status stays as the compatibility projection of canonical state,
	// EXCEPT where the legacy value carries attempt information canonical
	// state deliberately does not model — a failed or timed-out run leaves the
	// Ticket In Progress, and that legacy status is what NeedsAttention reads
	// to raise the flag (FR-32, FR-110).
	if task.Status != TaskStatusFailed && task.Status != TaskStatusTimeout {
		task.Status = legacyStatusForTicketState(task.TicketState, task.To)
	}

	findings = append(findings, migrateKanbanLabels(task)...)
	findings = append(findings, migrateKanbanDueDate(task)...)
	findings = append(findings, checkBacklogSafety(task)...)

	if task.StateRank == 0 {
		// Preserve the Backlog ordering the user arranged; everything else
		// starts at a stable rank derived from creation order later.
		task.StateRank = task.BacklogRank
	}
	if strings.TrimSpace(task.SourceType) == "" {
		// Records predating provenance tracking say so, rather than claiming
		// to have been captured manually.
		task.SourceType = TicketSourceMigration
	}
	if task.TicketVersion == 0 {
		task.TicketVersion = 1
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if len(task.StateHistory) == 0 {
		// One synthetic entry recording that this state came from migration,
		// not from a user action. Backdated to creation so the timeline reads
		// correctly rather than claiming everything happened at upgrade time.
		task.StateHistory = []TicketStateChange{{
			To:        task.TicketState,
			Actor:     TicketActorMigration,
			Reason:    "migrated from legacy task status",
			Timestamp: task.CreatedAt,
		}}
	}

	return findings
}

// migrateTicketState maps a legacy status onto canonical state
// (FR-107 through FR-114).
func migrateTicketState(task *Task) (TicketState, []TicketRepairFinding) {
	var findings []TicketRepairFinding

	switch task.Status {
	case TaskStatusBacklog:
		return TicketStateBacklog, findings

	case TaskStatusPending, TaskStatusAssigned:
		// Assignment does NOT imply work started (FR-108, FR-25).
		return TicketStateReady, findings

	case TaskStatusInProgress, TaskStatusWaitingForChoice:
		return TicketStateInProgress, findings

	case TaskStatusFailed, TaskStatusTimeout:
		// The work is still wanted; the attempt failed. Needs-attention is
		// derived at read time from the retained legacy status (FR-110).
		return TicketStateInProgress, findings

	case TaskStatusCancelled:
		return TicketStateCancelled, findings

	case TaskStatusCompleted:
		// Review only when a recognized review placement proves that intent;
		// otherwise Done, which preserves the record's prior meaning (FR-111).
		if recognizedReviewPlacement(task) {
			findings = append(findings, TicketRepairFinding{
				TicketID: task.ID,
				Severity: TicketRepairInfo,
				Field:    "state",
				Summary:  "Completed work sat in a review column, so it was migrated to Review rather than Done.",
			})
			return TicketStateReview, findings
		}
		return TicketStateDone, findings
	}

	// An unrecognized status. Do not guess: Ready is the least destructive
	// landing place — it neither claims the work finished nor that it is
	// running — and the finding tells the user to look (FR-113).
	findings = append(findings, TicketRepairFinding{
		TicketID: task.ID,
		Severity: TicketRepairWarning,
		Field:    "status",
		Summary:  "This record had a status the migration did not recognize. It was placed in Ready and its original data was preserved; move it where it belongs.",
	})
	return TicketStateReady, findings
}

// recognizedReviewPlacement reports whether a board column proves the record
// was awaiting review (FR-111, FR-114).
//
// Recognition is deliberately narrow. A custom column named something the
// migration cannot interpret is NOT evidence of review intent, so it falls
// through to Done and preserves the record's prior meaning rather than moving
// finished work back into a queue.
func recognizedReviewPlacement(task *Task) bool {
	if task.Context == nil {
		return false
	}
	column, _ := task.Context["kanban_column_id"].(string)
	column = strings.ToLower(strings.TrimSpace(column))
	if column == "" {
		return false
	}
	return strings.Contains(column, "review") || strings.Contains(column, "approval")
}

// migrateKanbanLabels merges legacy board labels into canonical tags
// (FR-13, FR-56, FR-116).
//
// The legacy values are RETAINED in context: this feature's rollback story
// depends on the original data still being there.
func migrateKanbanLabels(task *Task) []TicketRepairFinding {
	if task.Context == nil {
		return nil
	}
	labels := normalizeLegacyKanbanLabels(task.Context["kanban_labels"])
	if len(labels) == 0 {
		return nil
	}

	merged, err := ValidateWorkspaceTags(append(append([]string{}, task.Tags...), labels...))
	if err != nil {
		return []TicketRepairFinding{{
			TicketID: task.ID,
			Severity: TicketRepairWarning,
			Field:    "kanban_labels",
			Summary:  "Legacy board labels could not be converted into tags and were left untouched for review.",
		}}
	}
	task.Tags = merged
	return nil
}

func normalizeLegacyKanbanLabels(raw any) []string {
	switch value := raw.(type) {
	case []string:
		return value
	case string:
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(text); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	}
	return nil
}

// migrateKanbanDueDate promotes a legacy board due date to the first-class
// field (FR-14, FR-57, FR-117). An unparseable value is preserved and
// reported rather than dropped.
func migrateKanbanDueDate(task *Task) []TicketRepairFinding {
	if task.Context == nil || task.DueDate != nil {
		return nil
	}
	raw, _ := task.Context["kanban_due_date"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			due := parsed.UTC()
			task.DueDate = &due
			return nil
		}
	}

	return []TicketRepairFinding{{
		TicketID: task.ID,
		Severity: TicketRepairWarning,
		Field:    "kanban_due_date",
		Summary:  "This record's board due date could not be read as a date. It was left in place and no due date was set.",
	}}
}

// checkBacklogSafety reports a Backlog record carrying execution state it
// should not have (FR-20, FR-113).
//
// It REPORTS rather than repairs. Silently stripping an assignee or a schedule
// would destroy information the user may need to understand how the record got
// into that state.
func checkBacklogSafety(task *Task) []TicketRepairFinding {
	if task.TicketState != TicketStateBacklog {
		return nil
	}
	if err := ValidateBacklogTaskInvariants(task); err == nil {
		return nil
	}
	return []TicketRepairFinding{{
		TicketID: task.ID,
		Severity: TicketRepairWarning,
		Field:    "state",
		Summary:  "This Backlog record carries execution details a Backlog ticket should not have. Nothing was removed; promote it to Ready or clear those details.",
	}}
}

// MigrateAllWorkspaceTickets migrates every workspace in the store.
//
// Each workspace is migrated in its own store.Update transaction, so one
// workspace failing leaves the others migrated and leaves the failing one
// exactly as it was.
func MigrateAllWorkspaceTickets(store Store) ([]TicketMigrationResult, error) {
	ids, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("list workspaces for ticket migration: %w", err)
	}

	results := make([]TicketMigrationResult, 0, len(ids))
	for _, id := range ids {
		var result TicketMigrationResult
		err := store.Update(id, func(ws *Workspace) error {
			result = MigrateWorkspaceTickets(ws)
			return nil
		})
		if err != nil {
			results = append(results, TicketMigrationResult{
				WorkspaceID: id,
				Findings: []TicketRepairFinding{{
					Severity: TicketRepairWarning,
					Field:    "workspace",
					Summary:  "This workspace could not be migrated and was left unchanged.",
				}},
			})
			continue
		}
		if !result.Skipped {
			results = append(results, result)
		}
	}
	return results, nil
}
