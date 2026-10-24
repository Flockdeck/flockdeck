package help

import (
	"strings"
	"testing"
)

func walk(ns []Node, fn func(Node)) {
	for _, n := range ns {
		fn(n)
		walk(n.C, fn)
	}
}

func words(ns []Node) string {
	var b strings.Builder
	walk(ns, func(n Node) { b.WriteString(n.X) })
	return b.String()
}

// The tree carries only what the new interface may draw: a short list of tags,
// a link's address, and text. Everything else in the rendered page goes.
func TestTreeDropsWhatIsNotOnTheList(t *testing.T) {
	src := `<p>one <script>alert(1)</script><b onclick="x()">two</b> <a href="javascript:x()">bad</a> ` +
		`<a href="#panes">pane</a> <a href="#nowhere">gone</a> <a href="https://example.com/a" target="_blank" onclick="x()">out</a> ` +
		`<a href="data:text/html,x">data</a> <a href="/etc/passwd">rel</a> <img src="x" onerror="y()"><kbd>F1</kbd></p>`
	tree, err := treeOf(src, map[string]bool{"panes": true})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	var links []string
	walk(tree, func(n Node) {
		if n.T != "" {
			tags = append(tags, n.T)
		}
		if n.H != "" {
			links = append(links, n.H)
		}
	})
	for _, tag := range tags {
		if !allowedTags[tag] {
			t.Errorf("tag %q is not on the list", tag)
		}
	}
	if got := strings.Join(links, " "); got != "#panes https://example.com/a" {
		t.Errorf("links = %q, want only the page and the web address", got)
	}
	w := words(tree)
	for _, want := range []string{"one", "two", "bad", "gone", "data", "rel", "F1"} {
		if !strings.Contains(w, want) {
			t.Errorf("the word %q was lost with its tag: %q", want, w)
		}
	}
	// A script's body is its text node, so it is kept as text: drawn, never run.
	if strings.Contains(w, "onclick") || strings.Contains(w, "onerror") {
		t.Errorf("an attribute came through as text: %q", w)
	}
}

func TestTreeStopsAtADepth(t *testing.T) {
	src := strings.Repeat("<blockquote>", 500) + "deep" + strings.Repeat("</blockquote>", 500)
	tree, err := treeOf(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	depth := 0
	for n := tree; len(n) > 0 && n[0].T == "blockquote"; n = n[0].C {
		depth++
	}
	if depth > maxTreeDepth+1 {
		t.Errorf("depth %d, limit %d", depth, maxTreeDepth)
	}
}

// Every page of the help is in the tree with the same words as the page, so
// nothing the old interface shows is missing from the new one.
func TestEveryPageHasATreeWithItsWords(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if len(p.Tree) == 0 {
			t.Errorf("%s: no tree", p.Slug)
			continue
		}
		got := strings.Join(strings.Fields(words(p.Tree)), " ")
		want := p.Text
		// plainText turns a tag into a space where the tree has none, so compare
		// without spaces.
		if strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(want, " ", "") {
			t.Errorf("%s: the tree's words differ from the page's text\n tree: %.200s\n text: %.200s", p.Slug, got, want)
		}
		for _, leftover := range []string{"{{keys", "[[key:", "[[action:"} {
			if strings.Contains(words(p.Tree), leftover) {
				t.Errorf("%s: unexpanded %q in the tree", p.Slug, leftover)
			}
		}
	}
}

// A page's links in the tree are the page's own: those to another page name one,
// and those to the web open elsewhere in the interface because they are the only
// other kind.
func TestTreeLinksAreSlugsOrTheWeb(t *testing.T) {
	pages, _ := Pages()
	known := map[string]bool{}
	for _, s := range order {
		known[s] = true
	}
	pageLinks := 0
	for _, p := range pages {
		walk(p.Tree, func(n Node) {
			if n.T == "a" {
				if slug, ok := strings.CutPrefix(n.H, "#"); ok {
					pageLinks++
					if !known[slug] {
						t.Errorf("%s: link to #%s", p.Slug, slug)
					}
				} else if !strings.HasPrefix(n.H, "https://") && !strings.HasPrefix(n.H, "http://") {
					t.Errorf("%s: link to %q", p.Slug, n.H)
				}
			}
		})
	}
	if pageLinks == 0 {
		t.Error("no cross-references in the trees")
	}
}
