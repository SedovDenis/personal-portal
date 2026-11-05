// Run with: go run scripts/apply-dashboard.go
// Replaces only the HTTP registration for "/" in main.go.
// All unrelated handlers, functions, imports, and comments stay untouched.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	const file = "main.go"
	source, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	if strings.Contains(string(source), "registerDashboard(auth)") {
		fmt.Println("Dashboard already registered; nothing to change")
		return
	}

	set := token.NewFileSet()
	node, err := parser.ParseFile(set, file, source, 0)
	if err != nil {
		panic(err)
	}
	var start, end int
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "HandleFunc" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "http" {
			return true
		}
		first, ok := call.Args[0].(*ast.BasicLit)
		if !ok || first.Kind != token.STRING {
			return true
		}
		path, err := strconv.Unquote(first.Value)
		if err != nil || path != "/" {
			return true
		}
		start = set.Position(call.Pos()).Offset
		end = set.Position(call.End()).Offset
		count++
		return true
	})
	if count != 1 {
		panic(fmt.Sprintf("expected one old '/' handler, found %d; main.go unchanged", count))
	}
	output := make([]byte, 0, len(source)+50)
	output = append(output, source[:start]...)
	output = append(output, "registerDashboard(auth)"...)
	output = append(output, source[end:]...)
	if _, err := parser.ParseFile(token.NewFileSet(), file, output, 0); err != nil {
		panic(fmt.Errorf("modified file would be invalid Go: %w", err))
	}
	temp, err := os.CreateTemp(filepath.Dir(file), ".dashboard-main-*")
	if err != nil {
		panic(err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(output); err != nil {
		temp.Close()
		panic(err)
	}
	if err := temp.Chmod(0644); err != nil {
		temp.Close()
		panic(err)
	}
	if err := temp.Close(); err != nil {
		panic(err)
	}
	if err := os.Rename(tempName, file); err != nil {
		panic(err)
	}
	fmt.Println("Updated main.go: old '/' handler replaced with registerDashboard(auth)")
}
