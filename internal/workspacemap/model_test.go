package workspacemap

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestNormalizePatchRejectsUnsafeGeometry(t *testing.T) {
	cases := []struct {
		name string
		op   Operation
		want error
	}{
		{"NaN coordinate", SetPositions(map[string]Point{"ws": {X: math.NaN(), Y: 0}}), ErrInvalidCoordinate},
		{"infinite coordinate", SetPositions(map[string]Point{"ws": {X: math.Inf(1), Y: 0}}), ErrInvalidCoordinate},
		{"beyond safe world", SetPositions(map[string]Point{"ws": {X: MaxCoordinate + 1, Y: 0}}), ErrInvalidCoordinate},
		{"zoom too far in", SetViewport(Viewport{Zoom: MaxZoom + 0.1}), ErrInvalidZoom},
		{"zoom too far out", SetViewport(Viewport{Zoom: MinZoom - 0.01}), ErrInvalidZoom},
		{"zoom not finite", SetViewport(Viewport{Zoom: math.NaN()}), ErrInvalidZoom},
		{"empty node id", SetPositions(map[string]Point{"  ": {X: 0, Y: 0}}), ErrInvalidNodeID},
		{"reserved hq site", SetPositions(map[string]Point{ReservedHQSiteID: {X: 0, Y: 0}}), ErrInvalidNodeID},
		{"oversized node id", SetPositions(map[string]Point{strings.Repeat("x", MaxNodeIDLength+1): {X: 0, Y: 0}}), ErrInvalidNodeID},
		{"control characters", SetPositions(map[string]Point{"ws\x00id": {X: 0, Y: 0}}), ErrInvalidNodeID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizePatch(Patch{Operations: []Operation{tc.op}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizePatchRejectsAmbiguousOperations(t *testing.T) {
	// A "reset" carrying positions looks like it does two things; refusing it is
	// better than silently doing one of them.
	confused := Operation{Kind: OpReset, Positions: map[string]Point{"ws": {X: 1, Y: 1}}}
	if _, err := NormalizePatch(Patch{Operations: []Operation{confused}}); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("error = %v, want ErrInvalidPatch", err)
	}

	unknown := Operation{Kind: OpKind("teleport")}
	if _, err := NormalizePatch(Patch{Operations: []Operation{unknown}}); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("error = %v, want ErrInvalidPatch", err)
	}

	if _, err := NormalizePatch(Patch{}); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("empty patch error = %v, want ErrInvalidPatch", err)
	}
}

func TestNormalizePatchEnforcesBounds(t *testing.T) {
	tooMany := make([]Operation, MaxOperationsPerPatch+1)
	for i := range tooMany {
		tooMany[i] = Reset()
	}
	if _, err := NormalizePatch(Patch{Operations: tooMany}); !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("error = %v, want ErrPatchTooLarge", err)
	}

	positions := make(map[string]Point, MaxPositionsPerOperation+1)
	for i := 0; i <= MaxPositionsPerOperation; i++ {
		positions["ws-"+strings.Repeat("a", i%3)+string(rune('a'+i%26))+itoa(i)] = Point{}
	}
	if _, err := NormalizePatch(Patch{Operations: []Operation{SetPositions(positions)}}); !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("error = %v, want ErrPatchTooLarge", err)
	}
}

func TestNormalizePatchRoundsAndNormalizesValues(t *testing.T) {
	patch, err := NormalizePatch(Patch{Operations: []Operation{
		SetPositions(map[string]Point{"  ws-a  ": {X: 12.00000004, Y: -0.0000001}}),
		SetViewport(Viewport{CenterX: 1.000000009, CenterY: 2, Zoom: 1.2500001}),
	}})
	if err != nil {
		t.Fatalf("NormalizePatch: %v", err)
	}
	point, ok := patch.Operations[0].Positions["ws-a"]
	if !ok {
		t.Fatalf("node id was not trimmed: %v", patch.Operations[0].Positions)
	}
	if point.X != 12 {
		t.Errorf("x = %v, want float noise rounded away", point.X)
	}
	if math.Signbit(point.Y) {
		t.Errorf("y = %v, want negative zero normalized to 0", point.Y)
	}
	if patch.Operations[1].Viewport.Zoom != 1.25 {
		t.Errorf("zoom = %v, want 1.25", patch.Operations[1].Viewport.Zoom)
	}
}

func TestRestoreAcceptsAnEmptySetButMergeDoesNot(t *testing.T) {
	// Restoring "no anchors" is exactly what Undo needs after a reset; merging
	// "no anchors" is a caller mistake.
	if _, err := NormalizePatch(Patch{Operations: []Operation{RestorePositions(nil)}}); err != nil {
		t.Fatalf("restore of an empty set: %v", err)
	}
	if _, err := NormalizePatch(Patch{Operations: []Operation{SetPositions(nil)}}); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("empty merge error = %v, want ErrInvalidPatch", err)
	}
}

func TestSanitizersDegradeInsteadOfFailing(t *testing.T) {
	if _, ok := SanitizePoint(Point{X: math.NaN(), Y: 0}); ok {
		t.Error("a corrupt anchor must not read as usable")
	}
	if point, ok := SanitizePoint(Point{X: 38, Y: 76}); !ok || point != (Point{X: 38, Y: 76}) {
		t.Errorf("valid anchor = %v, %v; want it preserved", point, ok)
	}
	if _, ok := SanitizeViewport(Viewport{Zoom: 99}); ok {
		t.Error("an out-of-range stored zoom must not read as usable")
	}
	// A camera Fit All framed a wide layout with is a camera the map has to be
	// able to reopen on (#307).
	if viewport, ok := SanitizeViewport(Viewport{Zoom: MinZoom}); !ok || viewport.Zoom != MinZoom {
		t.Errorf("a fitted %v viewport = %v, %v; want it preserved", MinZoom, viewport, ok)
	}
	if _, ok := SanitizeViewport(Viewport{Zoom: MinZoom / 2}); ok {
		t.Error("but the floor still holds: a zoom below it is not usable state")
	}
}

// The stored range has to cover everything the map can reach, framed or
// gestured, or the view a user deliberately left the map on is rejected on save
// and snaps back on reload (#307).
func TestZoomRangeCoversEveryReachableView(t *testing.T) {
	if MinZoom != 0.1 {
		t.Fatalf("MinZoom = %v, want the 10%% floor the client clamps to", MinZoom)
	}

	cases := []struct {
		name string
		zoom float64
		want bool
	}{
		{"the floor", MinZoom, true},
		{"just below the floor", MinZoom - 0.001, false},
		{"a fitted wide layout", 0.26, true},
		{"the old 50% floor", 0.5, true},
		{"an ordinary view", DefaultZoom, true},
		{"the ceiling", MaxZoom, true},
		{"above the ceiling", MaxZoom + 0.001, false},
		{"not a number", math.NaN(), false},
		{"infinite", math.Inf(1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viewport := Viewport{CenterX: 12, CenterY: -8, Zoom: tc.zoom}
			if got := viewport.IsValid(); got != tc.want {
				t.Errorf("Viewport{Zoom: %v}.IsValid() = %v, want %v", tc.zoom, got, tc.want)
			}
			_, err := NormalizePatch(Patch{Operations: []Operation{SetViewport(viewport)}})
			if tc.want && err != nil {
				t.Errorf("NormalizePatch rejected a storable zoom %v: %v", tc.zoom, err)
			}
			if !tc.want && !errors.Is(err, ErrInvalidZoom) {
				t.Errorf("NormalizePatch(%v) error = %v, want ErrInvalidZoom", tc.zoom, err)
			}
		})
	}
}

func TestSchemaVersionSupport(t *testing.T) {
	if !IsSupportedSchemaVersion(0) {
		t.Error("version 0 means unwritten, not corrupt")
	}
	if !IsSupportedSchemaVersion(SchemaVersion) {
		t.Error("the current version must be supported")
	}
	if IsSupportedSchemaVersion(SchemaVersion + 1) {
		t.Error("a newer format must not be read as if it were this one")
	}
}

// itoa avoids pulling strconv into a table that only needs unique keys.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
