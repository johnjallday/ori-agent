package reapersetup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestSetupAdapterIsReadOnly enforces the property that makes the wizard's
// REAPER steps safe to click: answering a question installs nothing.
//
// Every plugin install, global enable, workspace attachment, agent assignment,
// and native-access grant stays an explicit action the user takes at its own
// endpoint. The adapter's job is to look and report — so an assertion about
// behavior is not enough here, because the regression this guards against is
// someone later making Confirm "helpfully" perform the repair it just
// described. This reads the source and refuses the tools to do it.
func TestSetupAdapterIsReadOnly(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "setup_adapter.go", nil, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Verbs that change the machine, the workspace, or the project file.
	forbidden := []string{
		"install", "enable", "disable", "attach", "detach", "assign",
		"grant", "repair", "start", "write", "create", "save", "update", "delete", "remove",
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		}
		lower := strings.ToLower(name)
		for _, verb := range forbidden {
			if strings.Contains(lower, verb) {
				t.Errorf("%s calls %s: choosing a mode must not %s anything",
					fset.Position(call.Pos()), name, verb)
			}
		}
		return true
	})
}
