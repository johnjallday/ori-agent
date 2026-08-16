package web

import (
	"io/fs"
	"strings"
	"testing"
)

// retiredProductLabels are the names Issue #350 retired in favour of "Ask Ori".
//
// They may still appear in migration aliases, resolver fixtures, diagnostics,
// and comments explaining the compatibility — but never in copy a user reads
// (FR61/FR62/FR85).
var retiredProductLabels = []string{
	"Workspace Manager",
	"Workspace Assistant",
	"Workspaces Assistant",
	"Task Assistant",
}

// legacyCopyAllowlist is the reviewed set of rendered-copy exceptions.
//
// It is deliberately empty. An entry here means a user can still read a retired
// product name somewhere, so adding one is a product decision, not a way to make
// this test pass — the fix is almost always to change the copy.
var legacyCopyAllowlist = map[string][]string{}

// stripTemplateComments removes HTML and Go-template comments, which is where
// the migration rationale legitimately names the retired labels.
func stripTemplateComments(body string) string {
	for _, delim := range [][2]string{{"<!--", "-->"}, {"{{/*", "*/}}"}} {
		var out strings.Builder
		rest := body
		for {
			start := strings.Index(rest, delim[0])
			if start < 0 {
				out.WriteString(rest)
				break
			}
			out.WriteString(rest[:start])
			end := strings.Index(rest[start:], delim[1])
			if end < 0 {
				break
			}
			rest = rest[start+end+len(delim[1]):]
		}
		body = out.String()
	}
	return body
}

// No template may render a retired product label to a user.
//
// This is the guard that keeps the rename from quietly regressing: the labels
// are still legal in code that resolves them, so nothing else would catch a new
// one appearing in markup.
func TestTemplatesRenderNoRetiredProductLabel(t *testing.T) {
	err := fs.WalkDir(Templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tmpl") {
			return nil
		}

		body := stripTemplateComments(readTemplate(t, path))
		allowed := legacyCopyAllowlist[path]

		for _, label := range retiredProductLabels {
			if !strings.Contains(body, label) {
				continue
			}
			if containsString(allowed, label) {
				continue
			}
			t.Errorf("%s renders the retired label %q; use Ask Ori plus context, "+
				"or add a reviewed entry to legacyCopyAllowlist", path, label)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
