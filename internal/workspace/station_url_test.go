package workspace

import "testing"

func TestStationURL(t *testing.T) {
	cases := []struct {
		name, slug, key, want string
	}{
		{"a folder slug and a station key", "my-hq", "daily-brief", "/workspaces/my-hq?station=daily-brief"},
		{"any station", "album-3", "memory", "/workspaces/album-3?station=memory"},
		{"surrounding space is trimmed", " hq-2 ", " memory ", "/workspaces/hq-2?station=memory"},
		{"empty slug", "", "memory", ""},
		{"empty key", "my-hq", "", ""},
		{"a display name is not a slug", "My HQ", "memory", ""},
		{"no path traversal", "../settings", "memory", ""},
		{"no nested path", "a/b", "memory", ""},
		{"no query injection through the slug", "hq?next=//evil.example", "memory", ""},
		{"no query injection through the key", "my-hq", "memory&next=//evil.example", ""},
		{"must start with a letter or digit", "-hq", "memory", ""},
	}
	for _, tc := range cases {
		if got := StationURL(tc.slug, tc.key); got != tc.want {
			t.Errorf("%s: StationURL(%q, %q) = %q, want %q", tc.name, tc.slug, tc.key, got, tc.want)
		}
	}
}
