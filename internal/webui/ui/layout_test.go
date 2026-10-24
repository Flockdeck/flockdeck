package ui

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

// Only the graph scrolls sideways. A graph wider than the page's column used to
// make the column itself scrollable, so anything that took focus scrolled the
// needs list, the tabs and the heading off the column's left edge. With the graph
// scrolling inside its own box, the column has nothing to scroll sideways.
func TestOnlyTheGraphScrollsSideways(t *testing.T) {
	b, err := os.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	if got := rule(t, css, ".graph-view"); strings.Contains(got, "overflow-x: auto") || !strings.Contains(got, "min-width: 0") {
		t.Errorf(".graph-view = %q, want it to hold the column and the graph without scrolling itself", got)
	}
	if got := rule(t, css, ".graph-main"); !strings.Contains(got, "overflow-x: auto") || !strings.Contains(got, "min-width: 0") {
		t.Errorf(".graph-main = %q, want the graph to scroll sideways inside itself", got)
	}
	if got := rule(t, css, ".home--graph"); !strings.Contains(got, "overflow-x: hidden") {
		t.Errorf(".home--graph = %q, want no sideways scroll", got)
	}
}
