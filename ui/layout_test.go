package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// rule is the declarations of the first rule whose selector is exactly sel.
func rule(t *testing.T, css, sel string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no rule for %s", sel)
	}
	return m[1]
}

// The home area, which holds the status lines, never scrolls sideways.
func TestHomeAreaDoesNotScrollSideways(t *testing.T) {
	b, err := os.ReadFile("src/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if got := rule(t, css, ".home--graph"); !strings.Contains(got, "overflow-x: hidden") {
		t.Errorf(".home--graph = %q, want no sideways scroll", got)
	}
}
