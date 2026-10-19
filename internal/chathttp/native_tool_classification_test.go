package chathttp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestNativeWorkspaceToolsAreClassified fails when a tool this package defines
// has no entry in the autonomy gate's native classification table
// (workspace.NativeToolSideEffect). A goal run reaches these tools, and an
// unclassified one would otherwise fall back to whatever the workspace's
// binding defaults say (workspace-goal-suggestions FR10).
//
// It reads every toolapi.ToolDefinition literal in the package source, so a
// new tool cannot be added without deciding how the gate treats it.
func TestNativeWorkspaceToolsAreClassified(t *testing.T) {
	names := toolDefinitionNames(t, ".")
	if len(names) < 20 {
		t.Fatalf("found only %d tool definitions; the source scan is broken: %v", len(names), names)
	}
	for _, name := range names {
		if _, ok := workspace.NativeToolSideEffect(name); !ok {
			t.Errorf("tool %q is not classified in workspace.nativeToolSideEffects", name)
		}
	}
}

// toolDefinitionNames returns the Name of every toolapi.ToolDefinition
// composite literal in the non-test Go files of dir. Names given as the
// workspace memory constants are resolved.
func toolDefinitionNames(t *testing.T, dir string) []string {
	t.Helper()
	constants := map[string]string{
		"MemoryWriteToolName":         workspace.MemoryWriteToolName,
		"MemoryForgetToolName":        workspace.MemoryForgetToolName,
		"MemoryProposeGlobalToolName": workspace.MemoryProposeGlobalToolName,
		"MemoryRecallToolName":        workspace.MemoryRecallToolName,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ToolDefinition" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "toolapi" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Name" {
					continue
				}
				var toolName string
				switch value := kv.Value.(type) {
				case *ast.BasicLit:
					toolName, _ = strconv.Unquote(value.Value)
				case *ast.SelectorExpr:
					toolName = constants[value.Sel.Name]
				}
				if toolName == "" {
					t.Errorf("%s: a tool definition's Name is not a literal or a known constant", fset.Position(kv.Pos()))
					continue
				}
				if !seen[toolName] {
					seen[toolName] = true
					names = append(names, toolName)
				}
			}
			return true
		})
	}
	return names
}
