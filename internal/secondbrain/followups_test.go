package secondbrain

import "testing"

func TestSplitDetail_ReadsBackWhatItemDetailWrites(t *testing.T) {
	link := "obsidian://open?vault=Notes&file=Projects%2FAlbum%20Release.md"
	cases := []struct {
		name     string
		detail   string
		wantNote string
		wantLink string
	}{
		{name: "note only", detail: itemDetail(LookItem{Note: "Projects/Album Release.md"}), wantNote: "Projects/Album Release.md"},
		{name: "note and link", detail: itemDetail(LookItem{Note: "Projects/Album Release.md", Link: link}), wantNote: "Projects/Album Release.md", wantLink: link},
		{name: "done suffix after the link", detail: itemDetail(LookItem{Note: "Inbox.md", Link: link}) + "\n" + DoneInNotes, wantNote: "Inbox.md", wantLink: link},
		{name: "done suffix without a link", detail: "Inbox.md\n" + DoneInView, wantNote: "Inbox.md"},
		{name: "windows line endings", detail: "Inbox.md\r\nOpen in Obsidian: " + link + "\r\n", wantNote: "Inbox.md", wantLink: link},
		{name: "empty", detail: "", wantNote: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note, gotLink := SplitDetail(tc.detail)
			if note != tc.wantNote || gotLink != tc.wantLink {
				t.Fatalf("SplitDetail(%q) = (%q, %q), want (%q, %q)", tc.detail, note, gotLink, tc.wantNote, tc.wantLink)
			}
		})
	}
}
