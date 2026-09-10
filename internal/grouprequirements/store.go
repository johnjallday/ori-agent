package grouprequirements

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// MemoryStore is useful for isolated handlers and tests. Production uses the
// SQLite implementation so review and operation receipts survive restart.
type MemoryStore struct {
	mu         sync.Mutex
	receipts   map[string]Receipt
	operations map[string]Operation
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{receipts: make(map[string]Receipt), operations: make(map[string]Operation)}
}

func (s *MemoryStore) SaveReceipt(_ context.Context, receipt Receipt) error {
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts[receipt.Token] = receipt
	return nil
}

func (s *MemoryStore) GetReceipt(_ context.Context, token string) (Receipt, error) {
	if s == nil {
		return Receipt{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[token]
	if !ok {
		return Receipt{}, ErrNotFound
	}
	return receipt, nil
}

func (s *MemoryStore) ConsumeReceipt(_ context.Context, token string, at time.Time) error {
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[token]
	if !ok {
		return ErrNotFound
	}
	receipt.ConsumedAt = &at
	s.receipts[token] = receipt
	return nil
}

func operationKey(owner string, kind OperationKind, key string) string {
	return owner + "\x00" + string(kind) + "\x00" + key
}

func (s *MemoryStore) GetOperation(_ context.Context, owner string, kind OperationKind, key string) (Operation, error) {
	if s == nil {
		return Operation{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, ok := s.operations[operationKey(owner, kind, key)]
	if !ok {
		return Operation{}, ErrNotFound
	}
	return operation, nil
}

func (s *MemoryStore) SaveOperation(_ context.Context, operation Operation) error {
	if s == nil {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := operationKey(operation.OwnerUserID, operation.OperationKind, operation.IdempotencyKey)
	if existing, ok := s.operations[key]; ok &&
		(existing.InputDigest != operation.InputDigest || existing.ReviewDigest != operation.ReviewDigest || existing.ChildWorkspaceID != operation.ChildWorkspaceID) {
		return ErrOperationConflict
	}
	s.operations[key] = operation
	return nil
}

type SQLiteStore struct {
	db *database.DB
}

func NewSQLiteStore(db *database.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	store := &SQLiteStore{db: db}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS group_requirement_reviews (
			token TEXT PRIMARY KEY,
			payload_json BLOB NOT NULL,
			expires_at TEXT NOT NULL,
			consumed_at TEXT
		);
		CREATE TABLE IF NOT EXISTS group_requirement_operations (
			owner_user_id TEXT NOT NULL,
			operation_kind TEXT NOT NULL,
			idempotency_key TEXT NOT NULL,
			input_digest TEXT NOT NULL,
			review_digest TEXT NOT NULL,
			child_workspace_id TEXT NOT NULL,
			payload_json BLOB NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (owner_user_id, operation_kind, idempotency_key)
		);
	`); err != nil {
		return nil, fmt.Errorf("initialize group requirement receipts: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) SaveReceipt(ctx context.Context, receipt Receipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO group_requirement_reviews(token, payload_json, expires_at, consumed_at)
		VALUES(?, ?, ?, NULL)
		ON CONFLICT(token) DO NOTHING`, receipt.Token, data, receipt.ExpiresAt.Format(time.RFC3339Nano))
	return err
}

func (s *SQLiteStore) GetReceipt(ctx context.Context, token string) (Receipt, error) {
	var data []byte
	var consumed sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json, consumed_at FROM group_requirement_reviews WHERE token = ?`, token).Scan(&data, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Receipt{}, ErrNotFound
		}
		return Receipt{}, err
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return Receipt{}, err
	}
	if consumed.Valid {
		at, err := time.Parse(time.RFC3339Nano, consumed.String)
		if err != nil {
			return Receipt{}, err
		}
		receipt.ConsumedAt = &at
	}
	return receipt, nil
}

func (s *SQLiteStore) ConsumeReceipt(ctx context.Context, token string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE group_requirement_reviews SET consumed_at = ? WHERE token = ?`, at.Format(time.RFC3339Nano), token)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) GetOperation(ctx context.Context, owner string, kind OperationKind, key string) (Operation, error) {
	var data []byte
	if err := s.db.QueryRowContext(ctx, `
		SELECT payload_json FROM group_requirement_operations
		WHERE owner_user_id = ? AND operation_kind = ? AND idempotency_key = ?`, owner, kind, key).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Operation{}, ErrNotFound
		}
		return Operation{}, err
	}
	var operation Operation
	if err := json.Unmarshal(data, &operation); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func (s *SQLiteStore) SaveOperation(ctx context.Context, operation Operation) error {
	data, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO group_requirement_operations(
			owner_user_id, operation_kind, idempotency_key,
			input_digest, review_digest, child_workspace_id, payload_json, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_user_id, operation_kind, idempotency_key) DO UPDATE SET
			payload_json = excluded.payload_json,
			updated_at = excluded.updated_at
		WHERE group_requirement_operations.input_digest = excluded.input_digest
		  AND group_requirement_operations.review_digest = excluded.review_digest
		  AND group_requirement_operations.child_workspace_id = excluded.child_workspace_id`,
		operation.OwnerUserID, operation.OperationKind, operation.IdempotencyKey,
		operation.InputDigest, operation.ReviewDigest, operation.ChildWorkspaceID, data, operation.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrOperationConflict
	}
	return nil
}
