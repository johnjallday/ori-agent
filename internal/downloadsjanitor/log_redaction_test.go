package downloadsjanitor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogs_CarryNoFilenamesOrPaths is FR-143 enforced structurally rather than
// by review.
//
// Logs are the one place this feature's data can end up somewhere the user
// never looks — a shared log file, a support bundle, a crash report — and a
// filename is often the most sensitive thing about a file ("severance
// agreement.pdf"). The action journal legitimately holds names and relative
// destinations, because that is the user's own local audit trail and undo
// depends on it; a log line has no such justification.
//
// This walks every logger call in the package and fails on a field key that
// would carry a name or a path. It is deliberately a source check: a runtime
// assertion would only catch the branches a test happens to exercise, and the
// dangerous ones are error paths that rarely run.
func TestLogs_CarryNoFilenamesOrPaths(t *testing.T) {
	// Keys that would put a filename or filesystem path into a log line.
	banned := map[string]bool{
		"name":         true,
		"filename":     true,
		"file":         true,
		"file_name":    true,
		"path":         true,
		"root":         true,
		"root_path":    true,
		"source":       true,
		"source_path":  true,
		"destination":  true,
		"dest":         true,
		"folder":       true,
		"display":      true,
		"display_name": true,
	}

	// Every package that handles File Janitor data, not just this one.
	//
	// The capability lifecycle and the HTTP layer log about the same workspaces
	// and the same folders; a filename leaking from the handler is exactly as
	// bad as one leaking from the service, and the guard living in only one
	// package is how the other three quietly drift.
	packages := []string{
		".",
		"../downloadsjanitorhttp",
		"../workspacecapability",
		"../workspacecapabilityhttp",
	}

	fset := token.NewFileSet()
	checked := 0
	var sources []string
	for _, pkg := range packages {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatalf("read %s: %v", pkg, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			sources = append(sources, filepath.Join(pkg, entry.Name()))
		}
	}

	for _, source := range sources {
		name := source
		file, err := parser.ParseFile(fset, source, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "logger" {
				return true
			}
			checked++

			// logger.Warn(msg, logger.Fields{...}) — inspect the composite
			// literal's keys.
			for _, arg := range call.Args {
				composite, ok := arg.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, element := range composite.Elts {
					pair, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := pair.Key.(*ast.BasicLit)
					if !ok || key.Kind != token.STRING {
						continue
					}
					field := strings.Trim(key.Value, `"`)
					if banned[strings.ToLower(field)] {
						position := fset.Position(pair.Pos())
						t.Errorf(
							"%s:%d logs field %q, which carries a filename or path; log identifiers instead (FR-143)",
							name, position.Line, field,
						)
					}
				}
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("found no logger calls to check; the guard is not actually running")
	}
}
