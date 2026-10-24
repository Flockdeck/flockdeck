package help

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
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

// One name per surface: the palette draws a surface under the rail's name
// and searches it by its label, so the name has to be a name the label does
// not already give, and docs/keys.md and the pages, which are written from the
// label, have to keep it.
func TestKeysKeepTheirLabelAndNameTheSurfaces(t *testing.T) {
	want := map[string]string{
		"agents":  "All agents across projects",
		"changes": "Review changes, commit and push",
		"history": "Resume — reopen a past conversation",
		"remote":  "Flockdeck Remote…",
		"apiKeys": "API keys…",
	}
	short := map[string]string{
		"agents": "Agents", "changes": "Changes", "remote": "Remote", "apiKeys": "API keys",
	}
	for id, label := range want {
		k, ok := Lookup(id)
		if !ok {
			t.Fatalf("no action %q", id)
		}
		if k.Label != label {
			t.Errorf("%s: the label is %q, which the docs/keys.md table and the pages are written from, not %q", id, k.Label, label)
		}
		if k.Short != short[id] {
			t.Errorf("%s: its short name is %q, not %q", id, k.Short, short[id])
		}
	}
	for _, k := range Keys {
		if k.Short != "" && k.Short == k.Label {
			t.Errorf("%s: the short name is the label, which is no short name", k.ID)
		}
	}
}

// Someone who remembers the old name types it into the help search, which
// matches every word against a page's title and text. The sentence on the
// Remote page that names the old name is what makes that work.
func TestSearchingTheOldNameFindsTheRemotePage(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if p.Slug != "remote" {
			continue
		}
		hay := strings.ToLower(strings.Join(strings.Fields(p.Title+" "+p.Text), " "))
		if !strings.Contains(hay, "remote access") {
			t.Error("searching the help for \"remote access\" does not find the Remote page")
		}
		return
	}
	t.Fatal("no remote page")
}

// The Settings row for the conflict radar is three sentences. What it used to
// spell out lives on the Worktrees page, and has to stay there.
func TestWorktreesPageHoldsTheConflictRadarDetail(t *testing.T) {
	pages, err := Pages()
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, p := range pages {
		if p.Slug == "worktrees" {
			text = strings.Join(strings.Fields(p.Text), " ")
		}
	}
	if text == "" {
		t.Fatal("no worktrees page")
	}
	for _, want := range []string{
		"15 seconds", "scratch folder", "relay", "up to five panes and twenty files each", "No chip is not a promise",
		"100 MB", "25 MB", "shallow clone", "bare repository", "submodule", "partial clone", "2.44", "2.38",
		"three refreshes", "Git LFS", "merge driver", "ten minutes", "split index", "main worktree's HEAD",
		"Every untracked file is read again", "a thousand changed files", "modification time",
		"never write to a checkout", "whether or not the radar is on", "paired phone", "at most 50 merges",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the worktrees page no longer says %q", want)
		}
	}
}

// A binding written into a page by hand is the drift the placeholders exist to
// prevent: remap the action and the page goes on saying the old key. Every
// binding of the table (a range such as "Alt+1 … Alt+9" excepted) has to come
// into a page through [[key:id]] or [[action:id]] or {{keys}}. Keys the table
// does not own, such as a terminal program's Ctrl+C, are not its business.
//
// Known limits, accepted: a prose pair of angle brackets, &nbsp;, an en dash,
// "Ctrl plus Shift plus D", and a table binding that is another program's too
// ("tmux uses Ctrl+B") are not told apart from a hand-written binding or are
// flagged; reword the page or use the placeholder.
//
// The check reads a paragraph at a time with the markup and the placeholders
// taken out, so a binding split over <kbd> elements, over a line break, in other
// case, with other separators or with its modifiers in another order is found
// as well as the plain spelling.
func TestNoPageWritesATableBindingByHand(t *testing.T) {
	for _, slug := range order {
		src, err := fs.ReadFile(pagesFS, "pages/"+slug+".md")
		if err != nil {
			t.Fatal(err)
		}
		line := 1
		for _, para := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n\n") {
			for _, k := range handWrittenKeys(para) {
				t.Errorf("%s.md:%d writes %q (%s) by hand; use [[key:%s]]:\n\t%s", slug, line, k.Keys, k.Label, k.ID, strings.TrimSpace(strings.SplitN(para, "\n", 2)[0]))
			}
			line += strings.Count(para, "\n") + 2
		}
	}
}

// handWrittenKeys is the table's bindings that a paragraph of a page writes
// itself, in any spelling normalBinding folds together.
func handWrittenKeys(para string) []Key {
	text := normalBinding(stripMarkup(stripPlaceholders(para)))
	var out []Key
	for _, k := range Keys {
		if k.Keys == "" || strings.Contains(k.Keys, "…") {
			continue
		}
		for _, v := range bindingVariants(k.Keys) {
			if containsWord(text, v) {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

var (
	placeholderRE = regexp.MustCompile(`\[\[(?:key|action):[^\[\]]*\]\]|\{\{keys[^{}]*\}\}`)
	tagRE         = regexp.MustCompile(`<[^>]*>`)
	plusRE        = regexp.MustCompile(`\s*\+\s*`)
	dashRE        = regexp.MustCompile(`(\w)\s*-\s*(\w)`)
	modRE         = regexp.MustCompile(`^((?:(?:ctrl|alt|shift|cmd)\+)*)(.+)$`)
)

// stripPlaceholders removes the [[key:...]] and [[action:...]] placeholders, and
// the {{keys...}} ones. Only a closed placeholder of those kinds goes, so a stray
// "[[" or "{{" hides nothing after it.
func stripPlaceholders(text string) string {
	return placeholderRE.ReplaceAllString(text, "")
}

// stripMarkup drops HTML tags, the backticks of code spans and the * and _ of
// emphasis, so <kbd>Ctrl</kbd>+<kbd>D</kbd>, `Ctrl+D` and **Ctrl**+**D** read as
// Ctrl+D.
func stripMarkup(text string) string {
	return strings.NewReplacer("`", "", "*", "", "_", "").Replace(tagRE.ReplaceAllString(text, ""))
}

// normalBinding lower-cases text, makes every run of white space one space, and
// writes a separator as a plain "+" without spaces around it, with Control, Cmd,
// Command and Meta as ctrl. A "-" is a separator only between two word
// characters, so the key "-" of Ctrl+- stays a key and keeps the space after it.
func normalBinding(text string) string {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	text = plusRE.ReplaceAllString(text, "+")
	for {
		next := dashRE.ReplaceAllString(text, "$1+$2")
		if next == text {
			break
		}
		text = next
	}
	for _, from := range []string{"control", "command", "meta", "cmd"} {
		text = strings.ReplaceAll(text, from, "ctrl")
	}
	return text
}

// bindingVariants is a binding in normal form with its modifiers in every order.
func bindingVariants(binding string) []string {
	lower := strings.ToLower(binding)
	m := modRE.FindStringSubmatch(lower)
	if m == nil || m[1] == "" {
		return []string{normalBinding(lower)}
	}
	mods := strings.Split(strings.TrimSuffix(m[1], "+"), "+")
	var out []string
	var walk func(rest, picked []string)
	walk = func(rest, picked []string) {
		if len(rest) == 0 {
			out = append(out, normalBinding(strings.Join(picked, "+")+"+"+m[2]))
			return
		}
		for i := range rest {
			next := append(append([]string{}, rest[:i]...), rest[i+1:]...)
			walk(next, append(append([]string{}, picked...), rest[i]))
		}
	}
	walk(mods, nil)
	return out
}

// containsWord says v is in text with no letter or digit against either end, so
// a page that names F12 does not count as naming F1.
func containsWord(text, v string) bool {
	alnum := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for from := 0; ; {
		i := strings.Index(text[from:], v)
		if i < 0 {
			return false
		}
		i += from
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		after, _ := utf8.DecodeRuneInString(text[i+len(v):])
		if (i == 0 || !alnum(before)) && (i+len(v) == len(text) || !alnum(after)) {
			return true
		}
		from = i + 1
	}
}

func TestStripPlaceholders(t *testing.T) {
	got := stripPlaceholders("press [[key:palette]] or {{keys:Panes}} not Ctrl+Shift+K [[action:help]]")
	if got != "press  or  not Ctrl+Shift+K " {
		t.Errorf("got %q", got)
	}
	// A stray opener does not swallow what follows it.
	if got := stripPlaceholders("a [[ b Ctrl+Shift+D c [[key:help]] d"); got != "a [[ b Ctrl+Shift+D c  d" {
		t.Errorf("got %q", got)
	}
}

// The ways a page can write Ctrl+Shift+D (and F1) that the check has to find, and
// the ones that are not a binding.
func TestHandWrittenKeysSpellings(t *testing.T) {
	found := []string{
		"Press Ctrl+Shift+D here",
		"Press `Ctrl+Shift+D` here",
		"Press <kbd>Ctrl+Shift+D</kbd> here",
		"Press <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>D</kbd> here",
		"Press <kbd>Ctrl</kbd> + <kbd>Shift</kbd> + <kbd>D</kbd> here",
		"Press ctrl+shift+d here",
		"Press CTRL+SHIFT+D here",
		"Press Ctrl-Shift-D here",
		"Press Ctrl + Shift + D here",
		"Press Control+Shift+D here",
		"Press Shift+Ctrl+D here",
		"Press Cmd+Shift+D here",
		"Press <kbd>Ctrl+Shift+d</kbd> here",
		"Press Ctrl+\nShift+D here",
		"Press Ctrl+Shift+\nD here",
		"Press Ctrl+Shift+D. Then more.",
		"Press **Ctrl**+**Shift**+**D** here",
		"Press _Ctrl_+_Shift_+_D_ here",
		"a [[ stray Ctrl+Shift+D",
		"[[key:help]] then Ctrl+Shift+D",
	}
	for _, p := range found {
		if got := handWrittenKeys(p); len(got) != 1 || got[0].Keys != "Ctrl+Shift+D" {
			t.Errorf("not found in %q: %v", p, got)
		}
	}
	for _, p := range []string{"Press Ctrl+- to shrink.", "Press Ctrl + - to shrink.", "Press **Ctrl**+**-** to shrink."} {
		if got := handWrittenKeys(p); len(got) != 1 || got[0].Keys != "Ctrl+-" {
			t.Errorf("Ctrl+- not found in %q: %v", p, got)
		}
	}
	for _, p := range []string{"Press F1 here", "Press f1 here", "Press <kbd>F1</kbd> here"} {
		if got := handWrittenKeys(p); len(got) != 1 || got[0].Keys != "F1" {
			t.Errorf("F1 not found in %q: %v", p, got)
		}
	}
	for _, p := range []string{
		"Press [[key:broadcast]] here",
		"F12 and F10 are the browser's",
		"Ctrl+Shift+Dx is nothing",
		"Ctrl+C copies",
		"A control like a dial, with a shift of a day",
		"{{keys:Panes}}",
	} {
		if got := handWrittenKeys(p); len(got) != 0 {
			t.Errorf("found %v in %q", got, p)
		}
	}
}
