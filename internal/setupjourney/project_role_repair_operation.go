package setupjourney

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ProjectRoleRepairOperation is a durable *intent*, never proof that a role was
// saved or permission to write. There is no product repair writer or terminal
// transition yet. A claimed child stays occupied across restart until a
// separately specified two-mirror reconciliation protocol exists.
type ProjectRoleRepairOperation struct {
	Token          string    `json:"token"`
	OwnerID        string    `json:"owner_id"`
	HomeID         string    `json:"home_id"`
	ProjectID      string    `json:"project_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	ReviewToken    string    `json:"-"`
	EvidenceDigest string    `json:"evidence_digest"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// ClaimProjectRoleRepairOperation consumes one still-current exact-child
// inspection review atomically with a SQLite-only pending slot. It deliberately
// does not call Workspace.Save/Update, install providers, staff a role, or make
// a pending operation replayable as a write. There is no host route to it.
func (r *ProjectRoleRepairReviewer) ClaimProjectRoleRepairOperation(ctx context.Context, ownerID, homeID, projectID, reviewToken, key string) (ProjectRoleRepairOperation, error) {
	refuse := func() (ProjectRoleRepairOperation, error) {
		return ProjectRoleRepairOperation{}, ErrProjectRoleRepairUnavailable
	}
	if r == nil || r.db == nil || r.now == nil || !validateCanonicalRef(key, false) || len(key) > MaxIdempotencyKeyBytes {
		return refuse()
	}
	review, err := r.InspectPendingReview(ctx, ownerID, homeID, projectID, reviewToken)
	if err != nil {
		return refuse()
	}
	data, err := json.Marshal(review.Inspection)
	if err != nil {
		return refuse()
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	now := r.now().UTC()
	operation := ProjectRoleRepairOperation{
		Token: uuid.NewString(), OwnerID: ownerID, HomeID: homeID, ProjectID: projectID,
		IdempotencyKey: key, ReviewToken: reviewToken, EvidenceDigest: digest,
		Status: "claimed", CreatedAt: now,
	}
	err = r.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var storedDigest string
		var expires time.Time
		var consumed sql.NullTime
		if err := tx.QueryRowContext(ctx, `SELECT evidence_digest, expires_at, consumed_at FROM project_role_repair_review
			WHERE token = ? AND owner_user_id = ? AND home_id = ? AND project_id = ?`,
			reviewToken, ownerID, homeID, projectID).Scan(&storedDigest, &expires, &consumed); err != nil ||
			consumed.Valid || !expires.After(now) || storedDigest != digest {
			return ErrProjectRoleRepairUnavailable
		}
		// Reject a competing process even if it has another independently
		// reviewed token. The partial unique index is the race backstop.
		var busy string
		if err := tx.QueryRowContext(ctx, `SELECT token FROM project_role_repair_operation
			WHERE project_id = ? AND status IN ('claimed', 'reconcile_required')`, projectID).Scan(&busy); err == nil {
			return ErrProjectRoleRepairUnavailable
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE project_role_repair_review SET consumed_at = ?
			WHERE token = ? AND owner_user_id = ? AND home_id = ? AND project_id = ?
				AND evidence_digest = ? AND consumed_at IS NULL AND expires_at > ?`,
			now, reviewToken, ownerID, homeID, projectID, digest, now)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return ErrProjectRoleRepairUnavailable
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO project_role_repair_operation
			(token, owner_user_id, home_id, project_id, idempotency_key, review_token,
			evidence_digest, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'claimed', ?, ?)`,
			operation.Token, ownerID, homeID, projectID, key, reviewToken, digest, now, now)
		return err
	})
	if err != nil {
		return refuse()
	}
	return operation, nil
}

// InspectProjectRoleRepairOperation reads an exact owner's durable intent after
// an uncertain reply, including when a mirror/provider has since diverged.
// "claimed" does not authorize retrying a child write; recovery is unbuilt.
func (r *ProjectRoleRepairReviewer) InspectProjectRoleRepairOperation(ctx context.Context, ownerID, homeID, projectID, key string) (ProjectRoleRepairOperation, error) {
	refuse := func() (ProjectRoleRepairOperation, error) {
		return ProjectRoleRepairOperation{}, ErrProjectRoleRepairUnavailable
	}
	if r == nil || r.db == nil || !validateCanonicalRef(key, false) || len(key) > MaxIdempotencyKeyBytes {
		return refuse()
	}
	var operation ProjectRoleRepairOperation
	err := r.db.QueryRowContext(ctx, `SELECT token, owner_user_id, home_id, project_id, idempotency_key,
		review_token, evidence_digest, status, created_at FROM project_role_repair_operation
		WHERE owner_user_id = ? AND home_id = ? AND project_id = ? AND idempotency_key = ?`,
		ownerID, homeID, projectID, key).Scan(&operation.Token, &operation.OwnerID,
		&operation.HomeID, &operation.ProjectID, &operation.IdempotencyKey,
		&operation.ReviewToken, &operation.EvidenceDigest, &operation.Status, &operation.CreatedAt)
	if err != nil {
		return refuse()
	}
	return operation, nil
}
