package agentmap

import (
	"errors"
	"math"
	"testing"
)

func TestNewLayoutStartsEmptyWithSnappingOn(t *testing.T) {
	layout := NewLayout()
	if layout.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", layout.SchemaVersion, SchemaVersion)
	}
	if layout.Revision != 0 {
		t.Fatalf("revision = %d, want 0", layout.Revision)
	}
	if len(layout.Positions) != 0 {
		t.Fatalf("positions = %v, want empty", layout.Positions)
	}
	if layout.Viewport != nil {
		t.Fatalf("viewport = %v, want nil so the Map opens on fit-to-screen", layout.Viewport)
	}
	if !layout.SnapToGrid {
		t.Fatal("a new layout should start with snapping enabled")
	}
}

// A record written before the layout carried a version reads as fresh rather
// than being refused, so an install that predates versioning is not locked out
// of its own map (FR-54).
func TestSchemaVersionZeroIsUnwrittenNotCorrupt(t *testing.T) {
	if !IsSupportedSchemaVersion(0) {
		t.Fatal("version 0 must read as unwritten, not as unsupported")
	}
	if !IsSupportedSchemaVersion(SchemaVersion) {
		t.Fatalf("version %d must be supported", SchemaVersion)
	}
	if IsSupportedSchemaVersion(SchemaVersion + 1) {
		t.Fatal("a newer format must be refused rather than read as empty")
	}
}

func TestPointSafeRange(t *testing.T) {
	cases := []struct {
		name  string
		point Point
		want  bool
	}{
		{"origin", Point{0, 0}, true},
		{"at the minimum corner", Point{MinCoordinate, MinCoordinate}, true},
		{"at the maximum corner", Point{MaxCoordinate, MaxCoordinate}, true},
		{"one past the maximum", Point{MaxCoordinate + 1, 0}, false},
		{"one before the minimum", Point{0, MinCoordinate - 1}, false},
		{"not a number", Point{math.NaN(), 0}, false},
		{"positive infinity", Point{0, math.Inf(1)}, false},
		{"negative infinity", Point{math.Inf(-1), 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.point.InSafeRange(); got != tc.want {
				t.Fatalf("InSafeRange(%v) = %v, want %v", tc.point, got, tc.want)
			}
		})
	}
}

func TestViewportValidation(t *testing.T) {
	cases := []struct {
		name     string
		viewport Viewport
		want     bool
	}{
		{"default", DefaultViewport(), true},
		{"at the zoom floor", Viewport{0, 0, MinZoom}, true},
		{"at the zoom ceiling", Viewport{0, 0, MaxZoom}, true},
		{"below the zoom floor", Viewport{0, 0, MinZoom / 2}, false},
		{"above the zoom ceiling", Viewport{0, 0, MaxZoom * 2}, false},
		{"zero zoom", Viewport{0, 0, 0}, false},
		{"centre outside the world", Viewport{MaxCoordinate + 1, 0, 1}, false},
		{"not a number", Viewport{0, 0, math.NaN()}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.viewport.IsValid(); got != tc.want {
				t.Fatalf("IsValid(%v) = %v, want %v", tc.viewport, got, tc.want)
			}
		})
	}
}

func TestNormalizeAgentName(t *testing.T) {
	got, err := NormalizeAgentName("  Atlas  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Atlas" {
		t.Fatalf("name = %q, want %q — case is preserved, surrounding space is not", got, "Atlas")
	}

	if _, err := NormalizeAgentName("   "); !errors.Is(err, ErrInvalidAgentName) {
		t.Fatalf("blank name error = %v, want ErrInvalidAgentName", err)
	}

	long := make([]byte, MaxAgentNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NormalizeAgentName(string(long)); !errors.Is(err, ErrInvalidAgentName) {
		t.Fatalf("oversized name error = %v, want ErrInvalidAgentName", err)
	}
}

func TestNormalizePatchRejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		patch Patch
		want  error
	}{
		{
			name:  "no operations",
			patch: Patch{},
			want:  ErrInvalidPatch,
		},
		{
			name:  "unknown operation",
			patch: Patch{Operations: []Operation{{Kind: "teleport"}}},
			want:  ErrInvalidPatch,
		},
		{
			name:  "set_positions with nothing to set",
			patch: Patch{Operations: []Operation{{Kind: OpSetPositions}}},
			want:  ErrInvalidPatch,
		},
		{
			name: "a coordinate outside the world",
			patch: Patch{Operations: []Operation{
				SetPositions(map[string]Point{"Atlas": {X: MaxCoordinate * 2, Y: 0}}),
			}},
			want: ErrInvalidCoordinate,
		},
		{
			name: "a non-finite coordinate",
			patch: Patch{Operations: []Operation{
				SetPositions(map[string]Point{"Atlas": {X: math.Inf(1), Y: 0}}),
			}},
			want: ErrInvalidCoordinate,
		},
		{
			name: "an empty agent name",
			patch: Patch{Operations: []Operation{
				SetPositions(map[string]Point{"  ": {X: 0, Y: 0}}),
			}},
			want: ErrInvalidAgentName,
		},
		{
			name: "a zoom below the floor",
			patch: Patch{Operations: []Operation{
				SetViewport(Viewport{CenterX: 0, CenterY: 0, Zoom: 0.001}),
			}},
			want: ErrInvalidZoom,
		},
		{
			name: "reset carrying fields that do not belong to it",
			patch: Patch{Operations: []Operation{
				{Kind: OpReset, Positions: map[string]Point{"Atlas": {}}},
			}},
			want: ErrInvalidPatch,
		},
		{
			name: "set_viewport carrying positions",
			patch: Patch{Operations: []Operation{
				{Kind: OpSetViewport, Viewport: &Viewport{Zoom: 1}, Positions: map[string]Point{"Atlas": {}}},
			}},
			want: ErrInvalidPatch,
		},
		{
			name:  "a negative expected revision",
			patch: Patch{Operations: []Operation{Reset()}, ExpectedRevision: -1},
			want:  ErrInvalidPatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizePatch(tc.patch); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizePatchEnforcesSizeLimits(t *testing.T) {
	ops := make([]Operation, MaxOperationsPerPatch+1)
	for i := range ops {
		ops[i] = Reset()
	}
	if _, err := NormalizePatch(Patch{Operations: ops}); !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("too many operations: error = %v, want ErrPatchTooLarge", err)
	}

	positions := make(map[string]Point, MaxPositionsPerOperation+1)
	for i := 0; i <= MaxPositionsPerOperation; i++ {
		positions[string(rune('a'+i%26))+string(rune('0'+i/26%10))+string(rune('A'+i/260))] = Point{}
	}
	if len(positions) <= MaxPositionsPerOperation {
		t.Skipf("fixture generated only %d unique names; nothing to assert", len(positions))
	}
	if _, err := NormalizePatch(Patch{Operations: []Operation{SetPositions(positions)}}); !errors.Is(err, ErrPatchTooLarge) {
		t.Fatalf("too many positions: error = %v, want ErrPatchTooLarge", err)
	}
}

// A restore with no positions is how a client says "the layout was empty before
// the reset", which is exactly what makes a reset undoable (FR-72).
func TestRestorePositionsAcceptsAnEmptySet(t *testing.T) {
	normalized, err := NormalizePatch(Patch{Operations: []Operation{RestorePositions(nil)}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(normalized.Operations) != 1 || normalized.Operations[0].Kind != OpRestorePositions {
		t.Fatalf("operations = %+v, want a single restore", normalized.Operations)
	}
}

func TestNormalizePatchTrimsNamesAndPreservesGeometry(t *testing.T) {
	normalized, err := NormalizePatch(Patch{
		Operations: []Operation{
			SetPositions(map[string]Point{" Atlas ": {X: 12.5, Y: -7.25}}),
			SetPreferences(false),
		},
		ExpectedRevision: 4,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if normalized.ExpectedRevision != 4 {
		t.Fatalf("expected revision = %d, want 4", normalized.ExpectedRevision)
	}
	point, ok := normalized.Operations[0].Positions["Atlas"]
	if !ok {
		t.Fatalf("positions = %v, want a trimmed \"Atlas\" key", normalized.Operations[0].Positions)
	}
	if point.X != 12.5 || point.Y != -7.25 {
		t.Fatalf("point = %v, want the exact input geometry", point)
	}
	if normalized.Operations[1].SnapToGrid == nil || *normalized.Operations[1].SnapToGrid {
		t.Fatal("snap preference should round-trip as false")
	}
}

func TestSortedAgentNamesIsStable(t *testing.T) {
	positions := map[string]Point{"Cinder": {}, "Atlas": {}, "Beacon": {}}
	got := SortedAgentNames(positions)
	want := []string{"Atlas", "Beacon", "Cinder"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}
