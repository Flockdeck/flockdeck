package sysproc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exempt names the functions that start a process without NoWindow, each with
// the reason it is right to. The key is the file, relative to the module root,
// and the function's name.
var exempt = map[string]string{
	"internal/appwindow/appwindow.go:startAppMode": "the browser is a GUI program, and its window is the whole point",
	"cli_update.go:relaunch":                       "it is the next Flockdeck, which has to come back the way this one was started",
}

// Every console program Flockdeck starts in the background opens a terminal
// window on Windows unless it is told not to, and forgetting leaves no trace
// on the machine the code was written on: a build run from a terminal shares
// that terminal's console with its children, so nothing ever appears. It was
// found by somebody who installed a release and watched their screen fill up.
//
// So the check is made on the source rather than on a screen. Every function
// that starts a process through os/exec has to hand it to NoWindow, or be
// listed in exempt with the reason it need not.
func TestEveryProcessHidesItsWindow(t *testing.T) {
	root := moduleRoot(t)
	seen := map[string]bool{}
	var missing []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel == ".":
				return nil
			// The tools under cmd are for developers, run from a terminal whose
			// console their children share, and never shipped.
			case rel == "cmd", strings.HasPrefix(d.Name(), "."), d.Name() == "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		execName, sysprocName := importName(file, "os/exec"), importName(file, "github.com/jmwri/flockdeck/internal/sysproc")
		check := func(name string, body ast.Node) {
			starts, hides := scan(body, execName, sysprocName)
			if !starts {
				return
			}
			key := rel + ":" + name
			seen[key] = true
			if _, ok := exempt[key]; !ok && !hides {
				missing = append(missing, key)
			}
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Body != nil {
					check(decl.Name.Name, decl.Body)
				}
			case *ast.GenDecl:
				// A function can also be a value -- var run = func() {...} --
				// and one of those starts processes just as well.
				for _, spec := range decl.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						check(vs.Names[0].Name, vs)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("%s starts a process without sysproc.NoWindow; on Windows it will open a terminal window", key)
	}
	// An exemption for a function that no longer starts anything is one that
	// would quietly excuse whatever is written under that name next.
	for key := range exempt {
		if !seen[key] {
			t.Errorf("exempt lists %s, which no longer starts a process", key)
		}
	}
}

// TestGuardSeesEveryWayToStartAProcess keeps the guard itself honest. It read
// function declarations for calls spelled exec.Command, so a process started
// from a function kept in a variable, or through os/exec imported under
// another name, went past it as though it were not there.
func TestGuardSeesEveryWayToStartAProcess(t *testing.T) {
	const src = `package x

import (
	run "os/exec"
	hide "github.com/jmwri/flockdeck/internal/sysproc"
)

var start = func() { _ = run.Command("git").Run() }

var hidden = func() {
	c := run.Command("git")
	hide.NoWindow(c)
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	execName, sysprocName := importName(file, "os/exec"), importName(file, "github.com/jmwri/flockdeck/internal/sysproc")
	got := map[string][2]bool{}
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					starts, hides := scan(vs, execName, sysprocName)
					got[vs.Names[0].Name] = [2]bool{starts, hides}
				}
			}
		}
	}
	if got["start"] != [2]bool{true, false} {
		t.Errorf("start: starts, hides = %v, want a process started and not hidden", got["start"])
	}
	if got["hidden"] != [2]bool{true, true} {
		t.Errorf("hidden: starts, hides = %v, want a process started and hidden", got["hidden"])
	}
}

// importName returns the name a file refers to an imported package by, which
// is its last path element unless the import renames it, or "" when the file
// does not import it.
func importName(file *ast.File, path string) string {
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != path {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

// scan reports whether code starts a process through os/exec, and whether it
// hides that process's window. execName and sysprocName are what the file
// calls the two packages.
func scan(body ast.Node, execName, sysprocName string) (starts, hides bool) {
	if execName == "" {
		return false, false
	}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == execName && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"):
			starts = true
		case pkg.Name == sysprocName && sel.Sel.Name == "NoWindow":
			hides = true
		}
		return true
	})
	return starts, hides
}

// moduleRoot finds the directory holding go.mod, above the one the test runs in.
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
