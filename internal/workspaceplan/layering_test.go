package workspaceplan

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The workspace execution slot sits ABOVE the Task executor (FR-100, FR-106).
//
// Standalone Tasks — the ones no Plan materialized — must keep their existing
// scheduler, recurring behavior, global maximum (executor.go's maxConcurrent),
// and provider-specific limits exactly as they were. The cheapest and most
// durable proof of that is structural: internal/workspace cannot consult the
// slot, because it cannot see this package at all.
//
// This is a layering test rather than a behavioral one on purpose. A behavioral
// test would show that one standalone Task still runs; this shows that NO code
// path in the task machinery could ever be routed through plan arbitration,
// which is the actual requirement.
func TestTaskMachineryCannotSeePlanArbitration(t *testing.T) {
	const planPackage = "internal/workspaceplan"

	// Both packages that own standalone task execution and scheduling.
	for _, dir := range []string{"../workspace", "../workspacerun"} {
		imports, err := packageImports(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, path := range imports {
			if strings.HasSuffix(path, planPackage) {
				t.Errorf("%s imports %s: standalone task scheduling must not be able "+
					"to reach plan arbitration", dir, path)
			}
		}
	}
}

// packageImports returns every import path in a directory's non-test Go files.
func packageImports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}
