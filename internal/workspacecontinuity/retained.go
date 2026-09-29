package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
)

// Retained records are the incoming assistant evidence of a workspace imported
// without adoption: its agreement and verified first-assignment results. They
// are inert — never read as this installation's relationship, never a grant or
// designation — and exist only so the workspace's own checkpoint keeps
// carrying the assistant it arrived with, for a later move to a machine that
// can adopt it. They are local receipt state, erased with it on reset.

// retainedDomains are the only domains a non-adopted import keeps.
var retainedDomains = map[string]bool{"assistant": true, "setup": true}

// MaxRetainedRecords bounds one workspace's retained records per domain.
const MaxRetainedRecords = 256

// RetainedRecord is one kept record with the family it was published under.
type RetainedRecord struct {
	Family string
	Record Record
}

// RetainRecords keeps records of one domain for the receipt-owned workspace in
// the caller's import transaction. A retry of the same operation is a no-op;
// leftovers of an earlier operation for the same workspace ID are replaced.
func RetainRecords(ctx context.Context, tx *sql.Tx, scope RestoreScope, domain, family string, records []Record) error {
	if tx == nil || !scope.valid() || !retainedDomains[domain] || !validLabel(family) || len(records) > MaxRetainedRecords {
		return ErrInvalid
	}
	if err := RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM continuity_retained_records WHERE workspace_id=? AND domain=? AND operation_id<>?`,
		scope.WorkspaceID, domain, scope.OperationID); err != nil {
		return err
	}
	for _, record := range records {
		if !ValidID(record.ID) || len(record.Data) == 0 || len(record.Data) > MaxRecordBytes {
			return ErrInvalid
		}
		var existing []byte
		err := tx.QueryRowContext(ctx, `SELECT data FROM continuity_retained_records WHERE workspace_id=? AND domain=? AND record_id=?`,
			scope.WorkspaceID, domain, record.ID).Scan(&existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.ExecContext(ctx, `INSERT INTO continuity_retained_records(workspace_id,domain,family,record_id,data,operation_id)
				VALUES(?,?,?,?,?,?)`, scope.WorkspaceID, domain, family, record.ID, []byte(record.Data), scope.OperationID); err != nil {
				return err
			}
		case err != nil:
			return err
		case Digest(existing) != Digest(record.Data):
			return ErrConflict
		}
	}
	return nil
}

// RetainedRecords reads one domain's retained records for a workspace in the
// caller's read view, ordered by record ID.
func RetainedRecords(ctx context.Context, q Queryer, workspaceID, domain string) ([]RetainedRecord, error) {
	if q == nil || !ValidID(workspaceID) || !retainedDomains[domain] {
		return nil, ErrInvalid
	}
	rows, err := q.QueryContext(ctx, `SELECT family,record_id,data FROM continuity_retained_records
		WHERE workspace_id=? AND domain=? ORDER BY record_id LIMIT ?`, workspaceID, domain, MaxRetainedRecords+1)
	if err != nil {
		return nil, err
	}
	var out []RetainedRecord
	for rows.Next() {
		var value RetainedRecord
		var data []byte
		if err := rows.Scan(&value.Family, &value.Record.ID, &data); err != nil {
			_ = rows.Close()
			return nil, err
		}
		value.Record.Data = data
		out = append(out, value)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(out) > MaxRetainedRecords {
		return nil, ErrLimit
	}
	return out, nil
}
