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
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const projectRoleRepairReviewTTL = 10 * time.Minute

// ProjectRoleRepairReview is a persisted inspection receipt, not a staffing
// review or permission to change a child. No repair commit exists yet.
type ProjectRoleRepairReview struct {
	Token      string                      `json:"token"`
	ExpiresAt  time.Time                   `json:"expires_at"`
	Inspection ProjectRoleRepairInspection `json:"inspection"`
}

// ProjectRoleRepairReviewer is only constructed with trusted host workspace,
// SQLite and installed-plugin dependencies. Never accept a browser-provided
// installed list or a project name in place of the exact child ID.
type ProjectRoleRepairReviewer struct {
	workspaces workspace.Store
	db         *database.DB
	installed  func() ([]plugin.InstalledPlugin, error)
	now        func() time.Time
}

func NewProjectRoleRepairReviewer(workspaces workspace.Store, db *database.DB, installed func() ([]plugin.InstalledPlugin, error)) *ProjectRoleRepairReviewer {
	return &ProjectRoleRepairReviewer{workspaces: workspaces, db: db, installed: installed, now: time.Now}
}

func (r *ProjectRoleRepairReviewer) Review(ctx context.Context, ownerID, homeID, projectID, key string) (ProjectRoleRepairReview, error) {
	refuse := func() (ProjectRoleRepairReview, error) {
		return ProjectRoleRepairReview{}, ErrProjectRoleRepairUnavailable
	}
	if r == nil || r.workspaces == nil || r.db == nil || r.installed == nil || r.now == nil ||
		!validateCanonicalRef(key, false) || len(key) > MaxIdempotencyKeyBytes {
		return refuse()
	}
	inspection, digest, err := r.currentProjectRoleRepairEvidence(ctx, ownerID, homeID, projectID)
	if err != nil {
		return refuse()
	}
	now := r.now().UTC()
	var token string
	var expires time.Time
	err = r.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var busy string
		if err := tx.QueryRowContext(ctx, `SELECT token FROM project_role_repair_operation
			WHERE project_id = ? AND status IN ('claimed', 'reconcile_required')`, projectID).Scan(&busy); err == nil {
			return ErrProjectRoleRepairUnavailable
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var oldDigest string
		var consumed sql.NullTime
		readErr := tx.QueryRowContext(ctx, `SELECT token, evidence_digest, expires_at, consumed_at FROM project_role_repair_review
			WHERE owner_user_id = ? AND home_id = ? AND project_id = ? AND idempotency_key = ?`, ownerID, homeID, projectID, key).
			Scan(&token, &oldDigest, &expires, &consumed)
		if readErr == nil {
			if oldDigest != digest || consumed.Valid || !expires.After(now) {
				return ErrProjectRoleRepairUnavailable
			}
			return nil
		}
		if !errors.Is(readErr, sql.ErrNoRows) {
			return readErr
		}
		// Keep expired keys as tombstones: pruning them would let a stale
		// browser retry reissue an old key as fresh consent. Bound both live
		// reviews and the lifetime total instead.
		var total, pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN consumed_at IS NULL AND expires_at > ? THEN 1 ELSE 0 END), 0)
			FROM project_role_repair_review WHERE owner_user_id = ? AND home_id = ? AND project_id = ?`, now, ownerID, homeID, projectID).
			Scan(&total, &pending); err != nil || total >= 4096 || pending >= 32 {
			return ErrProjectRoleRepairUnavailable
		}
		token, expires = uuid.NewString(), now.Add(projectRoleRepairReviewTTL)
		_, err := tx.ExecContext(ctx, `INSERT INTO project_role_repair_review
			(token, owner_user_id, home_id, project_id, idempotency_key, evidence_digest, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, token, ownerID, homeID, projectID, key, digest, now, expires)
		return err
	})
	if err != nil {
		return refuse()
	}
	return ProjectRoleRepairReview{Token: token, ExpiresAt: expires, Inspection: inspection}, nil
}

// InspectPendingReview is an inert exact-owner/read-only recheck. It is NOT a
// commit or consume operation, and the checks across SQLite, provider state
// and workspace mirrors are not a distributed transaction. A future commit
// must redo them under its own fenced operation/recovery protocol.
func (r *ProjectRoleRepairReviewer) InspectPendingReview(ctx context.Context, ownerID, homeID, projectID, token string) (ProjectRoleRepairReview, error) {
	refuse := func() (ProjectRoleRepairReview, error) {
		return ProjectRoleRepairReview{}, ErrProjectRoleRepairUnavailable
	}
	if r == nil || r.db == nil || r.now == nil {
		return refuse()
	}
	parsed, err := uuid.Parse(token)
	if err != nil || parsed.String() != token {
		return refuse()
	}
	var storedDigest string
	var expires time.Time
	var consumed sql.NullTime
	err = r.db.QueryRowContext(ctx, `SELECT evidence_digest, expires_at, consumed_at FROM project_role_repair_review
		WHERE owner_user_id = ? AND home_id = ? AND project_id = ? AND token = ?`, ownerID, homeID, projectID, token).
		Scan(&storedDigest, &expires, &consumed)
	if err != nil || consumed.Valid || !expires.After(r.now().UTC()) {
		return refuse()
	}
	inspection, currentDigest, err := r.currentProjectRoleRepairEvidence(ctx, ownerID, homeID, projectID)
	if err != nil || storedDigest != currentDigest {
		return refuse()
	}
	return ProjectRoleRepairReview{Token: token, ExpiresAt: expires, Inspection: inspection}, nil
}

// currentProjectRoleRepairEvidence binds both the complete portable snapshot
// and the original reviewed creator receipts. This method reads only.
func (r *ProjectRoleRepairReviewer) currentProjectRoleRepairEvidence(ctx context.Context, ownerID, homeID, projectID string) (ProjectRoleRepairInspection, string, error) {
	refuse := func() (ProjectRoleRepairInspection, string, error) {
		return ProjectRoleRepairInspection{}, "", ErrProjectRoleRepairUnavailable
	}
	if r == nil || r.workspaces == nil || r.db == nil || r.installed == nil {
		return refuse()
	}
	installed, err := r.installed()
	if err != nil {
		return refuse()
	}
	inspection, err := InspectMissingSplitProjectRoles(r.workspaces, installed, ownerID, homeID, projectID)
	if err != nil || !r.originalReviewedProjectConnection(ctx, ownerID, homeID, projectID, inspection) {
		return refuse()
	}
	data, err := json.Marshal(inspection)
	if err != nil {
		return refuse()
	}
	sum := sha256.Sum256(data)
	return inspection, hex.EncodeToString(sum[:]), nil
}
