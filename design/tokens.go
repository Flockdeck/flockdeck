// Package design reads design/tokens.css, the one source of truth for
// Flockdeck's colour, type, space, shape and motion, so that a test on any
// surface (the desktop app, the site, the docs, the remote client) can prove its
// own stylesheet carries the same values.
//
// Nothing outside a test imports it: tokens.css is embedded here only so that a
// test needs no path to find it.
//
// The helpers a surface's test calls:
//
//	design.Tokens(design.Dark)                      the canonical values
//	design.RootBlock(css, ":root")                  the text of a stylesheet's :root block
//	design.Declared(block)                          its custom properties
//	design.AssertSurface(t, "site.css", css, ":root", design.Dark, map[string]string{
//	    "--surface": "bg", "--raised": "bg-raised", // the surface's name -> the token's name
//	})
//
// A token's name is its name in tokens.css without the "--fd-" prefix
// ("bg-raised", "accent-line", "ansi-6", "swatch-blue").
package design

import (
	_ "embed"
	"regexp"
	"strings"
	"testing"
)

// TokensCSS is design/tokens.css.
//
//go:embed tokens.css
var TokensCSS string

// Theme names a set of values in tokens.css.
type Theme string

const (
	// Dark is the default: the bare :root block.
	Dark Theme = "dark"
	// Light is :root[data-theme="light"] laid over Dark.
	Light Theme = "light"
)

var (
	comment  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	property = regexp.MustCompile(`(--[\w-]+)\s*:\s*([^;{}]+?)\s*;`)
)

// RootBlock returns the text between the braces of the first block in css whose
// selector is exactly selector (":root", or `:root[data-theme="light"]`). The
// selector must start its line, so ":root" never matches a longer selector or a
// use inside another rule. It is the way to compare two stylesheets' :root
// blocks byte for byte. ok is false when there is no such block.
func RootBlock(css, selector string) (block string, ok bool) {
	css = comment.ReplaceAllString(css, "")
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(selector) + `[ \t]*\{`)
	loc := re.FindStringIndex(css)
	if loc == nil {
		return "", false
	}
	depth := 0
	for i := loc[1] - 1; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[loc[1]:i], true
			}
		}
	}
	return "", false
}

// Declared returns the custom properties declared in a block of text, by their
// full name ("--surface"). Hex colours are upper-cased so that "#4fd1db" and
// "#4FD1DB" compare equal; every other value is trimmed and left as written.
func Declared(block string) map[string]string {
	out := map[string]string{}
	for _, m := range property.FindAllStringSubmatch(comment.ReplaceAllString(block, ""), -1) {
		out[m[1]] = normalise(m[2])
	}
	return out
}

func normalise(v string) string {
	v = strings.TrimSpace(v)
	if len(v) == 7 && v[0] == '#' {
		return strings.ToUpper(v)
	}
	return v
}

// Tokens returns every token of a theme, by name without the "--fd-" prefix.
// Light is the dark set with the light block laid over it, so a value that is
// the same in both themes (the terminal, the ANSI colours, motion, type) is
// present in both.
func Tokens(theme Theme) map[string]string {
	out := map[string]string{}
	add := func(selector string) {
		block, ok := RootBlock(TokensCSS, selector)
		if !ok {
			panic("design: tokens.css has no block " + selector)
		}
		for k, v := range Declared(block) {
			out[strings.TrimPrefix(k, "--fd-")] = v
		}
	}
	add(":root")
	switch theme {
	case Dark:
	case Light:
		add(`:root[data-theme="light"]`)
	default:
		panic("design: unknown theme " + string(theme))
	}
	return out
}

// AssertSurface fails the test for every entry of mapping whose custom
// property, declared in css's block for selector, does not equal the token in
// theme. mapping goes from the surface's own variable name ("--surface") to the
// token's name ("bg"); the table is section 5 of the design direction. A
// variable the block does not declare is a failure too, so a rename cannot make
// the check pass by silence.
func AssertSurface(t testing.TB, name, css, selector string, theme Theme, mapping map[string]string) {
	t.Helper()
	block, ok := RootBlock(css, selector)
	if !ok {
		t.Errorf("%s: no %s block", name, selector)
		return
	}
	have := Declared(block)
	want := Tokens(theme)
	for surfaceVar, token := range mapping {
		canonical, known := want[token]
		if !known {
			t.Errorf("%s: %s is mapped to token %q, which tokens.css does not have", name, surfaceVar, token)
			continue
		}
		got, declared := have[surfaceVar]
		switch {
		case !declared:
			t.Errorf("%s: %s %s does not declare %s (design token %s = %s)", name, selector, string(theme), surfaceVar, token, canonical)
		case got != normalise(canonical):
			t.Errorf("%s: %s = %s, but design token %s is %s (design/tokens.css)", name, surfaceVar, got, token, canonical)
		}
	}
}
