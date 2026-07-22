package dailybrief

import (
	"reflect"
	"strings"
	"testing"
)

// TestSnapshotSourcesHasNoCalendarSource is a characterization guard for
// FR54: Calendar Ops must never add a live call to Daily Brief generation in
// this release. SnapshotSources is the exhaustive list of inputs
// BuildSnapshot reads from; asserting no field name mentions "calendar"
// documents the current boundary and fails loudly if a future change
// crosses it, rather than only failing an eyeballed code review.
func TestSnapshotSourcesHasNoCalendarSource(t *testing.T) {
	assertNoFieldMentionsCalendar(t, reflect.TypeFor[SnapshotSources]())
}

// TestSnapshotHasNoCalendarField is the same guard applied to the output
// side: the snapshot Daily Brief synthesis reads from must not carry a
// calendar-derived field either.
func TestSnapshotHasNoCalendarField(t *testing.T) {
	assertNoFieldMentionsCalendar(t, reflect.TypeFor[Snapshot]())
}

func assertNoFieldMentionsCalendar(t *testing.T, typ reflect.Type) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if strings.Contains(strings.ToLower(name), "calendar") {
			t.Fatalf("%s.%s: Daily Brief must not gain a calendar-derived field in this release (FR54)", typ.Name(), name)
		}
	}
}
