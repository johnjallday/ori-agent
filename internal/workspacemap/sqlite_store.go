package workspacemap

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

// ErrStoreUnavailable means no layout storage is wired. Surfaces treat it the
// same as a failed read: deterministic fallback placement, read-only navigation,
// and an honest explanation that positions cannot be saved (FR-105).
var ErrStoreUnavailable = errors.New("workspace map layout storage is not configured")

// ErrGroupResolverUnavailable means a group translation was requested without a
// resolver able to say which nodes belong to that group.
var ErrGroupResolverUnavailable = errors.New("workspace map group resolver is not configured")

// DescendantResolver reports which node anchors move together when a group's
// district is dragged as one cluster: the group itself plus its currently
// visible descendants (FR-86).
//
// It is injected rather than implemented here because membership is workspace
// hierarchy, which this package deliberately cannot read or write. The store
// calls it immediately before opening the write transaction, so a cluster move
// translates whatever the hierarchy says right now rather than whatever a
// browser tab believed a minute ago — but without taking a second database
// connection while the write lock is held (see Apply).
type DescendantResolver interface {
	GroupNodeIDs(ctx context.Context, groupID string) ([]string, error)
}

// SQLiteStore persists one coordinate layout per user.
//
// Reads are tolerant and never write: a corrupt anchor costs that one node its
// saved position and nothing else, and merely looking at a legacy map that has
// no saved positions must not mark anything as modified (FR-22, FR-23).
//
// Writes are partial and transactional: a patch merges against the latest
// stored record, bumps one revision, and either commits every row it touches or
// none of them (FR-97, FR-101).
type SQLiteStore struct {
	db       *database.DB
	resolver DescendantResolver
}

// NewSQLiteStore builds a layout store over the shared application database.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// SetDescendantResolver wires group membership resolution. Without it, group
// translation is refused rather than guessed at.
func (s *SQLiteStore) SetDescendantResolver(resolver DescendantResolver) {
	if s == nil {
		return
	}
	s.resolver = resolver
}

// Load returns the current user's layout.
//
// Every read is best-effort per field. An unreadable anchor, an unreadable
// camera axis, or an unreadable preference degrades to the deterministic
// default for that one thing while every valid sibling survives intact. The one
// deliberate exception is the schema version: a record written by a layout
// format this build does not understand is refused outright, because reading it
// as "empty" would invite the very next write to overwrite a newer format with
// this one (FR-14).
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
		FROM workspace_map_layouts
		WHERE user_id = ?
	`, userID).Scan(&schemaVersionRaw, &revisionRaw, &centerXRaw, &centerYRaw, &zoomRaw, &snapRaw, &updatedAtRaw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No record yet. This is the ordinary state for a user who has never
		// moved anything; it is not an error and must not provoke a write.
		return layout, nil
	case err != nil:
		return Layout{}, fmt.Errorf("failed to read workspace map layout: %w", err)
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

	groups, err := s.loadGroupPresentations(ctx, userID)
	if err != nil {
		return Layout{}, err
	}
	layout.Groups = groups
	return layout, nil
}

// loadGroupPresentations reads every district this user has customized.
//
// Each row is sanitized on its own. An unreadable column, an unknown sizing
// mode, an unusable rectangle, or a preset identifier this build does not know
// costs that district that one facet — the rest of the row, and every other
// district, survives (#346 FR-192, FR-194). A row that sanitizes back to nothing
// but defaults is dropped from the returned map rather than reported, because
// "absent" and "all defaults" must render identically (FR-31, FR-101).
func (s *SQLiteStore) loadGroupPresentations(ctx context.Context, userID string) (map[string]GroupPresentation, error) {
	groups := map[string]GroupPresentation{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT group_id, sizing_mode, frame_x, frame_y, frame_width, frame_height, collapsed, accent, theme
		FROM workspace_map_group_presentations
		WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace map group presentations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id           string
			modeRaw      any
			xRaw         any
			yRaw         any
			widthRaw     any
			heightRaw    any
			collapsedRaw any
			accentRaw    any
			themeRaw     any
		)
		if err := rows.Scan(&id, &modeRaw, &xRaw, &yRaw, &widthRaw, &heightRaw, &collapsedRaw, &accentRaw, &themeRaw); err != nil {
			// One unreadable row is one district on safe defaults, not a lost
			// map. Keep reading.
			continue
		}
		groupID, err := NormalizeNodeID(id)
		if err != nil {
			continue
		}
		record := GroupPresentation{
			SizingMode: SizingMode(stringValue(modeRaw)),
			Accent:     stringValue(accentRaw),
			Theme:      stringValue(themeRaw),
		}
		if collapsed, ok := boolValue(collapsedRaw); ok {
			record.Collapsed = collapsed
		}
		if frame, ok := readFrame(xRaw, yRaw, widthRaw, heightRaw); ok {
			record.Frame = &frame
		}
		sanitized := SanitizeGroupPresentation(record)
		if sanitized.IsDefault() {
			continue
		}
		groups[groupID] = sanitized
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read workspace map group presentations: %w", err)
	}
	return groups, nil
}

func (s *SQLiteStore) loadPositions(ctx context.Context, userID string) (map[string]Point, error) {
	positions := map[string]Point{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace_id, x, y
		FROM workspace_map_positions
		WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace map positions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id   string
			xRaw any
			yRaw any
		)
		if err := rows.Scan(&id, &xRaw, &yRaw); err != nil {
			// One unreadable row is one building on fallback placement, not a
			// lost map. Keep reading (FR-22).
			continue
		}
		nodeID, err := NormalizeNodeID(id)
		if err != nil {
			continue
		}
		x, xOK := floatValue(xRaw)
		y, yOK := floatValue(yRaw)
		if !xOK || !yOK {
			continue
		}
		point, ok := SanitizePoint(Point{X: x, Y: y})
		if !ok {
			continue
		}
		positions[nodeID] = point
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read workspace map positions: %w", err)
	}
	return positions, nil
}

// Apply commits a partial patch and returns the canonical values it produced.
//
// Everything happens in one transaction against the latest stored record, so a
// stale tab that moved one building cannot replace coordinates it never touched,
// and a cluster move or an Undo restore either lands whole or not at all
// (FR-97, FR-101). Each accepted call produces exactly one new revision, which
// the caller echoes back to the client so both Map surfaces reconcile against
// what was actually stored rather than what was hoped (FR-102).
func (s *SQLiteStore) Apply(ctx context.Context, userID string, patch Patch) (Result, error) {
	if s == nil || s.db == nil {
		return Result{}, ErrStoreUnavailable
	}
	normalized, err := NormalizePatch(patch)
	if err != nil {
		return Result{}, err
	}
	userID = normalizeUserID(userID)

	// Group membership is resolved BEFORE the transaction opens. The resolver
	// reads the workspace store, which lives in this same SQLite database — and
	// a read issued from inside the write transaction takes a second connection
	// that waits on the write lock this transaction is holding. That is a
	// deadlock, not a slow query, and it hangs the request until the busy
	// timeout gives up.
	members, err := s.resolveGroups(ctx, normalized)
	if err != nil {
		return Result{}, err
	}

	var result Result
	err = s.db.InTransaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		state, err := s.beginLayout(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		for i, op := range normalized.Operations {
			if err := s.applyOperation(ctx, tx, userID, now, op, members, state); err != nil {
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
		result = Result{
			SchemaVersion: SchemaVersion,
			Revision:      state.revision,
			Positions:     state.committed,
			Groups:        state.committedGroups,
			Viewport:      state.viewport,
			SnapToGrid:    state.snapToGrid,
		}
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
	// committed accumulates the anchors this patch actually stored, which is
	// what the response reports back — not the user's whole layout.
	committed map[string]Point
	// committedGroups accumulates the districts this patch touched, each as the
	// whole canonical record now stored (#346 FR-190).
	committedGroups map[string]GroupPresentation
}

// beginLayout makes sure the user has a layout row and returns its current
// values. The row is created on first write, never on a read (FR-23).
func (s *SQLiteStore) beginLayout(ctx context.Context, tx *sql.Tx, userID string, now time.Time) (*layoutState, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_map_layouts (user_id, schema_version, revision, snap_to_grid, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT(user_id) DO NOTHING
	`, userID, SchemaVersion, boolToInt(DefaultSnapToGrid), now, now); err != nil {
		return nil, fmt.Errorf("failed to create workspace map layout: %w", err)
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
		FROM workspace_map_layouts
		WHERE user_id = ?
	`, userID).Scan(&schemaVersionRaw, &revisionRaw, &centerXRaw, &centerYRaw, &zoomRaw, &snapRaw); err != nil {
		return nil, fmt.Errorf("failed to read workspace map layout: %w", err)
	}

	storedVersion, ok := intValue(schemaVersionRaw)
	if !ok || !IsSupportedSchemaVersion(int(storedVersion)) {
		return nil, fmt.Errorf("%w: stored layout reports version %v", ErrUnsupportedSchemaVersion, schemaVersionRaw)
	}

	state := &layoutState{
		snapToGrid:      DefaultSnapToGrid,
		committed:       map[string]Point{},
		committedGroups: map[string]GroupPresentation{},
	}
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

// resolveGroups asks the resolver which nodes belong to each group a patch
// translates, keyed by group ID. Running this ahead of the transaction is what
// keeps the write lock and the membership read off the same connection.
func (s *SQLiteStore) resolveGroups(ctx context.Context, patch Patch) (map[string][]string, error) {
	members := map[string][]string{}
	for _, op := range patch.Operations {
		if op.Kind != OpTranslateGroup {
			continue
		}
		if s.resolver == nil {
			return nil, ErrGroupResolverUnavailable
		}
		if _, done := members[op.GroupID]; done {
			continue
		}
		ids, err := s.resolver.GroupNodeIDs(ctx, op.GroupID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve group %q: %w", op.GroupID, err)
		}
		if len(ids) > MaxPositionsPerOperation {
			return nil, fmt.Errorf("%w: group %q has %d members, limit %d", ErrPatchTooLarge, op.GroupID, len(ids), MaxPositionsPerOperation)
		}
		members[op.GroupID] = ids
	}
	return members, nil
}

func (s *SQLiteStore) applyOperation(ctx context.Context, tx *sql.Tx, userID string, now time.Time, op Operation, members map[string][]string, state *layoutState) error {
	switch op.Kind {
	case OpSetPositions:
		return s.writePositions(ctx, tx, userID, now, op.Positions, state)

	case OpSetViewport:
		viewport := *op.Viewport
		state.viewport = &viewport
		return nil

	case OpSetPreferences:
		state.snapToGrid = *op.SnapToGrid
		return nil

	case OpTranslateGroup:
		return s.translateGroup(ctx, tx, userID, now, op, members[op.GroupID], state)

	case OpReset:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM workspace_map_positions WHERE user_id = ?
		`, userID); err != nil {
			return fmt.Errorf("failed to reset workspace map positions: %w", err)
		}
		// Reset clears anchors only. The snap preference and the camera are
		// deliberately left alone (FR-110).
		state.committed = map[string]Point{}
		return nil

	case OpRestorePositions:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM workspace_map_positions WHERE user_id = ?
		`, userID); err != nil {
			return fmt.Errorf("failed to clear workspace map positions: %w", err)
		}
		state.committed = map[string]Point{}
		return s.writePositions(ctx, tx, userID, now, op.Positions, state)

	default:
		if op.Kind.isGroupPresentation() {
			return s.applyGroupPresentation(ctx, tx, userID, now, op, state)
		}
		return fmt.Errorf("%w: unknown operation %q", ErrInvalidPatch, op.Kind)
	}
}

// applyGroupPresentation folds one district operation into that district's
// single stored row.
//
// Every kind reads the current record first, changes only its own facet, and
// writes the whole row back. That read-modify-write is what makes the operations
// genuinely partial: a tab that resizes a district cannot also re-assert a
// collapse state or an accent it read ten minutes ago, because it never sends
// them (#346 FR-178). A record that ends up at every default is deleted rather
// than stored, so an abandoned customization does not count against the bound.
func (s *SQLiteStore) applyGroupPresentation(ctx context.Context, tx *sql.Tx, userID string, now time.Time, op Operation, state *layoutState) error {
	current, err := s.readGroupPresentation(ctx, tx, userID, op.GroupID)
	if err != nil {
		return err
	}

	switch op.Kind {
	case OpSetGroupFrame:
		frame := *op.Frame
		current.SizingMode = SizingModeCustom
		current.Frame = &frame
	case OpFitGroupToContents:
		// Fit to contents discards the stored minimum and nothing else: not a
		// member coordinate, not the collapse state, not the appearance (FR-41).
		current.SizingMode = SizingModeAuto
		current.Frame = nil
	case OpSetGroupCollapsed:
		// The expanded frame is deliberately preserved across a collapse
		// (FR-114), so expanding restores the exact rectangle (FR-116).
		current.Collapsed = *op.Collapsed
	case OpSetGroupAppearance:
		if op.Accent != "" {
			current.Accent = op.Accent
		}
		if op.Theme != "" {
			current.Theme = op.Theme
		}
	case OpResetGroupAppearance:
		current.Accent = DefaultAccent
		current.Theme = DefaultTheme
	default:
		return fmt.Errorf("%w: unknown group operation %q", ErrInvalidPatch, op.Kind)
	}

	if err := s.writeGroupPresentation(ctx, tx, userID, now, op.GroupID, current); err != nil {
		return err
	}
	// The response carries the whole canonical record, including the facets this
	// operation did not mention, so the client reconciles one district rather
	// than merging a fragment into state it may already have wrong (FR-190).
	state.committedGroups[op.GroupID] = current
	return nil
}

// readGroupPresentation returns the district's stored record, or the documented
// safe default when it has none. Reading inside the transaction is what makes
// concurrent facet updates compose instead of clobbering each other.
func (s *SQLiteStore) readGroupPresentation(ctx context.Context, tx *sql.Tx, userID, groupID string) (GroupPresentation, error) {
	var (
		modeRaw      any
		xRaw         any
		yRaw         any
		widthRaw     any
		heightRaw    any
		collapsedRaw any
		accentRaw    any
		themeRaw     any
	)
	err := tx.QueryRowContext(ctx, `
		SELECT sizing_mode, frame_x, frame_y, frame_width, frame_height, collapsed, accent, theme
		FROM workspace_map_group_presentations
		WHERE user_id = ? AND group_id = ?
	`, userID, groupID).Scan(&modeRaw, &xRaw, &yRaw, &widthRaw, &heightRaw, &collapsedRaw, &accentRaw, &themeRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultGroupPresentation(), nil
	}
	if err != nil {
		return GroupPresentation{}, fmt.Errorf("failed to read district for %q: %w", groupID, err)
	}

	record := GroupPresentation{
		SizingMode: SizingMode(stringValue(modeRaw)),
		Accent:     stringValue(accentRaw),
		Theme:      stringValue(themeRaw),
	}
	if collapsed, ok := boolValue(collapsedRaw); ok {
		record.Collapsed = collapsed
	}
	if frame, ok := readFrame(xRaw, yRaw, widthRaw, heightRaw); ok {
		record.Frame = &frame
	}
	// A corrupt stored row is repaired to safe defaults by the very next write
	// that touches it, which is the only moment this package is allowed to
	// rewrite it (FR-39, FR-193).
	return SanitizeGroupPresentation(record), nil
}

func (s *SQLiteStore) writeGroupPresentation(ctx context.Context, tx *sql.Tx, userID string, now time.Time, groupID string, record GroupPresentation) error {
	if record.IsDefault() {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM workspace_map_group_presentations WHERE user_id = ? AND group_id = ?
		`, userID, groupID); err != nil {
			return fmt.Errorf("failed to clear district for %q: %w", groupID, err)
		}
		return nil
	}

	var frameX, frameY, frameWidth, frameHeight any
	if record.Frame != nil {
		frameX, frameY = record.Frame.X, record.Frame.Y
		frameWidth, frameHeight = record.Frame.Width, record.Frame.Height
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_map_group_presentations
			(user_id, group_id, sizing_mode, frame_x, frame_y, frame_width, frame_height, collapsed, accent, theme, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, group_id) DO UPDATE SET
			sizing_mode = excluded.sizing_mode,
			frame_x = excluded.frame_x,
			frame_y = excluded.frame_y,
			frame_width = excluded.frame_width,
			frame_height = excluded.frame_height,
			collapsed = excluded.collapsed,
			accent = excluded.accent,
			theme = excluded.theme,
			updated_at = excluded.updated_at
	`, userID, groupID, string(record.SizingMode), frameX, frameY, frameWidth, frameHeight,
		boolToInt(record.Collapsed), record.Accent, record.Theme, now, now); err != nil {
		return fmt.Errorf("failed to save district for %q: %w", groupID, err)
	}
	return nil
}

func (s *SQLiteStore) writePositions(ctx context.Context, tx *sql.Tx, userID string, now time.Time, positions map[string]Point, state *layoutState) error {
	for id, point := range positions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_map_positions (user_id, workspace_id, x, y, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id, workspace_id) DO UPDATE SET
				x = excluded.x,
				y = excluded.y,
				updated_at = excluded.updated_at
		`, userID, id, point.X, point.Y, now); err != nil {
			return fmt.Errorf("failed to save position for %q: %w", id, err)
		}
		state.committed[id] = point
	}
	return nil
}

// translateGroup moves a whole district by one world-space delta.
//
// Member anchors are read inside this transaction and translated there, so the
// cluster moves relative to where its buildings actually are — not relative to
// a snapshot the browser took before someone else nudged one of them. A member
// with no stored anchor is skipped: it is sitting on deterministic fallback
// placement, and inventing a coordinate for it here would silently promote a
// fallback into a saved position. Callers that want fallbacks materialized send
// an OpSetPositions ahead of the translation in the same patch, which this
// transaction will already have written by the time the read below runs.
func (s *SQLiteStore) translateGroup(ctx context.Context, tx *sql.Tx, userID string, now time.Time, op Operation, memberIDs []string, state *layoutState) error {
	if memberIDs == nil {
		return ErrGroupResolverUnavailable
	}

	delta := *op.Delta
	translated := make(map[string]Point, len(memberIDs))
	for _, rawID := range memberIDs {
		memberID, err := NormalizeNodeID(rawID)
		if err != nil {
			return err
		}
		var xRaw, yRaw any
		err = tx.QueryRowContext(ctx, `
			SELECT x, y FROM workspace_map_positions WHERE user_id = ? AND workspace_id = ?
		`, userID, memberID).Scan(&xRaw, &yRaw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to read position for %q: %w", memberID, err)
		}
		x, xOK := floatValue(xRaw)
		y, yOK := floatValue(yRaw)
		if !xOK || !yOK {
			// A corrupt member anchor has no meaningful "previous position" to
			// translate from. Leave it on fallback rather than moving the rest
			// of the cluster away from it.
			continue
		}
		moved, err := NormalizePoint(Point{X: x, Y: y}.Translate(delta))
		if err != nil {
			// The whole cluster keeps its relative spacing or none of it moves;
			// clamping one member would shear the district (FR-86, FR-87).
			return fmt.Errorf("group %q member %q: %w", op.GroupID, memberID, err)
		}
		translated[memberID] = moved
	}
	return s.writePositions(ctx, tx, userID, now, translated, state)
}

// enforceLayoutSize keeps one user's stored anchors inside the documented bound
// (FR-100). It runs after the operations so a patch that would push the layout
// over the limit rolls back whole.
func (s *SQLiteStore) enforceLayoutSize(ctx context.Context, tx *sql.Tx, userID string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM workspace_map_positions WHERE user_id = ?
	`, userID).Scan(&count); err != nil {
		return fmt.Errorf("failed to count workspace map positions: %w", err)
	}
	if count > MaxPositionsPerLayout {
		return fmt.Errorf("%w: %d stored positions exceeds %d", ErrPatchTooLarge, count, MaxPositionsPerLayout)
	}

	var districts int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM workspace_map_group_presentations WHERE user_id = ?
	`, userID).Scan(&districts); err != nil {
		return fmt.Errorf("failed to count workspace map districts: %w", err)
	}
	if districts > MaxGroupPresentationsPerLayout {
		return fmt.Errorf("%w: %d stored districts exceeds %d", ErrPatchTooLarge, districts, MaxGroupPresentationsPerLayout)
	}
	return nil
}

func (s *SQLiteStore) writeLayout(ctx context.Context, tx *sql.Tx, userID string, now time.Time, state *layoutState) error {
	var centerX, centerY, zoom any
	if state.viewport != nil {
		centerX, centerY, zoom = state.viewport.CenterX, state.viewport.CenterY, state.viewport.Zoom
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workspace_map_layouts
		SET schema_version = ?,
			revision = ?,
			viewport_center_x = ?,
			viewport_center_y = ?,
			viewport_zoom = ?,
			snap_to_grid = ?,
			updated_at = ?
		WHERE user_id = ?
	`, SchemaVersion, state.revision, centerX, centerY, zoom, boolToInt(state.snapToGrid), now, userID); err != nil {
		return fmt.Errorf("failed to save workspace map layout: %w", err)
	}
	return nil
}

// readFrame rebuilds a district rectangle from four nullable columns. Like the
// camera, all four must be present and usable together: half a rectangle is not
// a frame, so a missing or corrupt component drops the whole custom frame and
// the district falls back to automatic sizing, which is always drawable
// (#346 FR-192).
func readFrame(xRaw, yRaw, widthRaw, heightRaw any) (Frame, bool) {
	x, xOK := floatValue(xRaw)
	y, yOK := floatValue(yRaw)
	width, widthOK := floatValue(widthRaw)
	height, heightOK := floatValue(heightRaw)
	if !xOK || !yOK || !widthOK || !heightOK {
		return Frame{}, false
	}
	frame, err := NormalizeFrame(Frame{X: x, Y: y, Width: width, Height: height})
	if err != nil {
		return Frame{}, false
	}
	return frame, true
}

// readViewport rebuilds a camera from three nullable columns. All three must be
// present and usable together: half a camera is not a view, so a missing or
// corrupt axis drops the whole viewport to the default rather than pointing the
// map somewhere the user never looked (FR-45).
func readViewport(centerXRaw, centerYRaw, zoomRaw any) (Viewport, bool) {
	centerX, xOK := floatValue(centerXRaw)
	centerY, yOK := floatValue(centerYRaw)
	zoom, zoomOK := floatValue(zoomRaw)
	if !xOK || !yOK || !zoomOK {
		return Viewport{}, false
	}
	return SanitizeViewport(Viewport{CenterX: centerX, CenterY: centerY, Zoom: zoom})
}

// normalizeUserID mirrors userprofile's rule that an unset user is the local
// user, so a single-user install always reads and writes one layout.
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
// a real number reports false and becomes fallback placement for that node.
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

// stringValue converts a SQLite column into a trimmed string. Anything that is
// not text reads as empty, which the preset catalogs then reject in favour of
// the default — an unknown identifier must never reach the stylesheet (FR-194).
func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
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
