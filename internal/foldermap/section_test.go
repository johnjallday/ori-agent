package foldermap

import "testing"

func TestProjectSectionUsesFirstAncestor(t *testing.T) {
	for _, tc := range []struct{ folder, section, breadcrumb string }{
		{"2025/Artist A/Disc 1/Song One", "2025", "Artist A/Disc 1"},
		{"Albums/Album-5", "Albums", ""}, {"Picker Songs/Track-1", "Picker Songs", ""},
		{"Track-1", "", ""}, {"", "", ""}, {".", "", ""},
		{"../Albums/Song", "", ""}, {"/Music/Song", "", ""},
	} {
		if section, breadcrumb := ProjectSection(tc.folder); section != tc.section || breadcrumb != tc.breadcrumb {
			t.Errorf("ProjectSection(%q) = (%q, %q), want (%q, %q)", tc.folder, section, breadcrumb, tc.section, tc.breadcrumb)
		}
	}
}
