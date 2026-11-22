package economy

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	FilingBuilding  = "building"
	FilingDelivered = "delivered"
	FilingRefunded  = "refunded"

	RefKindFilingPurchase = "filing_expansion_purchase"
	RefKindFilingRefund   = "filing_expansion_refund"
)

// FilingClaim reserves a single workspace identity. The immutable PlanJSON
// describes the reviewed creation, not a folder selection or permission.
type FilingClaim struct {
	ID, Owner, RequestID, WorkspaceID string
	BaseCost, Cost                    int64
	PlanJSON, State                   string
	Registered                        bool
}

// FindFilingClaim returns the owner's active claim first, even for a different
// client request ID. A completed refund can only replay its original request.
func (s *SQLiteStore) FindFilingClaim(ctx context.Context, owner, requestID string) (*FilingClaim, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreUnavailable
	}
	var claim FilingClaim
	err := s.db.QueryRowContext(ctx, `SELECT id, request_id, workspace_id, base_cost, cost, plan_json, state, registered
		FROM economy_filing_claims WHERE owner_user_id = ? AND (state != 'refunded' OR request_id = ?)
		ORDER BY state = 'refunded' LIMIT 1`, owner, requestID).Scan(
		&claim.ID, &claim.RequestID, &claim.WorkspaceID, &claim.BaseCost, &claim.Cost, &claim.PlanJSON, &claim.State, &claim.Registered)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	claim.Owner = owner
	return &claim, nil
}

// ReserveFiling claims the offer and writes its debit (including a zero-Craft
// waiver) atomically. Other spends, including Farms, contend on the same ledger.
func (s *SQLiteStore) ReserveFiling(ctx context.Context, claim FilingClaim) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO economy_filing_claims
			(id, owner_user_id, request_id, workspace_id, base_cost, cost, plan_json) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			claim.ID, claim.Owner, claim.RequestID, claim.WorkspaceID, claim.BaseCost, claim.Cost, claim.PlanJSON); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO economy_ledger
			(resource, delta, reason, ref_kind, ref_id, workspace_id, created_at)
			SELECT 'craft', ?, ?, ?, ?, ?, ? WHERE ? = 0 OR
			(SELECT COALESCE(SUM(delta),0) FROM economy_ledger WHERE resource = 'craft') >= ?`,
			-claim.Cost, RefKindFilingPurchase, RefKindFilingPurchase, claim.ID, claim.WorkspaceID, formatTime(time.Now()), claim.Cost, claim.Cost)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrInsufficientBalance
		}
		return nil
	})
}

func (s *SQLiteStore) CompleteFiling(ctx context.Context, claim FilingClaim) error {
	result, err := s.db.ExecContext(ctx, `UPDATE economy_filing_claims SET state = 'delivered'
		WHERE id = ? AND owner_user_id = ? AND state = 'building' AND registered = 1`, claim.ID, claim.Owner)
	if err != nil {
		return err
	}
	return requireClaimTransition(result)
}

// RefundFiling can only reverse a claim that never registered its workspace.
// The distinct reversal ref and terminal state change commit together.
func (s *SQLiteStore) RefundFiling(ctx context.Context, claim FilingClaim) error {
	return s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE economy_filing_claims SET state = 'refunded'
			WHERE id = ? AND owner_user_id = ? AND state = 'building' AND registered = 0`, claim.ID, claim.Owner)
		if err != nil {
			return err
		}
		if err := requireClaimTransition(result); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO economy_ledger
			(resource, delta, reason, ref_kind, ref_id, workspace_id, created_at)
			SELECT 'craft', cost, ?, ?, id, workspace_id, ? FROM economy_filing_claims WHERE id = ?`,
			RefKindFilingRefund, RefKindFilingRefund, formatTime(time.Now()), claim.ID)
		return err
	})
}

func requireClaimTransition(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("Filing claim state changed")
	}
	return nil
}
