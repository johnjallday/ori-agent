package mailbox

import (
	"html"
	"regexp"
	"strings"
)

// Email content is UNTRUSTED input (contract §3, §8): it may contain hostile
// instructions, active HTML, and tracking. The sanitizer's job is to reduce any
// message body to bounded, inert plain text safe to place in an LLM prompt as
// DATA. It does NOT try to detect "instructions" — downstream prompts label the
// text untrusted; this layer guarantees it carries no markup or active content
// and is length-bounded.

var (
	// scriptStyleRe strips <script>/<style> blocks (including content) first, so
	// their inner text never survives tag stripping.
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</\s*(script|style)\s*>`)
	// tagRe strips any remaining HTML/XML tag.
	tagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	// wsRe collapses runs of whitespace (including newlines) to a single space.
	wsRe = regexp.MustCompile(`\s+`)
	// quotedLineRe matches classic quoted-reply lines beginning with ">".
	quotedLineRe = regexp.MustCompile(`(?m)^\s*>.*$`)
	// replyMarkerRe matches an "On <date>, <someone> wrote:" reply header, after
	// which quoted history typically follows.
	replyMarkerRe = regexp.MustCompile(`(?is)\n?On\s.+?\swrote:.*$`)
)

// SanitizeText reduces raw message text/HTML to bounded, inert plain text:
// strips <script>/<style> and all tags, unescapes HTML entities, collapses
// whitespace, and truncates to maxLen runes (adding an ellipsis when cut). A
// non-positive maxLen falls back to MaxSnippetLen.
func SanitizeText(raw string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = MaxSnippetLen
	}
	s := scriptStyleRe.ReplaceAllString(raw, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	return truncateRunes(s, maxLen)
}

// StripQuotedHistory removes quoted reply history where practical: everything
// from an "On ... wrote:" marker onward, and standalone ">"-prefixed lines. It
// operates on plain text (call before SanitizeText's whitespace collapse, or on
// already-plain text). Best-effort — it never errors and returns the trimmed
// remainder.
func StripQuotedHistory(text string) string {
	s := replyMarkerRe.ReplaceAllString(text, "")
	s = quotedLineRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// truncateRunes cuts s to at most n runes, appending an ellipsis when it cuts,
// so multi-byte characters are never split.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
