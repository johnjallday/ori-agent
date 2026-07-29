package setupwizard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestMigrationCannotActOnADomain is the structural half of FR-123. The
// behavioral tests show that today's migration changes nothing but the
// snapshot; this refuses the vocabulary to change anything else tomorrow.
//
// The risk is specific and plausible: a backfill that can see a workspace is
// half-configured is one short helpful edit away from "just" choosing the
// folder, or attaching the plugin, or starting the setup task — on someone's
// existing workspace, without them asking, on a page load they did not
// initiate. So this file may call the store, the clock, and the blueprint
// lookup, and nothing that acts.
func TestMigrationCannotActOnADomain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "migrate.go", nil, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	forbidden := []string{
		"install", "enable", "attach", "grant", "authorize", "authenticate",
		"connect", "link", "watch", "schedule", "seed", "start", "run",
		"approve", "confirm", "delete", "remove",
	}
	allowed := map[string]bool{
		// Reading and writing the workspace record itself is the whole job.
		"Update": true, "GetTemplateProvenance": true, "SetTemplateProvenance": true,
		"GetSetupWizardProgress": true, "SetSetupWizardProgress": true,
		"IsEmpty": true, "TrimSpace": true, "timestamp": true, "eligible": true,
		"blueprints": true, "Ready": true,
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
		if allowed[name] {
			return true
		}
		lower := strings.ToLower(name)
		for _, verb := range forbidden {
			if strings.Contains(lower, verb) {
				t.Errorf("%s calls %s: a backfill must not %s anything on a workspace the user did not ask to change",
					fset.Position(call.Pos()), name, verb)
			}
		}
		return true
	})
}
