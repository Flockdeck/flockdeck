package webui

import (
	"bytes"
	"go/build"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The embedded tree holds the page and every shared file the page and the
// server name: the terminal library, the end-to-end script, the fonts and the
// icons the window, the manifest and the site use.
func TestFSServesThePageAndWhatItNames(t *testing.T) {
	idx, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(idx, []byte(`<meta name="flockdeck-shell" content="next">`)) {
		t.Error("index.html is not the shell's")
	}
	for _, name := range []string{
		"main.js", "app.css", "e2e.js",
		"vendor/xterm.js", "vendor/xterm.css", "vendor/addon-fit.js", "vendor/addon-search.js", "vendor/addon-webgl.js", "vendor/preact.mjs",
		"fonts/ibm-plex-sans.woff2", "fonts/jetbrains-mono.woff2",
		"icon.svg", "icon.ico", "icon-180.png", "icon-256.png", "icon-512.png",
	} {
		b, err := fs.ReadFile(FS(), name)
		if err != nil || len(b) == 0 {
			t.Errorf("%s is not served (%v)", name, err)
		}
	}
}

func TestIconIsThe256PixelPNG(t *testing.T) {
	want, err := fs.ReadFile(FS(), "icon-256.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Icon(), want) {
		t.Error("Icon differs from icon-256.png")
	}
}

func TestMjsIsServedAsJavaScript(t *testing.T) {
	if ct := mime.TypeByExtension(".mjs"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("type of .mjs = %q", ct)
	}
}

// Only the asset tree is embedded: nothing from test, where the test-only
// render-to-string library and the node tests are.
func TestOnlyTheAssetTreeIsEmbedded(t *testing.T) {
	pkg, err := build.Default.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pkg.EmbedPatterns, []string{"src"}) {
		t.Errorf("embedded: %v", pkg.EmbedPatterns)
	}
}

func TestDirFSIsOffUnlessAFolderWithAPageIsNamed(t *testing.T) {
	t.Setenv(DirEnv, "")
	if _, ok := DirFS(); ok {
		t.Fatal("DirFS is on with nothing set")
	}
	dir := t.TempDir()
	t.Setenv(DirEnv, dir)
	if _, ok := DirFS(); ok {
		t.Fatal("DirFS is on for a folder with no index.html")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>dev</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys, ok := DirFS()
	if !ok {
		t.Fatal("DirFS is off for a folder with an index.html")
	}
	if b, err := fs.ReadFile(fsys, "index.html"); err != nil || string(b) != "<p>dev</p>" {
		t.Fatalf("index.html = %q, %v", b, err)
	}
}

func TestDirFSTakesOffTheQuotesAndSpacesCmdKeeps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my ui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"plain":          dir,
		"double quotes":  `"` + dir + `"`,
		"single quotes":  `'` + dir + `'`,
		"trailing space": dir + " ",
		"spaces":         "  " + dir + "  ",
		"quotes, spaces": ` "` + dir + `" `,
	} {
		t.Setenv(DirEnv, v)
		if _, ok := DirFS(); !ok {
			t.Errorf("%s: %q is not taken", name, v)
		}
	}
	for name, v := range map[string]string{"one quote": `"` + dir, "other quotes": `"` + dir + `'`, "only quotes": `""`} {
		t.Setenv(DirEnv, v)
		if _, ok := DirFS(); ok {
			t.Errorf("%s: %q is taken", name, v)
		}
	}
}
