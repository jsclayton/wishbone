package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every test of the package runs in parallel with the others. Each one has a
// server and a database of its own, so none waits on another, and the package
// runs in a fraction of the time that it took one test at a time under -race
// (docs/development.md, Tests). A test that can't run in parallel must say why
// here, in notParallel, and not leave t.Parallel out quietly.
func TestEveryTestRunsInParallel(t *testing.T) {
	t.Parallel()
	notParallel := map[string]string{}
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTestName(fn.Name.Name) || notParallel[fn.Name.Name] != "" {
				continue
			}
			if !startsWithParallel(fn) {
				t.Errorf("%s: %s doesn't start with t.Parallel(). Make it its first line, or name the test in notParallel with the reason", fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
}

// isTestName says whether a function is a test that go test runs. TestMain is
// not one.
func isTestName(name string) bool {
	const prefix = "Test"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix || name == "TestMain" {
		return false
	}
	c := name[len(prefix)]
	return c < 'a' || c > 'z'
}

func startsWithParallel(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	stmt, ok := fn.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := stmt.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Parallel" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && len(fn.Type.Params.List) == 1 && len(fn.Type.Params.List[0].Names) == 1 && recv.Name == fn.Type.Params.List[0].Names[0].Name
}
