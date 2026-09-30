package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The favicon is the application's own icon, copied rather than drawn again
// (see cmd/sitegen's TestFaviconIsTheAppIcon, which this mirrors), so the
// two must stay the same picture. Line endings are set aside: a Windows
// checkout holds either file with CRLF.
func TestFaviconIsTheAppIcon(t *testing.T) {
	icon, err := assets.ReadFile("assets/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(lf(icon)) != string(lf(app)) {
		t.Error("cmd/docgen/assets/favicon.svg differs from internal/webui/assets/icon.svg; copy the app's icon over it")
	}

	iconICO, err := assets.ReadFile("assets/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	ico, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(iconICO, ico) {
		t.Error("cmd/docgen/assets/favicon.ico differs from internal/webui/assets/icon.ico; copy the app's icon over it")
	}

	touchIcon, err := assets.ReadFile("assets/apple-touch-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	touch, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon-180.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(touchIcon, touch) {
		t.Error("cmd/docgen/assets/apple-touch-icon.png differs from internal/webui/assets/icon-180.png; copy the app's icon over it")
	}
}

// rootBlock is a style sheet's first bare :root rule, selector to closing
// brace: where both sites keep every colour, radius, font stack and duration.
var rootBlock = regexp.MustCompile(`(?m)^:root \{\n(?:[^}]*\n)?\}`)

// docs.css and flockdeck.ai's site.css are two files, because the two sites
// are built and deployed apart, and they were hand copies of each other with
// nothing to say when one was changed alone. Their tokens are the design's
// (design/tokens.css), under the same names in both, so the :root block that
// holds them is kept the same in both, character for character: a colour
// changed in one and not the other fails here.
func TestTheDocsAndTheSiteShareOneRootBlock(t *testing.T) {
	docs, err := assets.ReadFile("assets/docs.css")
	if err != nil {
		t.Fatal(err)
	}
	site, err := os.ReadFile(filepath.Join("..", "sitegen", "assets", "site.css"))
	if err != nil {
		t.Fatal(err)
	}
	docsRoot := rootBlock.Find(lf(docs))
	siteRoot := rootBlock.Find(lf(site))
	if docsRoot == nil {
		t.Fatal("cmd/docgen/assets/docs.css has no :root block")
	}
	if siteRoot == nil {
		t.Fatal("cmd/sitegen/assets/site.css has no :root block")
	}
	if !bytes.Contains(docsRoot, []byte("--surface:")) || !bytes.Contains(docsRoot, []byte("--mono:")) {
		t.Fatalf("the :root block found in docs.css is not the one that holds the tokens:\n%s", docsRoot)
	}
	if !bytes.Equal(docsRoot, siteRoot) {
		t.Errorf("the :root blocks of cmd/docgen/assets/docs.css and cmd/sitegen/assets/site.css differ; change both together\ndocs.css:\n%s\nsite.css:\n%s", docsRoot, siteRoot)
	}
	// One block each, so a second :root further down cannot quietly override
	// what the shared one says.
	for name, css := range map[string][]byte{"docs.css": docs, "site.css": site} {
		if n := len(regexp.MustCompile(`(?m)^\s*:root\b`).FindAll(css, -1)); n != 1 {
			t.Errorf("%s has %d :root rules; the tokens belong in the one shared block", name, n)
		}
	}
}

// The two sites serve the same fonts, and each generator embeds its own copy
// because a Go embed cannot reach outside its package. The copies are kept
// the same file for file, licences included, so the docs cannot go on serving
// a typeface the site has dropped.
func TestTheDocsAndTheSiteShipTheSameFonts(t *testing.T) {
	siteDir := filepath.Join("..", "sitegen", "assets", "fonts")
	siteFonts, err := os.ReadDir(siteDir)
	if err != nil {
		t.Fatal(err)
	}
	docsFonts, err := assets.ReadDir("assets/fonts")
	if err != nil {
		t.Fatal(err)
	}
	inDocs := map[string]bool{}
	for _, e := range docsFonts {
		inDocs[e.Name()] = true
	}
	if len(siteFonts) < 4 {
		t.Errorf("cmd/sitegen/assets/fonts holds %d files; it should hold two fonts and their two licences", len(siteFonts))
	}
	for _, e := range siteFonts {
		if !inDocs[e.Name()] {
			t.Errorf("cmd/sitegen/assets/fonts/%s is not in cmd/docgen/assets/fonts", e.Name())
			continue
		}
		delete(inDocs, e.Name())
		want, err := os.ReadFile(filepath.Join(siteDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := assets.ReadFile("assets/fonts/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		// A licence is text, which a Windows checkout may hold with CRLF;
		// a font is not, and must be the same bytes.
		if filepath.Ext(e.Name()) == ".txt" {
			want, got = lf(want), lf(got)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("fonts/%s differs between cmd/sitegen/assets and cmd/docgen/assets; copy one over the other", e.Name())
		}
	}
	for name := range inDocs {
		t.Errorf("cmd/docgen/assets/fonts/%s is not in cmd/sitegen/assets/fonts", name)
	}
}
