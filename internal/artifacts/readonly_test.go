package artifacts

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Threat: this package, or anything it imports, growing a way to change the
// machine or reach the network. It is meant to be read-only and offline for
// good. Two things are held to that:
//
//   - What it can reach. Every package this one imports, directly or not,
//     for each of the three platforms a release is built for, is walked, and
//     none may be one that starts a process or opens a connection. (A direct
//     import list is not enough: the package once imported internal/review,
//     which imports internal/sysproc, which imports os/exec.)
//   - What its own sources call. No call, by any receiver, of a function that
//     writes, removes, renames, links, creates or changes a file or a folder,
//     or that starts a process; and no open for writing.
//
// A later change that needs one has to change this test, and so be seen.
func TestPackageIsReadOnlyAndOffline(t *testing.T) {
	forbidden := map[string]bool{
		"os/exec": true, "net": true, "net/http": true, "net/rpc": true, "net/smtp": true, "plugin": true,
		"os/signal": true, "os/user": true, "log/syslog": true,
	}
	root := moduleRoot(t)
	for _, goos := range []string{"windows", "linux", "darwin"} {
		ctx := build.Default
		ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled = goos, "amd64", false
		deps := map[string]bool{}
		walkDeps(t, &ctx, root, "github.com/jmwri/flockdeck/internal/artifacts", deps)
		var names []string
		for d := range deps {
			names = append(names, d)
		}
		sort.Strings(names)
		for _, d := range names {
			if forbidden[d] {
				t.Errorf("on %s the package can reach %s", goos, d)
			}
			if strings.HasPrefix(d, "github.com/jmwri/flockdeck/") &&
				d != "github.com/jmwri/flockdeck/internal/artifacts" &&
				d != "github.com/jmwri/flockdeck/internal/secretname" {
				t.Errorf("on %s the package imports %s: only the leaf internal/secretname is allowed", goos, d)
			}
		}
		if len(deps) < 10 {
			t.Fatalf("on %s only %d packages were found; the walk is not looking where it should", goos, len(deps))
		}
	}
	sourceChecks(t)
}

// moduleRoot is this module's directory: the nearest one up with a go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

const modulePath = "github.com/jmwri/flockdeck"

// walkDeps adds path, and everything it imports, to seen, reading only source
// (the standard library from GOROOT, this module from its own tree) and running
// nothing.
func walkDeps(t *testing.T, ctx *build.Context, modRoot, path string, seen map[string]bool) {
	t.Helper()
	if path == "C" || path == "unsafe" || seen[path] {
		return
	}
	seen[path] = true
	var pkg *build.Package
	var err error
	if rest, ok := strings.CutPrefix(path, modulePath+"/"); ok {
		pkg, err = ctx.ImportDir(filepath.Join(modRoot, filepath.FromSlash(rest)), 0)
	} else if path == modulePath {
		pkg, err = ctx.ImportDir(modRoot, 0)
	} else {
		pkg, err = ctx.Import(path, filepath.Join(runtime.GOROOT(), "src"), 0)
		if err == nil {
			// The standard library's own vendored copies are named
			// vendor/golang.org/...; seen under that name.
			seen[pkg.ImportPath] = true
		}
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	for _, imp := range pkg.Imports {
		walkDeps(t, ctx, modRoot, imp, seen)
	}
}

// sourceChecks parses this package's own non-test sources, for every platform's
// files, and refuses calls that change files or start processes.
func sourceChecks(t *testing.T) {
	t.Helper()
	// Names refused whatever they are called on: os.Remove and root.Remove, a
	// method called Mkdir and a function called Mkdir.
	writes := map[string]bool{
		"WriteFile": true, "Create": true, "CreateTemp": true, "Remove": true, "RemoveAll": true,
		"Rename": true, "Mkdir": true, "MkdirAll": true, "MkdirTemp": true, "Chmod": true, "Chown": true,
		"Lchown": true, "Chtimes": true, "Truncate": true, "Symlink": true, "Link": true,
		"Setenv": true, "Unsetenv": true, "Clearenv": true, "Chdir": true, "Mkfifo": true, "Mknod": true,
		// syscall, by the names the platforms give them
		"Unlink": true, "Rmdir": true, "StartProcess": true, "ForkExec": true, "Exec": true,
		"CreateProcess": true, "DeleteFile": true, "MoveFile": true, "SetEndOfFile": true, "SetFileTime": true,
	}
	writeFlags := map[string]bool{"O_WRONLY": true, "O_RDWR": true, "O_CREATE": true, "O_TRUNC": true, "O_APPEND": true, "O_EXCL": true}
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
		// Parsed for every platform at once (no build constraints applied), so
		// a Windows or Unix file is read wherever this runs.
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "os/exec" || p == "net" || p == "net/http" {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var fn string
			switch x := call.Fun.(type) {
			case *ast.SelectorExpr:
				fn = x.Sel.Name
			case *ast.Ident:
				fn = x.Name
			}
			if writes[fn] {
				t.Errorf("%s calls %s, which changes files or starts processes", name, fn)
			}
			if fn == "OpenFile" {
				ast.Inspect(call, func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok && writeFlags[id.Name] {
						t.Errorf("%s opens a file with %s", name, id.Name)
					}
					if se, ok := m.(*ast.SelectorExpr); ok && writeFlags[se.Sel.Name] {
						t.Errorf("%s opens a file with %s", name, se.Sel.Name)
					}
					return true
				})
			}
			return true
		})
	}
	if checked < 6 {
		t.Fatalf("only %d source files were read; the check is not looking where it should", checked)
	}
}

// The check itself must be able to fail: run over source that does what it
// forbids, it says so. (A guard that cannot find anything is not a guard.)
func TestReadOnlyCheckCatchesWhatItForbids(t *testing.T) {
	src := `package x
import ("os"; "os/exec")
func f(r *os.Root) { os.Remove("a"); r.Mkdir("d", 0); os.OpenFile("a", os.O_WRONLY, 0); exec.Command("x") }`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if se, ok := c.Fun.(*ast.SelectorExpr); ok {
				calls = append(calls, se.Sel.Name)
			}
		}
		return true
	})
	for _, want := range []string{"Remove", "Mkdir", "OpenFile", "Command"} {
		found := false
		for _, c := range calls {
			found = found || c == want
		}
		if !found {
			t.Errorf("the walk over sample source did not see %s", want)
		}
	}
	// And the walk of imports reaches os/exec through a package that uses it.
	ctx := build.Default
	ctx.CgoEnabled = false
	deps := map[string]bool{}
	walkDeps(t, &ctx, moduleRoot(t), "github.com/jmwri/flockdeck/internal/review", deps)
	if !deps["os/exec"] {
		t.Error("the walk did not find os/exec under internal/review, which does import it by way of sysproc")
	}
}
