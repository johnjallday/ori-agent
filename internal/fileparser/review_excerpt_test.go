package fileparser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReviewExcerptUsesPreviewAllowlistAndBounds(t *testing.T) {
	ctx := t.Context()
	for _, name := range []string{"notes", "notes.html", "notes.docm", "app.sh"} {
		if SupportsReviewFile(name) {
			t.Fatal(name)
		}
		if _, err := ExtractReviewExcerpt(ctx, name, []byte("valid plaintext")); !errors.Is(err, ErrNotText) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	e, err := ExtractReviewExcerpt(ctx, "brief.DOCX", archive(t, map[string][3]string{"word/document.xml": wordPart}, 1))
	if err != nil || e.Truncated || !strings.Contains(e.Text, "A paragraph of the document.") {
		t.Fatal(e, err)
	}
	e, err = ExtractReviewExcerpt(ctx, "notes.md", []byte("x"+strings.Repeat("表", ReviewExcerptBytes)))
	if err != nil || !e.Truncated || len(e.Text) > ReviewExcerptBytes || !utf8.ValidString(e.Text) {
		t.Fatal("excerpt limit", err)
	}
	if _, err := ExtractReviewExcerpt(ctx, "large.csv", make([]byte, ReviewFileBytes+1)); !errors.Is(err, ErrExpandedTooLarge) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ExtractReviewExcerpt(canceled, "scan.pdf", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
