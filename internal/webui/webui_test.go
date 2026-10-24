package webui

import (
	"bytes"
	"go/build"
	"io/fs"
	"mime"
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

// Only the asset tree is embedded: nothing from ui/test, where the test-only
// render-to-string library and the node tests are.
func TestOnlyTheAssetTreeIsEmbedded(t *testing.T) {
	pkg, err := build.Default.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pkg.EmbedPatterns, []string{"ui/assets"}) {
		t.Errorf("embedded: %v", pkg.EmbedPatterns)
	}
}
