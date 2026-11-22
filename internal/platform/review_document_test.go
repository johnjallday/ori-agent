package platform

import "testing"

func TestReviewDocumentViewer(t *testing.T) {
	for name, want := range map[string]string{
		"draft (2).PDF": "Preview", "photo.jpg": "Preview", "draft.DOCX": "TextEdit", "sample.mp4": "QuickTime Player",
		"note.pdf.command": "", "installer.pkg": "", "archive.zip": "", "website.webloc": "", "macro.docm": "", "tool": "",
	} {
		if got := ReviewDocumentViewer(name); got != want {
			t.Errorf("viewer(%q) = %q, want %q", name, got, want)
		}
	}
}
