package downloadsjanitor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTestsCannotReachARealDownloadsFolder is FR-142 as a test about the tests.
//
// This package moves and trashes real files. A tilde path is harmless while it
// stays an unexpanded suggestion — several tests assert exactly that it is not
// resolved — but the moment one reaches a call that expands it, it points at a
// real person's folder. So the rule is narrow and about what actually bites:
// a test that hands a ~ path to a mutating call must redirect HOME to a temp
// dir first. The way this goes wrong is mundane — someone copies a case and
// drops the t.Setenv("HOME") — and the cost is the developer's own Downloads
// folder being filed.
func TestTestsCannotReachARealDownloadsFolder(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	funcStart := regexp.MustCompile(`^func (Test\w+)\(`)

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name)) // #nosec G304 -- fixed suffix inside this package's own directory
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		current := ""
		bodies := map[string]*strings.Builder{}
		for _, line := range strings.Split(string(data), "\n") {
			if match := funcStart.FindStringSubmatch(line); match != nil {
				current = match[1]
				bodies[current] = &strings.Builder{}
				continue
			}
			if current != "" {
				bodies[current].WriteString(line)
				bodies[current].WriteString("\n")
			}
		}
		// Calls that resolve a path against the real filesystem and then act on
		// what they find there.
		expanding := []string{"ConfirmSetup(", "Apply(", "Scan(", "ApproveAutomation(", "expandPath("}
		for testName, body := range bodies {
			text := body.String()
			if !strings.Contains(text, "~/") {
				continue
			}
			acts := false
			for _, call := range expanding {
				if strings.Contains(text, call) {
					acts = true
					break
				}
			}
			if !acts {
				// A tilde that is only ever compared as a string cannot reach a
				// folder; some tests exist precisely to prove it stays unresolved.
				continue
			}
			if !strings.Contains(text, `t.Setenv("HOME"`) {
				t.Errorf("%s/%s hands a ~ path to a call that expands it without redirecting HOME to a temp dir first", name, testName)
			}
		}
	}
}

// TestNoTestWritesOutsideATempDir pins the other half: the package's own
// helpers build fixtures under t.TempDir(), never under a path derived from the
// real environment.
func TestNoTestWritesOutsideATempDir(t *testing.T) {
	root := inboxFixture(t)
	temp := os.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}
	resolvedTemp, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatalf("resolve temp: %v", err)
	}
	if !strings.HasPrefix(resolvedRoot, resolvedTemp) {
		t.Fatalf("fixture %q is not under the temp dir %q", resolvedRoot, resolvedTemp)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(resolvedRoot, filepath.Join(home, "Downloads")) {
		t.Fatalf("fixture %q is inside a real Downloads folder", resolvedRoot)
	}
}
