package help

import (
	"encoding/json"
	"io/fs"
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
		for _, leftover := range []string{"{{keys", "[[key:", "[[action:"} {
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

// Punctuation right after a closing tag belongs to the word before it.
func TestPlainTextKeepsPunctuationAttached(t *testing.T) {
	got := plainText("<p>Press <kbd>F1</kbd>. Then <em>wait</em>, please.</p>")
	want := "Press F1. Then wait, please."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Key.Page says which page explains an action, and the interface sends the
// reader there. A page that never names the action is a promise the reader
// follows and finds nothing behind — and it is how an action comes to be
// undocumented while still looking documented.
//
// The reference has to be a placeholder rather than the action's name written
// out, because a name written out is exactly what goes stale when the key
// table is edited.
func TestEveryActionIsNamedOnItsOwnPage(t *testing.T) {
	for _, k := range Keys {
		if k.Page == "" {
			continue
		}
		src, err := fs.ReadFile(pagesFS, "pages/"+k.Page+".md")
		if err != nil {
			t.Errorf("%s: page %q: %v", k.ID, k.Page, err)
			continue
		}
		if strings.Contains(string(src), "[[key:"+k.ID+"]]") ||
			strings.Contains(string(src), "[[action:"+k.ID+"]]") {
			continue
		}
		t.Errorf("%s (%q) says it is explained on the %s page, which never refers to it; "+
			"add [[action:%s]] there, or clear its Page", k.ID, k.Label, k.Page, k.ID)
	}
}

// Grouping several directories into one project shipped with no help page at
// all -- discoverable only by finding the buttons. It belongs on the Projects
// page, since that is the page the dialog's own "?" opens, and it has to say
// a member does not need a git repository, which is the one thing about it
// that is not obvious from the button labels alone.
func TestProjectsPageDocumentsGrouping(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	var projects *Page
	for i := range pages {
		if pages[i].Slug == "projects" {
			projects = &pages[i]
		}
	}
	if projects == nil {
		t.Fatal("no projects page")
	}
	for _, want := range []string{"Group open projects", "Add directory", "does not need a git repository"} {
		if !strings.Contains(projects.Text, want) {
			t.Errorf("the projects page does not mention %q", want)
		}
	}
}

// The help is shown inside the application window, so a link out of it has to
// open somewhere else rather than replace the interface.
func TestExternalLinksOpenElsewhere(t *testing.T) {
	for _, src := range []string{
		"See [Claude Code](https://claude.com/claude-code).",
		"See <https://claude.com/claude-code>.",
	} {
		got, err := toHTML(src)
		if err != nil {
			t.Fatal(err)
		}
		want := `<a href="https://claude.com/claude-code" target="_blank" rel="noopener noreferrer">`
		if !strings.Contains(got, want) {
			t.Errorf("%s: got %s, want it to contain %s", src, got, want)
		}
	}
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		for _, l := range strings.Split(p.HTML, `<a href="http`)[1:] {
			if tag, _, _ := strings.Cut(l, ">"); !strings.Contains(tag, `target="_blank"`) {
				t.Errorf("%s: a link out of the help opens in the application window: <a href=\"http%s>", p.Slug, tag)
			}
		}
	}
}

// Pages refer to each other as [Title](#slug), which the help viewer opens as
// the page of that slug. A slug that names no page is a link that goes nowhere,
// so it fails here rather than in front of a reader.
func TestCrossReferencesNamePages(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, s := range order {
		known[s] = true
	}
	links := 0
	for _, p := range pages {
		for _, l := range strings.Split(p.HTML, `<a href="#`)[1:] {
			slug, _, _ := strings.Cut(l, `"`)
			links++
			if !known[slug] {
				t.Errorf("%s: links to #%s, which is not a help page", p.Slug, slug)
			}
		}
	}
	if links == 0 {
		t.Error("no page links to another; the cross-references have gone")
	}
}

// A JSON example in a page is there to be copied into a real file, so one that
// does not parse hands the reader a settings file the application rejects. A
// Windows path written with single backslashes is the easy way to get there.
func TestJSONExamplesParse(t *testing.T) {
	for _, slug := range order {
		src, err := fs.ReadFile(pagesFS, "pages/"+slug+".md")
		if err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		rest := strings.ReplaceAll(string(src), "\r\n", "\n")
		for {
			i := strings.Index(rest, "```json\n")
			if i < 0 {
				break
			}
			rest = rest[i+len("```json\n"):]
			end := strings.Index(rest, "```")
			if end < 0 {
				t.Fatalf("%s: unterminated json block", slug)
			}
			if !json.Valid([]byte(rest[:end])) {
				t.Errorf("%s: a json example does not parse:\n%s", slug, rest[:end])
			}
			rest = rest[end+3:]
		}
	}
}

// An action with a binding is reached by its keys; one without is reached by
// its name, and the name is the part of the label a sentence would use.
func TestExpandAction(t *testing.T) {
	cases := map[string]string{
		"[[action:splitRight]]":      "<kbd>Ctrl+Shift+D</kbd>",
		"[[action:splitRightShell]]": "**Split right (shell)**",
		// The palette needs the gloss after the dash; a page does not.
		"[[action:detach]]": "**Detach**",
	}
	for src, want := range cases {
		got, err := expand(src)
		if err != nil {
			t.Errorf("expand(%s): %v", src, err)
			continue
		}
		if got != want {
			t.Errorf("expand(%s) = %q, want %q", src, got, want)
		}
	}
	if _, err := expand("[[action:nosuchthing]]"); err == nil {
		t.Error("expand accepted an action that does not exist")
	}
}
