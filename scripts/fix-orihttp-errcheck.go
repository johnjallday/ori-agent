// Fixes common errcheck findings for orihttp.Respond* calls by wrapping them with:
//
//	if err := orihttp.RespondX(...); err != nil { logger.Error(...); }
//
// This is intentionally narrow and does NOT try to fix all errcheck violations.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	oriHTTPImportPath = "github.com/johnjallday/ori-agent/internal/http"
	loggerImportPath  = "github.com/johnjallday/ori-agent/internal/logger"
)

var respondFuncs = map[string]struct{}{
	"RespondAPIError":           {},
	"RespondBadRequest":         {},
	"RespondUnauthorized":       {},
	"RespondForbidden":          {},
	"RespondNotFound":           {},
	"RespondConflict":           {},
	"RespondValidationError":    {},
	"RespondInternalError":      {},
	"RespondServiceUnavailable": {},
	"RespondMethodNotAllowed":   {},
	"RespondNotImplemented":     {},
	"RespondError":              {},
	"RespondJSON":               {},
	"RespondSuccess":            {},
	"RespondCreated":            {},
}

type options struct {
	write bool
}

func main() {
	var opts options
	flag.BoolVar(&opts.write, "w", false, "write changes to files")
	flag.Parse()

	roots := flag.Args()
	if len(roots) == 0 {
		roots = []string{"internal"}
	}

	var changedFiles []string
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "dist", "bin", "build", "vendor", "node_modules":
					return fs.SkipDir
				}
				return nil
			}

			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}

			changed, err := fixFile(path, opts)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if changed {
				changedFiles = append(changedFiles, path)
			}
			return nil
		}); err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
	}

	if !opts.write {
		for _, f := range changedFiles {
			fmt.Println(f)
		}
	}
}

func fixFile(path string, opts options) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Contains(original, []byte("Code generated")) {
		return false, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, original, parser.ParseComments)
	if err != nil {
		return false, err
	}

	orihttpIdent, ok := importLocalName(file, oriHTTPImportPath)
	if !ok {
		return false, nil
	}

	loggerIdent, loggerImported := importLocalName(file, loggerImportPath)
	if !loggerImported {
		loggerIdent = "logger"
	}

	changed := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if processBlock(fn.Body, orihttpIdent, loggerIdent) {
			changed = true
		}
	}

	if !changed {
		return false, nil
	}

	if !loggerImported {
		addImportDecl(file, loggerImportPath)
	}

	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return false, err
	}
	if bytes.Equal(original, out.Bytes()) {
		return false, nil
	}

	if opts.write {
		return true, os.WriteFile(path, out.Bytes(), 0o644)
	}
	return true, nil
}

func importLocalName(f *ast.File, importPath string) (string, bool) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if p != importPath {
			continue
		}
		if imp.Name != nil && imp.Name.Name != "" {
			return imp.Name.Name, true
		}
		parts := strings.Split(p, "/")
		return parts[len(parts)-1], true
	}
	return "", false
}

func addImportDecl(f *ast.File, importPath string) {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err == nil && p == importPath {
			return
		}
	}

	newSpec := &ast.ImportSpec{
		Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(importPath)},
	}

	// Prefer adding to an existing import block.
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		gd.Specs = append(gd.Specs, newSpec)
		return
	}

	newImport := &ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{newSpec}}

	// Insert after the last existing import decl if present, else right after package decl.
	insertAt := 0
	for i, decl := range f.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			insertAt = i + 1
		}
	}
	f.Decls = append(f.Decls, nil)
	copy(f.Decls[insertAt+1:], f.Decls[insertAt:])
	f.Decls[insertAt] = newImport
}

func processBlock(b *ast.BlockStmt, orihttpIdent, loggerIdent string) bool {
	changed := rewriteStmtList(&b.List, orihttpIdent, loggerIdent)
	for _, stmt := range b.List {
		changed = processStmt(stmt, orihttpIdent, loggerIdent) || changed
	}
	return changed
}

func processStmt(stmt ast.Stmt, orihttpIdent, loggerIdent string) bool {
	switch s := stmt.(type) {
	case *ast.BlockStmt:
		return processBlock(s, orihttpIdent, loggerIdent)
	case *ast.IfStmt:
		changed := false
		if s.Body != nil {
			changed = processBlock(s.Body, orihttpIdent, loggerIdent) || changed
		}
		if s.Else != nil {
			changed = processStmt(s.Else, orihttpIdent, loggerIdent) || changed
		}
		return changed
	case *ast.ForStmt:
		if s.Body == nil {
			return false
		}
		return processBlock(s.Body, orihttpIdent, loggerIdent)
	case *ast.RangeStmt:
		if s.Body == nil {
			return false
		}
		return processBlock(s.Body, orihttpIdent, loggerIdent)
	case *ast.SwitchStmt:
		if s.Body == nil {
			return false
		}
		return processBlock(s.Body, orihttpIdent, loggerIdent)
	case *ast.TypeSwitchStmt:
		if s.Body == nil {
			return false
		}
		return processBlock(s.Body, orihttpIdent, loggerIdent)
	case *ast.SelectStmt:
		if s.Body == nil {
			return false
		}
		return processBlock(s.Body, orihttpIdent, loggerIdent)
	case *ast.CaseClause:
		changed := rewriteStmtList(&s.Body, orihttpIdent, loggerIdent)
		for _, stmt := range s.Body {
			changed = processStmt(stmt, orihttpIdent, loggerIdent) || changed
		}
		return changed
	case *ast.CommClause:
		changed := rewriteStmtList(&s.Body, orihttpIdent, loggerIdent)
		for _, stmt := range s.Body {
			changed = processStmt(stmt, orihttpIdent, loggerIdent) || changed
		}
		return changed
	default:
		return processFuncLitsInNode(stmt, orihttpIdent, loggerIdent)
	}
}

func processFuncLitsInNode(node ast.Node, orihttpIdent, loggerIdent string) bool {
	if node == nil {
		return false
	}
	changed := false
	ast.Inspect(node, func(n ast.Node) bool {
		fl, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if fl.Body != nil {
			if processBlock(fl.Body, orihttpIdent, loggerIdent) {
				changed = true
			}
		}
		return true
	})
	return changed
}

func rewriteStmtList(list *[]ast.Stmt, orihttpIdent, loggerIdent string) bool {
	stmts := *list
	changed := false

	for i, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ExprStmt:
			call, ok := isOriHTTPRespondCall(s.X, orihttpIdent)
			if !ok {
				continue
			}
			stmts[i] = wrapRespondCall(call, loggerIdent, s.Pos())
			changed = true
		case *ast.AssignStmt:
			if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
				continue
			}
			lhsIdent, ok := s.Lhs[0].(*ast.Ident)
			if !ok || lhsIdent.Name != "_" {
				continue
			}
			call, ok := isOriHTTPRespondCall(s.Rhs[0], orihttpIdent)
			if !ok {
				continue
			}
			stmts[i] = wrapRespondCall(call, loggerIdent, s.Pos())
			changed = true
		}
	}

	*list = stmts
	return changed
}

func isOriHTTPRespondCall(expr ast.Expr, orihttpIdent string) (*ast.CallExpr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != orihttpIdent {
		return nil, false
	}
	if _, ok := respondFuncs[sel.Sel.Name]; !ok {
		return nil, false
	}
	return call, true
}

func wrapRespondCall(call *ast.CallExpr, loggerIdent string, pos token.Pos) ast.Stmt {
	errIdent := ast.NewIdent("err")

	init := &ast.AssignStmt{
		Lhs:    []ast.Expr{errIdent},
		Tok:    token.DEFINE,
		TokPos: pos,
		Rhs:    []ast.Expr{call},
	}

	cond := &ast.BinaryExpr{
		X:  errIdent,
		Op: token.NEQ,
		Y:  ast.NewIdent("nil"),
	}

	logCall := &ast.ExprStmt{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(loggerIdent),
				Sel: ast.NewIdent("Error"),
			},
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("Failed to write response"), ValuePos: pos},
				&ast.CompositeLit{
					Type: &ast.SelectorExpr{
						X:   ast.NewIdent(loggerIdent),
						Sel: ast.NewIdent("Fields"),
					},
					Elts: []ast.Expr{
						&ast.KeyValueExpr{
							Key:   &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("error"), ValuePos: pos},
							Value: errIdent,
						},
					},
				},
			},
		},
	}

	return &ast.IfStmt{
		If:   pos,
		Init: init,
		Cond: cond,
		Body: &ast.BlockStmt{List: []ast.Stmt{logCall}},
	}
}
