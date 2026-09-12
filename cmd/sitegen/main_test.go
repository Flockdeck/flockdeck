package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// generate writes the site into a temporary directory and returns the page.
func generate(t *testing.T) (dir, page string) {
	t.Helper()
	dir = t.TempDir()
	if err := run(dir, defaultRepo, defaultModule, defaultURL); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, string(b)
}

var (
	idAttr  = regexp.MustCompile(`\sid="([^"]+)"`)
	imgTag  = regexp.MustCompile(`<img\s[^>]*>`)
	refAttr = regexp.MustCompile(`\s(?:aria-controls="|aria-labelledby="|href="#)([^"]+)"`)
	srcAttr    = regexp.MustCompile(`\s(?:src|href)="([^"#:]+)"`)
	srcsetAttr = regexp.MustCompile(`\ssrcset="([^"]+)"`)
)

// The page is written by hand in a template, so nothing but a test notices an
// anchor that points nowhere or an id used twice.
func TestPageReferencesResolve(t *testing.T) {
	dir, page := generate(t)

	ids := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(page, -1) {
		if ids[m[1]] {
			t.Errorf("id %q is used twice", m[1])
		}
		ids[m[1]] = true
	}
	for _, m := range refAttr.FindAllStringSubmatch(page, -1) {
		if !ids[m[1]] {
			t.Errorf("%q refers to an element the page does not have", strings.TrimSpace(m[0]))
		}
	}
	// A relative address is a file the site has to ship beside the page, and
	// so is every candidate a srcset offers.
	files := []string{}
	for _, m := range srcAttr.FindAllStringSubmatch(page, -1) {
		files = append(files, m[1])
	}
	for _, m := range srcsetAttr.FindAllStringSubmatch(page, -1) {
		for _, cand := range strings.Split(m[1], ",") {
			files = append(files, strings.Fields(cand)[0])
		}
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("the page links %s, which the site does not contain", f)
		}
	}
}

// The site is the same bytes whichever checkout generates it: a Windows
// working tree holds the assets with CRLF, and writing them out that way would
// rewrite every line of the site's repository the next time a Linux machine
// regenerated it.
func TestTextFilesUseLF(t *testing.T) {
	dir, _ := generate(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "\r\n") {
			t.Errorf("%s is written with CRLF line endings", e.Name())
		}
	}
}

// An address given with a trailing slash must not put a doubled one into the
// lines a visitor pastes into a terminal.
func TestTrailingSlashIsNotDoubled(t *testing.T) {
	dir := t.TempDir()
	if err := run(dir, defaultRepo+"/", defaultModule, defaultURL+"/"); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{defaultURL, defaultRepo} {
		if strings.Contains(string(b), base+"//") {
			t.Errorf("the page writes %s// into an address", base)
		}
	}
}

// Every picture needs words for a reader who cannot see it, and a size, so
// that the page does not jump about as each one arrives.
func TestImagesHaveTextAndSize(t *testing.T) {
	_, page := generate(t)
	imgs := imgTag.FindAllString(page, -1)
	if len(imgs) == 0 {
		t.Fatal("no images on the page")
	}
	for _, img := range imgs {
		for _, attr := range []string{` alt="`, ` width="`, ` height="`} {
			if !strings.Contains(img, attr) {
				t.Errorf("%s has no%s…\"", img, strings.TrimSuffix(attr, `"`))
			}
		}
	}
}
