package workspacecontinuity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
)

// EncodeRecord is for domain-owned, explicit portable DTOs, never live agent or
// runtime objects. Domain validation must run before this bounded wire encoder.
func EncodeRecord(id string, value any) (Record, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return Record{}, ErrInvalid
	}
	record := Record{ID: id, Data: data}
	var checked json.RawMessage
	if err := DecodeRecord(record, &checked); err != nil {
		return Record{}, err
	}
	return record, nil
}

// DecodeRecord applies the same strict field/key/UTF-8 rules as the manifest to
// a typed domain DTO. Top-level fields without omitempty are required, including
// false-valued intent flags: omission must not fabricate a recovered choice.
// It does not establish ownership or authorize persistence.
func DecodeRecord(record Record, destination any) error {
	if !ValidID(record.ID) {
		return ErrInvalid
	}
	if len(record.Data) > MaxRecordBytes {
		return ErrLimit
	}
	return DecodeRequiredDocument(record.Data, destination, MaxRecordBytes)
}

// DecodeRequiredDocument applies strict object decoding and requires every
// top-level field without omitempty. Local private DTOs can use the same field
// discipline without fabricating a portable record or import permission.
func DecodeRequiredDocument(data []byte, destination any, limit int) error {
	if limit <= 0 || limit > MaxChunkBytes || len(data) > limit {
		return ErrLimit
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return ErrInvalid
	}
	if err := strictJSON(data, destination); err != nil {
		return err
	}
	target := jsonValueType(reflect.TypeOf(destination))
	if target == nil || target.Kind() != reflect.Struct {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ErrInvalid
	}
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		parts := strings.Split(field.Tag.Get("json"), ",")
		if !field.IsExported() || parts[0] == "-" {
			continue
		}
		optional := false
		for _, option := range parts[1:] {
			optional = optional || option == "omitempty"
		}
		name := parts[0]
		if name == "" {
			name = field.Name
		}
		if _, exists := fields[name]; !optional && !exists {
			return ErrInvalid
		}
	}
	return nil
}

// RequireOwnedWorkspace checks the canonical workspace AND its receipt-owned
// insertion, in the adapter's transaction. A row created by another writer
// after BeginImport is not an imported workspace merely because its ID matches.
// Call only for new record claims: exact retries must not depend on current
// canonical content (which the user may already have edited or deleted).
func RequireOwnedWorkspace(ctx context.Context, tx *sql.Tx, scope RestoreScope) error {
	if tx == nil || !scope.valid() {
		return ErrInvalid
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces w
		JOIN continuity_records r ON r.record_id=w.id AND r.domain='workspace' AND r.family='workspaces'
		JOIN continuity_attachments a ON a.workspace_id=w.id AND a.operation_id=r.operation_id
		WHERE w.id=? AND w.owner_user_id=? AND w.deleted_at IS NULL
		AND r.workspace_id=w.id AND r.operation_id=? AND a.state='restoring'`,
		scope.WorkspaceID, scope.UserID, scope.OperationID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

// ImportWorkspaceIDs returns only the reviewed operation's members, not every
// destination workspace. In particular a source brief's "all" must never turn
// into authority over unrelated destination workspaces or future additions.
func ImportWorkspaceIDs(ctx context.Context, tx *sql.Tx, scope RestoreScope) ([]string, error) {
	if tx == nil || !scope.valid() {
		return nil, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.workspace_id FROM continuity_attachments a
		JOIN continuity_operations o ON o.id=a.operation_id
		WHERE a.operation_id=? AND o.owner_user_id=? ORDER BY a.workspace_id LIMIT 1025`, scope.OperationID, scope.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		if len(ids) == 1024 {
			return nil, ErrLimit
		}
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrConflict
	}
	return ids, nil
}
