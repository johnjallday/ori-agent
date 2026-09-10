package economy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// Resources. One install has one of each: this is a single-user city, not a
// per-workspace budget (PRD FR1).
const (
	ResourceCraft   = "craft"
	ResourceHarvest = "harvest"
)

// Reasons. Every ledger row says in plain words why it exists, so a balance can
// always be explained without joining anything.
const (
	ReasonChat          = "chat"          // a chat message the user sent
	ReasonManualTask    = "manual_task"   // a task the user ran by hand
	ReasonHarvest       = "harvest"       // Farm runs the user opened and banked
	ReasonBuild         = "build"         // turning a task into a Farm
	ReasonUpgrade       = "upgrade"       // speeding a Farm's cadence up
	ReasonBackfill      = "backfill"      // the one-time grant for an existing install
	ReasonGrandfathered = "grandfathered" // a Farm that predates the feature
)

// Reference kinds. Together with the reference id they form the idempotency key
// that makes every write in this package safe to replay (FR3).
const (
	RefKindMessage  = "message"
	RefKindTask     = "task"
	RefKindHarvest  = "harvest"
	RefKindBuild    = "build"
	RefKindUpgrade  = "upgrade"
	RefKindBackfill = "backfill"
)

// ErrStoreUnavailable means no ledger storage is wired. Callers treat it the way
// they treat a failed read: the economy shows nothing and charges nothing, which
// is the same behavior as the feature flag being off.
var ErrStoreUnavailable = errors.New("economy ledger storage is not configured")

// ErrInsufficientBalance means a debit would have taken a balance below zero.
// Balances are never negative (FR1), so the debit is refused outright rather
// than clamped — a clamp would silently give away whatever it could not charge.
var ErrInsufficientBalance = errors.New("insufficient balance")

// Balances is the whole city's stock. Craft is earned by hand, Harvest by
// reviewing Farm output.
type Balances struct {
	Craft   int64 `json:"craft"`
	Harvest int64 `json:"harvest"`
}

// Entry is one append-only ledger row. Amount is always positive: Credit and
// Debit decide the sign, so no caller can accidentally credit a debit.
//
// (Resource, RefKind, RefID) is the idempotency key. Pick a RefID that names the
// specific thing being paid for — an event id, a task id, a task id plus a
// timestamp — never something that repeats across unrelated writes.
type Entry struct {
	Resource    string
	Amount      int64
	Reason      string
	RefKind     string
	RefID       string
	WorkspaceID string
}

// PendingRun is one finished Farm run waiting to be collected. It becomes
// Harvest only when the user opens that Farm's result (FR10, FR11).
type PendingRun struct {
	TaskID      string
	WorkspaceID string
	RunKey      string
	ProducedAt  time.Time
}

// Store is the ledger's persistence contract. The service holds this rather than
// a concrete type so its rules can be tested without a database.
type Store interface {
	// Credit adds Amount of a resource. The bool reports whether the entry was
	// newly written; false means this exact reference was already recorded.
	Credit(ctx context.Context, entry Entry, now time.Time) (bool, error)

	// Debit subtracts Amount of a resource, refusing with
	// ErrInsufficientBalance rather than going negative.
	Debit(ctx context.Context, entry Entry, now time.Time) (bool, error)

	// Balances sums the ledger.
	Balances(ctx context.Context) (Balances, error)

	// HasEntry reports whether a specific reference has already been recorded.
	HasEntry(ctx context.Context, resource, refKind, refID string) (bool, error)

	// HasReason reports whether any entry carries a reason. The one-time
	// backfill guards on this (FR32).
	HasReason(ctx context.Context, reason string) (bool, error)

	// AddPending records a finished Farm run. The bool is false when this run
	// was already recorded.
	AddPending(ctx context.Context, run PendingRun) (bool, error)

	// PendingByWorkspace counts unharvested runs per workspace, for the tile
	// harvest piles.
	PendingByWorkspace(ctx context.Context) (map[string]int, error)

	// PendingByTask counts unharvested runs per task, for the Farm rows in the
	// harvest popover.
	PendingByTask(ctx context.Context) (map[string]int, error)

	// BankPending marks every unharvested run for one task as collected and
	// writes the matching Harvest credit in the same transaction. It returns
	// how many runs were banked; banking nothing is a success (FR26).
	BankPending(ctx context.Context, workspaceID, taskID string, now time.Time) (int, error)

	// DeletePendingForTask drops a deleted task's pending runs without banking
	// them (FR12).
	DeletePendingForTask(ctx context.Context, taskID string) error

	// CountUserChatMessages counts the user's stored chat messages, used only by
	// the first-run backfill to size its grant (FR32).
	CountUserChatMessages(ctx context.Context) (int64, error)
}

// SQLiteStore is the ledger over the shared application database.
//
// Every write is a single statement or a single transaction, and every one of
// them is idempotent: the same event delivered twice, the same save retried, or
// the same result modal opened twice all leave the same balance. That property
// is what lets the rest of the feature be fire-and-forget.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore builds a ledger store over the shared application database.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Credit records a positive entry.
func (s *SQLiteStore) Credit(ctx context.Context, entry Entry, now time.Time) (bool, error) {
	return s.record(ctx, entry, now, false)
}

// Debit records a negative entry, refusing to take the balance below zero.
func (s *SQLiteStore) Debit(ctx context.Context, entry Entry, now time.Time) (bool, error) {
	return s.record(ctx, entry, now, true)
}

func (s *SQLiteStore) record(ctx context.Context, entry Entry, now time.Time, debit bool) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrStoreUnavailable
	}
	normalized, err := normalizeEntry(entry)
	if err != nil {
		return false, err
	}
	// A zero-amount entry is deliberately allowed: a grandfathered Farm's build
	// entry is exactly that, and its mere presence is what stops the task being
	// charged later (FR33).
	delta := normalized.Amount
	if debit {
		delta = -delta
	}

	inserted := false
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if debit && delta < 0 {
			var balance int64
			// The balance is read inside the same transaction as the insert, so
			// two concurrent saves cannot both see enough Craft and both spend it.
			row := tx.QueryRowContext(ctx,
				`SELECT COALESCE(SUM(delta), 0) FROM economy_ledger WHERE resource = ?`,
				normalized.Resource)
			if err := row.Scan(&balance); err != nil {
				return fmt.Errorf("read %s balance: %w", normalized.Resource, err)
			}
			if balance+delta < 0 {
				return ErrInsufficientBalance
			}
		}

		result, err := tx.ExecContext(ctx, `
			INSERT INTO economy_ledger (resource, delta, reason, ref_kind, ref_id, workspace_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(resource, ref_kind, ref_id) DO NOTHING
		`, normalized.Resource, delta, normalized.Reason, normalized.RefKind, normalized.RefID,
			normalized.WorkspaceID, formatTime(now))
		if err != nil {
			return fmt.Errorf("write economy ledger entry: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count economy ledger write: %w", err)
		}
		inserted = affected > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// Balances sums the ledger by resource. A resource with no entries reads as
// zero, and the sum is clamped at zero so a hand-edited database cannot render a
// negative stockpile (FR1).
func (s *SQLiteStore) Balances(ctx context.Context) (Balances, error) {
	if s == nil || s.db == nil {
		return Balances{}, ErrStoreUnavailable
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT resource, COALESCE(SUM(delta), 0) FROM economy_ledger GROUP BY resource`)
	if err != nil {
		return Balances{}, fmt.Errorf("read economy balances: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var balances Balances
	for rows.Next() {
		var resource string
		var total int64
		if err := rows.Scan(&resource, &total); err != nil {
			return Balances{}, fmt.Errorf("scan economy balance: %w", err)
		}
		if total < 0 {
			total = 0
		}
		switch resource {
		case ResourceCraft:
			balances.Craft = total
		case ResourceHarvest:
			balances.Harvest = total
		}
	}
	if err := rows.Err(); err != nil {
		return Balances{}, fmt.Errorf("read economy balances: %w", err)
	}
	return balances, nil
}

// HasEntry reports whether this exact reference has already been recorded.
func (s *SQLiteStore) HasEntry(ctx context.Context, resource, refKind, refID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrStoreUnavailable
	}
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM economy_ledger WHERE resource = ? AND ref_kind = ? AND ref_id = ?
		)
	`, strings.TrimSpace(resource), strings.TrimSpace(refKind), strings.TrimSpace(refID)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("look up economy ledger entry: %w", err)
	}
	return exists == 1, nil
}

// HasReason reports whether any entry carries this reason.
func (s *SQLiteStore) HasReason(ctx context.Context, reason string) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrStoreUnavailable
	}
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM economy_ledger WHERE reason = ?)`,
		strings.TrimSpace(reason)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("look up economy ledger reason: %w", err)
	}
	return exists == 1, nil
}

// AddPending records one finished Farm run as waiting to be collected.
func (s *SQLiteStore) AddPending(ctx context.Context, run PendingRun) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrStoreUnavailable
	}
	taskID := strings.TrimSpace(run.TaskID)
	runKey := strings.TrimSpace(run.RunKey)
	if taskID == "" || runKey == "" {
		return false, errors.New("a pending harvest needs both a task id and a run key")
	}
	producedAt := run.ProducedAt
	if producedAt.IsZero() {
		producedAt = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO economy_harvest_pending (task_id, workspace_id, run_key, produced_at, harvested_at)
		VALUES (?, ?, ?, ?, NULL)
		ON CONFLICT(task_id, run_key) DO NOTHING
	`, taskID, strings.TrimSpace(run.WorkspaceID), runKey, formatTime(producedAt))
	if err != nil {
		return false, fmt.Errorf("record pending harvest: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count pending harvest write: %w", err)
	}
	return affected > 0, nil
}

// PendingByWorkspace counts unharvested runs per workspace.
func (s *SQLiteStore) PendingByWorkspace(ctx context.Context) (map[string]int, error) {
	return s.pendingCounts(ctx, "workspace_id")
}

// PendingByTask counts unharvested runs per task.
func (s *SQLiteStore) PendingByTask(ctx context.Context) (map[string]int, error) {
	return s.pendingCounts(ctx, "task_id")
}

func (s *SQLiteStore) pendingCounts(ctx context.Context, column string) (map[string]int, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreUnavailable
	}
	// column is never request data: both callers pass a compile-time constant.
	query := `SELECT ` + column + `, COUNT(1) FROM economy_harvest_pending
		WHERE harvested_at IS NULL GROUP BY ` + column
	rows, err := s.db.QueryContext(ctx, query) // #nosec G202 -- column is a package constant, not input
	if err != nil {
		return nil, fmt.Errorf("count pending harvests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, fmt.Errorf("scan pending harvest count: %w", err)
		}
		if strings.TrimSpace(key) == "" {
			continue
		}
		counts[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count pending harvests: %w", err)
	}
	return counts, nil
}

// BankPending collects one task's pending runs.
//
// Marking the rows and writing the credit share a transaction, so a crash
// between them cannot either pay twice or wipe a pile without paying. Banking
// zero rows is a success and writes nothing: opening a result you have already
// read must not mint Harvest.
func (s *SQLiteStore) BankPending(ctx context.Context, workspaceID, taskID string, now time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrStoreUnavailable
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return 0, errors.New("banking a harvest needs a task id")
	}

	banked := 0
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE economy_harvest_pending SET harvested_at = ?
			WHERE task_id = ? AND harvested_at IS NULL
		`, formatTime(now), taskID)
		if err != nil {
			return fmt.Errorf("mark pending harvests collected: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count collected harvests: %w", err)
		}
		if affected <= 0 {
			return nil
		}
		banked = int(affected)

		// One entry for the whole pile, referenced by task and collection time
		// so a second click a moment later cannot re-credit the same runs (FR11).
		_, err = tx.ExecContext(ctx, `
			INSERT INTO economy_ledger (resource, delta, reason, ref_kind, ref_id, workspace_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(resource, ref_kind, ref_id) DO NOTHING
		`, ResourceHarvest, int64(banked)*HarvestPerFarmRun, ReasonHarvest, RefKindHarvest,
			taskID+"@"+formatTime(now), strings.TrimSpace(workspaceID), formatTime(now))
		if err != nil {
			return fmt.Errorf("credit banked harvest: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return banked, nil
}

// DeletePendingForTask drops a task's pending runs. A deleted task's unopened
// output was never reviewed, so it is not owed (FR12).
func (s *SQLiteStore) DeletePendingForTask(ctx context.Context, taskID string) error {
	if s == nil || s.db == nil {
		return ErrStoreUnavailable
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM economy_harvest_pending WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("drop pending harvests for deleted task: %w", err)
	}
	return nil
}

// CountUserChatMessages counts the user's own stored chat messages.
//
// It reads the messages table directly rather than going through the session
// store because this is a one-time sizing question asked at startup, and the
// role filter keeps assistant replies out of a count that is meant to represent
// work the user did.
func (s *SQLiteStore) CountUserChatMessages(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrStoreUnavailable
	}
	var count int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM messages WHERE role = 'user'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count chat messages: %w", err)
	}
	return count, nil
}

func normalizeEntry(entry Entry) (Entry, error) {
	entry.Resource = strings.TrimSpace(entry.Resource)
	entry.Reason = strings.TrimSpace(entry.Reason)
	entry.RefKind = strings.TrimSpace(entry.RefKind)
	entry.RefID = strings.TrimSpace(entry.RefID)
	entry.WorkspaceID = strings.TrimSpace(entry.WorkspaceID)

	switch entry.Resource {
	case ResourceCraft, ResourceHarvest:
	default:
		return Entry{}, fmt.Errorf("unknown economy resource %q", entry.Resource)
	}
	if entry.Reason == "" {
		return Entry{}, errors.New("an economy ledger entry needs a reason")
	}
	if entry.RefKind == "" || entry.RefID == "" {
		return Entry{}, errors.New("an economy ledger entry needs a reference kind and id")
	}
	if entry.Amount < 0 {
		return Entry{}, errors.New("an economy ledger amount is never negative; use Debit")
	}
	return entry, nil
}

// formatTime writes timestamps the way the rest of the schema's TEXT columns do:
// UTC RFC3339 with nanoseconds, which sorts lexicographically.
func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}
