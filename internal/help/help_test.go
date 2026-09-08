package help

import (
	"strings"
	"testing"
)

func TestPagesRender(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	if len(pages) != len(order) {
		t.Fatalf("got %d pages, want %d", len(pages), len(order))
	}
	for _, p := range pages {
		if p.Title == "" {
			t.Errorf("%s: no title", p.Slug)
		}
		if p.Summary == "" {
			t.Errorf("%s: no summary paragraph under the heading", p.Slug)
		}
		if !strings.Contains(p.HTML, "<h1>") {
			t.Errorf("%s: rendered without its heading", p.Slug)
		}
		if len(p.Text) < 200 {
			t.Errorf("%s: only %d characters of searchable text", p.Slug, len(p.Text))
		}
		// A placeholder that survived rendering would be shown to the reader.
		// The summary matters as much as the page: the contents list shows it
		// as text, so markup and placeholders both arrive on screen verbatim.
		for _, leftover := range []string{"{{keys", "[[key:"} {
			if strings.Contains(p.HTML, leftover) {
				t.Errorf("%s: unexpanded %q in the rendered page", p.Slug, leftover)
			}
			if strings.Contains(p.Summary, leftover) {
				t.Errorf("%s: unexpanded %q in the contents summary: %s", p.Slug, leftover, p.Summary)
			}
		}
		if strings.Contains(p.Summary, "<") {
			t.Errorf("%s: markup in the contents summary: %s", p.Slug, p.Summary)
		}
	}
}

// The order list is what the contents are drawn from, so a page added without
// being listed would be invisible, and one listed without a file would fail to
// render at start-up rather than here.
func TestOrderMatchesFiles(t *testing.T) {
	files, err := fileSlugs()
	if err != nil {
		t.Fatalf("fileSlugs: %v", err)
	}
	listed := map[string]bool{}
	for _, s := range order {
		if listed[s] {
			t.Errorf("%s is listed twice in order", s)
		}
		listed[s] = true
	}
	present := map[string]bool{}
	for _, s := range files {
		present[s] = true
		if !listed[s] {
			t.Errorf("pages/%s.md exists but is not listed in order", s)
		}
	}
	for _, s := range order {
		if !present[s] {
			t.Errorf("order lists %s but pages/%s.md does not exist", s, s)
		}
	}
}

func TestKeyTableIsWellFormed(t *testing.T) {
	sections := map[string]bool{}
	for _, s := range Sections {
		sections[s] = true
	}
	seen := map[string]bool{}
	slugs := map[string]bool{}
	for _, s := range Slugs() {
		slugs[s] = true
	}
	for _, k := range Keys {
		if k.ID == "" || k.Label == "" {
			t.Errorf("%+v: id and label are both required", k)
		}
		if seen[k.ID] {
			t.Errorf("%s: duplicate id", k.ID)
		}
		seen[k.ID] = true
		if !sections[k.Section] {
			t.Errorf("%s: section %q is not in Sections", k.ID, k.Section)
		}
		if k.Page != "" && !slugs[k.Page] {
			t.Errorf("%s: page %q does not exist", k.ID, k.Page)
		}
		if k.Keys == "" && k.NoPalette {
			t.Errorf("%s: has no binding and is hidden from the palette, so nothing can reach it", k.ID)
		}
	}
}

// Every action has to appear in the shortcuts page, which is the only page
// that claims to be complete.
func TestShortcutsPageListsEveryAction(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	var text string
	for _, p := range pages {
		if p.Slug == "shortcuts" {
			text = p.Text
		}
	}
	if text == "" {
		t.Fatal("no shortcuts page")
	}
	for _, k := range Keys {
		if !strings.Contains(text, k.Label) {
			t.Errorf("%s (%q) is missing from the shortcuts page", k.ID, k.Label)
		}
		if k.Keys != "" && !strings.Contains(text, k.Keys) {
			t.Errorf("%s: binding %q is missing from the shortcuts page", k.ID, k.Keys)
		}
	}
}

func TestExpandRejectsUnknownPlaceholders(t *testing.T) {
	cases := map[string]string{
		"unknown section": "{{keys:Nonsense}}",
		"unknown action":  "press [[key:nosuchthing]] to win",
		"unterminated":    "{{keys:Panes",
		"keyless action":  "press [[key:restartPane]]",
	}
	for name, src := range cases {
		if _, err := expand(src); err == nil {
			t.Errorf("%s: expand(%q) succeeded, want an error", name, src)
		}
	}
}

func TestExpandInlineKey(t *testing.T) {
	out, err := expand("Press [[key:splitRight]] to split.")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	want := "Press <kbd>Ctrl+Shift+D</kbd> to split."
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestPlainTextStripsMarkup(t *testing.T) {
	got := plainText("<p>Press <kbd>Ctrl+Shift+D</kbd> &amp; wait.</p>\n<p>Done.</p>")
	want := "Press Ctrl+Shift+D & wait. Done."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
