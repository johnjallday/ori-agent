package web

import (
	"strings"
	"testing"
)

func TestQuestCalloutStaysCompactWithoutChangingMobileHelpSheet(t *testing.T) {
	data, err := Static.ReadFile("static/css/ori-guide.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	selector := `.ori-guide__panel:has(.ori-guide__reply[data-status="quest"]) {`
	start := strings.Index(css, selector)
	if start < 0 {
		t.Fatal("fixed walkthrough compact owner missing")
	}
	block := css[start:]
	end := strings.Index(block, "\n}")
	if end < 0 {
		t.Fatal("unbounded quest rule")
	}
	block = block[:end]
	for _, rule := range []string{"top: auto;", "left: auto;", "right: 20px;", "width: min(360px, calc(100vw - 40px));", "--ori-launcher-clearance-y"} {
		if !strings.Contains(block, rule) {
			t.Errorf("quest geometry missing %q", rule)
		}
	}
	if !strings.Contains(css, ".ori-guide__panel {\n    top:") || !strings.Contains(css, "width: 100%;\n    border-right: 0;") {
		t.Fatal("ordinary mobile Help sheet must remain available")
	}
}
