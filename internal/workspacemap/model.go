// Package workspacemap owns the current user's coordinate-based Workspace Map
// layout: where each workspace and group building sits in world space, where
// that user's camera is pointing, and whether snapping is enabled.
//
// The boundary of this package is deliberate (PRD #292 FR-5, FR-6, FR-104).
// Everything here is presentation geometry for one user. No type in this
// package can express a parent, an order index, a status, a designation, a file,
// or any other workspace semantic field, so a map operation has no vocabulary
// for mutating the workspace record it points at. Hierarchy stays with Tree and
// the workspace store; a workspace's own internal CanvasLayout stays with
// session.CanvasLayout and /api/workspaces/{id}/layout.
//
// World coordinates are viewport-independent logical units (FR-2). They are not
// CSS pixels, not percentages of the current viewport, not grid column numbers,
// and not DOM offsets, so the same saved layout renders identically on Home and
// on /workspaces at any container size.
package workspacemap

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// SchemaVersion is the layout format this build writes, and the only version it
// reads without deliberate migration or rejection (FR-14).
const SchemaVersion = 1

// Safe world bounds. V1 is an expanding world, not an infinite or procedurally
// generated one (FR-12), so every persisted coordinate must land inside a
// finite, documented range. The range is far larger than any realistic layout —
// it exists to keep arithmetic, rendering, and bounds fitting well behaved
// rather than to constrain how a user arranges their map.
const (
	MinCoordinate = -1_000_000.0
	MaxCoordinate = 1_000_000.0
)

// Zoom clamp (FR-38). Usable between 10% and 200%.
//
// The floor was 50% until #307. Fit All's promise is that it shows everything
// (FR-40), and a layout spread wider than two viewports cannot be shown at
// 50%, so the map now frames — and lets a user zoom — out to 10%. The camera
// that produces has to be storable, or the view a user deliberately left the
// map on is rejected on save and snaps back on reload. 10% is still a floor:
// one stray coordinate must not persist a camera that renders the map as a dot.
const (
	MinZoom     = 0.1
	MaxZoom     = 2.0
	DefaultZoom = 1.0
)

// SnapStep is the one logical snap step, in world units, shared by candidate
// placement math and the visible background grid at 100% zoom (FR-58). The map
// stylesheet draws its grid at the same 38-unit rhythm; changing one without
// the other makes snapped anchors drift off the grid a user can see.
const SnapStep = 38.0

// DefaultSnapToGrid is the snap preference a brand-new layout starts with
// (FR-57).
const DefaultSnapToGrid = true

// Bounded input sizes (FR-100). A patch is a partial update, so these caps are
// generous for real use and still keep a malformed or hostile request from
// turning into unbounded work or an unbounded stored record.
const (
	// MaxOperationsPerPatch caps how many operations one PATCH may carry.
	MaxOperationsPerPatch = 8
	// MaxPositionsPerOperation caps one operation's position map. It must stay
	// large enough for a group translation over a big cluster and for an exact
	// restore of a pre-reset layout.
	MaxPositionsPerOperation = 2000
	// MaxPositionsPerLayout caps how many anchors one user's stored layout may
	// hold in total.
	MaxPositionsPerLayout = 5000
	// MaxRequestBytes caps the decoded size of one layout request body.
	MaxRequestBytes = 1 << 20
	// MaxNodeIDLength caps an individual node identifier.
	MaxNodeIDLength = 256
)

// ReservedHQSiteID is the client-side identifier for the reserved "Personal HQ
// not created / needs repair" site. It is a rendering placeholder, not a
// workspace, so it gets a stable deterministic anchor on the map but must never
// be persisted as if it were a workspace ID (FR-30).
const ReservedHQSiteID = "__personal_hq_site__"

// Errors returned for input this package refuses to persist. Callers map these
// to bounded 4xx responses rather than letting malformed geometry reach the
// stored record (FR-100).
var (
	// ErrInvalidPatch marks a structurally invalid patch: no operations, an
	// unknown operation kind, or fields that do not belong to the stated kind.
	ErrInvalidPatch = errors.New("invalid workspace map patch")
	// ErrPatchTooLarge marks a patch that exceeds a documented bound.
	ErrPatchTooLarge = errors.New("workspace map patch exceeds size limit")
	// ErrInvalidCoordinate marks a non-finite or out-of-safe-range coordinate.
	ErrInvalidCoordinate = errors.New("invalid workspace map coordinate")
	// ErrInvalidZoom marks a non-finite or out-of-range zoom level.
	ErrInvalidZoom = errors.New("invalid workspace map zoom")
	// ErrInvalidNodeID marks an empty, oversized, or reserved node identifier.
	ErrInvalidNodeID = errors.New("invalid workspace map node id")
	// ErrUnsupportedSchemaVersion marks a stored record written by a layout
	// format this build does not read.
	ErrUnsupportedSchemaVersion = errors.New("unsupported workspace map schema version")
)

// Point is one node's anchor in world space (FR-3). A workspace or group is
// anchored by its immutable ID, never by its name, its position in a list, or
// its place in the hierarchy.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// IsFinite reports whether both components are real numbers.
func (p Point) IsFinite() bool {
	return isFinite(p.X) && isFinite(p.Y)
}

// InSafeRange reports whether the point is finite and inside the documented
// world bounds (FR-12).
func (p Point) InSafeRange() bool {
	return p.IsFinite() &&
		p.X >= MinCoordinate && p.X <= MaxCoordinate &&
		p.Y >= MinCoordinate && p.Y <= MaxCoordinate
}

// Translate returns the point moved by a world-space delta. Group cluster
// movement applies one identical delta to every member so relative spacing is
// preserved exactly (FR-86).
func (p Point) Translate(delta Point) Point {
	return Point{X: p.X + delta.X, Y: p.Y + delta.Y}
}

// Viewport is the camera: a world-space center plus a zoom level (FR-43).
// Storing a world center rather than a scroll offset is what lets one saved
// camera survive different container sizes, Home versus /workspaces, and a
// window resize without moving any building.
type Viewport struct {
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
	Zoom    float64 `json:"zoom"`
}

// DefaultViewport is the camera used when no valid viewport is stored (FR-45).
// Surfaces normally replace it with a Fit All result once content is measured.
func DefaultViewport() Viewport {
	return Viewport{CenterX: 0, CenterY: 0, Zoom: DefaultZoom}
}

// IsValid reports whether the viewport is finite, inside the world bounds, and
// within the persisted zoom range.
func (v Viewport) IsValid() bool {
	return isFinite(v.CenterX) && isFinite(v.CenterY) && isFinite(v.Zoom) &&
		v.CenterX >= MinCoordinate && v.CenterX <= MaxCoordinate &&
		v.CenterY >= MinCoordinate && v.CenterY <= MaxCoordinate &&
		v.Zoom >= MinZoom && v.Zoom <= MaxZoom
}

// Bounds is an axis-aligned world-space rectangle. It describes content extent
// and safe limits; it is presentation geometry only and never rewrites a node
// coordinate (FR-10, FR-84).
type Bounds struct {
	MinX float64 `json:"min_x"`
	MinY float64 `json:"min_y"`
	MaxX float64 `json:"max_x"`
	MaxY float64 `json:"max_y"`
}

// SafeBounds is the documented world rectangle every persisted coordinate must
// fall inside.
func SafeBounds() Bounds {
	return Bounds{MinX: MinCoordinate, MinY: MinCoordinate, MaxX: MaxCoordinate, MaxY: MaxCoordinate}
}

// Contains reports whether the point lies inside the rectangle, edges included.
func (b Bounds) Contains(p Point) bool {
	return p.IsFinite() && p.X >= b.MinX && p.X <= b.MaxX && p.Y >= b.MinY && p.Y <= b.MaxY
}

// Layout is one user's whole map state: node anchors, camera, snap preference,
// the format version it was written in, and the server-issued revision that
// orders concurrent updates (FR-15).
//
// Revision is issued by the store on every accepted write. A client echoes the
// revision it received so a stale tab can be recognised, and every accepted
// write returns the revision it produced (FR-102).
type Layout struct {
	SchemaVersion int              `json:"schema_version"`
	Revision      int64            `json:"revision"`
	Positions     map[string]Point `json:"positions"`
	Viewport      *Viewport        `json:"viewport,omitempty"`
	SnapToGrid    bool             `json:"snap_to_grid"`
	UpdatedAt     time.Time        `json:"updated_at,omitzero"`
}

// NewLayout returns the layout a user starts with before they have moved
// anything: no saved anchors, no saved camera, snapping enabled (FR-57).
func NewLayout() Layout {
	return Layout{
		SchemaVersion: SchemaVersion,
		Revision:      0,
		Positions:     map[string]Point{},
		SnapToGrid:    DefaultSnapToGrid,
	}
}

// IsSupportedSchemaVersion reports whether this build reads a stored layout of
// the given version. Version 0 is treated as unwritten rather than corrupt, so
// a record that predates versioning reads as a fresh layout (FR-14).
func IsSupportedSchemaVersion(version int) bool {
	return version == 0 || version == SchemaVersion
}

// OpKind names one explicit partial operation. Every write to a layout is one
// of these; there is no "replace the whole layout" verb, because a stale tab
// sending its full snapshot is exactly how unrelated coordinates get erased
// (FR-96, FR-101).
type OpKind string

const (
	// OpSetPositions merges the given anchors into the stored layout, leaving
	// every other anchor untouched. This is what a single drop sends.
	OpSetPositions OpKind = "set_positions"
	// OpSetViewport stores the latest settled camera. Camera saves are
	// debounced and best-effort: failing one must never disturb committed
	// building positions (FR-44, FR-108).
	OpSetViewport OpKind = "set_viewport"
	// OpSetPreferences stores map preferences — in V1, the snap toggle (FR-57).
	OpSetPreferences OpKind = "set_preferences"
	// OpTranslateGroup moves a group's whole visual cluster by one world-space
	// delta. The client sends only the group and the delta; the server resolves
	// the group's current descendants from the workspace store and commits the
	// whole cluster in one transaction or none of it (FR-86, FR-87).
	OpTranslateGroup OpKind = "translate_group"
	// OpReset clears every custom node anchor so deterministic fallback
	// placement takes over. It preserves the snap preference and touches no
	// workspace record (FR-110, FR-111).
	OpReset OpKind = "reset"
	// OpRestorePositions replaces the entire anchor set with an exact snapshot,
	// atomically. This is how the in-session Undo after a reset puts the
	// pre-reset layout back as one unit, without a permanent history table
	// (FR-112). Unlike OpSetPositions it does not merge: anchors absent from
	// the snapshot are removed.
	OpRestorePositions OpKind = "restore_positions"
)

// Operation is one explicit partial change. The Kind field selects which of the
// remaining fields carry meaning; Validate rejects a payload that sets fields
// belonging to another kind, so an ambiguous request fails loudly instead of
// silently doing part of what it looked like it asked for.
type Operation struct {
	Kind OpKind `json:"op"`

	// Positions carries anchors for OpSetPositions and OpRestorePositions.
	Positions map[string]Point `json:"positions,omitempty"`
	// Viewport carries the camera for OpSetViewport.
	Viewport *Viewport `json:"viewport,omitempty"`
	// SnapToGrid carries the snap preference for OpSetPreferences. It is a
	// pointer so "not mentioned" stays distinguishable from "set to false".
	SnapToGrid *bool `json:"snap_to_grid,omitempty"`
	// GroupID names the group whose cluster OpTranslateGroup moves.
	GroupID string `json:"group_id,omitempty"`
	// Delta is the world-space offset OpTranslateGroup applies to the group
	// anchor and to every visible descendant anchor.
	Delta *Point `json:"delta,omitempty"`
}

// SetPositions builds a merge operation for one or more dropped nodes.
func SetPositions(positions map[string]Point) Operation {
	return Operation{Kind: OpSetPositions, Positions: positions}
}

// SetViewport builds a camera-save operation.
func SetViewport(viewport Viewport) Operation {
	return Operation{Kind: OpSetViewport, Viewport: &viewport}
}

// SetSnapToGrid builds a snap-preference operation.
func SetSnapToGrid(enabled bool) Operation {
	return Operation{Kind: OpSetPreferences, SnapToGrid: &enabled}
}

// TranslateGroup builds a cluster-move operation for one group.
func TranslateGroup(groupID string, delta Point) Operation {
	return Operation{Kind: OpTranslateGroup, GroupID: groupID, Delta: &delta}
}

// Reset builds the operation that clears every custom node anchor.
func Reset() Operation {
	return Operation{Kind: OpReset}
}

// RestorePositions builds the atomic exact-restore operation used by Undo.
func RestorePositions(positions map[string]Point) Operation {
	return Operation{Kind: OpRestorePositions, Positions: positions}
}

// Patch is one bounded partial update. Operations apply in order within a
// single transaction and produce a single new revision (FR-97).
type Patch struct {
	Operations []Operation `json:"operations"`
}

// Result is what an accepted write returns: the committed canonical values and
// the revision they produced, so the client reconciles against what the server
// actually stored rather than against what it hoped it stored (FR-102).
//
// Positions holds only the anchors this write committed, not the whole layout.
// After OpReset it is empty; after OpRestorePositions it is the restored set.
type Result struct {
	SchemaVersion int              `json:"schema_version"`
	Revision      int64            `json:"revision"`
	Positions     map[string]Point `json:"positions"`
	Viewport      *Viewport        `json:"viewport,omitempty"`
	SnapToGrid    bool             `json:"snap_to_grid"`
}

// NormalizePatch validates a decoded patch and returns a normalized copy safe to
// persist. It never clamps a caller's numbers into range: an out-of-range
// coordinate is a bounded client error, because silently relocating a building
// the user did not move is worse than refusing the write (FR-100).
//
// Normalization is limited to rounding away float noise and trimming node IDs.
func NormalizePatch(patch Patch) (Patch, error) {
	if len(patch.Operations) == 0 {
		return Patch{}, fmt.Errorf("%w: no operations", ErrInvalidPatch)
	}
	if len(patch.Operations) > MaxOperationsPerPatch {
		return Patch{}, fmt.Errorf("%w: %d operations exceeds %d", ErrPatchTooLarge, len(patch.Operations), MaxOperationsPerPatch)
	}
	normalized := Patch{Operations: make([]Operation, 0, len(patch.Operations))}
	for i, op := range patch.Operations {
		normalizedOp, err := NormalizeOperation(op)
		if err != nil {
			return Patch{}, fmt.Errorf("operation %d: %w", i, err)
		}
		normalized.Operations = append(normalized.Operations, normalizedOp)
	}
	return normalized, nil
}

// NormalizeOperation validates one operation and returns a normalized copy.
func NormalizeOperation(op Operation) (Operation, error) {
	switch op.Kind {
	case OpSetPositions, OpRestorePositions:
		if err := rejectFields(op, "viewport", op.Viewport != nil, "snap_to_grid", op.SnapToGrid != nil,
			"group_id", strings.TrimSpace(op.GroupID) != "", "delta", op.Delta != nil); err != nil {
			return Operation{}, err
		}
		positions, err := normalizePositions(op.Positions)
		if err != nil {
			return Operation{}, err
		}
		// A merge with nothing to merge is a caller mistake; an exact restore
		// of an empty set is meaningful (it clears the layout).
		if op.Kind == OpSetPositions && len(positions) == 0 {
			return Operation{}, fmt.Errorf("%w: %s requires at least one position", ErrInvalidPatch, op.Kind)
		}
		return Operation{Kind: op.Kind, Positions: positions}, nil

	case OpSetViewport:
		if err := rejectFields(op, "positions", len(op.Positions) > 0, "snap_to_grid", op.SnapToGrid != nil,
			"group_id", strings.TrimSpace(op.GroupID) != "", "delta", op.Delta != nil); err != nil {
			return Operation{}, err
		}
		if op.Viewport == nil {
			return Operation{}, fmt.Errorf("%w: %s requires viewport", ErrInvalidPatch, op.Kind)
		}
		viewport, err := normalizeViewport(*op.Viewport)
		if err != nil {
			return Operation{}, err
		}
		return Operation{Kind: op.Kind, Viewport: &viewport}, nil

	case OpSetPreferences:
		if err := rejectFields(op, "positions", len(op.Positions) > 0, "viewport", op.Viewport != nil,
			"group_id", strings.TrimSpace(op.GroupID) != "", "delta", op.Delta != nil); err != nil {
			return Operation{}, err
		}
		if op.SnapToGrid == nil {
			return Operation{}, fmt.Errorf("%w: %s requires a preference", ErrInvalidPatch, op.Kind)
		}
		snap := *op.SnapToGrid
		return Operation{Kind: op.Kind, SnapToGrid: &snap}, nil

	case OpTranslateGroup:
		if err := rejectFields(op, "positions", len(op.Positions) > 0, "viewport", op.Viewport != nil,
			"snap_to_grid", op.SnapToGrid != nil); err != nil {
			return Operation{}, err
		}
		groupID, err := NormalizeNodeID(op.GroupID)
		if err != nil {
			return Operation{}, err
		}
		if op.Delta == nil {
			return Operation{}, fmt.Errorf("%w: %s requires delta", ErrInvalidPatch, op.Kind)
		}
		delta := *op.Delta
		if !delta.IsFinite() {
			return Operation{}, fmt.Errorf("%w: delta is not finite", ErrInvalidCoordinate)
		}
		// The delta itself only has to be a sane distance; the resulting
		// coordinates are validated against the safe world after translation,
		// where the store knows each descendant's current anchor.
		if math.Abs(delta.X) > worldSpan || math.Abs(delta.Y) > worldSpan {
			return Operation{}, fmt.Errorf("%w: delta out of safe range", ErrInvalidCoordinate)
		}
		delta = roundPoint(delta)
		return Operation{Kind: op.Kind, GroupID: groupID, Delta: &delta}, nil

	case OpReset:
		if err := rejectFields(op, "positions", len(op.Positions) > 0, "viewport", op.Viewport != nil,
			"snap_to_grid", op.SnapToGrid != nil, "group_id", strings.TrimSpace(op.GroupID) != "",
			"delta", op.Delta != nil); err != nil {
			return Operation{}, err
		}
		return Operation{Kind: OpReset}, nil

	default:
		return Operation{}, fmt.Errorf("%w: unknown operation %q", ErrInvalidPatch, op.Kind)
	}
}

// NormalizeNodeID validates and trims a workspace or group identifier. It
// refuses the reserved Personal HQ site, which is a rendering placeholder with a
// deterministic anchor rather than a persistable workspace (FR-30).
func NormalizeNodeID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidNodeID)
	}
	if len(trimmed) > MaxNodeIDLength {
		return "", fmt.Errorf("%w: longer than %d characters", ErrInvalidNodeID, MaxNodeIDLength)
	}
	if trimmed == ReservedHQSiteID {
		return "", fmt.Errorf("%w: %q is a reserved map site, not a workspace", ErrInvalidNodeID, trimmed)
	}
	if strings.ContainsFunc(trimmed, isControlRune) {
		return "", fmt.Errorf("%w: contains control characters", ErrInvalidNodeID)
	}
	return trimmed, nil
}

// NormalizePoint validates a single anchor and returns it rounded for storage.
func NormalizePoint(p Point) (Point, error) {
	if !p.IsFinite() {
		return Point{}, fmt.Errorf("%w: not finite", ErrInvalidCoordinate)
	}
	rounded := roundPoint(p)
	if !rounded.InSafeRange() {
		return Point{}, fmt.Errorf("%w: (%g, %g) outside [%g, %g]", ErrInvalidCoordinate, p.X, p.Y, MinCoordinate, MaxCoordinate)
	}
	return rounded, nil
}

// SanitizePoint is the tolerant read-side counterpart of NormalizePoint. It
// reports whether a stored anchor is usable instead of returning an error, so a
// single corrupt row degrades to fallback placement for that one node while
// every valid sibling stays exactly where the user put it (FR-22).
func SanitizePoint(p Point) (Point, bool) {
	sanitized, err := NormalizePoint(p)
	if err != nil {
		return Point{}, false
	}
	return sanitized, true
}

// SanitizeViewport is the tolerant read-side counterpart for the camera. An
// unusable stored camera drops to the default/Fit All view rather than making
// valid buildings unreachable (FR-45).
func SanitizeViewport(v Viewport) (Viewport, bool) {
	sanitized, err := normalizeViewport(v)
	if err != nil {
		return Viewport{}, false
	}
	return sanitized, true
}

func normalizePositions(positions map[string]Point) (map[string]Point, error) {
	if len(positions) > MaxPositionsPerOperation {
		return nil, fmt.Errorf("%w: %d positions exceeds %d", ErrPatchTooLarge, len(positions), MaxPositionsPerOperation)
	}
	normalized := make(map[string]Point, len(positions))
	for rawID, point := range positions {
		id, err := NormalizeNodeID(rawID)
		if err != nil {
			return nil, err
		}
		if _, duplicate := normalized[id]; duplicate {
			return nil, fmt.Errorf("%w: duplicate node id %q", ErrInvalidPatch, id)
		}
		normalizedPoint, err := NormalizePoint(point)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", id, err)
		}
		normalized[id] = normalizedPoint
	}
	return normalized, nil
}

func normalizeViewport(v Viewport) (Viewport, error) {
	if !isFinite(v.CenterX) || !isFinite(v.CenterY) {
		return Viewport{}, fmt.Errorf("%w: viewport center is not finite", ErrInvalidCoordinate)
	}
	if !isFinite(v.Zoom) {
		return Viewport{}, fmt.Errorf("%w: not finite", ErrInvalidZoom)
	}
	center, err := NormalizePoint(Point{X: v.CenterX, Y: v.CenterY})
	if err != nil {
		return Viewport{}, err
	}
	if v.Zoom < MinZoom || v.Zoom > MaxZoom {
		return Viewport{}, fmt.Errorf("%w: %g outside [%g, %g]", ErrInvalidZoom, v.Zoom, MinZoom, MaxZoom)
	}
	return Viewport{CenterX: center.X, CenterY: center.Y, Zoom: roundZoom(v.Zoom)}, nil
}

// rejectFields fails an operation that carries a field belonging to a different
// operation kind. Pairs are (field name, present) in order.
func rejectFields(op Operation, pairs ...any) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		name, _ := pairs[i].(string)
		present, _ := pairs[i+1].(bool)
		if present {
			return fmt.Errorf("%w: %s does not accept %s", ErrInvalidPatch, op.Kind, name)
		}
	}
	return nil
}

// worldSpan is the widest single move the safe world can express, used to reject
// an absurd group delta before any descendant lookup happens.
const worldSpan = MaxCoordinate - MinCoordinate

// coordinateScale rounds stored coordinates to three decimals. Free placement
// needs sub-unit precision; it does not need float noise from repeated
// screen-to-world conversions accumulating in the database.
const coordinateScale = 1000.0

// zoomScale rounds stored zoom to four decimals.
const zoomScale = 10000.0

func roundPoint(p Point) Point {
	return Point{X: roundTo(p.X, coordinateScale), Y: roundTo(p.Y, coordinateScale)}
}

func roundZoom(zoom float64) float64 {
	return roundTo(zoom, zoomScale)
}

// roundTo rounds to the given scale and normalizes negative zero, so an anchor
// dragged just left of the origin stores as 0 rather than -0.
func roundTo(value, scale float64) float64 {
	rounded := math.Round(value*scale) / scale
	if rounded == 0 {
		return 0
	}
	return rounded
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f
}
