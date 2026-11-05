//go:build ignore

// Apply auth integration to the current main.go without replacing dashboard,
// mortgage, or other user-written code. Run: go run scripts/apply-auth.go
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
)

func main() {
	const filename = "main.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}
	var modifiedRegister, modifiedServe bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		stmts := make([]ast.Stmt, 0, len(fn.Body.List)+1)
		for _, stmt := range fn.Body.List {
			ex, ok := stmt.(*ast.ExprStmt)
			if ok {
				call, isCall := ex.X.(*ast.CallExpr)
				if isCall {
					if name, isIdent := call.Fun.(*ast.Ident); isIdent && name.Name == "registerNotesApp" && len(call.Args) == 1 {
						stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("auth")}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("registerPortalAuth"), Args: []ast.Expr{ast.NewIdent("db")}}}})
						stmts = append(stmts, &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("registerNotesApp"), Args: []ast.Expr{ast.NewIdent("db"), ast.NewIdent("auth")}}})
						modifiedRegister = true
						continue
					}
				}
			}
			stmts = append(stmts, stmt)
		}
		fn.Body.List = stmts
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ListenAndServe" || len(call.Args) != 2 {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "http" {
				return true
			}
			if id, ok := call.Args[1].(*ast.Ident); !ok || id.Name != "nil" {
				return true
			}
			call.Args[1] = &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("auth"), Sel: ast.NewIdent("middleware")}, Args: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("http"), Sel: ast.NewIdent("DefaultServeMux")}}}
			modifiedServe = true
			return false
		})
	}
	if !modifiedRegister || !modifiedServe {
		fmt.Fprintln(os.Stderr, "main.go not modified: expected registerNotesApp(db) and http.ListenAndServe(..., nil). Inspect code manually.")
		os.Exit(1)
	}
	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		panic(err)
	}
	fmt.Println("main.go updated: added account service and protected HTTP routes")
}
