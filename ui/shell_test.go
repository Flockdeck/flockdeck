package webui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shell's key handling reads the hello's key table and names no key of its
// own, so a binding is changed in one place, ui/help/keys.go. Every file
// in shell/ is read; key names may appear only between the vocabulary markers in
// keys.js, which hold the words the table uses for arrows and the keys that move
// through a list.
//
// The scan looks for a quoted key name or a combination anywhere; for the
// event's key, code, keyCode or which (or a variable of one of those names, as a
// destructured key is) compared with a literal in either order, with or without
// a case change; for a switch on one; for a destructured one; and for a list of
// literals that is asked whether it holds the key. A literal is a string in any
// of the three quote styles, or a number.
const (
	keyField = `\b(?:key|code|keyCode|which)\b(?:\s*\.\s*to(?:Lower|Upper)Case\s*\(\s*\))?`
	literal  = `["'\x60\d]`
)

var (
	keyNameLiteral = regexp.MustCompile(`["'\x60](?:Escape|Esc|Enter|Return|Tab|Backspace|Delete|Home|End|PageUp|PageDown|Space|Arrow(?:Up|Down|Left|Right)|F(?:[1-9]|1[0-2])|(?:Ctrl|Control|Shift|Alt|Meta|Cmd)\+[^"'\x60]*)["'\x60]`)
	keyCompare     = regexp.MustCompile(keyField + `\s*[!=]==?\s*` + literal + `|` + literal + `[^;\n]*?[!=]==?\s*[\w.]*` + keyField)
	keySwitch      = regexp.MustCompile(`switch\s*\(\s*[\w.]*` + keyField + `[^)]*\)`)
	keyDestructure = regexp.MustCompile(`\{(?:[^}:]*,)?\s*(?:key|code|keyCode)\s*(?:,[^}:]*)?\}\s*(?:=\s*\w|\))`)
	keyIncludes    = regexp.MustCompile(`\[[^\]]*` + literal + `[^\]]*\]\s*\.\s*(?:includes|indexOf)\s*\(\s*[\w.]*` + keyField)
	keyScans       = []*regexp.Regexp{keyNameLiteral, keyCompare, keySwitch, keyDestructure, keyIncludes}
	vocabulary     = regexp.MustCompile(`(?s)// vocabulary:begin.*?// vocabulary:end`)
	jsComment      = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)
)

func TestShellNamesNoKeysOfItsOwn(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("src", "shell", "*.js"))
	if err != nil || len(files) < 4 {
		t.Fatalf("shell files %v, %v", files, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := jsComment.ReplaceAllString(vocabulary.ReplaceAllString(string(b), ""), "")
		if base := filepath.Base(f); base != "keys.js" && vocabulary.MatchString(string(b)) {
			t.Errorf("%s has a vocabulary block; key names belong in keys.js", base)
		}
		for _, re := range keyScans {
			if m := re.FindString(src); m != "" {
				t.Errorf("%s names a key outside the table: %s", filepath.Base(f), m)
			}
		}
	}
	keys, err := os.ReadFile(filepath.Join("src", "shell", "keys.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !vocabulary.Match(keys) {
		t.Error("keys.js has no vocabulary block, so the scan above would pass anything")
	}
}

func TestKeyScanCatchesWhatItIsFor(t *testing.T) {
	hits := func(s string) bool {
		for _, re := range keyScans {
			if re.MatchString(s) {
				return true
			}
		}
		return false
	}
	for _, s := range []string{
		`if (e.key === "Escape") close();`,
		`const k = "Ctrl+Shift+K";`,
		`if (e.key == 'Enter') go();`,
		"if (e.key === `Enter`) go();",
		"const k = `Escape`;",
		`switch (e.key) { }`,
		`switch (ev.code) { }`,
		`if (e.keyCode === 27) x();`,
		`if (27 === e.keyCode) x();`,
		`if ("Escape" === e.key) x();`,
		`const { key } = e; if (key === "x") go();`,
		`if (key === "x") go();`,
		`if (key.toLowerCase() === "x") go();`,
		`if (e.key.toLowerCase() !== 'k') go();`,
		`if (e.code === "KeyK") go();`,
		`if (which == 13) go();`,
		`const onKey = ({ key }) => go(key);`,
		`if (["Enter", "Tab"].includes(e.key)) go();`,
		`bind('F6', go)`,
	} {
		if !hits(s) {
			t.Errorf("the scan misses %q", s)
		}
	}
	for _, s := range []string{
		`const id = actionFor(bindings, e);`,
		`e.key === LIST.down`,
		`key === PLUS`,
		`title="Enter a name"`,
		`key=${r.key.id}`,
		`const { root, name } = project;`,
		`if (!keys.includes("…")) go();`,
	} {
		if hits(s) {
			t.Errorf("the scan flags %q", s)
		}
	}
}

// The stylesheet's typefaces and tokens are the text above the marker, and
// everything the shell adds below it uses them.
const layoutMarker = "/* ==== The new shell's layout"

func TestStylesheetHoldsTheTokens(t *testing.T) {
	next, err := os.ReadFile(filepath.Join("src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	head, _, ok := strings.Cut(string(next), layoutMarker)
	if !ok {
		t.Fatal("app.css has no layout marker")
	}
	if !strings.Contains(head, "--accent:") || !strings.Contains(head, "--sans:") || !strings.Contains(head, "@font-face") {
		t.Error("the copied head holds no tokens")
	}
}

var (
	colourLiteral = regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(`)
	// A named colour in a property that takes one. Transparent and currentcolor
	// are not colours of their own and are left alone; a var() is cut out first,
	// since a token may be named for a colour.
	colourName  = regexp.MustCompile(`(?i)\b(?:color|background(?:-color)?|border(?:-[a-z]+)?|outline(?:-color)?|fill|stroke|box-shadow|text-shadow|accent-color|caret-color)\s*:[^;{}]*\b(?:white|black|red|green|blue|gray|grey|silver|orange|yellow|purple|pink|cyan|magenta|lime|teal|navy|maroon|olive|aqua|fuchsia|brown|gold|coral|tomato|crimson|indigo|violet|salmon|khaki|tan|beige|ivory|lavender|plum|orchid|turquoise|azure|snow|linen|wheat|chocolate|firebrick)\b`)
	varCall     = regexp.MustCompile(`var\([^)]*\)`)
	fontLiteral = regexp.MustCompile(`(?i)font(?:-family)?\s*:[^;{}]*["'][A-Za-z ]+["']|font-family\s*:\s*(?:[^v\s;]|v[^a])`)
)

func TestStylesheetLayoutUsesTokensOnly(t *testing.T) {
	next, err := os.ReadFile(filepath.Join("src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	_, layout, ok := strings.Cut(string(next), layoutMarker)
	if !ok {
		t.Fatal("app.css has no layout marker")
	}
	// A comment may name a colour; a declaration may not hold one.
	code := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(layout, "")
	if m := colourLiteral.FindString(code); m != "" {
		t.Errorf("the layout writes a colour of its own: %s", m)
	}
	if m := colourName.FindString(varCall.ReplaceAllString(code, "var()")); m != "" {
		t.Errorf("the layout names a colour of its own: %s", m)
	}
	if m := fontLiteral.FindString(code); m != "" {
		t.Errorf("the layout writes a typeface of its own: %s", m)
	}
	for _, s := range []string{"background: #fff;", "color: rgb(1 2 3);", `font-family: "Arial";`, "font-family: Arial;", "color: white;", "border: 1px solid red;", "box-shadow: 0 0 2px black;"} {
		if !colourLiteral.MatchString(s) && !fontLiteral.MatchString(s) && !colourName.MatchString(s) {
			t.Errorf("the scan misses %q", s)
		}
	}
	for _, s := range []string{"color: var(--fg);", "font: 13px/1.4 var(--sans);", "font-family: var(--mono);", "border: 1px solid transparent;", "color: var(--green);"} {
		if colourLiteral.MatchString(s) || fontLiteral.MatchString(s) || colourName.MatchString(varCall.ReplaceAllString(s, "var()")) {
			t.Errorf("the scan flags %q", s)
		}
	}
}

// app.js and app.css name the phone width separately; they are held together
// here.
func TestNarrowQueryMatchesTheStylesheet(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("src", "shell", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`NARROW_QUERY\s*=\s*"(\([^"]+\))"`).FindStringSubmatch(string(js))
	if m == nil {
		t.Fatal("NARROW_QUERY is not in shell/app.js")
	}
	css, err := os.ReadFile(filepath.Join("src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	_, layout, _ := strings.Cut(string(css), layoutMarker)
	if !strings.Contains(layout, "@media "+m[1]) {
		t.Errorf("the layout has no rule for %s", m[1])
	}
}

// On a phone the palette has to be usable: it sits above the drawer, which
// covers the window there; its field is 16px so iOS does not zoom in when it
// takes the keyboard; and a rail row is a target a thumb can hit.
func TestPhoneRulesKeepThePaletteUsable(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	_, layout, _ := strings.Cut(string(css), layoutMarker)
	num := func(re, src string) int {
		m := regexp.MustCompile(re).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%s is not in the layout", re)
		}
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	at := strings.Index(layout, "@media (max-width: 640px)")
	if at < 0 {
		t.Fatal("no phone rules")
	}
	narrow := layout[at:]
	scrim := num(`\.palette-scrim\s*\{[^}]*z-index:\s*(\d+)`, layout)
	drawer := num(`\.drawer\s*\{[^}]*z-index:\s*(\d+)`, narrow)
	if scrim <= drawer {
		t.Errorf("the palette's scrim (z-index %d) is not above the drawer (%d), which covers the window on a phone", scrim, drawer)
	}
	if size := num(`\.palette-input\s*\{[^}]*font-size:\s*(\d+)px`, narrow); size < 16 {
		t.Errorf("the palette field is %dpx on a phone; under 16px iOS zooms in", size)
	}
	if h := num(`\.rail-item\s*\{[^}]*min-height:\s*(\d+)px`, narrow); h < 40 {
		t.Errorf("a rail row is %dpx tall on a phone, under 40", h)
	}
}

// The active project is marked by more than a tint, which some people cannot
// tell from the rest, and a collapsed rail still shows what each project is.
func TestRailMarksTheActiveProjectAndShowsInitials(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("src", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	_, layout, _ := strings.Cut(string(css), layoutMarker)
	if !regexp.MustCompile(`\.rail-item\.is-active\s*\{[^}]*box-shadow:\s*inset`).MatchString(layout) {
		t.Error("the active rail item has no mark beyond its tint")
	}
	if !regexp.MustCompile(`\.shell--rail-collapsed \.rail-initial\s*\{[^}]*display:\s*inline-block`).MatchString(layout) {
		t.Error("a collapsed rail does not show the project initials")
	}
}
