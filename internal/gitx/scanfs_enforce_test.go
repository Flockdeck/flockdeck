package gitx

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// banned lists, by import path, what a file in this package may not call outside
// scanfs.go: the ways to look at the disk, follow a link or change the working
// directory, which the scan must make only through the layer that refuses a
// network share first.
var banned = map[string]map[string]bool{
	"os": set("Stat", "Lstat", "ReadFile", "ReadDir", "Open", "OpenFile", "Readlink", "Create", "WriteFile",
		"DirFS", "OpenRoot", "Chdir", "Getwd"),
	"path/filepath": set("EvalSymlinks", "Walk", "WalkDir", "Glob"),
	"io/ioutil":     set("ReadFile", "ReadDir", "WriteFile", "TempFile", "TempDir"),
	"io/fs":         set("ReadFile", "ReadDir", "WalkDir", "Stat", "Glob", "Sub"),
	"syscall": set("Open", "Stat", "Lstat", "Fstat", "Readlink", "CreateFile", "FindFirstFile", "FindNextFile",
		"GetFileAttributes", "GetFileAttributesEx", "Chdir", "Getwd", "Mkfifo", "Getdents", "ReadDirent"),
	"os/exec": set("LookPath"),
}

// bannedImports are packages that reach the disk by their own means.
var bannedImports = map[string]bool{
	"golang.org/x/sys/windows": true,
	"golang.org/x/sys/unix":    true,
}

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// findBanned parses one file's source and lists the banned calls in it.
func findBanned(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, err
	}
	local := map[string]string{} // name used in the file -> import path
	var bad []string
	for _, imp := range af.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		n := filepath.Base(path)
		if imp.Name != nil {
			n = imp.Name.Name
		}
		local[n] = path
		if bannedImports[path] {
			bad = append(bad, fmt.Sprintf("%s: imports %s", fset.Position(imp.Pos()), path))
		}
	}
	ast.Inspect(af, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && banned[local[id.Name]][x.Sel.Name] {
				bad = append(bad, fmt.Sprintf("%s: %s.%s", fset.Position(x.Pos()), id.Name, x.Sel.Name))
			}
		case *ast.CallExpr:
			// exec.Command("cmd", ...): a shell that can make or read a link.
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok || len(x.Args) == 0 {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || local[id.Name] != "os/exec" || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
				return true
			}
			arg := x.Args[0]
			if sel.Sel.Name == "CommandContext" && len(x.Args) > 1 {
				arg = x.Args[1]
			}
			if lit, ok := arg.(*ast.BasicLit); ok {
				if s, _ := strconv.Unquote(lit.Value); strings.EqualFold(strings.TrimSuffix(strings.ToLower(s), ".exe"), "cmd") {
					bad = append(bad, fmt.Sprintf("%s: exec.%s(%s)", fset.Position(x.Pos()), sel.Sel.Name, lit.Value))
				}
			}
		}
		return true
	})
	return bad, nil
}

// notTheScan are the files in this package that use those calls and are not part
// of the scan: each is here for the reason beside it. A new file is not on the
// list, so a call it makes fails the test, and the choice is made then, in
// review, between sending it through scanfs.go and adding it here with a reason.
var notTheScan = map[string]string{
	"changes.go":      "the review panel's file list and diffs read the working tree the user opened",
	"commit.go":       "commit reads the conflict markers of the files it is about to stage",
	"indexlock.go":    "waits for and checks git's own index lock in the checkout",
	"indexrefresh.go": "runs update-index in the checkout and looks at its lock",
	"made.go":         "reads a file at a revision of the checkout",
	"snapshot.go":     "the radar's scratch index and the files of the working tree it snapshots",
	"status.go":       "status of the working tree the pane is in",
	"worktree.go":     "creates, lists and removes the worktrees Flockdeck itself makes",
	"bases.go":        "reads the checkout's branches",
}

func TestOnlyTheFileSystemLayerTouchesTheDisk(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files found: %v", err)
	}
	sort.Strings(files)
	var bad []string
	scanFiles := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "scanfs.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		found, err := findBanned(f, src)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(f, "programs") {
			scanFiles++
			// The scan's own files are never on the list.
			if _, listed := notTheScan[f]; listed {
				t.Errorf("%s is the scan and cannot be listed as not part of it", f)
			}
		}
		if _, listed := notTheScan[f]; listed {
			continue
		}
		bad = append(bad, found...)
	}
	if scanFiles == 0 {
		t.Fatal("the scan's source files were not found")
	}
	if len(bad) > 0 {
		t.Errorf("these reach the disk outside scanfs.go, where a share is not refused. If the file is not part of "+
			"the scan, add it to notTheScan with the reason; otherwise use the methods in scanfs.go:\n%s", strings.Join(bad, "\n"))
	}
	// Everything on the list should still be needed: a file that no longer
	// makes such a call is dropped from it.
	for f, why := range notTheScan {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("notTheScan lists %s (%s), which is not there", f, why)
			continue
		}
		if found, _ := findBanned(f, src); len(found) == 0 {
			t.Errorf("notTheScan lists %s (%s), which makes none of the banned calls; drop it from the list", f, why)
		}
	}

	// And the layer does make those calls, so the checks above are not vacuous.
	src, err := os.ReadFile("scanfs.go")
	if err != nil {
		t.Fatal(err)
	}
	if found, _ := findBanned("scanfs.go", src); len(found) < 5 {
		t.Errorf("scanfs.go makes %d banned calls; the check is looking at the wrong thing", len(found))
	}
}

// TestEachBanIsDetected proves the checker on source text, one case per ban, so
// that a ban cannot quietly stop working.
func TestEachBanIsDetected(t *testing.T) {
	cases := map[string]string{
		"os.Stat":               `package p; import "os"; func f() { os.Stat("x") }`,
		"os.Lstat":              `package p; import "os"; func f() { os.Lstat("x") }`,
		"os.ReadFile":           `package p; import "os"; func f() { os.ReadFile("x") }`,
		"os.ReadDir":            `package p; import "os"; func f() { os.ReadDir("x") }`,
		"os.Open":               `package p; import "os"; func f() { os.Open("x") }`,
		"os.OpenFile":           `package p; import "os"; func f() { os.OpenFile("x", 0, 0) }`,
		"os.Readlink":           `package p; import "os"; func f() { os.Readlink("x") }`,
		"os.DirFS":              `package p; import "os"; func f() { os.DirFS("x") }`,
		"os.OpenRoot":           `package p; import "os"; func f() { os.OpenRoot("x") }`,
		"os.Chdir":              `package p; import "os"; func f() { os.Chdir("x") }`,
		"os.Getwd":              `package p; import "os"; func f() { os.Getwd() }`,
		"renamed os":            `package p; import o "os"; func f() { o.Stat("x") }`,
		"filepath.EvalSymlinks": `package p; import "path/filepath"; func f() { filepath.EvalSymlinks("x") }`,
		"filepath.Walk":         `package p; import "path/filepath"; func f() { filepath.Walk("x", nil) }`,
		"filepath.WalkDir":      `package p; import "path/filepath"; func f() { filepath.WalkDir("x", nil) }`,
		"filepath.Glob":         `package p; import "path/filepath"; func f() { filepath.Glob("x") }`,
		"ioutil.ReadFile":       `package p; import "io/ioutil"; func f() { ioutil.ReadFile("x") }`,
		"ioutil.ReadDir":        `package p; import "io/ioutil"; func f() { ioutil.ReadDir("x") }`,
		"fs.ReadFile":           `package p; import "io/fs"; func f() { fs.ReadFile(nil, "x") }`,
		"fs.ReadDir":            `package p; import "io/fs"; func f() { fs.ReadDir(nil, "x") }`,
		"fs.WalkDir":            `package p; import "io/fs"; func f() { fs.WalkDir(nil, "x", nil) }`,
		"fs.Stat":               `package p; import "io/fs"; func f() { fs.Stat(nil, "x") }`,
		"syscall.Open":          `package p; import "syscall"; func f() { syscall.Open("x", 0, 0) }`,
		"syscall.Stat":          `package p; import "syscall"; func f() { syscall.Stat("x", nil) }`,
		"syscall.Readlink":      `package p; import "syscall"; func f() { syscall.Readlink("x", nil) }`,
		"syscall.CreateFile":    `package p; import "syscall"; func f() { syscall.CreateFile(nil, 0, 0, nil, 0, 0, 0) }`,
		"x/sys/windows":         `package p; import "golang.org/x/sys/windows"; var _ = windows.MAX_PATH`,
		"x/sys/unix":            `package p; import "golang.org/x/sys/unix"; var _ = unix.O_RDONLY`,
		"exec.LookPath":         `package p; import "os/exec"; func f() { exec.LookPath("x") }`,
		"exec.Command cmd":      `package p; import "os/exec"; func f() { exec.Command("cmd", "/c", "dir") }`,
		"exec.Command cmd.exe":  `package p; import "os/exec"; func f() { exec.Command("CMD.EXE", "/c", "dir") }`,
		"exec.CommandContext":   `package p; import ("context"; "os/exec"); func f() { exec.CommandContext(context.TODO(), "cmd", "/c") }`,
	}
	for name, src := range cases {
		found, err := findBanned(name+".go", []byte(src))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(found) == 0 {
			t.Errorf("%s was not detected", name)
		}
	}
	// What is allowed is not flagged.
	for name, src := range map[string]string{
		"git":                 `package p; import "os/exec"; func f() { exec.Command("git", "status") }`,
		"os.Getenv":           `package p; import "os"; func f() { os.Getenv("X") }`,
		"a method named Stat": `package p; type t struct{}; func (t) Stat() {}; func f() { var x t; x.Stat() }`,
		"another os":          `package p; func f() { os := 1; _ = os }`,
		"syscall constant":    `package p; import "syscall"; var _ = syscall.ENOTDIR`,
	} {
		if found, err := findBanned(name+".go", []byte(src)); err != nil || len(found) != 0 {
			t.Errorf("%s was flagged: %v %v", name, found, err)
		}
	}
}
