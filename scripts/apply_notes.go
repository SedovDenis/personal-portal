//go:build ignore

// Run: go run scripts/apply_notes.go
// Safely replaces legacy notes/folders route registrations in main.go via Go AST.
package main

import (
    "bytes"
    "fmt"
    "go/ast"
    "go/format"
    "go/parser"
    "go/token"
    "os"
    "strconv"
)

func handlePath(s ast.Stmt) (string, bool) {
    callStmt, ok := s.(*ast.ExprStmt)
    if !ok { return "",false }
    call,ok:=callStmt.X.(*ast.CallExpr)
    if !ok||len(call.Args)<1 { return "",false }
    fn,ok:=call.Fun.(*ast.SelectorExpr)
    if !ok || fn.Sel.Name!="HandleFunc" {return "",false}
    pkg,ok:=fn.X.(*ast.Ident)
    if !ok||pkg.Name!="http" {return "",false}
    arg,ok:=call.Args[0].(*ast.BasicLit)
    if !ok||arg.Kind!=token.STRING {return "",false}
    path,err:=strconv.Unquote(arg.Value)
    return path,err==nil
}
func isMonitorGo(s ast.Stmt) bool {
    goStmt,ok:=s.(*ast.GoStmt)
    if !ok {return false}
    fn,ok:=goStmt.Call.Fun.(*ast.Ident)
    return ok&&fn.Name=="monitorCPU"
}
func isRegister(s ast.Stmt) bool {
    callStmt,ok:=s.(*ast.ExprStmt)
    if !ok {return false}
    call,ok:=callStmt.X.(*ast.CallExpr)
    if !ok {return false}
    fn,ok:=call.Fun.(*ast.Ident)
    return ok&&fn.Name=="registerNotesApp"
}
func main() {
    const path="main.go"
    fset:=token.NewFileSet()
    file,err:=parser.ParseFile(fset,path,nil,parser.ParseComments)
    if err!=nil {fmt.Fprintln(os.Stderr,err);os.Exit(1)}
    legacy:=map[string]bool{"/notes":true,"/notes/update":true,"/notes/delete":true,"/folders":true}
    replaced:=0
    registered:=false
    foundMain:=false
    for _,d:=range file.Decls {
        fn,ok:=d.(*ast.FuncDecl)
        if !ok||fn.Name.Name!="main"||fn.Body==nil {continue}
        foundMain=true
        filtered:=make([]ast.Stmt,0,len(fn.Body.List))
        for _,s:=range fn.Body.List {
            if path,ok:=handlePath(s);ok&&legacy[path] {replaced++;continue}
            if isRegister(s) {registered=true}
            filtered=append(filtered,s)
        }
        if !registered {
            inserted:=false
            output:=make([]ast.Stmt,0,len(filtered)+1)
            for _,s:=range filtered {
                output=append(output,s)
                if isMonitorGo(s) {
                    output=append(output,&ast.ExprStmt{X:&ast.CallExpr{
                        Fun:ast.NewIdent("registerNotesApp"), Args:[]ast.Expr{ast.NewIdent("db")},
                    }})
                    inserted=true
                }
            }
            if !inserted {fmt.Fprintln(os.Stderr,"Не найден вызов go monitorCPU() — main.go не изменён");os.Exit(1)}
            filtered=output
        }
        fn.Body.List=filtered
    }
    if !foundMain {fmt.Fprintln(os.Stderr,"Не найден main() — main.go не изменён");os.Exit(1)}
    var buffer bytes.Buffer
    if err:=format.Node(&buffer,fset,file);err!=nil {fmt.Fprintln(os.Stderr,err);os.Exit(1)}
    if err:=os.WriteFile(path,buffer.Bytes(),0644);err!=nil {fmt.Fprintln(os.Stderr,err);os.Exit(1)}
    fmt.Printf("main.go обновлён: заменено устаревших обработчиков — %d, зарегистрирован новый модуль.\n",replaced)
}
