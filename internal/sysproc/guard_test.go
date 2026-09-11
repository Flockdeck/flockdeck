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
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			starts, hides := scan(fn.Body)
			if !starts {
				continue
			}
			key := rel + ":" + fn.Name.Name
			seen[key] = true
			if _, ok := exempt[key]; !ok && !hides {
				missing = append(missing, key)
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

// scan reports whether a function body starts a process through os/exec, and
// whether it hides that process's window.
func scan(body *ast.BlockStmt) (starts, hides bool) {
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
		case pkg.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"):
			starts = true
		case pkg.Name == "sysproc" && sel.Sel.Name == "NoWindow":
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
