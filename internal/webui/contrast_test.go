package webui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The headers of a pane waiting on you, and of one whose process has exited,
// are tinted amber and red, and the small text in them kept the colours chosen
// for the plain header: the dim 11px details came to 3.72:1 on amber, the blue
// detail 4.04:1 and the red behind count 3.53:1, short of the 4.5:1 text
// needs. Every colour a header draws its text in has to reach that on both.
func TestTextOnATintedPaneHeaderCanBeRead(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	root := cssColours(ruleBody(css, ":root"))
	for _, status := range []string{"waiting", "exited"} {
		header := `.pane[data-status="` + status + `"] .pane-header`
		body := ruleBody(css, header)
		if body == "" {
			t.Fatalf("app.css has no %s rule", header)
		}
		ground := mixedBackground(t, body, root)
		if !regexp.MustCompile(`(?:^|[;\s])color:\s*var\(--fg\)`).MatchString(body) {
			t.Errorf("%s leaves its own text dim, so the pane's name inherits the dim colour", header)
		}
		inside := cssColours(ruleBody(css, header+" > *"))
		for _, token := range []string{"--fg", "--fg-dim", "--accent", "--exited", "--cast", "--waiting", "--working"} {
			c := inside[token]
			if c == "" {
				c = root[token]
			}
			if r := contrast(t, c, ground); r < 4.5 {
				t.Errorf("on the %s header (%s) text in %s (%s) is %.2f:1, short of 4.5:1", status, ground, token, c, r)
			}
		}
	}
}

// stripComments takes the comments out of a style sheet, which may mention a
// brace or a colour of their own.
func stripComments(css string) string {
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
}

// ruleBody returns the declarations of the first rule whose selector list
// includes selector exactly, or "" if none does.
func ruleBody(css, selector string) string {
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		for _, s := range strings.Split(m[1], ",") {
			if strings.Join(strings.Fields(s), " ") == selector {
				return m[2]
			}
		}
	}
	return ""
}

// cssColours reads the custom properties a block sets to a six-digit colour.
func cssColours(body string) map[string]string {
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(--[\w-]+)\s*:\s*(#[0-9a-fA-F]{6})\b`).FindAllStringSubmatch(body, -1) {
		out[m[1]] = strings.ToLower(m[2])
	}
	return out
}

// mixedBackground works out the colour a rule's color-mix background comes to.
func mixedBackground(t *testing.T, body string, vars map[string]string) string {
	t.Helper()
	m := regexp.MustCompile(`background:\s*color-mix\(in srgb,\s*var\((--[\w-]+)\)\s*(\d+)%,\s*var\((--[\w-]+)\)\)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no color-mix background in %q", body)
	}
	share, _ := strconv.Atoi(m[2])
	a, b := rgb(t, vars[m[1]]), rgb(t, vars[m[3]])
	var c [3]int
	for i := range c {
		c[i] = int(math.Round(a[i]*float64(share)/100 + b[i]*float64(100-share)/100))
	}
	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

// rgb reads a six-digit colour.
func rgb(t *testing.T, hex string) [3]float64 {
	t.Helper()
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		t.Fatalf("not a six-digit colour: %q", hex)
	}
	return [3]float64{float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255)}
}

// contrast is the WCAG contrast ratio between two colours.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	lum := func(hex string) float64 {
		c := rgb(t, hex)
		var l [3]float64
		for i, v := range c {
			v /= 255
			if v <= 0.03928 {
				l[i] = v / 12.92
			} else {
				l[i] = math.Pow((v+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
	}
	x, y := lum(a), lum(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}
