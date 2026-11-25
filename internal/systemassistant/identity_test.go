package systemassistant

import "testing"

// The retired assistant's record is recognized by its durable marker only.
func TestHasProtectedMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
		want bool
	}{
		{name: "marker", tags: []string{"system", ProtectedMarker}, want: true},
		{name: "marker case and spacing", tags: []string{"  ORI:System-Assistant "}, want: true},
		{name: "ordinary descriptive tags", tags: []string{"system", "assistant", "orchestrator"}, want: false},
		{name: "no tags", tags: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasProtectedMarker(tc.tags); got != tc.want {
				t.Fatalf("HasProtectedMarker(%v) = %v, want %v", tc.tags, got, tc.want)
			}
		})
	}
}
