package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The default accent became the mark's cyan, and blue became a swatch of its
// own. "teal" was a swatch while the default was blue and is the default now,
// so a preference saved as teal must open on the default swatch rather than
// on none of them, and must not put an accent on the document that the style
// sheet has no rule for.
func TestASavedTealAccentIsTheDefault(t *testing.T) {
	runFrontEnd(t, `
h.hello({ accentColor: "teal" });
assert.strictEqual(h.doc.documentElement.dataset.accent, undefined, "a saved teal accent was put on the document");
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-appearance"));
const on = (id) => h.$(id).getAttribute("aria-checked") === "true";
assert.ok(on("set-accent-cyan"), "a saved teal accent does not show the default swatch as chosen");
assert.ok(!h.$("set-accent-teal"), "teal is still offered as a swatch of its own");

h.click(h.$("set-accent-blue"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "accentColor", text: "blue" });
assert.strictEqual(h.doc.documentElement.dataset.accent, "blue", "the document did not take the blue accent");
assert.ok(on("set-accent-blue") && !on("set-accent-cyan"), "the accent choice did not follow the click");

h.click(h.$("set-accent-cyan"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "accentColor", text: "" });
assert.strictEqual(h.doc.documentElement.dataset.accent, undefined, "the default accent left an accent on the document");

// A preference from another window, or from a newer or older version, that
// names no swatch is the default too.
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], accentColor: "chartreuse" } });
assert.strictEqual(h.doc.documentElement.dataset.accent, undefined, "an accent that is no swatch was put on the document");
h.recv({ type: "prefs", prefs: { helpSeen: true, dismissedTips: [], accentColor: "pink" } });
assert.strictEqual(h.doc.documentElement.dataset.accent, "pink");
`)
}

// The swatches in Settings are coloured from a list in app.js, and the accent
// they choose from [data-accent] rules in app.css. Nothing reads one from the
// other, so a swatch could promise a colour the window then does not take.
func TestEverySwatchIsTheAccentItChooses(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	js := readAsset(t, "app.js")
	list := regexp.MustCompile(`(?s)const ACCENTS = \[(.*?)\];`).FindStringSubmatch(js)
	if list == nil {
		t.Fatal("app.js has no ACCENTS list")
	}
	swatches := regexp.MustCompile(`\["([a-z]*)", "[^"]+", "(#[0-9a-fA-F]{6})"\]`).FindAllStringSubmatch(list[1], -1)
	if len(swatches) < 2 {
		t.Fatalf("read %d swatches from app.js", len(swatches))
	}
	for _, m := range swatches {
		name, colour := m[1], strings.ToLower(m[2])
		if name == "teal" {
			t.Error("teal is offered as a swatch; it is kept only as a saved value meaning the default")
		}
		selector := ":root"
		if name != "" {
			selector = `:root[data-accent="` + name + `"]`
		}
		got := cssColours(ruleBody(css, selector))["--accent"]
		if got != colour {
			t.Errorf("the %q swatch is drawn %s, and %s sets --accent to %q", name, colour, selector, got)
		}
		if name == "" {
			continue
		}
		// Each swatch has a light value too, in both places the light
		// palette is written.
		for _, light := range []string{`:root[data-theme="light"][data-accent="` + name + `"]`, `:root:not([data-theme])[data-accent="` + name + `"]`} {
			if cssColours(ruleBody(css, light))["--accent"] == "" {
				t.Errorf("app.css has no %s rule setting --accent", light)
			}
		}
	}
}

// The light palette is written twice: once for Light, once for Follow system
// on a light system. They are kept the same by hand, and a colour changed in
// one only would show as a window that looks different depending on how it
// came to be light.
func TestTheLightPaletteIsTheSameBothWaysOfReachingIt(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	chosen := ruleBody(css, `:root[data-theme="light"]`)
	system := ruleBody(css, `:root:not([data-theme])`)
	if chosen == "" || system == "" {
		t.Fatal("app.css does not have both light palettes")
	}
	decls := func(body string) map[string]string {
		out := map[string]string{}
		for _, d := range strings.Split(body, ";") {
			if name, value, ok := strings.Cut(d, ":"); ok {
				out[strings.TrimSpace(name)] = strings.Join(strings.Fields(value), " ")
			}
		}
		return out
	}
	a, b := decls(chosen), decls(system)
	for name, value := range a {
		if b[name] != value {
			t.Errorf("%s is %q for Light and %q for Follow system", name, value, b[name])
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			t.Errorf("%s is set for Follow system and not for Light", name)
		}
	}
	// Every colour the dark palette defines has a light value, so nothing
	// dark is left showing through a light window. The terminal's own
	// colours are the exception: a terminal is dark in both.
	for name := range cssColours(ruleBody(css, ":root")) {
		if strings.HasPrefix(name, "--term-") {
			continue
		}
		if _, ok := a[name]; !ok {
			t.Errorf("the light palette does not set %s", name)
		}
	}
}

// The typefaces ship inside the application: the style sheet names files that
// are embedded beside it, with their licences, and neither it nor the page
// asks another origin for anything.
func TestTheFontsShipWithTheApplication(t *testing.T) {
	css := readAsset(t, "app.css")
	urls := regexp.MustCompile(`url\(\s*["']?([^"')]+)`).FindAllStringSubmatch(css, -1)
	fonts := 0
	for _, m := range urls {
		ref := m[1]
		if strings.Contains(ref, "//") || strings.HasPrefix(ref, "data:") {
			t.Errorf("app.css asks for %s, which is not a file shipped beside it", ref)
			continue
		}
		if _, err := fs.Stat(FS(), ref); err != nil {
			t.Errorf("app.css asks for %s, which is not embedded: %v", ref, err)
		}
		if strings.HasSuffix(ref, ".woff2") {
			fonts++
		}
	}
	if fonts != 2 {
		t.Errorf("app.css names %d font files; it should name the sans and the mono", fonts)
	}
	for _, name := range []string{"fonts/OFL-IBMPlexSans.txt", "fonts/OFL-JetBrainsMono.txt"} {
		if _, err := fs.Stat(FS(), name); err != nil {
			t.Errorf("the licence %s does not ship with its font: %v", name, err)
		}
	}
	if strings.Contains(css, "@import") {
		t.Error("app.css imports another style sheet")
	}
	page := readAsset(t, "index.html")
	for _, m := range regexp.MustCompile(`(?:href|src)="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		if strings.Contains(m[1], "//") {
			t.Errorf("index.html asks another origin for %s", m[1])
		}
	}
	preload := regexp.MustCompile(`<link rel="preload" href="assets/([^"]+)" as="font" type="font/woff2" crossorigin>`).FindStringSubmatch(page)
	if preload == nil {
		t.Fatal("index.html does not preload the sans")
	}
	if _, err := fs.Stat(FS(), preload[1]); err != nil {
		t.Errorf("index.html preloads %s, which is not embedded: %v", preload[1], err)
	}
}

// A status is a shape before it is a colour: a circle is alive, a triangle
// needs you, a square has ended, and a filled one has something happening.
// Two marks that differed only in colour would be one mark to anybody who
// cannot tell those colours apart, which is what these shapes are for.
func TestEveryStatusHasAShapeOfItsOwn(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	type mark struct{ triangle, square, filled bool }
	read := func(status string) mark {
		body := ruleBody(css, ".dot."+status)
		if body == "" {
			t.Fatalf("app.css has no .dot.%s rule", status)
		}
		return mark{
			triangle: strings.Contains(body, "clip-path: polygon("),
			square:   regexp.MustCompile(`border-radius:\s*1\.5px`).MatchString(body),
			filled:   regexp.MustCompile(`(?:^|[;\s])background:\s*var\(`).MatchString(body),
		}
	}
	want := map[string]mark{
		"working":  {filled: true},
		"idle":     {},
		"starting": {},
		"waiting":  {triangle: true, filled: true},
		"blocked":  {triangle: true, filled: true},
		"failed":   {square: true, filled: true},
		"exited":   {square: true},
	}
	for status, w := range want {
		if got := read(status); got != w {
			t.Errorf(".dot.%s is drawn %+v, want %+v", status, got, w)
		}
	}
	// The colours: working is the accent's cyan, not the green of a verdict,
	// and a pane that simply ended is not the red of one that failed.
	for status, token := range map[string]string{"working": "--working", "waiting": "--waiting", "failed": "--exited", "exited": "--ended", "idle": "--idle"} {
		if !strings.Contains(ruleBody(css, ".dot."+status), "var("+token+")") {
			t.Errorf(".dot.%s is not drawn in %s", status, token)
		}
	}
	root := cssColours(ruleBody(css, ":root"))
	if root["--working"] != root["--accent"] {
		t.Errorf("working is %s and the accent %s; working is drawn in the mark's cyan", root["--working"], root["--accent"])
	}
	if root["--ok"] == "" || root["--ok"] == root["--working"] {
		t.Errorf("a verdict (--ok, %q) has no colour of its own apart from working (%s)", root["--ok"], root["--working"])
	}
}
