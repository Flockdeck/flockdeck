package webui

import (
	"strings"
	"testing"
)

// The command palette of the pop-over redesign (part C1): group headings, the
// short names, the footer, Go to as a mode of the palette, and what it says
// when nothing matches. The harness has no CSS, so what is pinned here is
// what the script decides; the style sheet is read as text.

const palRows = `
const palList = () => h.$("palette-list");
const palRowsNow = () => palList().children.filter((r) => r.classList.contains("pal-row"));
const palHeads = () => palList().children.filter((r) => r.classList.contains("pal-grp")).map((g) => g.textContent);
const palLabels = () => palRowsNow().map((r) => r.querySelector(".pal-label").textContent);
const palType = (text) => { const i = h.$("palette-input"); i.value = text; i.oninput(); };
`

// Nothing typed, the palette is listed under headings: what was run last,
// then the key table's own groups with every surface lifted into Go to. A
// query lists what it finds with none, so a ranking is not cut in two.
func TestThePaletteListsItsCommandsUnderHeadings(t *testing.T) {
	runFrontEnd(t, palRows+paletteRun+`
h.hello();
h.recv(fixture());
h.press("palette");
const heads = palHeads();
assert.deepStrictEqual(heads.slice(0, 1), ["Panes"], "the first group is not Panes: " + heads.join(", "));
for (const want of ["Panes", "Tabs", "Agents", "Go to", "The window", "Settings"]) {
  assert.ok(heads.includes(want), "no " + want + " heading: " + heads.join(", "));
}
const order = ["Recent", "Panes", "Tabs", "Agents", "Go to", "Finding your way", "The window", "Settings", "Projects"];
assert.deepStrictEqual(heads, order.filter((g) => heads.includes(g)), "the groups are not in order: " + heads.join(", "));
assert.ok(!heads.includes("Git"), "the surfaces were left in the key table's Git group");

// A heading is followed by the rows it heads, and the surfaces are in Go to.
const under = (name) => {
  const kids = palList().children;
  const at = kids.findIndex((k) => k.classList.contains("pal-grp") && k.textContent === name);
  const out = [];
  for (let i = at + 1; i < kids.length && kids[i].classList.contains("pal-row"); i++) {
    out.push(kids[i].querySelector(".pal-label").textContent);
  }
  return out;
};
const goto = under("Go to");
for (const name of ["Agents", "Changes", "Worktrees", "History", "Projects", "Settings", "Help", "Remote", "Go to…"]) {
  assert.ok(goto.includes(name), name + " is not under Go to: " + goto.join(", "));
}
assert.ok(!under("Panes").includes("Settings"), "a surface is under Panes");

// What was run last heads the list, and is not listed a second time.
paletteRun("tile");
h.press("palette");
assert.strictEqual(palHeads()[0], "Recent");
assert.deepStrictEqual(under("Recent"), ["Tile these panes evenly"]);
assert.strictEqual(palLabels().filter((l) => l === "Tile these panes evenly").length, 1, "a recent command is listed twice");
h.key({ key: "Escape" });

// Typed, there are no headings at all.
h.press("palette");
palType("split");
assert.deepStrictEqual(palHeads(), [], "a search is listed under headings");
assert.ok(palRowsNow().length >= 2);
`)
}

// Headings are not things to choose: the arrow keys and Page keys count rows.
func TestTheHighlightSkipsTheHeadings(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
const sel = () => palRowsNow().findIndex((r) => r.classList.contains("sel"));
assert.strictEqual(sel(), 0);
const total = palRowsNow().length;
for (let i = 0; i < total + 5; i++) h.key({ key: "ArrowDown" });
assert.strictEqual(sel(), total - 1, "the highlight did not stop at the last row");
assert.ok(palList().children.filter((k) => k.classList.contains("pal-grp")).every((k) => !k.classList.contains("sel")), "a heading was highlighted");
h.key({ key: "Home" });
assert.strictEqual(sel(), 0);
assert.ok(palRowsNow()[0].classList.contains("sel"));
// The first row of a group is brought into view with the heading above it.
const firsts = palList().children.map((k, i) => [k, i]).filter(([k, i]) => i > 0 && k.classList.contains("pal-row") && palList().children[i - 1].classList.contains("pal-grp"));
const [row, at] = firsts[0];
h.dispatch(row, new h.Ev("mousemove", {}));
assert.ok(row.scrolledTo > 0, "the row was not scrolled to");
assert.ok(palList().children[at - 1].scrolledTo > 0, "its heading was left out of sight");
`)
}

// One name per surface: the row is drawn with the short name, the long label
// stays what is searched, and is the row's description.
func TestThePaletteNamesEachSurfaceByItsRailName(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
const labels = palLabels();
for (const short of ["Agents", "Changes", "History", "Worktrees", "Remote", "API keys", "Fan out", "Todos"]) {
  assert.ok(labels.includes(short), short + " is not a name in the palette: " + labels.join(", "));
}
for (const long of ["All agents across projects", "Review changes, commit and push", "Resume a past conversation", "Flockdeck Remote…", "API keys…"]) {
  assert.ok(!labels.includes(long), long + " is still what the row is called");
}
const history = palRowsNow().find((r) => r.querySelector(".pal-label").textContent === "History");
assert.strictEqual(history.getAttribute("aria-description"), "Resume a past conversation");

// And it is still found by what it used to be called.
for (const [typed, name] of [["resume a past", "History"], ["all agents across", "Agents"], ["review changes", "Changes"],
  ["remote access", "Remote"], ["past jobs", "Fan-out history"]]) {
  palType(typed);
  assert.ok(palLabels().includes(name), typed + " does not find " + name + ": " + palLabels().join(", "));
}
palType("resume a past");
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "conversations" });
`)
}

// Where a row has no key, what the short name leaves out is its hint: the
// reason a command is what it is.
func TestARowWithNoKeyShowsWhatItsNameLeavesOut(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
palType("close finished");
const row = palRowsNow()[0];
assert.strictEqual(row.querySelector(".pal-label").textContent, "Close finished panes");
assert.ok(/idle agents and cleanly exited panes/.test(row.querySelector(".pal-hint").textContent), "the row does not say what it closes");
// A bound command shows its keys as caps, and still reads as Ctrl+Shift+G.
palType("worktrees");
const wt = palRowsNow()[0];
const caps = wt.querySelectorAll("kbd").map((k) => k.textContent);
assert.deepStrictEqual(caps, ["Ctrl", "Shift", "G"]);
assert.strictEqual(wt.querySelector(".pal-hint").textContent, "Ctrl+Shift+G");
`)
}

// The footer says how many commands there are, and how many a search has left.
func TestTheFooterCountsTheCommands(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
const count = () => h.$("palette-count").textContent;
h.press("palette");
assert.ok(!h.$("palette-foot").hidden, "the palette has no footer");
const all = /^(\d+) commands$/.exec(count());
assert.ok(all && Number(all[1]) > 20, "the footer does not count the commands: " + count());
assert.strictEqual(Number(all[1]), palRowsNow().length, "the count is not what is listed");
palType("split");
const some = /^(\d+) of (\d+) commands$/.exec(count());
assert.ok(some, "a search is not counted out of the whole: " + count());
assert.strictEqual(Number(some[1]), palRowsNow().length);
assert.strictEqual(some[2], all[1]);
palType("zzzzzz");
assert.strictEqual(count(), "0 of " + all[1] + " commands");
assert.strictEqual(h.$("palette-count").getAttribute("role"), "status", "a change of the count is not said");
`)
}

// Nothing matching says what is missing and what to try, in one element the
// list's only child.
func TestNoMatchSaysWhatToTry(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
palType("zzzzzz nothing");
assert.strictEqual(palList().children.length, 1, "the empty note is not all that is left");
const note = palList().children[0];
assert.ok(note.classList.contains("pal-empty"));
assert.strictEqual(note.querySelector("b").textContent, "No command matches “zzzzzz nothing”");
assert.ok(/first letter of each word/.test(note.textContent), "the note suggests nothing: " + note.textContent);
// In Go to it says so of pages, and how to get the commands back.
h.key({ key: "Escape" });
h.press("palette");
palType(">");
palType("zzzz");
assert.strictEqual(palList().children[0].querySelector("b").textContent, "No page matches “zzzz”");
assert.ok(/Backspace/.test(palList().children[0].textContent));
`)
}

// Among the commands that hold every word typed, the one whose name opens
// with it comes first: "settings" used to find the window's settings after
// every command that merely mentions them.
func TestANameThatOpensWithWhatWasTypedComesFirst(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
palType("settings");
assert.strictEqual(palLabels()[0], "Settings", "got: " + palLabels().slice(0, 4).join(", "));
assert.ok(palLabels().length > 1, "the other settings commands are no longer offered");
palType("api");
assert.strictEqual(palLabels()[0], "API keys");
palType("history");
assert.strictEqual(palLabels()[0], "History", "a search for a surface's name does not lead with the surface: " + palLabels().join(", "));
`)
}

// Go to is the palette in a mode: a tag in the field, the surfaces by their
// rail names with their kind and keys, in the rail's order, no footer.
func TestGoToIsAModeOfThePalette(t *testing.T) {
	runFrontEnd(t, palRows+paletteRun+`
h.hello();
h.recv(fixture());
h.press("palette");
assert.ok(h.$("palette-tag").hidden, "the Go to tag is showing in the palette proper");
paletteRun("go to");
assert.ok(!h.$("palette").hidden);
assert.ok(!h.$("palette-tag").hidden, "Go to does not mark itself in the field");
assert.strictEqual(h.$("palette-tag").textContent, "Go to");
assert.strictEqual(h.$("palette-input").placeholder, "Go to…");
assert.ok(h.$("palette-foot").hidden, "Go to lists a handful of windows and needs no count");
assert.deepStrictEqual(palHeads(), [], "Go to is listed under headings");
const labels = palLabels();
assert.deepStrictEqual(labels.slice(0, 10),
  ["Agents", "Changes", "Worktrees", "History", "Todos", "GitHub", "Remote", "Projects", "Settings", "Help"]);
for (const extra of ["Fan out", "Fan-out history", "API keys"]) assert.ok(labels.includes(extra), extra + " left Go to");
const kind = (name) => palRowsNow().find((r) => r.querySelector(".pal-label").textContent === name).querySelector(".pal-kind").textContent;
assert.strictEqual(kind("Agents"), "sheet");
assert.strictEqual(kind("Remote"), "sheet");
assert.strictEqual(kind("Settings"), "dialog");
assert.strictEqual(kind("Help"), "dialog");
assert.strictEqual(kind("Fan out"), "dialog");
const keys = (name) => palRowsNow().find((r) => r.querySelector(".pal-label").textContent === name).querySelector(".pal-hint").textContent;
assert.strictEqual(keys("Agents"), "Ctrl+Shift+A");
assert.strictEqual(keys("Settings"), "Ctrl+,");
assert.strictEqual(keys("Help"), "F1");
// Only the pages are offered, and typing narrows them.
assert.ok(!labels.some((l) => l.startsWith("Split")));
palType("set");
assert.strictEqual(palLabels()[0], "Settings");
`)
}

// The tag comes off the way a tag does in a field: Backspace in the empty
// field. ">" first asks for Go to without the entry.
func TestGoToIsEnteredAndLeftFromTheField(t *testing.T) {
	runFrontEnd(t, palRows+`
h.hello();
h.recv(fixture());
h.press("palette");
palType(">");
assert.ok(!h.$("palette-tag").hidden, "> did not enter Go to");
assert.strictEqual(h.$("palette-input").value, "", "the > was left in the field");
assert.ok(palLabels().includes("Agents") && !palLabels().includes("Zoom pane"));

// Backspace with text in the field is the caret's.
palType("set");
const edit = h.key({ key: "Backspace" });
assert.ok(!h.$("palette-tag").hidden, "Backspace left Go to with text in the field");
assert.ok(!edit.defaultPrevented, "Backspace was taken from a field with text in it");
palType("");
const back = h.key({ key: "Backspace" });
assert.ok(back.defaultPrevented);
assert.ok(h.$("palette-tag").hidden, "Backspace in the empty field did not leave Go to");
assert.strictEqual(h.$("palette-input").placeholder, "Type a command…");
assert.ok(palLabels().includes("Zoom pane"), "the commands did not come back");
assert.ok(!h.$("palette-foot").hidden);
// Backspace in the palette proper is the field's own.
assert.ok(!h.key({ key: "Backspace" }).defaultPrevented);
// It is opened fresh each time: Go to does not outlive the palette.
h.key({ key: "Escape" });
h.press("palette");
assert.ok(h.$("palette-tag").hidden);
`)
}

// Choosing a page in Go to opens it.
func TestChoosingAPageInGoToOpensIt(t *testing.T) {
	runFrontEnd(t, palRows+paletteRun+`
h.hello();
h.recv(fixture());
for (const [name, title] of [["Agents", "Agents"], ["Settings", "Settings"], ["Help", "Help"]]) {
  paletteRun("go to");
  palType(name);
  assert.strictEqual(palLabels()[0], name);
  h.key({ key: "Enter" });
  assert.ok(h.$("palette").hidden);
  assert.strictEqual(h.$("overlay-title").textContent, title);
  h.key({ key: "Escape" });
}
`)
}

// The style sheet: the headings, the footer and the tag keep to the tokens,
// the highlight's shape is unchanged, and no heading is set in the faint text.
func TestThePaletteStyles(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for _, want := range []string{"#palette-head {", "#palette-tag {", ".pal-grp {", "#palette-foot {", ".pal-legend {", ".pal-kind {", ".pal-empty b {"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css has no %q", want)
		}
	}
	grp := ruleBody(css, ".pal-grp")
	if !strings.Contains(grp, "var(--fg-dim)") || strings.Contains(grp, "--fg-faint") {
		t.Errorf("a heading is not in the dim text colour: %q", grp)
	}
	if row := ruleBody(css, ".pal-row"); !strings.Contains(row, "height: 32px") {
		t.Errorf("a palette row is not 32px: %q", row)
	}
	if head := ruleBody(css, "#palette-head"); !strings.Contains(head, "height: 48px") {
		t.Errorf("the palette's head is not 48px: %q", head)
	}
	if sel := ruleBody(css, ".pal-row.sel"); !strings.Contains(sel, "var(--accent-subtle)") || !strings.Contains(sel, "inset 2px 0 0 var(--accent)") {
		t.Errorf("the chosen row lost its tint and bar: %q", sel)
	}
	if !strings.Contains(ruleBody(css, "#palette-list"), "overscroll-behavior: contain") {
		t.Error("the list lets a scroll past its end carry on")
	}
}
