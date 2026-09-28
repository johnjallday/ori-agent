package dailybrief

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// ContinuityConfig is a closed portable allowlist, separate from live Config.
// Enable/notification/scope values here are source intent, not local permission.
// The original remains in the checkpoint/report when the destination is bounded.
type ContinuityConfig struct {
	Version                 int       `json:"version"`
	WorkspaceID             string    `json:"workspace_id"`
	SourceUserID            string    `json:"source_user_id"`
	Timezone                string    `json:"timezone"`
	ScheduleDays            []string  `json:"schedule_days"`
	ScheduleTime            string    `json:"schedule_time"`
	ScheduleEnabled         bool      `json:"schedule_enabled"`
	Scope                   Scope     `json:"scope"`
	SelectedWorkspaceIDs    []string  `json:"selected_workspace_ids"`
	IncludeFutureWorkspaces bool      `json:"include_future_workspaces"`
	NotifyOnReady           bool      `json:"notify_on_ready"`
	ConfigRevision          int       `json:"config_revision"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (c ContinuityConfig) validate() error {
	if c.Version != workspacecontinuity.Version {
		return workspacecontinuity.ErrVersion
	}
	if !workspacecontinuity.ValidID(c.WorkspaceID) || c.SourceUserID != userprofile.LocalUserID ||
		c.ConfigRevision < 1 || c.ConfigRevision >= math.MaxInt32 || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return workspacecontinuity.ErrInvalid
	}
	// Do not call NormalizeConfig: it fabricates defaults, drops invalid days
	// and converts an unknown scope to all. Imported omissions are not choices.
	if c.Timezone == "" || c.Timezone == "Local" || len(c.Timezone) > 256 || strings.TrimSpace(c.Timezone) != c.Timezone {
		return workspacecontinuity.ErrInvalid
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return workspacecontinuity.ErrInvalid
	}
	if parsed, err := time.Parse("15:04", c.ScheduleTime); err != nil || parsed.Format("15:04") != c.ScheduleTime {
		return workspacecontinuity.ErrInvalid
	}
	if len(c.ScheduleDays) == 0 || len(c.ScheduleDays) > 7 || len(c.SelectedWorkspaceIDs) > 1024 {
		return workspacecontinuity.ErrLimit
	}
	seen := map[string]bool{}
	for _, day := range c.ScheduleDays {
		if !validDays[day] || seen[day] {
			return workspacecontinuity.ErrInvalid
		}
		seen[day] = true
	}
	if c.Scope != ScopeAll && c.Scope != ScopeSelected {
		return workspacecontinuity.ErrInvalid
	}
	seen = map[string]bool{}
	for _, id := range c.SelectedWorkspaceIDs {
		if !workspacecontinuity.ValidID(id) || seen[id] {
			return workspacecontinuity.ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

// SnapshotContinuityConfig reads at most one configuration through the shared
// checkpoint read view. nil means absent, not a recovered default configuration.
// It never calls the store pool while the coordinator holds its read transaction.
func (s *SQLiteStore) SnapshotContinuityConfig(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string) (*workspacecontinuity.Record, error) {
	if query == nil || !workspacecontinuity.ValidID(workspaceID) {
		return nil, workspacecontinuity.ErrInvalid
	}
	var c ContinuityConfig
	c.Version = workspacecontinuity.Version
	var days, selected string
	var enabled, future, notify int
	var owner sql.NullString
	err := query.QueryRowContext(ctx, `SELECT c.workspace_id,c.user_id,c.timezone,c.schedule_days,c.schedule_time,
		c.schedule_enabled,c.scope,c.selected_workspace_ids,c.include_future_workspaces,c.notify_on_ready,
		c.config_revision,c.created_at,c.updated_at,w.owner_user_id FROM daily_brief_config c
		LEFT JOIN workspaces w ON w.id=c.workspace_id AND w.deleted_at IS NULL WHERE c.workspace_id=?`, workspaceID).Scan(
		&c.WorkspaceID, &c.SourceUserID, &c.Timezone, &days, &c.ScheduleTime, &enabled, &c.Scope, &selected,
		&future, &notify, &c.ConfigRevision, &c.CreatedAt, &c.UpdatedAt, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !owner.Valid || owner.String != c.SourceUserID || len(days) > 128 || len(selected) > workspacecontinuity.MaxRecordBytes ||
		(enabled != 0 && enabled != 1) || (future != 0 && future != 1) || (notify != 0 && notify != 1) {
		return nil, workspacecontinuity.ErrInvalid
	}
	if json.Unmarshal([]byte(days), &c.ScheduleDays) != nil || json.Unmarshal([]byte(selected), &c.SelectedWorkspaceIDs) != nil {
		return nil, workspacecontinuity.ErrInvalid
	}
	c.ScheduleEnabled, c.IncludeFutureWorkspaces, c.NotifyOnReady = enabled == 1, future == 1, notify == 1
	if err := c.validate(); err != nil {
		return nil, err
	}
	record, err := workspacecontinuity.EncodeRecord(workspaceID, c)
	return &record, err
}

// CollectContinuityConfig adds the workspace's configuration to the spool, or
// declares the component explicitly empty when this workspace has none.
func (s *SQLiteStore) CollectContinuityConfig(ctx context.Context, query workspacecontinuity.Queryer, workspaceID string, spool *workspacecontinuity.Spool) error {
	if spool == nil {
		return workspacecontinuity.ErrInvalid
	}
	record, err := s.SnapshotContinuityConfig(ctx, query, workspaceID)
	if err != nil {
		return err
	}
	if record == nil {
		return spool.SetAvailability(ctx, "brief_config", workspacecontinuity.Empty, "")
	}
	return spool.AddRecord(ctx, "brief_config", "configs", *record)
}

// DecodeContinuityConfig is shared by read-only review and restoration. No
// incoming field is normalized away or interpreted as authorization.
func DecodeContinuityConfig(record workspacecontinuity.Record) (ContinuityConfig, error) {
	var c ContinuityConfig
	if err := workspacecontinuity.DecodeRecord(record, &c); err != nil {
		return c, err
	}
	if record.ID != c.WorkspaceID {
		return c, workspacecontinuity.ErrInvalid
	}
	return c, c.validate()
}

// RestoreContinuityConfig inserts settings without UpsertConfig's new dates or
// revision changes. The caller must roll back tx on ANY error, including errors
// after the record claim. false,nil is an exact receipt-owned no-op, even after
// local edits/deletion. Automatic generation still needs the separate admission
// gate (first-open does not consult ScheduleEnabled).
func (s *SQLiteStore) RestoreContinuityConfig(ctx context.Context, tx *sql.Tx, scope workspacecontinuity.RestoreScope, record workspacecontinuity.Record) (bool, error) {
	c, err := DecodeContinuityConfig(record)
	if err != nil {
		return false, err
	}
	if c.WorkspaceID != scope.WorkspaceID || scope.UserID != userprofile.LocalUserID {
		return false, workspacecontinuity.ErrInvalid
	}
	claimed, err := workspacecontinuity.ClaimRecord(ctx, tx, scope, "brief_config", "configs", record.ID, workspacecontinuity.Digest(record.Data))
	if err != nil || !claimed {
		return false, err
	}
	if err := workspacecontinuity.RequireOwnedWorkspace(ctx, tx, scope); err != nil {
		return false, err
	}
	members, err := workspacecontinuity.ImportWorkspaceIDs(ctx, tx, scope)
	if err != nil {
		return false, err
	}
	allowed := make(map[string]bool, len(members))
	for _, id := range members {
		allowed[id] = true
	}
	selected := make([]string, 0, len(members))
	if c.Scope == ScopeAll {
		for _, id := range members {
			// Preserve the source's frozen-all cutoff as well as the import
			// tree bound. A later-created child did not belong to that scope.
			if !c.IncludeFutureWorkspaces {
				var created time.Time
				if err := tx.QueryRowContext(ctx, `SELECT created_at FROM workspaces WHERE id=? AND owner_user_id=? AND deleted_at IS NULL`, id, scope.UserID).Scan(&created); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return false, workspacecontinuity.ErrConflict
					}
					return false, err
				}
				if created.After(c.UpdatedAt) {
					continue
				}
			}
			selected = append(selected, id)
		}
	} else {
		for _, id := range c.SelectedWorkspaceIDs {
			if allowed[id] {
				selected = append(selected, id)
			}
		}
	}
	// Every reviewed member is bounded; no destination-wide or future scope.
	daysJSON, err := json.Marshal(c.ScheduleDays)
	if err != nil {
		return false, workspacecontinuity.ErrInvalid
	}
	selectedJSON, err := json.Marshal(selected)
	if err != nil {
		return false, workspacecontinuity.ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO daily_brief_config
		(workspace_id,user_id,timezone,schedule_days,schedule_time,schedule_enabled,scope,selected_workspace_ids,
		include_future_workspaces,notify_on_ready,config_revision,created_at,updated_at)
		VALUES (?,?,?,?,?,0,'selected',?,0,0,?,?,?)`, scope.WorkspaceID, scope.UserID, c.Timezone, string(daysJSON),
		c.ScheduleTime, string(selectedJSON), c.ConfigRevision, c.CreatedAt, c.UpdatedAt)
	if isUniqueConstraintError(err) {
		return false, workspacecontinuity.ErrConflict
	}
	return err == nil, err
}
