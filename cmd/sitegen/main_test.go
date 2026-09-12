package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	idAttr     = regexp.MustCompile(`\sid="([^"]+)"`)
	imgTag     = regexp.MustCompile(`<img\s[^>]*>`)
	refAttr    = regexp.MustCompile(`\s(?:aria-controls="|aria-labelledby="|href="#)([^"]+)"`)
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
	if err := run(dir, defaultRepo+"/", defaultModule+"/", defaultURL+"/"); err != nil {
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
	// `go install path/@latest` is not a command go accepts.
	if strings.Contains(string(b), defaultModule+"/@latest") {
		t.Errorf("the page writes %s/@latest into the go install line", defaultModule)
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

// Every screenshot is offered at two sizes, and the page has to describe them
// truthfully: a srcset width that is not the file's own has the browser choose
// the wrong one, and a width and height out of proportion to the picture
// reserve the wrong space for it while it loads. Every capture shipped is on
// the page, bar the social card, which is the page's metadata rather than a
// picture on it.
func TestShotsAreOfferedAtBothSizes(t *testing.T) {
	dir, page := generate(t)
	offered := map[string]bool{}
	for _, img := range imgTag.FindAllString(page, -1) {
		set := srcsetAttr.FindStringSubmatch(img)
		if set == nil {
			continue
		}
		for _, attr := range []string{` sizes="`, ` loading="lazy"`} {
			if !strings.Contains(img, attr) {
				t.Errorf("%s has no%s", img, strings.TrimSuffix(attr, `"`))
			}
		}
		w, h := intAttr(img, widthAttr), intAttr(img, heightAttr)
		cands := strings.Split(set[1], ",")
		if len(cands) < 2 {
			t.Errorf("%s offers only one size", img)
		}
		for _, cand := range cands {
			f := strings.Fields(cand)
			if len(f) != 2 || !strings.HasSuffix(f[1], "w") {
				t.Errorf("srcset candidate %q is not a file and a width", cand)
				continue
			}
			offered[f[0]] = true
			want, _ := strconv.Atoi(strings.TrimSuffix(f[1], "w"))
			file, err := os.Open(filepath.Join(dir, f[0]))
			if err != nil {
				continue // TestPageReferencesResolve says so
			}
			cfg, err := png.DecodeConfig(file)
			file.Close()
			if err != nil {
				t.Errorf("%s: %v", f[0], err)
				continue
			}
			if cfg.Width != want {
				t.Errorf("%s is %dpx wide, but the srcset calls it %dw", f[0], cfg.Width, want)
			}
			// The same shape as the width and height given, to a pixel.
			if d := cfg.Height*w - cfg.Width*h; d > w || d < -w {
				t.Errorf("%s is %dx%d, out of proportion to the width %d and height %d given for it", f[0], cfg.Width, cfg.Height, w, h)
			}
		}
	}
	shots, err := assets.ReadDir("assets/shots")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range shots {
		if e.Name() != "og.png" && !offered[e.Name()] {
			t.Errorf("assets/shots/%s is shipped, but no picture on the page offers it", e.Name())
		}
	}
}

var (
	widthAttr  = regexp.MustCompile(`\swidth="(\d+)"`)
	heightAttr = regexp.MustCompile(`\sheight="(\d+)"`)
)

// intAttr is the number an attribute holds, or 0.
func intAttr(tag string, attr *regexp.Regexp) int {
	m := attr.FindStringSubmatch(tag)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// The installers are what a visitor pipes straight into a shell, so a syntax
// slip in either reaches every machine that runs the one-liner. dash is the
// strict POSIX shell `curl … | sh` gets on Debian and Ubuntu; PowerShell's own
// parser reads install.ps1 without running it. Either half is skipped where
// its shell is absent.
func TestInstallScriptsParse(t *testing.T) {
	dir, _ := generate(t)

	t.Run("install.sh", func(t *testing.T) {
		sh, err := exec.LookPath("dash")
		if err != nil {
			if sh, err = exec.LookPath("sh"); err != nil {
				t.Skip("no sh to parse it with")
			}
		}
		out, err := exec.Command(sh, "-n", filepath.Join(dir, "install.sh")).CombinedOutput()
		if err != nil {
			t.Errorf("%s -n install.sh: %v\n%s", filepath.Base(sh), err, out)
		}
	})

	t.Run("install.ps1", func(t *testing.T) {
		ps, err := exec.LookPath("pwsh")
		if err != nil {
			if ps, err = exec.LookPath("powershell"); err != nil {
				t.Skip("no PowerShell to parse it with")
			}
		}
		path := strings.ReplaceAll(filepath.Join(dir, "install.ps1"), "'", "''")
		script := "$errs = $null; " +
			"[void][System.Management.Automation.Language.Parser]::ParseFile('" + path + "', [ref]$null, [ref]$errs); " +
			"if ($errs) { $errs | ForEach-Object { $_.ToString() }; exit 1 }"
		out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
		if err != nil {
			t.Errorf("parsing install.ps1: %v\n%s", err, out)
		}
	})
}

// The favicon is the application's own icon, copied rather than drawn again
// (main.go says why), so the two must stay the same picture. Line endings are
// set aside: a Windows checkout holds either file with CRLF.
func TestFaviconIsTheAppIcon(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "internal", "webui", "assets", "icon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	lf := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
	if lf(icon) != lf(app) {
		t.Error("cmd/sitegen/assets/favicon.svg differs from internal/webui/assets/icon.svg; copy the app's icon over it")
	}
}
