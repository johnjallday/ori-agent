package llm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shared retry budget is a property of the whole package, not of any one
// function, and it is enforced by a single easily-deleted line per client:
// option.WithMaxRetries(0).
//
// Without it the SDKs retry internally, so Ori's three task-level attempts
// become up to nine HTTP requests — against, say, an exhausted quota. Nothing
// observable fails when that line goes missing; the cost is just silently
// tripled and slower failures. That is exactly the kind of regression a normal
// test cannot see, so this asserts on the source.

// TestEveryProviderClientDisablesSDKRetries fails if any provider constructs an
// SDK client without opting out of its built-in retry loop (FR 54, 84).
func TestEveryProviderClientDisablesSDKRetries(t *testing.T) {
	// Constructors whose clients Ori drives itself. A new provider using either
	// SDK must be added here (and must disable SDK retries).
	sdkConstructors := map[string]bool{
		"openai.NewClient":    true,
		"anthropic.NewClient": true,
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path) // #nosec G304 -- package-local source, test-only
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			if !sdkConstructors[name] {
				return true
			}
			checked++
			if !disablesRetries(call, src, fset) {
				t.Errorf("%s at %s constructs an SDK client without option.WithMaxRetries(0); "+
					"the SDK would then retry on top of Ori's own budget",
					name, fset.Position(call.Pos()))
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("found no SDK client constructions to check — has the wiring moved?")
	}
}

// callName renders a call's function as "pkg.Func", or "" when it is not a
// simple qualified call.
func callName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// disablesRetries reports whether the construction opts out of SDK retries,
// either inline or through an options slice built in the same function.
func disablesRetries(call *ast.CallExpr, src []byte, fset *token.FileSet) bool {
	for _, arg := range call.Args {
		if isWithMaxRetriesZero(arg) {
			return true
		}
	}
	// Options are often assembled into a slice first (the local-provider case).
	// Scan the enclosing function's source for the opt-out rather than
	// re-implementing data-flow analysis.
	start := fset.Position(call.Pos()).Offset
	windowStart := max(start-2000, 0)
	window := string(src[windowStart:min(start+200, len(src))])
	return strings.Contains(window, "WithMaxRetries(0)")
}

func isWithMaxRetriesZero(arg ast.Expr) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	if !strings.HasSuffix(callName(call), "WithMaxRetries") {
		return false
	}
	if len(call.Args) != 1 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "0"
}
