package workspacecontinuity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ImportAction string

const (
	Continue      ImportAction = "continue"
	WorkspaceOnly ImportAction = "workspace_only"
)

type Operation struct {
	ID                string
	TreeDigest        string
	DestinationDigest string
	UserID            string
	Action            ImportAction
	Status            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ImportMember struct {
	WorkspaceID string
	Generation  string
	Digest      string
	Disposition Disposition
}

// RestoreScope is local receipt ownership, not a portable permission. Domain
// adapters must use ClaimRecord and insert canonical rows in this same SQL tx.
type RestoreScope struct {
	OperationID string
	WorkspaceID string
	UserID      string
}

func (scope RestoreScope) valid() bool {
	return ValidID(scope.OperationID) && ValidID(scope.WorkspaceID) && ValidID(scope.UserID)
}

// BeginImport commits inactive admission before a directory can be registered.
// Review/source/destination identity checks precede this call at the coordinator.
// Repeated completed operations return their original receipt without changing
// local edits, activation, disposition, or canonical domain records.
func (s *LocalStore) BeginImport(ctx context.Context, requested Operation, members []ImportMember) (Operation, error) {
	return s.beginImport(ctx, requested, members, false)
}

// BeginReviewedImport rejects a changed destination in the SAME SQL transaction
// that creates the inactive receipt. It does not inspect the source, authorize
// the action, or install any file: the coordinator must revalidate those first.
// Exact completed/pending operation retries are matched by existing receipt
// membership and never re-adopt current destination state by name or contents.
func (s *LocalStore) BeginReviewedImport(ctx context.Context, requested Operation, members []ImportMember) (Operation, error) {
	return s.beginImport(ctx, requested, members, true)
}

func (s *LocalStore) beginImport(ctx context.Context, requested Operation, members []ImportMember, checked bool) (Operation, error) {
	var result Operation
	if !ValidID(requested.ID) || !ValidID(requested.UserID) || !validDigest(requested.TreeDigest) || !validDigest(requested.DestinationDigest) || requested.Action != Continue && requested.Action != WorkspaceOnly {
		return result, ErrInvalid
	}
	if len(members) == 0 || len(members) > 1024 {
		return result, ErrLimit
	}
	seen := map[string]bool{}
	adopted := 0
	for _, member := range members {
		if !ValidID(member.WorkspaceID) || !validGeneration(member.Generation) || !validDigest(member.Digest) || seen[member.WorkspaceID] {
			return result, ErrInvalid
		}
		seen[member.WorkspaceID] = true
		switch member.Disposition {
		case Ordinary, WorkspaceOnlyHQ:
		case AdoptedHQ:
			adopted++
		default:
			return result, ErrInvalid
		}
	}
	if adopted > 1 || requested.Action == WorkspaceOnly && adopted != 0 || requested.Action == Continue && adopted != 1 {
		return result, ErrInvalid
	}
	err := s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		existing, err := scanOperation(tx.QueryRowContext(ctx, `SELECT id,tree_digest,destination_digest,owner_user_id,action,status,created_at,updated_at FROM continuity_operations WHERE tree_digest=? AND owner_user_id=?`, requested.TreeDigest, requested.UserID))
		if err == nil {
			if existing.Action != requested.Action || existing.Status != "complete" && existing.DestinationDigest != requested.DestinationDigest {
				return ErrConflict
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_attachments WHERE operation_id=?`, existing.ID).Scan(&count); err != nil {
				return err
			}
			if count != len(members) {
				return ErrConflict
			}
			for _, member := range members {
				a, err := readAttachment(ctx, tx, member.WorkspaceID)
				if err != nil {
					return err
				}
				if a.OperationID != existing.ID || a.SourceGeneration != member.Generation || a.SourceDigest != member.Digest || a.Disposition != member.Disposition {
					return ErrConflict
				}
				if a.State == Detached || a.State == Unreviewed {
					return ErrConflict
				}
			}
			result = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if checked {
			current, err := DestinationDigest(ctx, tx, requested.UserID)
			if err != nil {
				return err
			}
			if current != requested.DestinationDigest {
				return ErrChanged
			}
		}
		now := time.Now().UTC()
		_, err = tx.ExecContext(ctx, `INSERT INTO continuity_operations(id,tree_digest,destination_digest,owner_user_id,action,status,created_at,updated_at) VALUES (?,?,?,?,?,'restoring',?,?)`, requested.ID, requested.TreeDigest, requested.DestinationDigest, requested.UserID, requested.Action, now, now)
		if err != nil {
			return ErrConflict
		}
		for _, member := range members {
			// An unexpected canonical row is never adopted by matching its ID.
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=?`, member.WorkspaceID).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return ErrConflict
			}
			attachment, err := readAttachment(ctx, tx, member.WorkspaceID)
			if err != nil {
				return err
			}
			if attachment.Version != 0 && attachment.State != Unreviewed && attachment.State != Detached {
				return ErrConflict
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO continuity_attachments(workspace_id,state,disposition,operation_id,source_generation,source_digest,updated_at)
				VALUES (?,'restoring',?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET state='restoring',disposition=excluded.disposition,operation_id=excluded.operation_id,
				source_generation=excluded.source_generation,source_digest=excluded.source_digest,version=version+1,updated_at=excluded.updated_at`, member.WorkspaceID, member.Disposition, requested.ID, member.Generation, member.Digest, now)
			if err != nil {
				return err
			}
			for _, domain := range DomainNames() {
				_, err = tx.ExecContext(ctx, `INSERT INTO continuity_components(operation_id,workspace_id,domain,status,updated_at) VALUES (?,?,?,'restoring',?)`, requested.ID, member.WorkspaceID, domain, now)
				if err != nil {
					return err
				}
			}
		}
		result = requested
		result.Status, result.CreatedAt, result.UpdatedAt = "restoring", now, now
		return nil
	})
	return result, err
}

func scanOperation(row *sql.Row) (Operation, error) {
	var operation Operation
	err := row.Scan(&operation.ID, &operation.TreeDigest, &operation.DestinationDigest, &operation.UserID, &operation.Action, &operation.Status, &operation.CreatedAt, &operation.UpdatedAt)
	return operation, err
}

func (s *LocalStore) Operation(ctx context.Context, id, userID string) (Operation, error) {
	if !ValidID(id) || !ValidID(userID) {
		return Operation{}, ErrInvalid
	}
	return scanOperation(s.db.QueryRowContext(ctx, `SELECT id,tree_digest,destination_digest,owner_user_id,action,status,created_at,updated_at FROM continuity_operations WHERE id=? AND owner_user_id=?`, id, userID))
}

// ClaimRecord returns false for the exact previously restored source record.
// Never compare against/rewrite the current canonical content on a retry: the
// user may have edited or deleted it since this receipt completed. Every
// restored record passes here, so this is where an imported ID is held to
// SafeRecordID before it can reach any view.
func ClaimRecord(ctx context.Context, tx *sql.Tx, scope RestoreScope, domain, family, recordID, digest string) (bool, error) {
	if tx == nil || !scope.valid() || !knownDomain(domain) || !validLabel(family) || !SafeRecordID(recordID) || !validDigest(digest) {
		return false, ErrInvalid
	}
	var status, attachmentState string
	err := tx.QueryRowContext(ctx, `SELECT o.status,a.state FROM continuity_operations o JOIN continuity_attachments a ON a.operation_id=o.id
		WHERE o.id=? AND o.owner_user_id=? AND a.workspace_id=?`, scope.OperationID, scope.UserID, scope.WorkspaceID).Scan(&status, &attachmentState)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrConflict
	}
	if err != nil {
		return false, err
	}
	var operationID, workspaceID, sourceDigest string
	err = tx.QueryRowContext(ctx, `SELECT operation_id,workspace_id,source_digest FROM continuity_records WHERE domain=? AND family=? AND record_id=?`, domain, family, recordID).Scan(&operationID, &workspaceID, &sourceDigest)
	if err == nil {
		if operationID == scope.OperationID && workspaceID == scope.WorkspaceID && sourceDigest == digest {
			return false, nil
		}
		return false, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if status == "complete" || attachmentState != string(Restoring) && attachmentState != string(ImportedInactive) && attachmentState != string(ImportedActive) {
		return false, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO continuity_records(domain,family,record_id,operation_id,workspace_id,source_digest) VALUES (?,?,?,?,?,?)`, domain, family, recordID, scope.OperationID, scope.WorkspaceID, digest)
	return err == nil, err
}

type ComponentOutcome struct {
	Domain string `json:"domain"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
	Reason string `json:"reason,omitempty"`
}

// ComponentOutcomes is scoped to one reviewed workspace and local operation
// owner. An absent operation/member is a conflict, never a verified-empty report.
func (s *LocalStore) ComponentOutcomes(ctx context.Context, scope RestoreScope) ([]ComponentOutcome, error) {
	if !scope.valid() {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.domain,c.status,c.record_count,c.reason FROM continuity_components c
		JOIN continuity_operations o ON o.id=c.operation_id WHERE c.operation_id=? AND c.workspace_id=? AND o.owner_user_id=? ORDER BY c.domain`, scope.OperationID, scope.WorkspaceID, scope.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]ComponentOutcome, 0, len(DomainNames()))
	for rows.Next() {
		if len(result) >= len(DomainNames()) {
			return nil, ErrLimit
		}
		var outcome ComponentOutcome
		if err := rows.Scan(&outcome.Domain, &outcome.Status, &outcome.Count, &outcome.Reason); err != nil {
			return nil, err
		}
		result = append(result, outcome)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrConflict
	}
	return result, nil
}

func SetComponentOutcome(ctx context.Context, tx *sql.Tx, scope RestoreScope, outcome ComponentOutcome) error {
	if tx == nil || !scope.valid() || !knownDomain(outcome.Domain) || outcome.Count < 0 || outcome.Count > MaxReferences*MaxRecords || outcome.Reason != "" && !validLabel(outcome.Reason) {
		return ErrInvalid
	}
	switch outcome.Status {
	case "restoring", "restored", "unavailable", "unsupported", "conflict", "failed", "interrupted":
	default:
		return ErrInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE continuity_components SET status=?,record_count=?,reason=?,updated_at=? WHERE operation_id=? AND workspace_id=? AND domain=?
		AND EXISTS(SELECT 1 FROM continuity_operations o WHERE o.id=continuity_components.operation_id AND o.owner_user_id=? AND o.status!='complete')`, outcome.Status, outcome.Count, outcome.Reason, time.Now().UTC(), scope.OperationID, scope.WorkspaceID, outcome.Domain, scope.UserID)
	if err != nil {
		return err
	}
	return requireOne(result)
}

func (s *LocalStore) CompleteImport(ctx context.Context, id, userID string) error {
	if !ValidID(id) || !ValidID(userID) {
		return ErrInvalid
	}
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		op, err := scanOperation(tx.QueryRowContext(ctx, `SELECT id,tree_digest,destination_digest,owner_user_id,action,status,created_at,updated_at FROM continuity_operations WHERE id=? AND owner_user_id=?`, id, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if op.Status == "complete" {
			return nil
		}
		var blocked, members, components int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_components WHERE operation_id=? AND status NOT IN ('restored','unavailable','unsupported')`, id).Scan(&blocked); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_attachments WHERE operation_id=? AND state='restoring'`, id).Scan(&members); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_components WHERE operation_id=?`, id).Scan(&components); err != nil {
			return err
		}
		if blocked != 0 || members == 0 || components != members*len(DomainNames()) {
			return ErrConflict
		}
		// Receipt statuses alone cannot make missing workspace or assistant
		// identities complete. Domain validation additionally proves the exact
		// folder/profile/entry instance before reaching this final transition.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_attachments a LEFT JOIN workspaces w ON w.id=a.workspace_id WHERE a.operation_id=? AND w.id IS NULL`, id).Scan(&blocked); err != nil {
			return err
		}
		if blocked != 0 {
			return ErrConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_components WHERE operation_id=? AND domain IN ('workspace','agents') AND status!='restored'`, id).Scan(&blocked); err != nil {
			return err
		}
		if blocked != 0 {
			return ErrConflict
		}
		if op.Action == Continue {
			var valid int
			err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM continuity_attachments a
				JOIN personal_assistant_state p ON p.hq_workspace_id=a.workspace_id
				JOIN users u ON u.id=p.user_id AND u.personal_workspace_id=a.workspace_id
				JOIN continuity_components c ON c.operation_id=a.operation_id AND c.workspace_id=a.workspace_id AND c.domain='assistant'
				WHERE a.operation_id=? AND a.disposition='adopted_hq' AND p.user_id=?
				AND p.status IN ('active','paused') AND p.hq_entry_agent_instance_id!='' AND c.status='restored'`, id, userID).Scan(&valid)
			if err != nil {
				return err
			}
			if valid != 1 {
				return ErrConflict
			}
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `UPDATE continuity_attachments SET state='imported_inactive',version=version+1,updated_at=? WHERE operation_id=? AND state='restoring'`, now, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE continuity_operations SET status='complete',updated_at=? WHERE id=?`, now, id)
		return err
	})
}

// Interrupt records cancellation/failure without removing receipt-owned rows or
// activating partial work. Retry uses the same receipt and exact source digests.
func (s *LocalStore) Interrupt(ctx context.Context, id, userID string) error {
	if !ValidID(id) || !ValidID(userID) {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE continuity_operations SET status='interrupted',updated_at=? WHERE id=? AND owner_user_id=? AND status!='complete'`, time.Now().UTC(), id, userID)
	if err != nil {
		return err
	}
	return requireOne(result)
}
