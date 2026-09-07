package agentmap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

// ErrStoreUnavailable means no layout storage is wired. The Map treats it the
// same as a failed read: deterministic automatic placement, read-only
// navigation, and an honest explanation that positions cannot be saved.
var ErrStoreUnavailable = errors.New("agent map layout storage is not configured")

// AgentLister reports which agents currently exist, so the read path can ignore
// a saved position whose agent is gone (FR-57).
//
// It is an interface rather than a concrete store because this package must not
// depend on the agent store, and because "which agents exist" is the only agent
// question the map is allowed to ask. It cannot read a role, a model, or a
// prompt, and it cannot write anything at all.
type AgentLister interface {
	ListAgents() []string
}

// SQLiteStore persists one coordinate layout per user.
//
// Reads are tolerant and never write: a corrupt anchor costs that one agent its
// saved position and nothing else, and merely looking at a map that has no
// saved positions must not mark anything as modified.
//
// Writes are partial and transactional: a patch merges against the latest
// stored record, bumps one revision, and either commits every row it touches or
// none of them.
type SQLiteStore struct {
	db     *database.DB
	lister AgentLister
}

// NewSQLiteStore builds a layout store over the shared application database.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// SetAgentLister wires the existence check the read path uses to drop orphans.
// Without it, every stored position is returned — which is the safe direction:
// an unknown extra anchor renders nothing, while dropping a live agent's
// position would move a tile the user placed.
func (s *SQLiteStore) SetAgentLister(lister AgentLister) {
	if s == nil {
		return
	}
	s.lister = lister
}

// Load returns the current user's layout.
//
// Every read is best-effort per field. An unreadable anchor, an unreadable
// camera axis, or an unreadable preference degrades to the deterministic
// default for that one thing while every valid sibling survives intact. The one
// deliberate exception is the schema version: a record written by a format this
// build does not understand is refused outright, because reading it as "empty"
// would invite the very next write to overwrite a newer format with this one
// (FR-54).
func (s *SQLiteStore) Load(ctx context.Context, userID string) (Layout, error) {
	if s == nil || s.db == nil {
		return Layout{}, ErrStoreUnavailable
	}
	userID = normalizeUserID(userID)

	layout := NewLayout()
	var (
		schemaVersionRaw any
		revisionRaw      any
		centerXRaw       any
		centerYRaw       any
		zoomRaw          any
		snapRaw          any
		updatedAtRaw     any
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT schema_version, revision, viewport_center_x, viewport_center_y, viewport_zoom, snap_to_grid, updated_at
		FROM agent_map_layouts
		WHERE user_id = ?
	`, userID).Scan(&schemaVersionRaw, &revisionRaw, &centerXRaw, &centerYRaw, &zoomRaw, &snapRaw, &updatedAtRaw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No record yet. This is the ordinary state for a user who has never
		// moved anything; it is not an error and must not provoke a write.
		return layout, nil
	case err != nil:
		return Layout{}, fmt.Errorf("failed to read agent map layout: %w", err)
	}

	storedVersion, ok := intValue(schemaVersionRaw)
	if !ok || !IsSupportedSchemaVersion(int(storedVersion)) {
		return Layout{}, fmt.Errorf("%w: stored layout reports version %v", ErrUnsupportedSchemaVersion, schemaVersionRaw)
	}
	if revision, ok := intValue(revisionRaw); ok && revision > 0 {
		layout.Revision = revision
	}
	if snap, ok := boolValue(snapRaw); ok {
		layout.SnapToGrid = snap
	}
	if updatedAt, ok := timeValue(updatedAtRaw); ok {
		layout.UpdatedAt = updatedAt
	}
	if viewport, ok := readViewport(centerXRaw, centerYRaw, zoomRaw); ok {
		layout.Viewport = &viewport
	}

	positions, err := s.loadPositions(ctx, userID)
	if err != nil {
		return Layout{}, err
	}
	layout.Positions = positions
	return layout, nil
}

// loadPositions reads every anchor this user has saved, dropping the ones that
// cannot be rendered.
//
// A position for an agent that no longer exists is ignored rather than
// returned, so an orphan left by any path — a delete that missed its cleanup, a
// hand-edited database, an agent removed by another process — is harmless
// rather than corrupting (FR-57). The delete path also removes them; this is
// the safety net that makes that path's failure survivable.
func (s *SQLiteStore) loadPositions(ctx context.Context, userID string) (map[string]Point, error) {
	positions := map[string]Point{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_name, x, y
		FROM agent_map_positions
		WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent map positions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	live := s.liveAgents()
	for rows.Next() {
		var (
			name string
			xRaw any
			yRaw any
		)
		if err := rows.Scan(&name, &xRaw, &yRaw); err != nil {
			// One unreadable row is one tile on automatic placement, not a lost
			// map. Keep reading.
			continue
		}
		agentName, err := NormalizeAgentName(name)
		if err != nil {
			continue
		}
		if live != nil && !live[strings.ToLower(agentName)] {
			continue
		}
		x, xOK := floatValue(xRaw)
		y, yOK := floatValue(yRaw)
		if !xOK || !yOK {
			continue
		}
		point := Point{X: x, Y: y}
		if !point.InSafeRange() {
			continue
		}
		positions[agentName] = point
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read agent map positions: %w", err)
	}
	return positions, nil
}

// liveAgents returns the lowercased set of existing agent names, or nil when no
// lister is wired — in which case the caller keeps every stored position.
func (s *SQLiteStore) liveAgents() map[string]bool {
	if s.lister == nil {
		return nil
	}
	names := s.lister.ListAgents()
	live := make(map[string]bool, len(names))
	for _, name := range names {
		live[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return live
}

// Apply commits a partial patch and returns the layout it produced.
//
// Everything happens in one transaction against the latest stored record, so a
// stale tab that moved one tile cannot replace coordinates it never touched,
// and a reset-undo either lands whole or not at all. Each accepted call
// produces exactly one new revision, which the caller echoes back so both the
// client and the store reconcile against what was actually stored rather than
// what was hoped (FR-53).
func (s *SQLiteStore) Apply(ctx context.Context, userID string, patch Patch) (Result, error) {
	if s == nil || s.db == nil {
		return Result{}, ErrStoreUnavailable
	}
	normalized, err := NormalizePatch(patch)
	if err != nil {
		return Result{}, err
	}
	userID = normalizeUserID(userID)

	var result Result
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		state, err := s.beginLayout(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		// The staleness check happens inside the transaction, against the row
		// this write will update. Checking it outside would leave a window in
		// which another write lands between the check and the commit — which is
		// the exact race the revision exists to catch.
		if normalized.ExpectedRevision != 0 && normalized.ExpectedRevision != state.revision {
			return fmt.Errorf("%w: expected revision %d, stored revision is %d",
				ErrStaleRevision, normalized.ExpectedRevision, state.revision)
		}
		for i, op := range normalized.Operations {
			if err := s.applyOperation(ctx, tx, userID, now, op, state); err != nil {
				return fmt.Errorf("operation %d (%s): %w", i, op.Kind, err)
			}
		}
		if err := s.enforceLayoutSize(ctx, tx, userID); err != nil {
			return err
		}
		state.revision++
		if err := s.writeLayout(ctx, tx, userID, now, state); err != nil {
			return err
		}
		// The whole stored layout is read back rather than assembled from the
		// patch, so the client adopts what is actually persisted.
		committed, err := s.readPositionsTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		result = Result{Layout: Layout{
			SchemaVersion: SchemaVersion,
			Revision:      state.revision,
			Positions:     committed,
			Viewport:      state.viewport,
			SnapToGrid:    state.snapToGrid,
			UpdatedAt:     now,
		}}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// layoutState is the working copy of one user's layout row during a patch.
// Position rows are written as the operations run; these header fields are
// written once at the end so a single failed operation rolls the whole patch
// back rather than leaving a bumped revision over unchanged coordinates.
type layoutState struct {
	revision   int64
	viewport   *Viewport
	snapToGrid bool
}

// beginLayout makes sure the user has a layout row and returns its current
// values. The row is created on first write, never on a read.
func (s *SQLiteStore) beginLayout(ctx context.Context, tx *sql.Tx, userID string, now time.Time) (*layoutState, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT(user_id) DO NOTHING
	`, userID, SchemaVersion, boolToInt(DefaultSnapToGrid), now, now); err != nil {
		return nil, fmt.Errorf("failed to create agent map layout: %w", err)
	}

	var (
		schemaVersionRaw any
		revisionRaw      any
		centerXRaw       any
		centerYRaw       any
		zoomRaw          any
		snapRaw          any
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT schema_version, revision, viewport_center_x, viewport_center_y, viewport_zoom, snap_to_grid
		FROM agent_map_layouts
		WHERE user_id = ?
	`, userID).Scan(&schemaVersionRaw, &revisionRaw, &centerXRaw, &centerYRaw, &zoomRaw, &snapRaw); err != nil {
		return nil, fmt.Errorf("failed to read agent map layout: %w", err)
	}

	storedVersion, ok := intValue(schemaVersionRaw)
	if !ok || !IsSupportedSchemaVersion(int(storedVersion)) {
		return nil, fmt.Errorf("%w: stored layout reports version %v", ErrUnsupportedSchemaVersion, schemaVersionRaw)
	}

	state := &layoutState{snapToGrid: DefaultSnapToGrid}
	if revision, ok := intValue(revisionRaw); ok && revision > 0 {
		state.revision = revision
	}
	if snap, ok := boolValue(snapRaw); ok {
		state.snapToGrid = snap
	}
	if viewport, ok := readViewport(centerXRaw, centerYRaw, zoomRaw); ok {
		state.viewport = &viewport
	}
	return state, nil
}

func (s *SQLiteStore) applyOperation(ctx context.Context, tx *sql.Tx, userID string, now time.Time, op Operation, state *layoutState) error {
	switch op.Kind {
	case OpSetPositions:
		return s.writePositions(ctx, tx, userID, now, op.Positions)

	case OpSetViewport:
		viewport := *op.Viewport
		state.viewport = &viewport
		return nil

	case OpSetPreferences:
		state.snapToGrid = *op.SnapToGrid
		return nil

	case OpReset:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM agent_map_positions WHERE user_id = ?
		`, userID); err != nil {
			return fmt.Errorf("failed to reset agent map positions: %w", err)
		}
		// Reset clears the ARRANGEMENT. The snap preference and the camera are
		// deliberately left alone: neither is part of where the tiles are, and
		// having to re-find your view after a reset would make it a thing users
		// learn to fear.
		return nil

	case OpRestorePositions:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM agent_map_positions WHERE user_id = ?
		`, userID); err != nil {
			return fmt.Errorf("failed to clear agent map positions: %w", err)
		}
		return s.writePositions(ctx, tx, userID, now, op.Positions)

	default:
		return fmt.Errorf("%w: unhandled operation %q", ErrInvalidPatch, op.Kind)
	}
}

func (s *SQLiteStore) writePositions(ctx context.Context, tx *sql.Tx, userID string, now time.Time, positions map[string]Point) error {
	for name, point := range positions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_map_positions (user_id, agent_name, x, y, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id, agent_name) DO UPDATE SET
				x = excluded.x,
				y = excluded.y,
				updated_at = excluded.updated_at
		`, userID, name, point.X, point.Y, now); err != nil {
			return fmt.Errorf("failed to save position for %q: %w", name, err)
		}
	}
	return nil
}

// readPositionsTx reads the stored anchors inside the write transaction, so the
// response reports the committed state rather than the requested one.
//
// It applies the SAME orphan and range filtering as Load. A write response the
// client adopts has to match what its next read would return, or the client
// ends up holding a name the server will not accept: an orphaned row was
// reported here as stored, echoed back in the next patch, and refused as an
// unknown agent — a save failure caused entirely by the response that preceded
// it.
func (s *SQLiteStore) readPositionsTx(ctx context.Context, tx *sql.Tx, userID string) (map[string]Point, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT agent_name, x, y FROM agent_map_positions WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read agent map positions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	live := s.liveAgents()
	positions := map[string]Point{}
	for rows.Next() {
		var (
			name string
			x, y float64
		)
		if err := rows.Scan(&name, &x, &y); err != nil {
			continue
		}
		agentName, nameErr := NormalizeAgentName(name)
		if nameErr != nil {
			continue
		}
		if live != nil && !live[strings.ToLower(agentName)] {
			continue
		}
		point := Point{X: x, Y: y}
		if !point.InSafeRange() {
			continue
		}
		positions[agentName] = point
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read agent map positions: %w", err)
	}
	return positions, nil
}

// DeletePositions removes the saved anchors for the given agents.
//
// Called from the agent delete path so a deleted agent leaves no orphan row
// (FR-57). It is not an error to name an agent with no stored position.
func (s *SQLiteStore) DeletePositions(ctx context.Context, names ...string) error {
	if s == nil || s.db == nil {
		return ErrStoreUnavailable
	}
	for _, raw := range names {
		name, err := NormalizeAgentName(raw)
		if err != nil {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			DELETE FROM agent_map_positions WHERE agent_name = ?
		`, name); err != nil {
			return fmt.Errorf("failed to delete agent map position for %q: %w", name, err)
		}
	}
	return nil
}

// RenamePosition carries a saved anchor from one agent name to another.
//
// The rename path saves the new record and then deletes the old one, which is
// the exact shape that has twice destroyed per-agent data in this repository.
// The position row is keyed by name and would be orphaned by default, so it is
// explicitly carried (FR-58).
//
// A renameable agent is always library-only — PATCH /api/agents/{name} rejects
// a rename for any agent attached to a workspace — so there is no membership
// state to reconcile alongside it.
func (s *SQLiteStore) RenamePosition(ctx context.Context, oldName, newName string) error {
	if s == nil || s.db == nil {
		return ErrStoreUnavailable
	}
	from, err := NormalizeAgentName(oldName)
	if err != nil {
		return err
	}
	to, err := NormalizeAgentName(newName)
	if err != nil {
		return err
	}
	if from == to {
		return nil
	}
	// UPDATE OR REPLACE, not a bare UPDATE: if the destination name somehow
	// already holds a row, a plain UPDATE would fail the primary key and abort
	// the rename, stranding the position under a name that no longer exists.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE OR REPLACE agent_map_positions SET agent_name = ? WHERE agent_name = ?
	`, to, from); err != nil {
		return fmt.Errorf("failed to carry agent map position from %q to %q: %w", from, to, err)
	}
	return nil
}

func (s *SQLiteStore) enforceLayoutSize(ctx context.Context, tx *sql.Tx, userID string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM agent_map_positions WHERE user_id = ?
	`, userID).Scan(&count); err != nil {
		return fmt.Errorf("failed to count agent map positions: %w", err)
	}
	if count > MaxPositionsPerLayout {
		return fmt.Errorf("%w: %d stored positions exceeds %d", ErrPatchTooLarge, count, MaxPositionsPerLayout)
	}
	return nil
}

func (s *SQLiteStore) writeLayout(ctx context.Context, tx *sql.Tx, userID string, now time.Time, state *layoutState) error {
	var centerX, centerY, zoom any
	if state.viewport != nil {
		centerX, centerY, zoom = state.viewport.CenterX, state.viewport.CenterY, state.viewport.Zoom
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_map_layouts
		SET schema_version = ?,
			revision = ?,
			viewport_center_x = ?,
			viewport_center_y = ?,
			viewport_zoom = ?,
			snap_to_grid = ?,
			updated_at = ?
		WHERE user_id = ?
	`, SchemaVersion, state.revision, centerX, centerY, zoom, boolToInt(state.snapToGrid), now, userID); err != nil {
		return fmt.Errorf("failed to save agent map layout: %w", err)
	}
	return nil
}

// readViewport rebuilds the camera from three nullable columns. All three must
// be present and usable together: a camera missing an axis is not a camera, so
// a corrupt component drops the whole viewport and the Map opens on
// fit-to-screen, which is always drawable.
func readViewport(centerXRaw, centerYRaw, zoomRaw any) (Viewport, bool) {
	centerX, xOK := floatValue(centerXRaw)
	centerY, yOK := floatValue(centerYRaw)
	zoom, zoomOK := floatValue(zoomRaw)
	if !xOK || !yOK || !zoomOK {
		return Viewport{}, false
	}
	viewport := Viewport{CenterX: centerX, CenterY: centerY, Zoom: zoom}
	if !viewport.IsValid() {
		return Viewport{}, false
	}
	return viewport, true
}

func normalizeUserID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return LocalUserID
	}
	return trimmed
}

// LocalUserID is the single-user install's user identifier. It matches
// userprofile.LocalUserID; this package keeps its own copy so layout storage
// does not depend on the profile package.
const LocalUserID = "local"

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// floatValue converts a SQLite column into a float. SQLite columns are
// dynamically typed, so a value written by an older build or a hand-edited
// database can arrive as an integer, a string, or a blob. Anything that is not
// a real number reports false and becomes automatic placement for that tile.
func floatValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, isFinite(v)
	case float32:
		return float64(v), isFinite(float64(v))
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case []byte:
		return parseFloat(string(v))
	case string:
		return parseFloat(v)
	default:
		return 0, false
	}
}

func parseFloat(raw string) (float64, bool) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || !isFinite(parsed) {
		return 0, false
	}
	return parsed, true
}

func intValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case []byte:
		return parseInt(string(v))
	case string:
		return parseInt(v)
	default:
		return 0, false
	}
}

func parseInt(raw string) (int64, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func boolValue(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	default:
		number, ok := intValue(value)
		if !ok {
			return false, false
		}
		return number != 0, true
	}
}

func timeValue(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		return v, true
	case string:
		return parseTimestamp(v)
	case []byte:
		return parseTimestamp(string(v))
	default:
		return time.Time{}, false
	}
}

func parseTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
