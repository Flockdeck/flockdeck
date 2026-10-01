package webui

import (
	"strings"
	"testing"
)

// The Worktrees sheet of the pop-over redesign (part B1): a row is a name and
// one action with a ⋯ menu for the rest, the new-worktree form is the
// footer, and the empty and error states say what to do next. The harness
// has no CSS, so what is pinned is what the script draws and where the
// keyboard is.

const worktreeFixture = `
const tree = (items, extra) => Object.assign({ type: "worktrees", root: "C:/code/shopfront", defaultBase: "main",
  branches: [{ name: "main", checkedIn: true }], items }, extra || {});
const MAIN = { label: "main", path: "C:/code/shopfront", main: true, head: "cabde95", panes: 2 };
const FEAT = { label: "feat/pricing", path: "C:/code/shopfront-pricing", dirty: 0, untracked: 1, head: "cabde95", panes: 1 };
const SPARE = { label: "spare", path: "C:/code/shopfront-spare", dirty: 2, untracked: 0, head: "cabde95" };
const GONE = { label: "fix/flaky", path: "C:/code/shopfront-flaky", prunable: true, dirty: 0, untracked: 0 };
const rows = () => h.$("overlay-body").querySelectorAll("div.wt-row");
const menuOf = () => h.$("overlay-panel").querySelector("div.menu");
const openMenu = (row) => { h.click(row.querySelector("button.wt-more")); return menuOf(); };
`

// Each row has one action, Open agent, and a ⋯ that opens a menu with the
// rest; the menu holds the keyboard, and Escape puts away the menu and
// nothing else.
func TestAWorktreeRowHasOneActionAndAMenuForTheRest(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, FEAT, SPARE]));
for (const row of rows()) {
  assert.deepStrictEqual(row.querySelector("div.wt-actions").children.map((b) => b.textContent), ["Open agent", "\u22ef"]);
}
const more = rows()[1].querySelector("button.wt-more");
assert.strictEqual(more.getAttribute("aria-haspopup"), "menu");
assert.strictEqual(more.getAttribute("aria-expanded"), "false");

const menu = openMenu(rows()[1]);
assert.strictEqual(menu.getAttribute("role"), "menu");
assert.strictEqual(more.getAttribute("aria-expanded"), "true");
const items = menu.querySelectorAll("button");
assert.ok(items.every((b) => b.getAttribute("role") === "menuitem"));
assert.deepStrictEqual(items.map((b) => b.textContent.replace("\u21b5", "")),
  ["Open agent", "Open shell", "Split beside this pane", "Review changes", "Remove worktree\u2026"]);
assert.ok(h.doc.activeElement === items[0], "the menu did not take the keyboard on its first item");

// Arrows walk and wrap; Home and End; a letter goes to the next item with it.
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === items[1]);
h.key({ key: "ArrowUp" });
h.key({ key: "ArrowUp" });
assert.ok(h.doc.activeElement === items[4], "ArrowUp from the first did not wrap");
h.key({ key: "Home" });
assert.ok(h.doc.activeElement === items[0]);
h.key({ key: "End" });
assert.ok(h.doc.activeElement === items[4]);
h.key({ key: "s" });
assert.ok(h.doc.activeElement === items[2], "a letter did not go to the item that starts with it");
h.key({ key: "r" });
assert.ok(h.doc.activeElement === items[3], "the next letter did not go on from there");

// Escape puts away the menu, the sheet stays, and the keyboard is back on the button.
h.key({ key: "Escape" });
assert.ok(!menuOf(), "Escape left the menu up");
assert.ok(!h.$("overlay").hidden, "Escape closed the sheet with the menu");
assert.ok(h.doc.activeElement === more, "the keyboard did not go back to the ⋯");
assert.strictEqual(more.getAttribute("aria-expanded"), "false");
// And the next Escape is the sheet's.
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden);

// A pick runs the item: a shell in that worktree.
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, FEAT]));
openMenu(rows()[1]);
h.key({ key: "ArrowDown" });
h.click(menuOf().querySelectorAll("button")[1]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "newTab", kind: "shell", path: "C:/code/shopfront-pricing", text: "feat/pricing" });
assert.ok(h.$("overlay").hidden, "opening a shell left the sheet up");
assert.ok(!menuOf(), "the menu outlived the sheet");
`)
}

// The main worktree cannot be removed, so its menu has no Remove; a row whose
// folder is gone has only Prune; and Prune gone is offered only when something is.
func TestWhatARowOffersFollowsWhatItIs(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, FEAT]));
const names = () => h.$("overlay-body").querySelectorAll("button").map((b) => b.textContent);
assert.ok(!names().includes("Prune gone"), "Prune gone offered with nothing to prune");
assert.ok(!menuOf());
const m = openMenu(rows()[0]);
assert.ok(!m.querySelectorAll("button").some((b) => /Remove/.test(b.textContent)), "the main worktree can be removed");
h.key({ key: "Escape" });

h.recv(tree([MAIN, GONE]));
assert.ok(names().includes("Prune gone"), "no Prune gone with a folder gone");
assert.deepStrictEqual(rows()[1].querySelectorAll("button").map((b) => b.textContent), ["Prune"]);
h.click(h.$("wt-prune-all"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreePrune" });
`)
}

// The row on show follows the keyboard and the pointer, and up and down walk
// the rows; a right-click opens the same menu.
func TestTheRowOnShowFollowsTheKeyboard(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, FEAT, SPARE]));
await h.sleep(5);
assert.ok(h.doc.activeElement === rows()[0].querySelector("button"), "the keyboard did not start on the first row");
// A browser says so when the keyboard arrives in a row; the harness does not.
const arrived = () => h.dispatch(h.doc.activeElement, new h.Ev("focusin", { target: h.doc.activeElement }));
arrived();
const on = () => rows().map((r, i) => r.classList.contains("sel") ? i : -1).filter((i) => i >= 0);
assert.deepStrictEqual(on(), [0], "the row the keyboard is in is not the one on show");
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows()[1].querySelector("button"), "ArrowDown did not move to the next row");
assert.deepStrictEqual(on(), [1]);
h.key({ key: "ArrowDown" });
h.key({ key: "ArrowDown" });
assert.ok(h.doc.activeElement === rows()[2].querySelector("button"), "ArrowDown went past the last row");
h.key({ key: "ArrowUp" });
assert.deepStrictEqual(on(), [1]);

// Left and right still walk a row's own buttons.
h.key({ key: "ArrowRight" });
assert.ok(h.doc.activeElement === rows()[1].querySelector("button.wt-more"));

// A click on a row puts it on show, and a redraw keeps it so.
h.click(rows()[2]);
assert.deepStrictEqual(on(), [2]);
h.recv(tree([MAIN, FEAT, SPARE]));
assert.deepStrictEqual(on(), [2], "a redraw forgot the row on show");

// Right-click: the same menu.
h.dispatch(rows()[0], new h.Ev("contextmenu", { target: rows()[0] }));
assert.ok(menuOf(), "a right-click on a row opened no menu");
assert.strictEqual(menuOf().querySelectorAll("button")[0].textContent.replace("\u21b5", ""), "Open agent");
`)
}

// The new-worktree form is the footer: the last child of the body, with the
// one primary after the fields, and the folder it is about under it.
func TestTheNewWorktreeFormIsTheFooter(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, FEAT]));
const body = h.$("overlay-body");
const foot = body.children[body.children.length - 1];
assert.ok(foot.classList.contains("ov-foot"), "the form is not the footer");
assert.ok(foot.contains(h.$("wt-branch")) && foot.contains(h.$("wt-base")) && foot.contains(h.$("wt-create")));
assert.ok(foot.querySelector(".wt-root"), "the footer does not say which folder it is about");
assert.strictEqual(h.$("wt-create").classList.contains("primary"), true);
assert.strictEqual(foot.querySelectorAll("button.primary").length, 1, "the footer has more than one primary");
// The one filled button in the rows is on the row on show alone.
assert.ok(!foot.textContent.includes("Refresh"), "the footer carries the list's own buttons");
assert.strictEqual(h.$("overlay-scope").textContent, "shopfront", "the head does not say which project");

// With nothing in the list, the keyboard starts in the branch field.
h.key({ key: "Escape" });
h.click(h.$("btn-worktrees"));
h.recv(tree([]));
await h.sleep(5);
assert.ok(/No worktrees yet/.test(body.textContent), "the empty list has no words: " + body.textContent);
assert.ok(h.doc.activeElement === h.$("wt-branch"), "an empty list did not hand the keyboard to the branch field");
`)
}

// A folder that is not a repository says so, and what to do.
func TestTheWorktreesErrorSaysWhatToDoNext(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/src", error: "C:/src is not a git repository",
  repos: [{ name: "alpha", path: "C:/src/alpha" }] });
const empty = h.$("overlay-body").querySelector("div.wt-empty");
assert.ok(empty, "an error has no state of its own");
assert.strictEqual(empty.querySelector("strong").textContent, "C:/src is not a git repository");
assert.ok(/Pick the repository/.test(empty.textContent), "the error does not say what to do: " + empty.textContent);
h.recv({ type: "worktrees", root: "C:/src", error: "C:/src is not a git repository" });
assert.ok(/git repository to see/.test(h.$("overlay-body").querySelector("div.wt-empty").textContent));
`)
}

// Removing a worktree with uncommitted work asks in the window, stacked on
// the sheet; Cancel and Escape send nothing and come back to the sheet with
// what was typed still there; the action names what it does.
func TestRemovingUncommittedWorkAsksInTheWindowAndComesBack(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.win.confirm = undefined;
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, SPARE]));
h.$("wt-branch").value = "half-typed";
h.$("wt-branch").oninput();
h.click(openMenu(rows()[1]).querySelectorAll("button").find((b) => /^Remove/.test(b.textContent)));
assert.strictEqual(h.$("overlay-title").textContent, "Remove spare?");
assert.strictEqual(h.$("overlay-panel").getAttribute("role"), "alertdialog");
assert.ok(h.doc.activeElement === h.$("ask-cancel"), "a destructive question put the keyboard on its action");
assert.strictEqual(h.$("ask-ok").textContent, "Remove and discard");
const sent = h.commands().length;
h.key({ key: "Escape" });
assert.strictEqual(h.$("overlay-title").textContent, "Worktrees", "Escape did not come back to the sheet");
assert.ok(!h.commands().slice(sent).some((c) => c.cmd === "worktreeRemove"), "a refused question removed it");
h.recv(tree([MAIN, SPARE]));
assert.strictEqual(h.$("wt-branch").value, "half-typed", "coming back threw away what was typed");

h.click(openMenu(rows()[1]).querySelectorAll("button").find((b) => /^Remove/.test(b.textContent)));
h.click(h.$("ask-ok"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktreeRemove", path: "C:/code/shopfront-spare", force: true });
`)
}

// A menu does not outlive what it was opened on: a redraw, and another
// surface, both put it away.
func TestAMenuGoesWithItsRow(t *testing.T) {
	runFrontEnd(t, worktreeFixture+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.recv(tree([MAIN, FEAT]));
openMenu(rows()[1]);
h.recv(tree([MAIN, FEAT]));
assert.ok(!menuOf(), "a redraw left a menu open on a row that is gone");
openMenu(rows()[1]);
h.click(h.$("btn-agents"));
assert.ok(!menuOf(), "a menu stayed over another surface");
`)
}

// The style sheet: the sheet's rows and footer are scoped, the menu has its
// own rules, and the high-contrast block still comes last and marks the row on show.
func TestTheWorktreesSheetIsStyled(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for _, want := range []string{".wt-list .wt-row", ".wt-foot", ".wt-more", ".menu {", ".menu-item", ".wt-empty", ".wt-sec-head"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css has no rule for %s", want)
		}
	}
	// The API keys, Remote and GitHub rows share .wt-row, .wt-flag and
	// .wt-actions: the sheet's own look must not reach them.
	for _, bare := range []string{".wt-row.sel", ".wt-row:focus-within", ".wt-row.gone"} {
		for _, at := range allIndexes(css, bare) {
			if at < 6 || !strings.HasSuffix(css[:at], ".wt-list ") {
				t.Errorf("%s is styled outside .wt-list: %q", bare, css[max(0, at-20):at+len(bare)])
			}
		}
	}
	last := strings.LastIndex(css, "@media (forced-colors: active)")
	if strings.LastIndex(css, ".menu {") > last {
		t.Errorf("the menu's rule comes after the high-contrast block")
	}
	if !strings.Contains(css[last:], ".wt-list .wt-row.sel") {
		t.Errorf("the high-contrast block does not mark the worktree on show")
	}
	if !strings.Contains(css, "forced-colors") || !strings.Contains(ruleBody(css, ".menu"), "box-shadow") {
		t.Errorf("the menu has no edge")
	}
}

func allIndexes(s, sub string) []int {
	var out []int
	for from := 0; ; {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return out
		}
		out = append(out, from+i)
		from += i + len(sub)
	}
}
