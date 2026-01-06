package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/database"
)

// ToolCallStore defines the interface for tool call storage operations.
type ToolCallStore interface {
	// AddToolCall stores a new tool call record.
	AddToolCall(ctx context.Context, tc *ToolCall) error

	// GetToolCalls retrieves all tool calls for a session.
	GetToolCalls(ctx context.Context, sessionID string) ([]ToolCall, error)

	// GetToolCallsByMessage retrieves tool calls for a specific message.
	GetToolCallsByMessage(ctx context.Context, messageID string) ([]ToolCall, error)

	// GetToolCallsByName retrieves tool calls filtered by tool name.
	GetToolCallsByName(ctx context.Context, sessionID, toolName string) ([]ToolCall, error)
}

// SQLiteToolCallStore implements ToolCallStore using SQLite.
type SQLiteToolCallStore struct {
	db *database.DB
}

// NewSQLiteToolCallStore creates a new SQLite-backed tool call store.
func NewSQLiteToolCallStore(db *database.DB) *SQLiteToolCallStore {
	return &SQLiteToolCallStore{db: db}
}

// AddToolCall stores a new tool call record.
func (s *SQLiteToolCallStore) AddToolCall(ctx context.Context, tc *ToolCall) error {
	if tc.ID == "" {
		tc.ID = uuid.New().String()
	}
	if tc.CreatedAt.IsZero() {
		tc.CreatedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_calls (id, message_id, session_id, tool_name, arguments, result, error, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, tc.ID, tc.MessageID, tc.SessionID, tc.ToolName, tc.Arguments, tc.Result, tc.Error, tc.DurationMs, tc.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert tool call: %w", err)
	}

	return nil
}

// GetToolCalls retrieves all tool calls for a session ordered by creation time.
func (s *SQLiteToolCallStore) GetToolCalls(ctx context.Context, sessionID string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, message_id, session_id, tool_name, arguments, result, error, duration_ms, created_at
		FROM tool_calls
		WHERE session_id = ?
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool calls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanToolCalls(rows)
}

// GetToolCallsByMessage retrieves tool calls for a specific message.
func (s *SQLiteToolCallStore) GetToolCallsByMessage(ctx context.Context, messageID string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, message_id, session_id, tool_name, arguments, result, error, duration_ms, created_at
		FROM tool_calls
		WHERE message_id = ?
		ORDER BY created_at ASC
	`, messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool calls by message: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanToolCalls(rows)
}

// GetToolCallsByName retrieves tool calls filtered by tool name within a session.
func (s *SQLiteToolCallStore) GetToolCallsByName(ctx context.Context, sessionID, toolName string) ([]ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, message_id, session_id, tool_name, arguments, result, error, duration_ms, created_at
		FROM tool_calls
		WHERE session_id = ? AND tool_name = ?
		ORDER BY created_at ASC
	`, sessionID, toolName)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool calls by name: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanToolCalls(rows)
}

// scanToolCalls is a helper to scan rows into ToolCall structs.
func scanToolCalls(rows *sql.Rows) ([]ToolCall, error) {
	toolCalls := make([]ToolCall, 0)

	for rows.Next() {
		var tc ToolCall
		var arguments, result, errorMsg sql.NullString
		var durationMs sql.NullInt64

		if err := rows.Scan(
			&tc.ID,
			&tc.MessageID,
			&tc.SessionID,
			&tc.ToolName,
			&arguments,
			&result,
			&errorMsg,
			&durationMs,
			&tc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan tool call: %w", err)
		}

		tc.Arguments = arguments.String
		tc.Result = result.String
		tc.Error = errorMsg.String
		if durationMs.Valid {
			tc.DurationMs = int(durationMs.Int64)
		}

		toolCalls = append(toolCalls, tc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tool calls: %w", err)
	}

	return toolCalls, nil
}
