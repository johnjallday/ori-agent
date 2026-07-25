package drive

import (
	"strings"
	"testing"
)

func TestSanitizeText_StripsControlAndTrims(t *testing.T) {
	// Bell + NUL + a terminal escape are dropped; tab and newline survive.
	in := "  he\x07llo\x00\nwo\x1b[31mrld\t  "
	got := SanitizeText(in)
	want := "hello\nwo[31mrld" // ESC (\x1b) stripped, "[31mrld" remains; tab trimmed off the end
	if got != want {
		t.Fatalf("SanitizeText = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\x00\x07\x1b") {
		t.Errorf("control characters survived: %q", got)
	}
}

func TestSanitizeText_BoundsWithMarker(t *testing.T) {
	in := strings.Repeat("a", maxContentLen+500)
	got := SanitizeText(in)
	if !strings.HasSuffix(got, truncationMarker) {
		t.Errorf("over-long content must carry the truncation marker; got tail %q", got[len(got)-40:])
	}
	if len([]rune(got)) > maxContentLen+len([]rune(truncationMarker)) {
		t.Errorf("bounded length exceeded: %d runes", len([]rune(got)))
	}
}

func TestSanitizeText_Empty(t *testing.T) {
	if got := SanitizeText(""); got != "" {
		t.Errorf("SanitizeText(\"\") = %q, want empty", got)
	}
	if got := SanitizeText("\x00\x01  "); got != "" {
		t.Errorf("all-control/whitespace input should sanitize to empty, got %q", got)
	}
}

func TestFenceText_NoticeOnceOnFirstBlock(t *testing.T) {
	first := FenceText("some file body", true)
	if !strings.HasPrefix(first, UntrustedContentNotice) {
		t.Error("first block must be prefixed with the untrusted-content notice")
	}
	if !strings.Contains(first, "some file body") {
		t.Error("first block must retain the sanitized content")
	}

	rest := FenceText("second block", false)
	if strings.Contains(rest, UntrustedContentNotice) {
		t.Error("non-first blocks must not repeat the notice")
	}

	// An empty block never becomes a bare notice.
	if got := FenceText("   ", true); got != "" {
		t.Errorf("empty first block should stay empty, got %q", got)
	}
}
