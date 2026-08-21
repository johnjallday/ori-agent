package reaper

import "strings"

// namedColors is the fixed REAPER-compatible swatch set (PRD open question 1:
// a fixed set, not a full picker). It is the single source of truth for the
// color name an agent's propose_reaper_track_edits call may use; the console
// palette in reaper-console.js hardcodes the identical hex values so a click
// on its "Red" swatch and an agent's "red" produce the exact same
// I_CUSTOMCOLOR integer. Keep the two in sync if either changes.
var namedColors = map[string]int64{
	"red":    trackCustomColorFlag | 0xef765d,
	"orange": trackCustomColorFlag | 0xe8b54b,
	"green":  trackCustomColorFlag | 0x5ed0a7,
	"blue":   trackCustomColorFlag | 0x4f8ff7,
	"purple": trackCustomColorFlag | 0x9b7fe8,
	"pink":   trackCustomColorFlag | 0xe87fc0,
	"gray":   trackCustomColorFlag | 0x8a97a1,
	"none":   0,
}

// NamedColor resolves a fixed palette name (case-insensitive) to REAPER's raw
// I_CUSTOMCOLOR integer. The second return is false when name is not in the
// fixed set.
func NamedColor(name string) (int64, bool) {
	value, ok := namedColors[strings.ToLower(strings.TrimSpace(name))]
	return value, ok
}

// NamedColorNames lists the fixed palette in a stable order, for a tool
// description or a validation error message.
func NamedColorNames() []string {
	return []string{"red", "orange", "green", "blue", "purple", "pink", "gray", "none"}
}
