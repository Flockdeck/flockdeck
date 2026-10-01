package artifacts

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Threat: this package growing a way to change the machine or reach the
// network. It is meant to be read-only and offline for good, so the sources
// (not the tests) may not import anything that starts a process or opens a
// connection, nor call an os function that writes, removes, renames, links or
// changes a file. A later change that needs one has to change this test, and
// so be seen.
func TestPackageIsReadOnlyAndOffline(t *testing.T) {
	forbiddenImports := map[string]bool{
		"os/exec": true, "net": true, "net/http": true, "os/signal": true, "plugin": true,
	}
	forbiddenOS := map[string]bool{
		"WriteFile": true, "Create": true, "CreateTemp": true, "Remove": true, "RemoveAll": true,
		"Rename": true, "Mkdir": true, "MkdirAll": true, "MkdirTemp": true, "Chmod": true,
		"Chown": true, "Chtimes": true, "Truncate": true, "Symlink": true, "Link": true,
		"Setenv": true, "Unsetenv": true, "Chdir": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if forbiddenImports[p] {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && forbiddenOS[sel.Sel.Name] {
				t.Errorf("%s calls os.%s", name, sel.Sel.Name)
			}
			return true
		})
	}
	if checked < 6 {
		t.Fatalf("only %d source files were read; the check is not looking where it should", checked)
	}
}
