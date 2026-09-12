package webui

import (
	"testing"
)

// The disconnected panel takes the keyboard so that typing does not go to a
// terminal nobody can see. When the connection came back the panel was hidden
// with the keyboard still on its button, so nothing typed went anywhere until
// something was clicked. It goes back to where it was: the terminal, or the
// dialog that was open.
func TestReconnectingGivesTheKeyboardBack(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.control.close();
assert.ok(h.doc.activeElement === h.$("retry"), "the reconnect button did not take the keyboard");
h.terms.forEach((t) => { t.focused = false; });
h.click(h.$("retry"));
h.open();
assert.ok(h.$("disconnected").hidden, "the panel is still up");
// The harness's terminals do not move activeElement, so it is the terminal
// being focused that says the keyboard left the hidden button.
assert.ok(h.terms[0].focused, "the keyboard was left on the hidden reconnect button");

// A dialog that was open gets it back instead.
h.recv(fixture());
h.click(h.$("btn-worktrees"));
assert.ok(h.doc.activeElement === h.$("overlay-panel"), "the dialog did not take the keyboard");
h.controls().pop().close();
assert.ok(h.doc.activeElement === h.$("retry"));
h.terms.forEach((t) => { t.focused = false; });
h.click(h.$("retry"));
h.open();
assert.ok(h.doc.activeElement === h.$("overlay-panel"), "the dialog did not get the keyboard back");
assert.ok(!h.terms.some((t) => t.focused), "a terminal behind the dialog took the keyboard");
`)
}

// The commit buttons wait while a commit is out, and Ctrl+Enter in the
// message did not: pressed twice, or held a moment too long, it sent the
// same commit twice, the second while the first was still running.
func TestCtrlEnterDoesNotCommitTwice(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const tree = { type: "changes", cwd: "C:/repo", branch: "main", upstream: "origin/main", hasRemote: true,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] };
h.recv(tree);
const box = h.$("commit-message");
box.value = "webui: something";
box.oninput();
box.focus();
h.key({ key: "Enter", ctrlKey: true });
h.key({ key: "Enter", ctrlKey: true });
const commits = () => h.commands().filter((c) => c.cmd === "commit").length;
assert.strictEqual(commits(), 1, "a second Ctrl+Enter sent the commit again while the first was out");

// Once the answer is back, the box commits again.
h.recv({ type: "notice", text: "The commit was refused", error: true });
h.recv(tree);
h.$("commit-message").focus();
h.key({ key: "Enter", ctrlKey: true });
assert.strictEqual(commits(), 2, "the box stayed dead after the answer came back");
`)
}

// A pane with no live process is covered by its error and a Restart button.
// The cover was built once and never written again, so a pane that exited and
// then failed to restart went on saying only that it had exited, and a
// second, different failure went on showing the first.
func TestThePaneErrorFollowsTheError(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const push = (p1) => h.recv(fixture({ panes: { p1: pane("p1", p1), p2: pane("p2") } }));
push({ status: "exited" });
const cover = () => h.$("workspace").querySelector(".pane-error");
assert.ok(cover(), "an exited pane is not covered");
assert.ok(cover().textContent.includes("The process exited."), cover().textContent);
const restart = cover().querySelector("button");

push({ status: "exited", err: "claude was not found on PATH" });
assert.ok(cover().textContent.includes("claude was not found on PATH"), "the error that arrived is not shown: " + cover().textContent);
push({ status: "exited", err: "permission denied" });
assert.ok(cover().textContent.includes("permission denied"), "a new error is not shown: " + cover().textContent);
assert.ok(!cover().textContent.includes("PATH"), "the old error is still shown");
assert.ok(cover().querySelector("button") === restart, "the Restart button was built again, taking the keyboard off it");

push({ status: "idle" });
assert.ok(!cover(), "a pane running again is still covered");
`)
}

// A divider is a separator with a value, which is how a screen reader says
// how the space is shared. The value was written only once a divider had
// been dragged or arrowed, so every split a layout was restored with, or
// resized from another window, had none, or a stale one.
func TestADividerSaysHowTheSpaceIsShared(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tab = (a, b) => ({ id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
  root: { id: "s1", dir: "h", weight: 1, children: [
    { id: "n1", pane: "p1", weight: a }, { id: "n2", pane: "p2", weight: b }] } });
h.recv(fixture({ tabs: [tab(3, 1)] }));
const d = h.$("workspace").querySelector(".divider");
assert.strictEqual(d.getAttribute("aria-valuenow"), "75", "a restored split does not say how it is shared");
h.recv(fixture({ tabs: [tab(1, 1)] }));
assert.strictEqual(d.getAttribute("aria-valuenow"), "50", "the value did not follow the weights");
`)
}

// Every row a list is walked by from the keyboard has to show where the
// keyboard is. The conversations were walked with the arrows and showed
// nothing at all: the style sheet's focus ring left them out.
func TestEveryRowTheKeyboardWalksShowsItsFocus(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const css = h.css();
const check = (what) => {
  const rows = h.$("overlay-body").querySelectorAll("[tabindex]").filter((n) => n.getAttribute("role") === "button");
  assert.ok(rows.length, "no rows to walk in " + what);
  for (const r of rows) {
    const cls = r.className.split(" ")[0];
    assert.ok(new RegExp("\\." + cls + ":focus-visible").test(css), "." + cls + " in " + what + " has no focus ring");
  }
};
h.click(h.$("btn-history"));
h.recv({ type: "conversations", cwd: "C:/repo", items: [
  { id: "0123456789", summary: "fix the parser", ago: "1h ago", messages: 4, kb: 2, open: false }] });
check("the conversations");
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
check("the review");
h.click(h.$("summary"));
h.recv({ type: "agents", items: [{ paneId: "p1", tabId: "t1", root: "C:/repo", tab: "one", name: "agent p1",
  project: "repo", status: "idle" }] });
check("the agents");
`)
}

// The header's figures are rounded to a few places, and a figure a hair under
// a boundary came out on the wrong side of it: 1023.8 KB as "1024 KB" rather
// than "1.0 MB", 99.97 MB with a fourth figure, 9,999 tokens as "10.0k" beside
// 10,001 as "10k", and $0.9999 as "~$1.000" beside $1.01 as "~$1.01".
func TestTheHeadersFiguresRoundAcrossTheirBoundaries(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const head = () => h.$("workspace").querySelectorAll(".pane-header")[0];
const usage = (rss) => {
  h.recv(fixture({ panes: { p1: pane("p1", { procs: 1, cpu: 0, rss }), p2: pane("p2") } }));
  return head().querySelector(".pane-usage").textContent;
};
assert.strictEqual(usage(1048575), "0% 1.0 MB");
assert.strictEqual(usage(Math.round(99.97 * 1048576)), "0% 100 MB");
assert.strictEqual(usage(Math.round(5.25 * 1048576)), "0% 5.3 MB");
const spend = (sp) => {
  h.recv(fixture({ panes: { p1: pane("p1", { agent: "ollama", spend: sp }), p2: pane("p2") } }));
  return head().querySelector(".pane-spend").textContent;
};
assert.strictEqual(spend({ tokens: 9999 }), "9.9k tok");
assert.strictEqual(spend({ tokens: 1999999 }), "1.9M tok");
assert.strictEqual(spend({ tokens: 84210 }), "84k tok");
assert.strictEqual(spend({ usd: 0.9999, source: "table", tokens: 10 }), "~$1.00");
assert.strictEqual(spend({ usd: 0.009999, source: "table", tokens: 10 }), "~$0.010");
assert.strictEqual(spend({ usd: 0.0421, source: "table", tokens: 10 }), "~$0.042");
`)
}

// The summary in the top bar is a button whose words are the counts. It was
// named from the action table while it was still empty, and that name
// outlasted every count written into it: tabbed to, it said what it opens and
// never who was waiting.
func TestTheSummarySaysItsCountsWhenTabbedTo(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ waiting: 1, working: 2, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2", { status: "working" }) } }));
const s = h.$("summary");
const name = () => s.getAttribute("aria-label") || s.textContent;
assert.ok(name().includes("1 waiting") && name().includes("2 working"), "the button's name leaves out the counts: " + name());
h.recv(fixture({ waiting: 0, working: 0 }));
assert.ok(!name().includes("waiting") && !name().includes("working"), "the name kept counts that are gone: " + name());
assert.ok(name().length > 0, "an idle summary has no name at all");
`)
}

// The window's title is what the taskbar shows of it behind other windows,
// and it went on saying agents were waiting in a Flockdeck that had stopped.
// It says the window is disconnected, and goes back to the counts after.
func TestTheTitleSaysTheWindowIsDisconnected(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const waiting = () => fixture({ waiting: 1, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2") } });
h.recv(waiting());
assert.strictEqual(h.doc.title, "▲ 1 waiting · flockdeck");
h.control.close();
assert.ok(/disconnected/i.test(h.doc.title), "the title still counts agents in a window that lost them: " + h.doc.title);
h.click(h.$("retry"));
h.open();
h.recv(waiting());
assert.strictEqual(h.doc.title, "▲ 1 waiting · flockdeck", "the title went on saying the window was disconnected");
`)
}

// A message is often read with the pointer resting on it, and it went on its
// own timer regardless, taking a long git error away mid-sentence. It stays
// while the pointer is on it - a second message arriving then as well - and
// goes a moment after the pointer leaves.
func TestANoticeStaysWhileThePointerIsOnIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
h.recv({ type: "notice", text: "pushed 2 commits to origin/main", error: false });
h.dispatch(n, new h.Ev("pointerenter", { target: n }));
h.recv({ type: "notice", text: "fetched origin", error: false });
await h.sleep(4300);
assert.ok(!n.hidden, "the message was taken away from under the pointer reading it");
assert.ok(n.textContent.includes("fetched"), "the newer message is not the one shown");
h.dispatch(n, new h.Ev("pointerleave", { target: n }));
await h.sleep(1800);
assert.ok(n.hidden, "the message stayed on after the pointer left it");
`)
}

// Renaming a tab was a double-click, or the palette's entry for the tab on
// screen. F2 on the tab the keyboard is on renames that one, as F2 renames a
// file almost everywhere, and the tab's bubble says so.
func TestF2RenamesTheTabTheKeyboardIsOn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const tab = h.$("tabs").children[1];
tab.focus();
const ev = h.key({ key: "F2" });
assert.ok(ev.defaultPrevented, "F2 went on to the browser");
assert.strictEqual(h.$("overlay-title").textContent, "Rename tab", "F2 did not open the rename dialog");
assert.strictEqual(h.$("tab-name").value, "two", "the dialog is not for the tab the keyboard was on");
assert.ok(/F2/.test(tab.dataset.tip), "nothing says F2 renames: " + tab.dataset.tip);
`)
}

// A dialog redrawn from a push finds the control the keyboard was on by what
// it says. A project row says how many agents are waiting there, a changed
// file how many lines, a conversation how long ago it was - and the redraws
// come because those moved - so the keyboard fell out of the dialog whenever
// an agent changed what it was doing.
func TestARowKeepsTheKeyboardWhenItsFiguresMove(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const two = (w) => fixture({ projects: [
  { root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 },
  { root: "C:/other", name: "other", active: false, tabs: 1, waiting: w, working: 0 }] });
h.recv(two(0));
h.click(h.$("project-btn"));
const go = () => h.$("overlay-body").querySelectorAll("button.proj-go")[1];
go().focus();
h.recv(two(1));
assert.ok(h.doc.activeElement === go(), "an agent starting to wait took the keyboard off the project row");

h.key({ key: "Escape" });
h.click(h.$("btn-changes"));
const tree = (added) => ({ type: "changes", cwd: "C:/repo", branch: "main", files: [
  { path: "a.go", label: "M", added: 1, removed: 0 }, { path: "b.go", label: "M", added, removed: 0 }] });
h.recv(tree(1));
const files = () => h.$("overlay-body").querySelectorAll("div.rev-file");
files()[1].focus();
h.recv(tree(5));
assert.ok(h.doc.activeElement === files()[1], "an agent writing to the file took the keyboard off its row");

h.key({ key: "Escape" });
h.click(h.$("btn-history"));
const convs = (ago) => ({ type: "conversations", cwd: "C:/repo", items: [
  { id: "aaaa1111", summary: "one", ago, messages: 3, open: false },
  { id: "bbbb2222", summary: "two", ago, messages: 3, open: false }] });
h.recv(convs("1 minute ago"));
const rows = () => h.$("overlay-body").querySelectorAll("div.conv-row");
rows()[1].focus();
h.recv(convs("2 minutes ago"));
assert.ok(h.doc.activeElement === rows()[1], "a conversation growing older took the keyboard off its row");
`)
}
