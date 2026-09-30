package webui

import (
	"strings"
	"testing"
)

// The overlay system of the pop-over redesign (part A): how each surface is
// presented, the stack a surface opened from another goes on, where the
// keyboard goes on the way in and out, and the questions asked in the
// window rather than by the browser. The harness has no CSS, so what is
// pinned here is what the script decides: data-present, the stack, focus.

// shownIn is whether an element with that id is on the page: the harness
// finds an element removed from the page by its id as well.
const shownIn = `
const shown = (id) => { const n = h.$(id); return !!(n && n.isConnected); };
`

// Each surface says how it is presented, and the rail tools are sheets.
func TestEachSurfaceSaysHowItIsPresented(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const present = () => h.$("overlay-panel").getAttribute("data-present");
const sheets = ["btn-agents", "btn-changes", "btn-history", "btn-worktrees", "btn-fanout-history",
  "btn-todos", "btn-github", "btn-apikeys", "btn-remote", "rail-open"];
for (const id of sheets) {
  h.click(h.$(id));
  assert.ok(!h.$("overlay").hidden, id + " opened nothing");
  assert.strictEqual(present(), "sheet", id + " did not open a sheet");
  h.key({ key: "Escape" });
  assert.ok(h.$("overlay").hidden, "Escape did not close what " + id + " opened");
}
// Changes is the wide one, which the style sheet turns into a large dialog
// below 1100px.
h.click(h.$("btn-changes"));
assert.strictEqual(h.$("overlay-panel").getAttribute("data-size"), "wide", "Changes is not the wide sheet");
h.key({ key: "Escape" });
h.click(h.$("btn-agents"));
assert.strictEqual(h.$("overlay-panel").getAttribute("data-size"), null, "Agents took Changes' width");
h.key({ key: "Escape" });

for (const [open, want] of [["btn-settings", "dialog-l"], ["btn-help", "dialog-l"], ["new-tab-pick", "picker"]]) {
  h.click(h.$(open));
  assert.strictEqual(present(), want, open + " is not a " + want);
  h.key({ key: "Escape" });
  if (!h.$("overlay").hidden) h.key({ key: "Escape" });
}
h.press("fanout");
assert.strictEqual(present(), "dialog-m", "Fan out is not a medium dialog");
h.key({ key: "Escape" });
h.win._prompt = undefined;
h.$("tabs").children[0].ondblclick();
assert.strictEqual(h.$("overlay-title").textContent, "Rename tab");
assert.strictEqual(present(), "dialog-s", "Rename tab is not a small dialog");
`)
}

// "Go to…" has left the header; the ? stays, and opens Help on top of the
// surface it was asked from, so Escape comes back to it rather than closing
// both, and the header of Help says where it goes back to.
func TestTheQuestionMarkStacksHelpOnTheSurface(t *testing.T) {
	runFrontEnd(t, shownIn+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
assert.ok(!shown("overlay-goto"), "the header still carries Go to…");
assert.ok(h.$("overlay-help"), "Settings has no ?");
h.click(h.$("overlay-help"));
await h.sleep(30);
assert.strictEqual(h.$("overlay-title").textContent, "Help");
assert.ok(h.$("overlay-back"), "Help does not say what it goes back to");
assert.strictEqual(h.$("overlay-back").textContent, "← Settings");
h.key({ key: "Escape" });
assert.strictEqual(h.$("overlay-title").textContent, "Settings", "Escape did not come back from Help to Settings");
assert.ok(!shown("overlay-back"), "Settings, opened from the rail, has a way back to nothing");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape from Settings did not close it");

// The × and the ← go back one level too, as Escape does.
h.click(h.$("btn-settings"));
h.click(h.$("overlay-help"));
await h.sleep(30);
h.click(h.$("overlay-close"));
assert.strictEqual(h.$("overlay-title").textContent, "Settings", "the × closed Settings along with Help");
h.click(h.$("overlay-help"));
await h.sleep(30);
h.click(h.$("overlay-back"));
assert.strictEqual(h.$("overlay-title").textContent, "Settings", "the ← did not go back to Settings");

// F1, a window-wide key, still replaces what is open, as it always has.
h.press("help");
await h.sleep(30);
assert.ok(!shown("overlay-back"), "F1 stacked Help rather than replacing");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden);
`)
}

// A sheet opened from another -- Review on a worktree -- stacks on it: its
// header names the way back, and Escape draws the first again, asking for
// its list afresh, where closing used to lose it.
func TestASheetOpenedFromAnotherGoesBackToIt(t *testing.T) {
	runFrontEnd(t, shownIn+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
const tree = { type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, head: "abc1234", panes: 1 },
          { label: "fix-auth", path: "C:/fix-auth", head: "def5678" }],
  branches: [{ name: "main", checkedIn: true }] };
h.recv(tree);
// Review is in the row's ⋯ menu.
h.click(h.$("overlay-body").querySelectorAll("div.wt-row")[1].querySelector("button.wt-more"));
const review = h.$("overlay-panel").querySelector("div.menu").querySelectorAll("button").find((b) => b.textContent === "Review changes");
assert.ok(review, "no Review on a worktree");
h.click(review);
assert.strictEqual(h.$("overlay-title").textContent, "Changes");
assert.deepStrictEqual(h.commands().pop(), { cmd: "changes", path: "C:/fix-auth" });
assert.strictEqual(h.$("overlay-back").textContent, "← Worktrees");
assert.ok(h.$("btn-changes").classList.contains("surface-open"), "the rail does not mark Changes as open");
assert.ok(!h.$("btn-worktrees").classList.contains("surface-open"), "the rail marks two surfaces");
h.key({ key: "Escape" });
assert.strictEqual(h.$("overlay-title").textContent, "Worktrees", "Escape did not go back to Worktrees");
assert.strictEqual(h.commands().pop().cmd, "worktrees", "going back did not ask for the worktrees again");
assert.ok(h.$("btn-worktrees").classList.contains("surface-open"));
// A late answer for the surface left is not drawn into the one returned to.
h.recv({ type: "changes", cwd: "C:/fix-auth", branch: "fix-auth", files: [{ path: "a.go", label: "M" }] });
assert.strictEqual(h.$("overlay-title").textContent, "Worktrees");
assert.ok(!h.$("overlay-body").textContent.includes("a.go"), "Changes' answer was drawn into Worktrees");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape from the first sheet did not close it");
assert.ok(!h.$("rail").querySelectorAll(".surface-open").length, "the rail still marks a surface as open");

// Opened from the rail rather than from a surface, one replaces another.
h.click(h.$("btn-worktrees"));
h.click(h.$("btn-agents"));
assert.ok(!shown("overlay-back"), "a surface opened from the rail has a way back");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape went back to a surface that was replaced");
`)
}

// Closing gives the keyboard back to what opened the surface -- the rail
// button, not the terminal -- and a surface opened from a terminal gives it
// back to the terminal.
func TestClosingGivesTheKeyboardBackToTheOpener(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const btn = h.$("btn-worktrees");
btn.focus();
h.click(btn);
assert.ok(h.doc.activeElement === h.$("overlay-panel"), "the sheet did not take the keyboard");
h.terms.forEach((t) => { t.focused = false; });
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden);
assert.ok(h.doc.activeElement === btn, "the keyboard did not go back to the rail button");
assert.ok(!h.terms.some((t) => t.focused), "a terminal took the keyboard from the rail button");

// The ×, and a click on the scrim, the same.
btn.focus();
h.click(btn);
h.click(h.$("overlay-close"));
assert.ok(h.doc.activeElement === btn, "the × did not give the keyboard back to the rail button");
h.$("btn-settings").focus();
h.click(h.$("btn-settings"));
h.dispatch(h.$("overlay"), new h.Ev("mousedown", { target: h.$("overlay") }));
assert.ok(h.$("overlay").hidden, "a click beside the dialog did not close it");
assert.ok(h.doc.activeElement === h.$("btn-settings"), "the scrim did not give the keyboard back");

// From the terminal, by a key, back to the terminal.
h.doc.body.focus && h.doc.activeElement.blur();
h.press("worktrees");
h.terms.forEach((t) => { t.focused = false; });
h.key({ key: "Escape" });
assert.ok(h.terms.some((t) => t.focused), "a surface opened from the terminal did not give it back");

// Work done in a surface -- an agent opened in a worktree -- goes to the
// terminal, as it did.
btn.focus();
h.click(btn);
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, head: "abc1234", panes: 1 }], branches: [] });
h.terms.forEach((t) => { t.focused = false; });
h.click(Array.from(h.$("overlay-body").querySelectorAll("button")).find((b) => b.textContent === "Open agent"));
assert.ok(h.$("overlay").hidden);
assert.ok(h.terms.some((t) => t.focused), "opening an agent left the keyboard on the rail");
`)
}

// The key that opened a surface puts it away again; any other window key
// still waits behind it.
func TestTheKeyThatOpenedASurfaceClosesIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("worktrees");
assert.strictEqual(h.$("overlay-title").textContent, "Worktrees");
h.press("agents");
assert.strictEqual(h.$("overlay-title").textContent, "Worktrees", "another surface's key was not held back");
h.press("worktrees");
assert.ok(h.$("overlay").hidden, "the key that opened Worktrees did not close it");
h.press("settings");
h.press("settings");
assert.ok(h.$("overlay").hidden, "Ctrl+, did not close Settings");
`)
}

// The keyboard goes to the first control a surface draws -- here the first
// worktree's own button -- rather than staying on the panel, and a surface
// that puts it somewhere of its own keeps it there.
func TestASurfaceHandsTheKeyboardToItsFirstControl(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
assert.ok(h.doc.activeElement === h.$("overlay-panel"), "loading, the panel should hold the keyboard");
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, head: "abc1234", panes: 1 },
          { label: "fix-auth", path: "C:/fix-auth", head: "def5678" }], branches: [] });
const at = h.doc.activeElement;
assert.ok(h.$("overlay-body").contains(at), "the keyboard stayed on the panel after the list arrived");
// A later answer does not move it again.
h.key({ key: "Tab" });
const moved = h.doc.activeElement;
h.recv({ type: "worktrees", root: "C:/repo", defaultBase: "main",
  items: [{ label: "main", path: "C:/repo", main: true, head: "abc1234", panes: 1 },
          { label: "fix-auth", path: "C:/fix-auth", head: "def5678" }], branches: [] });
assert.strictEqual(h.doc.activeElement.textContent, moved.textContent, "a redraw took the keyboard back to the first control");
h.key({ key: "Escape" });

h.click(h.$("btn-settings"));
assert.ok(h.doc.activeElement === h.$("settings-tab-general"), "Settings lost its own choice of the section list");
`)
}

// The questions asked in the window: until part C2 turns them on, a call
// that passes the browser's words is answered by the browser exactly as
// before; where the browser has no dialog to give, the designed one is
// drawn, and answers the same way.
func TestTheAskShimKeepsTheBrowsersQuestionsUntilTheyAreReplaced(t *testing.T) {
	runFrontEnd(t, paletteRun+`
h.hello();
h.recv(fixture());
// Native: the same words, the same answer, nothing drawn.
h.win._prompt = "25,000";
paletteRun("terminal scrollback");
assert.ok(h.$("overlay").hidden, "the native question drew a dialog as well");
assert.deepStrictEqual(h.commands().pop(), { cmd: "scrollback", size: 25000 });
h.win._confirm = false;
paletteRun("quit");
assert.strictEqual(h.win._confirmed, "Stop every agent in every open project?");
assert.ok(!h.commands().some((c) => c.cmd === "quit"), "quit ran though the question was refused");

// In the window: the designed dialog.
h.win.prompt = undefined;
h.win.confirm = undefined;
paletteRun("terminal scrollback");
assert.ok(!h.$("overlay").hidden, "no question was asked");
assert.strictEqual(h.$("overlay-panel").getAttribute("data-present"), "dialog-s");
assert.strictEqual(h.$("overlay-title").textContent, "Terminal scrollback");
const field = h.$("ask-field");
assert.ok(h.doc.activeElement === field, "the field did not take the keyboard");
assert.strictEqual(field.value, "25000");
const foot = h.$("overlay-body").children[h.$("overlay-body").children.length - 1];
assert.ok(foot.classList.contains("ov-foot"), "the footer is not the body's last child");
assert.strictEqual(h.$("overlay-body").querySelector("button.primary"), h.$("ask-ok"), "the first primary in the body is not the answer");
field.value = "30000";
h.key({ key: "Enter" });
assert.ok(h.$("overlay").hidden, "Enter did not answer");
assert.deepStrictEqual(h.commands().pop(), { cmd: "scrollback", size: 30000 });

// Escape answers no, and nothing is sent.
paletteRun("terminal scrollback");
const sent = h.commands().length;
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape left the question up");
assert.strictEqual(h.commands().length, sent, "Escape sent something");

// A confirm: the question as its title, an alertdialog, and a destructive
// action that Enter does not land on.
paletteRun("quit");
assert.strictEqual(h.$("overlay-title").textContent, "Stop every agent in every open project?");
assert.strictEqual(h.$("overlay-panel").getAttribute("role"), "alertdialog");
assert.ok(h.doc.activeElement === h.$("ask-cancel"), "a destructive question put the keyboard on its action");
assert.strictEqual(h.$("ask-ok").textContent, "Quit");
assert.ok(h.$("ask-ok").classList.contains("danger"));
h.click(h.$("ask-cancel"));
assert.ok(!h.commands().some((c) => c.cmd === "quit"), "Cancel ran it");
assert.strictEqual(h.$("overlay-panel").getAttribute("role"), "alertdialog", "(closed: the role is reset on the next open)");
paletteRun("quit");
h.click(h.$("ask-ok"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "quit" });
`)
}

// Asked from a surface, the question stacks on it and closing it comes back.
func TestAQuestionAskedFromASurfaceComesBackToIt(t *testing.T) {
	runFrontEnd(t, paletteRun+shownIn+`
h.hello();
h.recv(fixture());
h.win.prompt = undefined;
h.click(h.$("btn-settings"));
paletteRun("terminal font");
assert.strictEqual(h.$("overlay-title").textContent, "Terminal font");
assert.ok(!shown("overlay-back"), "a small dialog carries a back link");
h.key({ key: "Escape" });
assert.strictEqual(h.$("overlay-title").textContent, "Settings", "the question did not go back to Settings");
assert.ok(h.$("btn-settings").classList.contains("surface-open"));
`)
}

// The style sheet has a rule for every presentation, a sticky footer inside
// the body, a rail mark, and keeps the high-contrast block last.
func TestThePresentationsAreStyled(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for _, want := range []string{
		`[data-present="sheet"]`, `[data-present="dialog-s"]`, `[data-present="dialog-m"]`,
		`[data-present="dialog-l"]`, `[data-present="picker"]`,
		".ov-foot", ".ov-tabs", ".ov-back", ".rail-btn.surface-open", "#sheet-ghost",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css has no rule for %s", want)
		}
	}
	foot := ruleBody(css, ".ov-foot")
	if !strings.Contains(foot, "position: sticky") || !strings.Contains(foot, "bottom:") {
		t.Errorf(".ov-foot is not sticky to the bottom: %q", foot)
	}
	last := strings.LastIndex(css, "@media (forced-colors: active)")
	if i := strings.LastIndex(css, `[data-present=`); i > last {
		t.Errorf("a presentation rule comes after the high-contrast block")
	}
	fc := css[last:]
	if !strings.Contains(fc, ".rail-btn.surface-open") {
		t.Errorf("the forced-colors block does not mark the open surface's rail button")
	}
	if !strings.Contains(mediaBlock(t, css, `\(prefers-reduced-motion:\s*reduce\)`), "animation-duration") {
		t.Errorf("reduced motion no longer stills the sheets' arrival")
	}
}
