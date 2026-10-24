// Package boundary checks the line between the Flockdeck server and its web
// interface, for the layout the separation plan ends in:
//
//	api/        the contract: catalog, schemas, fixtures
//	ui/         the interface, its own Go module
//	internal/   the server
//	main.go     the one file that imports both
//
// Nothing here runs in CI until step 3 of the plan has moved the interface into
// ui/ and boundary.yml.disabled has been renamed (see the README). Until then
// Check finds no ui/ directory and reports nothing, so running it is harmless.
// It reads the tree and never changes it.
package boundary

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Module is the server module's path, and UIModule the interface's.
const (
	Module   = "github.com/jmwri/flockdeck"
	UIModule = "github.com/jmwri/flockdeck/ui"
)

// Violation is one place where the line is crossed.
type Violation struct {
	File string // slash path, relative to the root
	Line int
	Rule string
	Text string
}

// Check walks the tree under root and returns every crossing, in file order.
//
// The rules:
//  1. Go in ui/ does not import the server (anything under Module that is not
//     under UIModule). A separate module already makes this a compile error for
//     internal/; this also catches a replace directive or a copy.
//  2. Go in internal/, and in the root package other than main.go, does not
//     import the interface. Programs under cmd/ may, to bundle it.
//  3. JavaScript in ui/ imports nothing that resolves outside ui/ (a relative
//     path that climbs out of it), except api/fixtures, which the node tests read.
//  4. api/ holds data only: no Go and no JavaScript.
func Check(root string) ([]Violation, error) {
	var out []Violation
	if _, err := os.Stat(filepath.Join(root, "ui")); err != nil {
		return nil, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(rel, ".go"):
			out = append(out, goImports(root, rel)...)
		case isJS(rel):
			out = append(out, jsImports(root, rel)...)
		}
		if strings.HasPrefix(rel, "api/") && (strings.HasSuffix(rel, ".go") || isJS(rel)) {
			out = append(out, Violation{File: rel, Line: 1, Rule: "api-is-data", Text: "api/ holds data only, not code"})
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, err
}

func isJS(rel string) bool {
	return strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".mjs")
}

func goImports(root, rel string) []Violation {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	var out []Violation
	inUI := strings.HasPrefix(rel, "ui/")
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		line := fset.Position(imp.Pos()).Line
		toUI := p == UIModule || strings.HasPrefix(p, UIModule+"/")
		toServer := (p == Module || strings.HasPrefix(p, Module+"/")) && !toUI
		switch {
		case inUI && toServer:
			out = append(out, Violation{rel, line, "ui-imports-server", "the interface imports the server: " + p})
		case !inUI && toUI && rel != "main.go" && !strings.HasPrefix(rel, "cmd/"):
			out = append(out, Violation{rel, line, "server-imports-ui", "the server imports the interface: " + p})
		}
	}
	return out
}

// jsImport finds the module specifier of a static import or export-from and of a
// dynamic import with a string literal.
var jsImport = regexp.MustCompile(`(?m)(?:^|[;{}\s])(?:import|export)\s[^'"\n]*?['"]([^'"\n]+)['"]|import\(\s*['"]([^'"\n]+)['"]\s*\)`)

func jsImports(root, rel string) []Violation {
	if !strings.HasPrefix(rel, "ui/") {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil
	}
	src := string(data)
	var out []Violation
	for _, m := range jsImport.FindAllStringSubmatchIndex(src, -1) {
		spec := ""
		for _, g := range []int{2, 4} {
			if m[g] >= 0 {
				spec = src[m[g]:m[g+1]]
			}
		}
		if !strings.HasPrefix(spec, ".") {
			continue
		}
		target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(rel), spec)))
		if strings.HasPrefix(target, "ui/") || strings.HasPrefix(target, "api/fixtures/") {
			continue
		}
		line := 1 + strings.Count(src[:m[0]], "\n")
		out = append(out, Violation{rel, line, "ui-reaches-out", "the interface imports " + target + ", outside ui/"})
	}
	return out
}

// PathsOutsideUI returns the changed paths a "ui:" pull request may not touch:
// anything outside ui/ and api/fixtures/.
func PathsOutsideUI(changed []string) []string {
	var out []string
	for _, p := range changed {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || strings.HasPrefix(p, "ui/") || strings.HasPrefix(p, "api/fixtures/") {
			continue
		}
		out = append(out, p)
	}
	return out
}
