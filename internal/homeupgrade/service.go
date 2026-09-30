package homeupgrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Service reviews, commits and recovers Home package upgrades.
type Service struct {
	db         *database.DB
	workspaces workspace.Store
	plugins    Plugins
	profiles   Profiles
	now        func() time.Time

	mu sync.Mutex
	// inflight holds operations a Commit in this process is still driving, so
	// a concurrent recovery does not mistake them for crashed ones.
	inflight map[string]bool
}

// New wires the service. workspaces must be the host's mirrored store.
func New(db *database.DB, workspaces workspace.Store, plugins Plugins, profiles Profiles) *Service {
	return &Service{db: db, workspaces: workspaces, plugins: plugins, profiles: profiles, now: time.Now, inflight: map[string]bool{}}
}

func (s *Service) ready() bool {
	return s != nil && s.db != nil && s.workspaces != nil && s.plugins != nil && s.profiles != nil && s.now != nil
}

// Review derives the plan and records a ten-minute review bound to its digest.
func (s *Service) Review(ctx context.Context, ownerID, pluginID string) (Review, error) {
	if !s.ready() || strings.TrimSpace(ownerID) == "" || strings.TrimSpace(pluginID) == "" {
		return Review{}, ErrUnavailable
	}
	if err := s.Recover(ctx); err != nil {
		return Review{}, errors.Join(ErrUnavailable, err)
	}
	if active, err := s.activeOperation(ctx, pluginID); err != nil {
		return Review{}, errors.Join(ErrUnavailable, err)
	} else if active != nil {
		return Review{}, ErrUpgradeActive
	}
	plan, target, err := s.derivePlan(ctx, ownerID, pluginID)
	if err != nil {
		return Review{}, err
	}
	digest, err := planDigest(plan)
	if err != nil {
		return Review{}, errors.Join(ErrUnavailable, err)
	}
	now := s.now().UTC()
	review := Review{Token: uuid.NewString(), ExpiresAt: now.Add(reviewTTL), Plan: plan, Trust: target.Inspection.Report}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO home_package_upgrade_review
		(token, owner_user_id, plugin_id, plan_digest, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		review.Token, ownerID, plan.PluginID, digest, now, review.ExpiresAt); err != nil {
		return Review{}, errors.Join(ErrUnavailable, err)
	}
	return review, nil
}

// Commit re-derives the plan, claims it against the review, and drives the
// operation to succeeded (or cancelled/reconcile_required). It returns the
// operation's final record; an error means the review could not be claimed or
// the operation stopped.
func (s *Service) Commit(ctx context.Context, ownerID, pluginID, reviewToken string) (Operation, error) {
	if !s.ready() || strings.TrimSpace(ownerID) == "" || strings.TrimSpace(pluginID) == "" || strings.TrimSpace(reviewToken) == "" {
		return Operation{}, ErrUnavailable
	}
	if err := s.Recover(ctx); err != nil {
		return Operation{}, errors.Join(ErrUnavailable, err)
	}
	plan, target, err := s.derivePlan(ctx, ownerID, pluginID)
	if err != nil {
		return Operation{}, err
	}
	// Mark the operation in flight before it exists, so a concurrent recovery
	// can never see it claimed but undriven.
	operationID := uuid.NewString()
	s.mu.Lock()
	s.inflight[operationID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, operationID)
		s.mu.Unlock()
	}()
	operation, err := s.claim(ctx, operationID, ownerID, reviewToken, plan)
	if err != nil {
		return Operation{}, err
	}
	return s.drive(ctx, operation, &target)
}

// Status recovers interrupted operations, then returns the newest operation
// for the package, or nil when there has never been one.
func (s *Service) Status(ctx context.Context, ownerID, pluginID string) (*Operation, error) {
	if !s.ready() {
		return nil, ErrUnavailable
	}
	if err := s.Recover(ctx); err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	operations, err := s.operations(ctx, `WHERE owner_user_id = ? AND plugin_id = ? ORDER BY created_at DESC LIMIT 1`, ownerID, pluginID)
	if err != nil || len(operations) == 0 {
		return nil, err
	}
	return &operations[0], nil
}

// Recover settles every claimed or replaced operation this process is not
// already driving: see independent-program-homes.md §6.1 Recovery.
func (s *Service) Recover(ctx context.Context) error {
	if !s.ready() {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	operations, err := s.operations(ctx, `WHERE status IN ('claimed', 'replaced') ORDER BY created_at`)
	if err != nil {
		return err
	}
	var failures []error
	for _, operation := range operations {
		if s.inflight[operation.ID] {
			continue
		}
		if _, err := s.drive(ctx, operation, nil); err != nil {
			failures = append(failures, fmt.Errorf("home package upgrade %s: %w", operation.ID, err))
		}
	}
	return errors.Join(failures...)
}

// AllowsReplacement is the replacement guard's one exception: while an
// operation is claimed, exactly its from→to replacement of the package may
// proceed. Every other replacement of a package some Home is pinned to stays
// refused.
func (s *Service) AllowsReplacement(current plugin.InstalledPlugin, nextVersion, nextFingerprint string) bool {
	if !s.ready() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	operations, err := s.operations(ctx, `WHERE plugin_id = ? AND status = 'claimed'`, current.Name)
	if err != nil || len(operations) != 1 {
		return false
	}
	plan := operations[0].Plan
	return plan.FromVersion == current.Version && plan.FromFingerprint == current.ComponentFingerprint &&
		plan.FromGeneration == current.EvidenceGeneration() && plan.ToVersion == nextVersion && plan.ToFingerprint == nextFingerprint
}

func (s *Service) claim(ctx context.Context, operationID, ownerID, reviewToken string, plan Plan) (Operation, error) {
	digest, err := planDigest(plan)
	if err != nil {
		return Operation{}, errors.Join(ErrUnavailable, err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Operation{}, errors.Join(ErrUnavailable, err)
	}
	now := s.now().UTC()
	operation := Operation{ID: operationID, PluginID: plan.PluginID, Status: StatusClaimed, Plan: plan, CreatedAt: now, UpdatedAt: now}
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		var busy string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM home_package_upgrade_operation
			WHERE plugin_id = ? AND status IN ('claimed', 'replaced', 'reconcile_required')`, plan.PluginID).Scan(&busy); err == nil {
			return ErrUpgradeActive
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE home_package_upgrade_review SET consumed_at = ?
			WHERE token = ? AND owner_user_id = ? AND plugin_id = ? AND plan_digest = ?
				AND consumed_at IS NULL AND expires_at > ?`,
			now, reviewToken, ownerID, plan.PluginID, digest, now)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrReviewStale
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO home_package_upgrade_operation
			(id, owner_user_id, plugin_id, review_token, plan_digest, plan_json, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'claimed', ?, ?)`,
			operation.ID, ownerID, plan.PluginID, reviewToken, digest, string(planJSON), now, now)
		return err
	})
	switch {
	case errors.Is(err, ErrUpgradeActive), errors.Is(err, ErrReviewStale):
		return Operation{}, err
	case err != nil:
		// The partial unique index is the race backstop for a competing claim.
		if active, activeErr := s.activeOperation(ctx, plan.PluginID); activeErr == nil && active != nil {
			return Operation{}, ErrUpgradeActive
		}
		return Operation{}, errors.Join(ErrUnavailable, err)
	}
	return operation, nil
}

// transition moves an operation from exactly one status to the next.
func (s *Service) transition(ctx context.Context, operation *Operation, from, to string) error {
	outcome, err := json.Marshal(operation.Outcome)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE home_package_upgrade_operation
		SET status = ?, target_generation = ?, outcome_json = ?, updated_at = ? WHERE id = ? AND status = ?`,
		to, int64(operation.TargetGeneration), string(outcome), now, operation.ID, from) // #nosec G115 -- generations are small counters
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("operation %s is no longer %s", operation.ID, from)
	}
	operation.Status, operation.UpdatedAt = to, now
	return nil
}

func (s *Service) activeOperation(ctx context.Context, pluginID string) (*Operation, error) {
	operations, err := s.operations(ctx, `WHERE plugin_id = ? AND status IN ('claimed', 'replaced', 'reconcile_required')`, pluginID)
	if err != nil || len(operations) == 0 {
		return nil, err
	}
	return &operations[0], nil
}

func (s *Service) operations(ctx context.Context, where string, args ...any) ([]Operation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, plugin_id, status, plan_json, target_generation, outcome_json, created_at, updated_at
		FROM home_package_upgrade_operation `+where, args...) // #nosec G202 -- where is a constant clause from this file
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var operations []Operation
	for rows.Next() {
		var operation Operation
		var planJSON, outcomeJSON string
		var generation int64
		if err := rows.Scan(&operation.ID, &operation.PluginID, &operation.Status, &planJSON, &generation, &outcomeJSON,
			&operation.CreatedAt, &operation.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(planJSON), &operation.Plan); err != nil {
			return nil, fmt.Errorf("operation %s plan: %w", operation.ID, err)
		}
		if outcomeJSON != "" {
			if err := json.Unmarshal([]byte(outcomeJSON), &operation.Outcome); err != nil {
				return nil, fmt.Errorf("operation %s outcome: %w", operation.ID, err)
			}
		}
		operation.TargetGeneration = uint64(max(generation, 0)) // #nosec G115 -- clamped non-negative
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

// repairActive reports a claimed or unreconciled project role repair under any
// of the Homes. An upgrade must not move pins a repair is still bound to.
func (s *Service) repairActive(ctx context.Context, homeIDs []string) (bool, error) {
	if len(homeIDs) == 0 {
		return false, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(homeIDs)), ", ")
	args := make([]any, 0, len(homeIDs))
	for _, id := range homeIDs {
		args = append(args, id)
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_role_repair_operation
		WHERE status IN ('claimed', 'reconcile_required') AND home_id IN (`+placeholders+`)`, args...).Scan(&count) // #nosec G202 -- placeholders only
	return count > 0, err
}

func promptOf(profile *agent.Agent) string {
	if profile == nil {
		return ""
	}
	return profile.Settings.SystemPrompt
}
