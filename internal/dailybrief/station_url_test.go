package dailybrief

import "testing"

func TestStationURL(t *testing.T) {
	cases := []struct {
		name, slug, want string
	}{
		{"a folder slug", "my-hq", "/workspaces/my-hq?station=daily-brief"},
		{"surrounding space is trimmed", " hq-2 ", "/workspaces/hq-2?station=daily-brief"},
		{"empty", "", ""},
		{"a display name is not a slug", "My HQ", ""},
		{"no path traversal", "../settings", ""},
		{"no nested path", "a/b", ""},
		{"no query injection", "hq?next=//evil.example", ""},
		{"must start with a letter or digit", "-hq", ""},
	}
	for _, tc := range cases {
		if got := StationURL(tc.slug); got != tc.want {
			t.Errorf("%s: StationURL(%q) = %q, want %q", tc.name, tc.slug, got, tc.want)
		}
	}
}
