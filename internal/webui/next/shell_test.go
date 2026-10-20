//go:build next

package next

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shell's key handling reads the hello's key table and names no key of its
// own, so a binding is changed in one place, internal/help/keys.go. Every file
// in shell/ is read; key names may appear only between the vocabulary markers in
// keys.js, which hold the words the table uses for arrows and the keys that move
// through a list.
var (
	keyNameLiteral = regexp.MustCompile(`["'](?:Escape|Enter|Tab|Backspace|Delete|Home|End|PageUp|PageDown|Space|Arrow(?:Up|Down|Left|Right)|F(?:[1-9]|1[0-2])|(?:Ctrl|Control|Shift|Alt|Meta|Cmd)\+[^"']*)["']`)
	keyCompare     = regexp.MustCompile(`\.(?:key|code|keyCode|which)\s*[=!]==?\s*["'0-9]`)
	keySwitch      = regexp.MustCompile(`switch\s*\(\s*[A-Za-z_.]*\.(?:key|code|keyCode)\s*\)`)
	vocabulary     = regexp.MustCompile(`(?s)// vocabulary:begin.*?// vocabulary:end`)
	jsComment      = regexp.MustCompile(`(?s)/\*.*?\*/|//[^
]*`)
)

func TestShellNamesNoKeysOfItsOwn(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("assets", "shell", "*.js"))
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
		for _, re := range []*regexp.Regexp{keyNameLiteral, keyCompare, keySwitch} {
			if m := re.FindString(src); m != "" {
				t.Errorf("%s names a key outside the table: %s", filepath.Base(f), m)
			}
		}
	}
	keys, err := os.ReadFile(filepath.Join("assets", "shell", "keys.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !vocabulary.Match(keys) {
		t.Error("keys.js has no vocabulary block, so the scan above would pass anything")
	}
}

func TestKeyScanCatchesWhatItIsFor(t *testing.T) {
	for _, s := range []string{
		`if (e.key === "Escape") close();`,
		`const k = "Ctrl+Shift+K";`,
		`if (e.key == 'Enter') go();`,
		`switch (e.key) { }`,
		`if (e.keyCode === 27) x();`,
		`bind('F6', go)`,
	} {
		hit := false
		for _, re := range []*regexp.Regexp{keyNameLiteral, keyCompare, keySwitch} {
			hit = hit || re.MatchString(s)
		}
		if !hit {
			t.Errorf("the scan misses %q", s)
		}
	}
	for _, s := range []string{`const id = actionFor(bindings, e);`, `e.key === LIST.down`, `title="Enter a name"`} {
		for _, re := range []*regexp.Regexp{keyNameLiteral, keyCompare, keySwitch} {
			if re.MatchString(s) {
				t.Errorf("the scan flags %q", s)
			}
		}
	}
}

// The stylesheet's typefaces and tokens are copied from the old app.css, so the
// two interfaces match, and everything the shell adds uses them. The copy is the
// text above the marker, and the old sheet must begin with the same text.
const layoutMarker = "/* ==== The new shell's layout"

func TestStylesheetTokensAreTheOldOnes(t *testing.T) {
	next, err := os.ReadFile(filepath.Join("assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join("..", "assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	head, _, ok := strings.Cut(string(next), layoutMarker)
	if !ok {
		t.Fatal("app.css has no layout marker")
	}
	if !strings.HasPrefix(string(old), head) {
		t.Error("the tokens at the top of next/assets/app.css are not the start of the old app.css; copy them again")
	}
	if !strings.Contains(head, "--accent:") || !strings.Contains(head, "--sans:") || !strings.Contains(head, "@font-face") {
		t.Error("the copied head holds no tokens")
	}
}

var (
	colourLiteral = regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(`)
	fontLiteral   = regexp.MustCompile(`(?i)font(?:-family)?\s*:[^;{}]*["'][A-Za-z ]+["']|font-family\s*:\s*(?:[^v\s;]|v[^a])`)
)

func TestStylesheetLayoutUsesTokensOnly(t *testing.T) {
	next, err := os.ReadFile(filepath.Join("assets", "app.css"))
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
	if m := fontLiteral.FindString(code); m != "" {
		t.Errorf("the layout writes a typeface of its own: %s", m)
	}
	for _, s := range []string{"background: #fff;", "color: rgb(1 2 3);", `font-family: "Arial";`, "font-family: Arial;"} {
		if !colourLiteral.MatchString(s) && !fontLiteral.MatchString(s) {
			t.Errorf("the scan misses %q", s)
		}
	}
	for _, s := range []string{"color: var(--fg);", "font: 13px/1.4 var(--sans);", "font-family: var(--mono);"} {
		if colourLiteral.MatchString(s) || fontLiteral.MatchString(s) {
			t.Errorf("the scan flags %q", s)
		}
	}
}

// app.js and app.css name the phone width separately; they are held together
// here.
func TestNarrowQueryMatchesTheStylesheet(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("assets", "shell", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`NARROW_QUERY\s*=\s*"(\([^"]+\))"`).FindStringSubmatch(string(js))
	if m == nil {
		t.Fatal("NARROW_QUERY is not in shell/app.js")
	}
	css, err := os.ReadFile(filepath.Join("assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	_, layout, _ := strings.Cut(string(css), layoutMarker)
	if !strings.Contains(layout, "@media "+m[1]) {
		t.Errorf("the layout has no rule for %s", m[1])
	}
	// The old sheet's own phone width is the same, so the two interfaces change
	// shape together.
	old, err := os.ReadFile(filepath.Join("..", "assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), "@media "+m[1]) {
		t.Errorf("the old stylesheet has no %s rule, so the two interfaces would narrow at different widths", m[1])
	}
}
