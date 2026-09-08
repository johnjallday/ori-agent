// Package agentmap owns the current user's coordinate-based Agent Map layout:
// where each agent tile sits in world space, where that user's camera is
// pointing, and whether snapping is enabled.
//
// It is a deliberate port of internal/workspacemap, minus everything that
// package needs for districts. The Agent Map draws no frames, no groups and no
// membership geometry (agents-page-ux FR-61), because a workspace belongs to
// exactly one group while an agent belongs to many workspaces: non-overlapping
// frames cannot express a many-to-many relationship without either duplicating
// a tile or overlapping frames, and the workspace map forbids the latter. So
// there is no GroupPresentation, no Frame, no district operation, and no
// descendant resolver here.
//
// The boundary is the same one workspacemap draws. Everything in this package
// is presentation geometry for one user: no type here can express an agent's
// role, model, prompt, skills, or workspace membership, so a map operation has
// no vocabulary for mutating the agent record it points at.
//
// World coordinates are viewport-independent logical units. They are not CSS
// pixels, not percentages of the current viewport, not grid column numbers, and
// not DOM offsets, so the same saved layout renders identically at any
// container size.
package agentmap

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the layout format this build writes, and the only version it
// reads without deliberate migration or rejection.
const SchemaVersion = 1

// Safe world bounds, matching workspacemap. Every persisted coordinate must
// land inside a finite, documented range: the range is far larger than any
// realistic layout and exists to keep arithmetic, rendering, and bounds fitting
// well behaved rather than to constrain how a user arranges their map
// (agents-page-ux FR-52).
const (
	MinCoordinate = -1_000_000.0
	MaxCoordinate = 1_000_000.0
)

// Zoom clamp. One floor, applied to both framing and gestures, so fit-to-screen
// can never move the camera somewhere the user cannot zoom back out of by hand
// (FR-66). The workspace map settled on 10% for exactly this reason.
const (
	MinZoom     = 0.1
	MaxZoom     = 2.0
	DefaultZoom = 1.0
)

// SnapStep is the one logical snap step, in world units, shared by placement
// math and the visible background grid at 100% zoom. The map stylesheet draws
// its grid at the same rhythm; changing one without the other makes snapped
// tiles drift off a grid the user can see.
const SnapStep = 38.0

// DefaultSnapToGrid is the snap preference a brand-new layout starts with.
const DefaultSnapToGrid = true

// Bounded input sizes. A patch is a partial update, so these caps are generous
// for real use and still keep a malformed or hostile request from turning into
// unbounded work or an unbounded stored record.
const (
	// MaxOperationsPerPatch caps how many operations one PATCH may carry.
	MaxOperationsPerPatch = 8
	// MaxPositionsPerOperation caps one operation's position map. It must stay
	// large enough for an exact restore of a pre-reset layout, which is what
	// makes the reset undoable.
	MaxPositionsPerOperation = 2000
	// MaxPositionsPerLayout caps how many tiles one user's stored layout may
	// hold in total.
	MaxPositionsPerLayout = 5000
	// MaxRequestBytes caps the decoded size of one layout request body.
	MaxRequestBytes = 1 << 20
	// MaxAgentNameLength caps an individual agent identifier. Agents are keyed
	// by name rather than by an opaque id, so this is a name length, not a UUID
	// length.
	MaxAgentNameLength = 256
)

// Errors returned for input this package refuses to persist. Callers map these
// to bounded 4xx responses rather than letting malformed geometry reach the
// stored record.
var (
	// ErrInvalidPatch marks a structurally invalid patch: no operations, an
	// unknown operation kind, or fields that do not belong to the stated kind.
	ErrInvalidPatch = errors.New("invalid agent map patch")
	// ErrPatchTooLarge marks a patch that exceeds a documented bound.
	ErrPatchTooLarge = errors.New("agent map patch exceeds size limit")
	// ErrInvalidCoordinate marks a non-finite or out-of-safe-range coordinate.
	ErrInvalidCoordinate = errors.New("invalid agent map coordinate")
	// ErrInvalidZoom marks a non-finite or out-of-range zoom level.
	ErrInvalidZoom = errors.New("invalid agent map zoom")
	// ErrInvalidAgentName marks an empty or oversized agent identifier.
	ErrInvalidAgentName = errors.New("invalid agent map agent name")
	// ErrUnsupportedSchemaVersion marks a stored record written by a layout
	// format this build does not read.
	ErrUnsupportedSchemaVersion = errors.New("unsupported agent map schema version")
	// ErrStaleRevision marks a write whose echoed revision is behind the stored
	// one — a tab that has been open across someone else's change. It is
	// detected, not merged: the client reloads and re-applies (FR-53).
	ErrStaleRevision = errors.New("agent map layout was modified by another client")
)

// Point is one agent's anchor in world space.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// IsFinite reports whether both components are real numbers.
func (p Point) IsFinite() bool {
	return isFinite(p.X) && isFinite(p.Y)
}

// InSafeRange reports whether the point is finite and inside the documented
// world bounds. A coordinate outside them is treated as unreadable and falls
// back to automatic placement rather than rendering somewhere impossible
// (FR-52).
func (p Point) InSafeRange() bool {
	return p.IsFinite() &&
		p.X >= MinCoordinate && p.X <= MaxCoordinate &&
		p.Y >= MinCoordinate && p.Y <= MaxCoordinate
}

// Viewport is the stored camera: a world-space point to look at plus a zoom
// level, never a scroll offset. A scroll offset would tie the saved view to the
// container it was saved from (FR-67).
type Viewport struct {
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
	Zoom    float64 `json:"zoom"`
}

// DefaultViewport is the camera used when no valid viewport is stored.
// Surfaces normally replace it with a fit-to-screen result once content is
// measured.
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

// Bounds is an axis-aligned world-space rectangle describing content extent. It
// is presentation geometry only and never rewrites a stored coordinate.
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

// Layout is one user's whole stored map: the format version, the server-issued
// revision, every saved tile anchor, an optional camera, and the snap
// preference (FR-51).
type Layout struct {
	SchemaVersion int              `json:"schema_version"`
	Revision      int64            `json:"revision"`
	Positions     map[string]Point `json:"positions"`
	Viewport      *Viewport        `json:"viewport,omitempty"`
	SnapToGrid    bool             `json:"snap_to_grid"`
	UpdatedAt     time.Time        `json:"updated_at,omitzero"`
}

// NewLayout returns the layout a user starts with before they have moved
// anything: no saved anchors, no saved camera, snapping enabled.
func NewLayout() Layout {
	return Layout{
		SchemaVersion: SchemaVersion,
		Revision:      0,
		Positions:     map[string]Point{},
		SnapToGrid:    DefaultSnapToGrid,
	}
}

// IsSupportedSchemaVersion reports whether this build reads a stored layout of
// the given version.
//
// Version 0 is treated as unwritten rather than corrupt, so a record that
// predates versioning reads as a fresh layout instead of being refused
// (FR-54).
func IsSupportedSchemaVersion(version int) bool {
	return version == 0 || version == SchemaVersion
}

// OpKind names one explicit partial operation.
//
// Every write is one of these; there is deliberately no "replace the whole
// layout" verb, because a stale tab sending its full snapshot is exactly how
// unrelated coordinates get erased.
type OpKind string

const (
	// OpSetPositions moves one or more tiles, leaving every unmentioned anchor
	// alone.
	OpSetPositions OpKind = "set_positions"
	// OpRestorePositions replaces the whole position set with the one given. It
	// exists for undoing a reset (FR-72) — the one case where a client
	// legitimately holds the complete prior layout — and for nothing else.
	OpRestorePositions OpKind = "restore_positions"
	// OpSetViewport stores the camera.
	OpSetViewport OpKind = "set_viewport"
	// OpSetPreferences stores the snap preference.
	OpSetPreferences OpKind = "set_preferences"
	// OpReset clears every saved anchor so automatic placement takes over,
	// while preserving the user's snap preference.
	OpReset OpKind = "reset"
)

// IsValid reports whether the kind is one this build applies.
func (k OpKind) IsValid() bool {
	switch k {
	case OpSetPositions, OpRestorePositions, OpSetViewport, OpSetPreferences, OpReset:
		return true
	default:
		return false
	}
}

// Operation is one partial change to a layout.
type Operation struct {
	Kind OpKind `json:"op"`

	// Positions carries anchors for OpSetPositions and OpRestorePositions.
	Positions map[string]Point `json:"positions,omitempty"`
	// Viewport carries the camera for OpSetViewport.
	Viewport *Viewport `json:"viewport,omitempty"`
	// SnapToGrid carries the snap preference for OpSetPreferences. It is a
	// pointer so "not mentioned" stays distinguishable from "set to false".
	SnapToGrid *bool `json:"snap_to_grid,omitempty"`
}

// SetPositions builds a partial move of the given anchors.
func SetPositions(positions map[string]Point) Operation {
	return Operation{Kind: OpSetPositions, Positions: positions}
}

// RestorePositions builds a whole-set replacement, for undoing a reset.
func RestorePositions(positions map[string]Point) Operation {
	return Operation{Kind: OpRestorePositions, Positions: positions}
}

// SetViewport builds a camera write.
func SetViewport(v Viewport) Operation {
	return Operation{Kind: OpSetViewport, Viewport: &v}
}

// SetPreferences builds a snap-preference write.
func SetPreferences(snap bool) Operation {
	return Operation{Kind: OpSetPreferences, SnapToGrid: &snap}
}

// Reset builds the clear-every-anchor operation.
func Reset() Operation {
	return Operation{Kind: OpReset}
}

// Patch is one request's ordered list of operations.
//
// ExpectedRevision is the revision the client last received. Zero means "I did
// not check", which is what a first write from a freshly loaded page sends; any
// other value must match the stored revision or the write is refused as stale
// (FR-53).
type Patch struct {
	Operations       []Operation `json:"operations"`
	ExpectedRevision int64       `json:"expected_revision,omitempty"`
}

// Result is what an accepted write returns: the committed layout, so a client
// can adopt the canonical values and the new revision without a second read.
type Result struct {
	Layout Layout `json:"layout"`
}

// NormalizeAgentName validates and canonicalizes one agent identifier.
//
// Agents are keyed by name, which is also how the rest of the agent API
// addresses them. Case is preserved — an agent named "Atlas" is stored as
// "Atlas" — because the name is a display string as well as a key.
func NormalizeAgentName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidAgentName)
	}
	if len(trimmed) > MaxAgentNameLength {
		return "", fmt.Errorf("%w: %d bytes exceeds %d", ErrInvalidAgentName, len(trimmed), MaxAgentNameLength)
	}
	return trimmed, nil
}

// NormalizePatch validates a whole patch and returns a canonical copy.
//
// Everything is checked before anything is applied, so a patch with one bad
// coordinate consumes no revision and moves no tile (FR-53's sibling rule: a
// rejected write leaves the stored layout byte-identical).
func NormalizePatch(patch Patch) (Patch, error) {
	if len(patch.Operations) == 0 {
		return Patch{}, fmt.Errorf("%w: no operations", ErrInvalidPatch)
	}
	if len(patch.Operations) > MaxOperationsPerPatch {
		return Patch{}, fmt.Errorf("%w: %d operations exceeds %d", ErrPatchTooLarge, len(patch.Operations), MaxOperationsPerPatch)
	}
	if patch.ExpectedRevision < 0 {
		return Patch{}, fmt.Errorf("%w: negative expected revision", ErrInvalidPatch)
	}

	out := Patch{
		Operations:       make([]Operation, 0, len(patch.Operations)),
		ExpectedRevision: patch.ExpectedRevision,
	}
	for i, op := range patch.Operations {
		normalized, err := normalizeOperation(op)
		if err != nil {
			return Patch{}, fmt.Errorf("operation %d: %w", i, err)
		}
		out.Operations = append(out.Operations, normalized)
	}
	return out, nil
}

func normalizeOperation(op Operation) (Operation, error) {
	if !op.Kind.IsValid() {
		return Operation{}, fmt.Errorf("%w: unknown operation %q", ErrInvalidPatch, op.Kind)
	}

	switch op.Kind {
	case OpSetPositions, OpRestorePositions:
		if op.Viewport != nil || op.SnapToGrid != nil {
			return Operation{}, fmt.Errorf("%w: %s carries fields that do not belong to it", ErrInvalidPatch, op.Kind)
		}
		// A restore with no positions is how a client says "the layout was
		// empty before the reset", which is a legitimate undo target. A
		// set_positions with none is a no-op request and is refused, because it
		// would consume a revision while changing nothing.
		if op.Kind == OpSetPositions && len(op.Positions) == 0 {
			return Operation{}, fmt.Errorf("%w: set_positions carries no positions", ErrInvalidPatch)
		}
		if len(op.Positions) > MaxPositionsPerOperation {
			return Operation{}, fmt.Errorf("%w: %d positions exceeds %d", ErrPatchTooLarge, len(op.Positions), MaxPositionsPerOperation)
		}
		positions := make(map[string]Point, len(op.Positions))
		for name, point := range op.Positions {
			key, err := NormalizeAgentName(name)
			if err != nil {
				return Operation{}, err
			}
			if !point.InSafeRange() {
				return Operation{}, fmt.Errorf("%w: %q at (%v, %v)", ErrInvalidCoordinate, key, point.X, point.Y)
			}
			positions[key] = point
		}
		return Operation{Kind: op.Kind, Positions: positions}, nil

	case OpSetViewport:
		if op.Viewport == nil {
			return Operation{}, fmt.Errorf("%w: set_viewport carries no viewport", ErrInvalidPatch)
		}
		if len(op.Positions) > 0 || op.SnapToGrid != nil {
			return Operation{}, fmt.Errorf("%w: set_viewport carries fields that do not belong to it", ErrInvalidPatch)
		}
		if !op.Viewport.IsValid() {
			return Operation{}, fmt.Errorf("%w: (%v, %v) at %v", ErrInvalidZoom, op.Viewport.CenterX, op.Viewport.CenterY, op.Viewport.Zoom)
		}
		v := *op.Viewport
		return Operation{Kind: OpSetViewport, Viewport: &v}, nil

	case OpSetPreferences:
		if op.SnapToGrid == nil {
			return Operation{}, fmt.Errorf("%w: set_preferences carries no preference", ErrInvalidPatch)
		}
		if len(op.Positions) > 0 || op.Viewport != nil {
			return Operation{}, fmt.Errorf("%w: set_preferences carries fields that do not belong to it", ErrInvalidPatch)
		}
		snap := *op.SnapToGrid
		return Operation{Kind: OpSetPreferences, SnapToGrid: &snap}, nil

	default: // OpReset
		if len(op.Positions) > 0 || op.Viewport != nil || op.SnapToGrid != nil {
			return Operation{}, fmt.Errorf("%w: reset carries fields that do not belong to it", ErrInvalidPatch)
		}
		return Operation{Kind: OpReset}, nil
	}
}

// SortedAgentNames returns a layout's anchored agent names in a stable order,
// so callers that render or compare layouts do not depend on map iteration.
func SortedAgentNames(positions map[string]Point) []string {
	names := make([]string, 0, len(positions))
	for name := range positions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
