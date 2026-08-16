package workspacemap

import (
	"fmt"
	"sort"
	"strings"
)

// This file owns the current user's *district presentation* for a group: the
// rectangle they sized by hand, whether the district is collapsed, and which
// curated accent and theme it wears (#346 FR-3, FR-173, FR-174).
//
// It is deliberately separate from Point. A district is a rectangle with a
// sizing mode, a collapsed state, and two preset identifiers; overloading the
// node-anchor type with width, height, collapse, and theme fields would have
// made every ordinary building carry five fields that can only ever be null for
// it, and would have let a plain drop operation express a theme change
// (PRD Technical Considerations).
//
// Nothing here can name a parent, an order index, or any other hierarchy field,
// so no presentation write has the vocabulary to change membership (FR-5, FR-62).

// SizingMode says where a district's effective frame comes from.
//
// auto   — the frame follows the members: it is the tight padded box around
// them, and it shrinks again when they move inward (FR-32, FR-33).
// custom — the stored frame is the user's chosen *minimum*: the effective frame
// is the union of it and whatever the members currently need, and it never
// shrinks on its own (FR-34 – FR-37).
type SizingMode string

const (
	// SizingModeAuto is the default for a district that has never been resized
	// and the mode Fit to contents returns a district to (FR-31, FR-40).
	SizingModeAuto SizingMode = "auto"
	// SizingModeCustom marks a district whose stored rectangle is a user-chosen
	// minimum (FR-42).
	SizingModeCustom SizingMode = "custom"
)

// IsValid reports whether the mode is one this build understands. An unknown
// mode read from storage degrades to auto rather than failing the whole layout
// (FR-192).
func (m SizingMode) IsValid() bool {
	return m == SizingModeAuto || m == SizingModeCustom
}

// DefaultAccent and DefaultTheme are the migration-safe fallbacks. They are what
// an un-customized district wears, what an unknown or removed identifier falls
// back to, and what Use default appearance restores (FR-127, FR-137).
const (
	DefaultAccent = "default"
	DefaultTheme  = "default"
)

// accentCatalog is the app-defined accent set: the default Ori district accent
// plus five visually distinct alternatives (FR-122). The identifiers are stable
// app values — a client cannot invent one, and none of them is ever interpolated
// into CSS as a value; they select a checked-in preset class (FR-125, FR-194).
var accentCatalog = map[string]string{
	DefaultAccent: "Keeper amber",
	"moss":        "Moss green",
	"tide":        "Tide teal",
	"beacon":      "Beacon blue",
	"orchid":      "Orchid violet",
	"slate":       "Slate grey",
}

// themeCatalog is the app-defined district treatment set: the default plus two
// distinct alternatives (FR-123). A theme controls only the district frame,
// surface, header, and an optional code-native motif — never a building, a
// character, the terrain, or application chrome (FR-128).
var themeCatalog = map[string]string{
	DefaultTheme: "Standard district",
	"blueprint":  "Blueprint",
	"terrace":    "Terrace",
}

// Accents returns the curated accent identifiers in a stable order. The client
// renders the same catalog; this is the canonical list the server validates
// against so the two cannot drift apart silently (FR-125).
func Accents() []string { return sortedKeys(accentCatalog) }

// Themes returns the curated district-theme identifiers in a stable order.
func Themes() []string { return sortedKeys(themeCatalog) }

// AccentLabel returns the human name for an accent, or "" when unknown.
func AccentLabel(id string) string { return accentCatalog[id] }

// ThemeLabel returns the human name for a district theme, or "" when unknown.
func ThemeLabel(id string) string { return themeCatalog[id] }

// IsSupportedAccent reports whether the identifier is in the curated catalog.
func IsSupportedAccent(id string) bool {
	_, ok := accentCatalog[id]
	return ok
}

// IsSupportedTheme reports whether the identifier is in the curated catalog.
func IsSupportedTheme(id string) bool {
	_, ok := themeCatalog[id]
	return ok
}

func sortedKeys(catalog map[string]string) []string {
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Frame is a district's axis-aligned world-space rectangle (FR-29).
//
// Its top-left corner is the district's visible *and* logical anchor: selection,
// centering, and movement all resolve against it, and there is no second hidden
// anchor that can enlarge the rendered outline (FR-46).
type Frame struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// IsFinite reports whether every component is a real number.
func (f Frame) IsFinite() bool {
	return isFinite(f.X) && isFinite(f.Y) && isFinite(f.Width) && isFinite(f.Height)
}

// MaxX is the right edge.
func (f Frame) MaxX() float64 { return f.X + f.Width }

// MaxY is the bottom edge.
func (f Frame) MaxY() float64 { return f.Y + f.Height }

// InSafeRange reports whether the whole rectangle — corner and far edges — lies
// inside the documented world bounds (FR-44).
func (f Frame) InSafeRange() bool {
	return f.IsFinite() &&
		f.X >= MinCoordinate && f.Y >= MinCoordinate &&
		f.MaxX() <= MaxCoordinate && f.MaxY() <= MaxCoordinate
}

// Translate returns the rectangle moved by a world-space delta, keeping its
// width and height exactly (FR-91).
func (f Frame) Translate(delta Point) Frame {
	return Frame{X: f.X + delta.X, Y: f.Y + delta.Y, Width: f.Width, Height: f.Height}
}

// Union returns the smallest rectangle containing both. This is how a custom
// minimum and the rectangle its members currently require resolve into one
// effective frame (FR-35).
func (f Frame) Union(other Frame) Frame {
	minX := min(f.X, other.X)
	minY := min(f.Y, other.Y)
	maxX := max(f.MaxX(), other.MaxX())
	maxY := max(f.MaxY(), other.MaxY())
	return Frame{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}
}

// GroupPresentation is one group district's presentation state for one user.
//
// Frame is populated only in custom mode: an automatic district has nothing to
// store, because its rectangle is recomputed from its members on every render
// and persisting it would turn a read into a write (FR-26, FR-193).
type GroupPresentation struct {
	SizingMode SizingMode `json:"sizing_mode"`
	Frame      *Frame     `json:"frame,omitempty"`
	Collapsed  bool       `json:"collapsed"`
	Accent     string     `json:"accent"`
	Theme      string     `json:"theme"`
}

// DefaultGroupPresentation is what a district with no saved record renders as:
// automatic sizing, expanded, default appearance (FR-18 – FR-20, FR-31, FR-101).
func DefaultGroupPresentation() GroupPresentation {
	return GroupPresentation{
		SizingMode: SizingModeAuto,
		Collapsed:  false,
		Accent:     DefaultAccent,
		Theme:      DefaultTheme,
	}
}

// IsDefault reports whether the record carries nothing worth storing. A record
// that has drifted back to every default is deleted rather than kept, so an
// abandoned customization does not count against the per-layout bound.
func (p GroupPresentation) IsDefault() bool {
	return p.SizingMode == SizingModeAuto && p.Frame == nil && !p.Collapsed &&
		p.Accent == DefaultAccent && p.Theme == DefaultTheme
}

// HasCustomAppearance reports whether the district wears anything other than the
// default accent and theme. Use default appearance is offered only when it would
// actually change something (FR-137, FR-146).
func (p GroupPresentation) HasCustomAppearance() bool {
	return p.Accent != DefaultAccent || p.Theme != DefaultTheme
}

// SanitizeGroupPresentation is the tolerant read-side normalizer.
//
// It never fails: one corrupt field costs that district that one thing and
// nothing else, and one corrupt record must not discard the valid presentation
// of every other group (FR-192). An unknown preset falls back to the default
// rather than reaching the stylesheet (FR-194), and a custom mode whose stored
// rectangle is unusable degrades to automatic sizing, which is always drawable.
func SanitizeGroupPresentation(p GroupPresentation) GroupPresentation {
	clean := DefaultGroupPresentation()
	clean.Collapsed = p.Collapsed
	if IsSupportedAccent(p.Accent) {
		clean.Accent = p.Accent
	}
	if IsSupportedTheme(p.Theme) {
		clean.Theme = p.Theme
	}
	if p.SizingMode == SizingModeCustom && p.Frame != nil {
		if frame, err := NormalizeFrame(*p.Frame); err == nil {
			clean.SizingMode = SizingModeCustom
			clean.Frame = &frame
		}
	}
	return clean
}

// NormalizeFrame validates a district rectangle and returns it rounded for
// storage. Like NormalizePoint it refuses rather than clamps: silently resizing
// a district the user did not resize is worse than telling them the gesture was
// rejected (FR-45).
func NormalizeFrame(f Frame) (Frame, error) {
	if !f.IsFinite() {
		return Frame{}, fmt.Errorf("%w: not finite", ErrInvalidFrame)
	}
	normalized := Frame{
		X:      roundTo(f.X, coordinateScale),
		Y:      roundTo(f.Y, coordinateScale),
		Width:  roundTo(f.Width, coordinateScale),
		Height: roundTo(f.Height, coordinateScale),
	}
	if normalized.Width < MinFrameWidth || normalized.Height < MinFrameHeight {
		return Frame{}, fmt.Errorf("%w: %g x %g is smaller than the %g x %g minimum",
			ErrInvalidFrame, normalized.Width, normalized.Height, MinFrameWidth, MinFrameHeight)
	}
	if !normalized.InSafeRange() {
		return Frame{}, fmt.Errorf("%w: (%g, %g) %g x %g leaves the safe world",
			ErrInvalidFrame, normalized.X, normalized.Y, normalized.Width, normalized.Height)
	}
	return normalized, nil
}

// NormalizeAccent validates a curated accent identifier for a write.
func NormalizeAccent(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if !IsSupportedAccent(trimmed) {
		return "", fmt.Errorf("%w: accent %q", ErrUnsupportedPreset, id)
	}
	return trimmed, nil
}

// NormalizeTheme validates a curated district-theme identifier for a write.
func NormalizeTheme(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if !IsSupportedTheme(trimmed) {
		return "", fmt.Errorf("%w: theme %q", ErrUnsupportedPreset, id)
	}
	return trimmed, nil
}
