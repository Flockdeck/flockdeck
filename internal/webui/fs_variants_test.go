package webui

import (
	"crypto/sha256"
	"go/build"
	"io/fs"
	"os"
	"slices"
	"testing"
)

// files lists every file of a tree, by path.
func files(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// oldTree is the old front end as it is on disk, which the embedded one is a
// copy of.
func oldTree() fs.FS { return os.DirFS("assets") }

// Whichever variant is built serves the old test runner byte for byte, so the
// two front ends run the same end-to-end code, shared and not copied.
func TestServedE2EScriptIsTheOldOne(t *testing.T) {
	want, err := fs.ReadFile(oldTree(), "e2e.js")
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile(FS(), "e2e.js")
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(got) != sha256.Sum256(want) {
		t.Errorf("e2e.js served in the %s build differs from assets/e2e.js", Variant)
	}
}

// The icon the window is given comes from the old tree in both variants.
func TestIconIsTheOldOne(t *testing.T) {
	want, err := fs.ReadFile(oldTree(), "icon-256.png")
	if err != nil {
		t.Fatal(err)
	}
	if got := Icon(); sha256.Sum256(got) != sha256.Sum256(want) {
		t.Error("Icon differs from assets/icon-256.png")
	}
}

// The package's files for the default build, as the go command picks them. The
// new shell's file is not among them and nothing of next/ is embedded, so no
// symbol or byte of the new shell is in a default binary.
func TestDefaultBuildCompilesNothingOfTheNextShell(t *testing.T) {
	ctx := build.Default
	ctx.BuildTags = nil
	pkg, err := ctx.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(pkg.GoFiles, "fs_next.go") || !slices.Contains(pkg.GoFiles, "fs_default.go") {
		t.Errorf("default build compiles %v", pkg.GoFiles)
	}
	if !slices.Contains(pkg.IgnoredGoFiles, "fs_next.go") {
		t.Errorf("fs_next.go is not left out of the default build: ignored %v", pkg.IgnoredGoFiles)
	}
	if !slices.Equal(pkg.EmbedPatterns, []string{"assets"}) {
		t.Errorf("embedded in the default build: %v, want only assets", pkg.EmbedPatterns)
	}
}
