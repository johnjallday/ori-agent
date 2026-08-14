package workspaceplan

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// MemorySlotStore is an in-memory execution slot, for tests and for builds
// without a database. It enforces the same one-Plan-per-workspace rule as the
// SQLite store; a looser fake would let concurrency tests pass over code that
// is broken in production.
type MemorySlotStore struct {
	mu          sync.Mutex
	leases      map[string]*Lease
	queues      map[string][]QueueEntry
	generations map[string]int64
}

// NewMemorySlotStore returns an empty in-memory slot store.
func NewMemorySlotStore() *MemorySlotStore {
	return &MemorySlotStore{
		leases:      map[string]*Lease{},
		queues:      map[string][]QueueEntry{},
		generations: map[string]int64{},
	}
}

var _ SlotStore = (*MemorySlotStore)(nil)

func (s *MemorySlotStore) Acquire(_ context.Context, workspaceID, planID, owner string, at time.Time) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, held := s.leases[workspaceID]; held {
		if existing.PlanID != planID {
			return nil, fmt.Errorf("%w: plan %s is executing in this workspace",
				ErrExecutionConflict, existing.PlanID)
		}
		// Already ours. Acquiring twice is idempotent so a retry does not
		// consume a generation or look like a conflict.
		clone := *existing
		return &clone, nil
	}

	// The generation is monotonic per workspace and never resets on release,
	// so a token from an earlier holder can never be reissued.
	s.generations[workspaceID]++
	lease := &Lease{
		WorkspaceID: workspaceID,
		PlanID:      planID,
		Generation:  s.generations[workspaceID],
		Owner:       owner,
		AcquiredAt:  at,
		HeartbeatAt: at,
	}
	s.leases[workspaceID] = lease
	clone := *lease
	return &clone, nil
}

func (s *MemorySlotStore) Release(_ context.Context, workspaceID, planID string, generation int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, held := s.leases[workspaceID]
	if !held {
		return nil
	}
	if existing.PlanID != planID {
		return fmt.Errorf("%w: plan %s holds this slot", ErrExecutionConflict, existing.PlanID)
	}
	if generation != 0 && existing.Generation != generation {
		return ErrStaleGeneration
	}
	delete(s.leases, workspaceID)
	return nil
}

func (s *MemorySlotStore) CurrentLease(_ context.Context, workspaceID string) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, held := s.leases[workspaceID]
	if !held {
		return nil, nil
	}
	clone := *existing
	return &clone, nil
}

func (s *MemorySlotStore) Heartbeat(_ context.Context, workspaceID, planID string, generation int64, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, held := s.leases[workspaceID]
	if !held || existing.PlanID != planID {
		return fmt.Errorf("%w: this plan does not hold the slot", ErrExecutionConflict)
	}
	if existing.Generation != generation {
		return ErrStaleGeneration
	}
	existing.HeartbeatAt = at
	return nil
}

func (s *MemorySlotStore) Enqueue(_ context.Context, workspaceID, planID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, entry := range s.queues[workspaceID] {
		if entry.PlanID == planID {
			// Already in line. Asking twice must not send it to the back.
			return nil
		}
	}
	s.queues[workspaceID] = append(s.queues[workspaceID], QueueEntry{
		WorkspaceID: workspaceID, PlanID: planID, QueuedAt: at,
	})
	return nil
}

func (s *MemorySlotStore) Dequeue(_ context.Context, workspaceID, planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	queue := s.queues[workspaceID]
	kept := make([]QueueEntry, 0, len(queue))
	for _, entry := range queue {
		if entry.PlanID != planID {
			kept = append(kept, entry)
		}
	}
	s.queues[workspaceID] = kept
	return nil
}

func (s *MemorySlotStore) Queue(_ context.Context, workspaceID string) ([]QueueEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	queue := append([]QueueEntry(nil), s.queues[workspaceID]...)
	sort.SliceStable(queue, func(i, j int) bool {
		if !queue[i].QueuedAt.Equal(queue[j].QueuedAt) {
			return queue[i].QueuedAt.Before(queue[j].QueuedAt)
		}
		return queue[i].PlanID < queue[j].PlanID
	})
	for i := range queue {
		queue[i].Position = i + 1
	}
	return queue, nil
}

// SQLiteSlotStore is the durable execution slot.
//
// The one-Plan-per-workspace rule is the slots table's primary key, so two
// processes racing to acquire resolve in the database rather than in
// application code that might forget to check.
type SQLiteSlotStore struct {
	db *database.DB
}

// NewSQLiteSlotStore returns a slot store backed by the application database.
func NewSQLiteSlotStore(db *database.DB) *SQLiteSlotStore {
	return &SQLiteSlotStore{db: db}
}

var _ SlotStore = (*SQLiteSlotStore)(nil)

func (s *SQLiteSlotStore) Acquire(ctx context.Context, workspaceID, planID, owner string, at time.Time) (*Lease, error) {
	var lease *Lease

	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		existing, err := currentLeaseTx(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.PlanID != planID {
				return fmt.Errorf("%w: plan %s is executing in this workspace",
					ErrExecutionConflict, existing.PlanID)
			}
			lease = existing
			return nil
		}

		// Bump the workspace's generation before claiming. It is monotonic and
		// survives release, so a stale worker's token is never reissued.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_plan_execution_generations (workspace_id, generation)
			VALUES (?, 1)
			ON CONFLICT(workspace_id) DO UPDATE SET generation = generation + 1
		`, workspaceID); err != nil {
			return fmt.Errorf("bump execution generation: %w", err)
		}
		var generation int64
		if err := tx.QueryRowContext(ctx, `
			SELECT generation FROM workspace_plan_execution_generations WHERE workspace_id = ?
		`, workspaceID).Scan(&generation); err != nil {
			return fmt.Errorf("read execution generation: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_plan_execution_slots
				(workspace_id, plan_id, generation, owner, acquired_at, heartbeat_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, workspaceID, planID, generation, owner, at, at); err != nil {
			if isUniqueConstraint(err) {
				// Another process won between the read and the insert. The
				// primary key is what caught it.
				return fmt.Errorf("%w: another plan took the slot", ErrExecutionConflict)
			}
			return fmt.Errorf("acquire execution slot: %w", err)
		}

		lease = &Lease{
			WorkspaceID: workspaceID, PlanID: planID, Generation: generation,
			Owner: owner, AcquiredAt: at, HeartbeatAt: at,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lease, nil
}

func (s *SQLiteSlotStore) Release(ctx context.Context, workspaceID, planID string, generation int64) error {
	// The generation predicate is the fence: a stale worker's release matches
	// no row and is refused rather than dropping the current holder's claim.
	query := `DELETE FROM workspace_plan_execution_slots WHERE workspace_id = ? AND plan_id = ?`
	args := []any{workspaceID, planID}
	if generation != 0 {
		query += ` AND generation = ?`
		args = append(args, generation)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("release execution slot: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("release execution slot: %w", err)
	}
	if affected > 0 {
		return nil
	}

	// Nothing was released. Distinguish "already free" from "someone else
	// holds it" and "your token is stale", because they call for different
	// responses.
	existing, err := s.CurrentLease(ctx, workspaceID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if existing.PlanID != planID {
		return fmt.Errorf("%w: plan %s holds this slot", ErrExecutionConflict, existing.PlanID)
	}
	return ErrStaleGeneration
}

func (s *SQLiteSlotStore) CurrentLease(ctx context.Context, workspaceID string) (*Lease, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT workspace_id, plan_id, generation, owner, acquired_at, heartbeat_at
		FROM workspace_plan_execution_slots
		WHERE workspace_id = ?
	`, workspaceID)
	return scanLease(row)
}

func currentLeaseTx(ctx context.Context, tx *sql.Tx, workspaceID string) (*Lease, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT workspace_id, plan_id, generation, owner, acquired_at, heartbeat_at
		FROM workspace_plan_execution_slots
		WHERE workspace_id = ?
	`, workspaceID)
	return scanLease(row)
}

func scanLease(row rowScanner) (*Lease, error) {
	var lease Lease
	err := row.Scan(&lease.WorkspaceID, &lease.PlanID, &lease.Generation,
		&lease.Owner, &lease.AcquiredAt, &lease.HeartbeatAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read execution slot: %w", err)
	}
	return &lease, nil
}

func (s *SQLiteSlotStore) Heartbeat(ctx context.Context, workspaceID, planID string, generation int64, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE workspace_plan_execution_slots
		SET heartbeat_at = ?
		WHERE workspace_id = ? AND plan_id = ? AND generation = ?
	`, at, workspaceID, planID, generation)
	if err != nil {
		return fmt.Errorf("heartbeat execution slot: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("heartbeat execution slot: %w", err)
	}
	if affected == 0 {
		return ErrStaleGeneration
	}
	return nil
}

func (s *SQLiteSlotStore) Enqueue(ctx context.Context, workspaceID, planID string, at time.Time) error {
	// DO NOTHING keeps an already-waiting Plan's original position: asking
	// twice must not send it to the back of the line.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO workspace_plan_execution_queue (workspace_id, plan_id, queued_at)
		VALUES (?, ?, ?)
		ON CONFLICT(workspace_id, plan_id) DO NOTHING
	`, workspaceID, planID, at); err != nil {
		return fmt.Errorf("enqueue plan: %w", err)
	}
	return nil
}

func (s *SQLiteSlotStore) Dequeue(ctx context.Context, workspaceID, planID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM workspace_plan_execution_queue WHERE workspace_id = ? AND plan_id = ?
	`, workspaceID, planID); err != nil {
		return fmt.Errorf("dequeue plan: %w", err)
	}
	return nil
}

func (s *SQLiteSlotStore) Queue(ctx context.Context, workspaceID string) ([]QueueEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace_id, plan_id, queued_at
		FROM workspace_plan_execution_queue
		WHERE workspace_id = ?
		ORDER BY queued_at ASC, plan_id ASC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("read execution queue: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var queue []QueueEntry
	for rows.Next() {
		var entry QueueEntry
		if err := rows.Scan(&entry.WorkspaceID, &entry.PlanID, &entry.QueuedAt); err != nil {
			return nil, fmt.Errorf("scan execution queue: %w", err)
		}
		entry.Position = len(queue) + 1
		queue = append(queue, entry)
	}
	return queue, rows.Err()
}
