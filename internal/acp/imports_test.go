package acp

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/jmwri/flockdeck/"

// The protocol client is a layer under the workspace. If it, or anything it
// imports, reached back into workspace or server, a change to either would
// reach the wire code, and the packages that start agents could not be told
// apart from the one that speaks to them. The check follows the imports of
// this package and of every package below it, tests included, through the
// module's own packages.
func TestACPImportsNothingFromWorkspaceOrServer(t *testing.T) {
	forbidden := []string{"internal/workspace", "internal/server"}
	root := filepath.Join("..", "..")

	// imports of the package in dir, test files included when tests is set.
	importsOf := func(dir string, tests bool) []string {
		t.Helper()
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range files {
			if !tests && strings.HasSuffix(f, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			for _, spec := range parsed.Imports {
				p, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				out = append(out, p)
			}
		}
		return out
	}

	// Every directory of this package and below it is a starting point.
	var starts []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			starts = append(starts, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) < 4 {
		t.Fatalf("found %v below internal/acp, expected the package and its subpackages", starts)
	}

	seen := map[string]bool{}
	var walk func(pkg, from string)
	walk = func(pkg, from string) {
		rel := strings.TrimPrefix(pkg, modulePath)
		for _, bad := range forbidden {
			if rel == bad || strings.HasPrefix(rel, bad+"/") {
				t.Errorf("%s reaches %s", from, pkg)
				return
			}
		}
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, next := range importsOf(filepath.Join(root, filepath.FromSlash(rel)), false) {
			if strings.HasPrefix(next, modulePath) {
				walk(next, pkg)
			}
		}
	}
	for _, dir := range starts {
		pkg := modulePath + "internal/acp"
		if dir != "." {
			pkg += "/" + filepath.ToSlash(dir)
		}
		for _, imp := range importsOf(dir, true) {
			if strings.HasPrefix(imp, modulePath) {
				walk(imp, pkg)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("followed no imports, so the check proves nothing")
	}
	var names []string
	for p := range seen {
		names = append(names, p)
	}
	sort.Strings(names)
	t.Logf("followed %d packages: %s", len(names), strings.Join(names, " "))
}
