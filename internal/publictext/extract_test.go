package publictext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExtractBoundedHTMLContainsVisibleReferenceTextOnly(t *testing.T) {
	body := `<html><title>Community &amp; groups</title><script>PRIVATE_SCRIPT_SENTINEL</script><style>PRIVATE_STYLE_SENTINEL</style><noscript>PRIVATE_NOSCRIPT_SENTINEL</noscript><body><h1>Public guidance</h1><p>Compare ` + strings.Repeat("界", 6000) + `</p></body></html>`
	title, text, truncated := Extract([]byte(body), "text/html", 4000)
	if title != "Community & groups" || utf8.RuneCountInString(text) != 4000 || !truncated || strings.Contains(text, "PRIVATE_") || strings.Contains(text, "<p>") {
		t.Fatalf("visible bounded text: %q %d %v", title, utf8.RuneCountInString(text), truncated)
	}
}

func TestExtractStreamingHandlesHostileDepthAndLegacyFullPlainText(t *testing.T) {
	body := strings.Repeat("<div>", 100000) + "safe reference" + strings.Repeat("</div>", 100000)
	_, text, truncated := Extract([]byte(body), "text/html", 4000)
	if text != "safe reference" || truncated {
		t.Fatalf("deep HTML extraction: %q %v", text, truncated)
	}
	_, text, truncated = Extract([]byte("  hello\n world  "), "text/plain", 0)
	if text != "hello world" || truncated {
		t.Fatalf("legacy full extraction: %q %v", text, truncated)
	}
	_, text, truncated = Extract([]byte(strings.Repeat("界", 20)), "text/markdown", 8)
	if utf8.RuneCountInString(text) != 8 || !truncated {
		t.Fatal("multibyte text limit was not honored")
	}
}
