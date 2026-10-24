package ui

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	serverModule = "github.com/jmwri/flockdeck"
	uiModule     = "github.com/jmwri/flockdeck/ui"
)

func isUI(path string) bool { return path == uiModule || strings.HasPrefix(path, uiModule+"/") }

// importsOf walks dir and calls fn for each import of each Go file in it.
func importsOf(t *testing.T, dir string, fn func(file, path string)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			fn(filepath.ToSlash(p), path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheInterfaceImportsNothingOfTheServer(t *testing.T) {
	importsOf(t, "../../ui", func(file, path string) {
		if strings.HasPrefix(path, serverModule+"/") && !isUI(path) {
			t.Errorf("%s imports %s: the interface may not import the server", file, path)
		}
	})
}

func TestTheServerImportsTheInterfaceOnlyFromMainAndCmd(t *testing.T) {
	importsOf(t, "../../internal", func(file, path string) {
		if isUI(path) && !strings.HasSuffix(file, "_test.go") {
			t.Errorf("%s imports %s: only main.go and cmd/ may import the interface", file, path)
		}
	})
}
