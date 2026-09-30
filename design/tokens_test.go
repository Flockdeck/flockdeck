package design

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// dir is the design/ directory these tests run in.
func dir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate design/")
	}
	return filepath.Dir(file)
}

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

var hexColour = regexp.MustCompile(`#[0-9A-Fa-f]{6}\b`)

// colourTokens are the tokens a theme must define for itself: everything that
// is a colour and not part of the terminal, which is the same in both themes.
func colourTokens(m map[string]string) []string {
	var out []string
	for k, v := range m {
		if hexColour.MatchString(v) && !strings.HasPrefix(k, "term-") && !strings.HasPrefix(k, "ansi-") {
			out = append(out, k)
		}
	}
	return out
}

func TestTokensCSSIsWellFormed(t *testing.T) {
	dark, light := Tokens(Dark), Tokens(Light)

	// The names every surface's mapping (color.md, "Token mapping") relies on.
	names := []string{
		"bg", "bg-raised", "bg-sunken", "bg-overlay", "bg-hover", "bg-active",
		"line-subtle", "line", "line-strong", "fg", "fg-muted", "fg-faint",
		"accent", "accent-strong", "accent-subtle", "accent-line", "on-accent",
		"working", "waiting", "failed", "idle", "exited", "ok", "cast", "on-cast",
		"on-waiting", "on-failed", "on-ok", "term-bg", "term-fg", "term-cursor", "term-selection",
		"swatch-cyan", "swatch-blue", "swatch-purple", "swatch-green", "swatch-orange", "swatch-pink",
		"sans", "mono", "r-1", "r-2", "r-3", "dur-1", "dur-2", "dur-3", "dur-4",
	}
	for i := 0; i < 16; i++ {
		names = append(names, fmt.Sprintf("ansi-%d", i))
	}
	for _, k := range names {
		if dark[k] == "" {
			t.Errorf("tokens.css: no --fd-%s in the dark theme", k)
		}
	}

	// Owner decisions 1 and 4: working is the accent, and cyan is the default swatch.
	for name, m := range map[string]map[string]string{"dark": dark, "light": light} {
		if m["working"] != m["accent"] {
			t.Errorf("%s: working %s must equal the accent %s", name, m["working"], m["accent"])
		}
		if m["swatch-cyan"] != m["accent"] {
			t.Errorf("%s: the cyan swatch %s must equal the accent %s", name, m["swatch-cyan"], m["accent"])
		}
	}

	// The light block must restate every colour: a token left out would silently
	// keep its dark value on a light ground.
	lightBlock, _ := RootBlock(TokensCSS, `:root[data-theme="light"]`)
	own := Declared(lightBlock)
	for _, k := range colourTokens(dark) {
		if _, ok := own["--fd-"+k]; !ok {
			t.Errorf("tokens.css: the light theme does not restate --fd-%s", k)
		}
	}
}

// The system block is the light block again, for a page that follows the
// operating system; the two must not drift.
func TestSystemBlockEqualsLightBlock(t *testing.T) {
	lightBlock, ok := RootBlock(TokensCSS, `:root[data-theme="light"]`)
	if !ok {
		t.Fatal("no light block")
	}
	systemBlock, ok := RootBlock(TokensCSS, `:root[data-theme="system"]`)
	if !ok {
		t.Fatal("no system block")
	}
	light, system := Declared(lightBlock), Declared(systemBlock)
	for k, v := range light {
		if system[k] != v {
			t.Errorf("%s: light is %q, system is %q", k, v, system[k])
		}
	}
	for k := range system {
		if _, ok := light[k]; !ok {
			t.Errorf("%s is in the system block only", k)
		}
	}
}

// design/ is the single source: it holds one palette, and what it says in prose
// is the palette it holds.
func TestDesignFolderIsTheSingleSource(t *testing.T) {
	for _, name := range []string{
		"tokens.css", "check-contrast.mjs", "contrast-report.md", "color.md",
		"fonts/OFL-IBMPlexSans.txt", "fonts/OFL-JetBrainsMono.txt",
	} {
		if _, err := os.Stat(filepath.Join(dir(t), filepath.FromSlash(name))); err != nil {
			t.Errorf("design/%s is missing: %v", name, err)
		}
	}
	// The pre-restyle sources named the same colours differently and carried a
	// second palette. They must not come back beside the new one.
	for _, name := range []string{"color.css", "contrast.js", "color-spec.html"} {
		if _, err := os.Stat(filepath.Join(dir(t), name)); err == nil {
			t.Errorf("design/%s is the retired palette; tokens.css replaced it", name)
		}
	}

	// Every colour written in color.md is a token value, so the prose cannot
	// state one the palette does not have.
	known := map[string]bool{}
	for _, theme := range []Theme{Dark, Light} {
		for _, v := range Tokens(theme) {
			for _, h := range hexColour.FindAllString(v, -1) {
				known[strings.ToUpper(h)] = true
			}
		}
	}
	for _, h := range hexColour.FindAllString(read(t, "color.md"), -1) {
		if !known[strings.ToUpper(h)] {
			t.Errorf("color.md names %s, which is not a value in tokens.css", h)
		}
	}
}

// The contrast script reads tokens.css itself. Running it here means a change
// to a value that fails a ratio, or a report gone stale, fails go test on every
// platform CI runs, not only in the design-tokens workflow.
func TestContrastScriptPasses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is not on PATH, and CI is set: the contrast check cannot run where it is meant to")
		}
		t.Skip("node is not on PATH, so the contrast check cannot run")
	}
	cmd := exec.Command(node, filepath.Join(dir(t), "check-contrast.mjs"), "--check-report")
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("check-contrast.mjs failed: %v\n%s", r.err, r.out)
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("check-contrast.mjs did not finish in 60s")
	}
}

func TestRootBlockFindsTheBareRoot(t *testing.T) {
	css := `/* :root { --x: #000000; } */
.a { color: red; }
@media (min-width: 1px) {
  :root[data-theme="light"] { --a: #ffffff; }
}
:root {
  --a: #0F1418;
  --b: 6px;
}
:root[data-theme="light"] { --a: #FAFBFB; }
`
	block, ok := RootBlock(css, ":root")
	if !ok {
		t.Fatal("no :root block")
	}
	got := Declared(block)
	if got["--a"] != "#0F1418" || got["--b"] != "6px" || len(got) != 2 {
		t.Errorf("bare :root = %v", got)
	}
	light, ok := RootBlock(css, `:root[data-theme="light"]`)
	if !ok || Declared(light)["--a"] != "#FFFFFF" {
		t.Errorf("light block = %q", light)
	}
	if _, ok := RootBlock(css, ":host"); ok {
		t.Error("found a block that is not there")
	}
}

// recorder stands in for *testing.T so a test can see what AssertSurface reports.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestAssertSurface(t *testing.T) {
	// The fixtures are built from the tokens themselves, so a change to the
	// palette does not break the helper's own test.
	d, l := Tokens(Dark), Tokens(Light)
	good := fmt.Sprintf(":root { --surface: %s; --raised: %s; --muted: %s; }",
		strings.ToLower(d["bg"]), d["bg-raised"], d["fg-muted"])
	ok := &recorder{}
	AssertSurface(ok, "good.css", good, ":root", Dark, map[string]string{
		"--surface": "bg", "--raised": "bg-raised", "--muted": "fg-muted",
	})
	if len(ok.errs) != 0 {
		t.Errorf("a matching surface failed: %v", ok.errs)
	}

	bad := fmt.Sprintf(":root { --surface: #010203; --raised: %s; }", d["bg-raised"])
	fail := &recorder{}
	AssertSurface(fail, "bad.css", bad, ":root", Dark, map[string]string{
		"--surface": "bg", "--raised": "bg-raised", "--muted": "fg-muted", "--x": "no-such-token",
	})
	if len(fail.errs) != 3 {
		t.Errorf("want 3 failures (wrong value, missing variable, unknown token), got %d: %v", len(fail.errs), fail.errs)
	}

	light := &recorder{}
	AssertSurface(light, "light.css", fmt.Sprintf(":root { --surface: %s; }", l["bg"]), ":root", Light, map[string]string{"--surface": "bg"})
	if len(light.errs) != 0 {
		t.Errorf("the light value of bg failed: %v", light.errs)
	}
	if d["bg"] == l["bg"] {
		t.Error("the light and dark grounds are the same, so the light check proves nothing")
	}
}
