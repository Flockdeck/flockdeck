package webui

import (
	"regexp"
	"strconv"
	"testing"
)

// The tab strip scrolls sideways, and a scrolling box clips whatever is drawn
// past its padding edge. A tab's focus ring is drawn outside the tab, and the
// strip had no padding, so the ring was cut off: above and below, and at the
// ends of the strip. The padding has to be at least as wide as the ring and
// its offset together.
func TestTheTabStripDoesNotClipAFocusRing(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	// The ring is drawn round the whole tab, its close button and all, when the
	// tab's button inside it has the keyboard.
	ring := ruleBody(css, ".tab:has(> .tab-btn:focus-visible)")
	if ring == "" {
		t.Fatal("app.css has no focus ring for .tab")
	}
	need := cssPx(ring, "outline") + cssPx(ring, "outline-offset")
	if got := cssPx(ruleBody(css, "#tabs"), "padding"); got < need {
		t.Errorf("the tab strip is padded %dpx, and a tab's focus ring reaches %dpx outside it, so the strip cuts it off", got, need)
	}
}

// The buttons in a pane's header - fan out, broadcast, restart, zoom and close -
// were about twenty pixels tall, under the 24 a target needs to be hit without
// care. They have to be at least that, and still fit inside the header, whose
// height includes its one-pixel rule.
func TestAPaneHeadersButtonsAreBigEnoughToHit(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	tall := cssPx(ruleBody(css, ".pane-actions button"), "min-height")
	if tall < 24 {
		t.Errorf("a pane header's buttons are %dpx tall at least, short of 24px", tall)
	}
	if header := cssPx(ruleBody(css, ":root"), "--header-h"); tall > header-1 {
		t.Errorf("a pane header's buttons are %dpx tall, and the %dpx header has room for %dpx", tall, header, header-1)
	}
}

// cssPx reads a length in pixels that a block gives a property, or 0.
func cssPx(body, prop string) int {
	m := regexp.MustCompile(`(?:^|[;\s])` + prop + `:\s*(\d+)px`).FindStringSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// A text field's automatic minimum width is its default size, about twenty
// characters, so in a narrow window the prompt bar and the find bar could not
// shrink below it and ran off the side, their buttons with them. min-width: 0
// lets the field give up width, as the worktree form's fields already do.
func TestThePromptAndFindFieldsShrinkInANarrowWindow(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for _, sel := range []string{"#prompt-input", "#searchbar input"} {
		if !regexp.MustCompile(`(?:^|[;\s])min-width:\s*0\b`).MatchString(ruleBody(css, sel)) {
			t.Errorf("%s keeps a text field's default minimum width, so its bar overflows a narrow window", sel)
		}
	}
}

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
h.dispatch(n, new h.Ev("pointermove", { target: n }));
h.recv({ type: "notice", text: "fetched origin", error: false });
await h.sleep(4300);
assert.ok(!n.hidden, "the message was taken away from under the pointer reading it");
assert.ok(n.textContent.includes("fetched"), "the newer message is not the one shown");
h.dispatch(n, new h.Ev("pointerleave", { target: n }));
await h.sleep(1800);
assert.ok(n.hidden, "the message stayed on after the pointer left it");
`)
}

// Chromium sends pointerenter when a message appears under a pointer that has
// not moved, so a pointer parked where the messages appear held every one of
// them for as long as someone went on typing. A message is held by a pointer
// moving on it, and a pointer left still on it lets it go in the end.
func TestANoticeUnderAParkedPointerStillGoes(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const n = h.$("notice");
// The page's own timers run a hundred times faster here, so that the longest
// a message can be held is waited out in a moment.
const real = h.win.setTimeout;
h.win.setTimeout = (fn, ms, ...args) => real(fn, (ms || 0) / 100, ...args);
h.recv({ type: "notice", text: "fetched origin", error: false });
h.dispatch(n, new h.Ev("pointerenter", { target: n }));
await h.sleep(80);
assert.ok(n.hidden, "a message that appeared under a parked pointer stayed as though it were being read");

h.recv({ type: "notice", text: "push rejected: the remote has commits you do not", error: true });
h.dispatch(n, new h.Ev("pointermove", { target: n }));
await h.sleep(200);
assert.ok(!n.hidden, "a message went from under the pointer moving on it");
await h.sleep(250);
assert.ok(n.hidden, "a message the pointer stopped on never went");
`)
}

// Renaming a tab was a double-click, or the palette's entry for the tab on
// screen. F2 on the tab the keyboard is on renames that one, as F2 renames a
// file almost everywhere, and the tab's bubble says so.
func TestF2RenamesTheTabTheKeyboardIsOn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const tab = h.$("tab-t2");
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
h.click(h.$("rail-open"));
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

// A changed file's row was named "rev-" and its path, the prefix the review's
// own buttons are named with, so a file called push at the top of the tree
// took the Push button's name. Redrawn after an agent wrote to it, the dialog
// put the keyboard back on the first control by that name - the button - and
// Enter pushed rather than opening the file.
func TestAFileNamedPushIsNotThePushButton(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const tree = { type: "changes", cwd: "C:/repo", branch: "main", upstream: "origin/main", hasRemote: true, ahead: 1,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }, { path: "push", label: "M", added: 2, removed: 0 }] };
h.recv(tree);
assert.strictEqual(h.$("rev-push").tagName, "BUTTON", "the file named push took the Push button's id");
const row = () => h.$("overlay-body").querySelectorAll("div.rev-file")[1];
row().focus();
h.recv(tree);
assert.ok(h.doc.activeElement === row(), "a redraw put the keyboard on the Push button rather than on the file named push");
`)
}

// A pairing link is usually sent to the device it is for, and copying it
// meant clicking into the field, selecting it and pressing the copy key. A
// button copies it; where the clipboard is refused, the link is selected so
// the copy key does it, and that is said.
func TestThePairingLinkCanBeCopied(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0,
  since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: true, devices: [], hosts: [{ id: "h1", name: "desk", online: true, self: true }] });
h.click(h.$("remote-pair"));
h.recv({ type: "remotePair", kind: "device", code: "fdp_c", url: "https://relay.example/pair#fdp_c",
  expiresAt: "2030-01-01T00:10:00Z" });
const copy = h.$("remote-copy");
assert.ok(copy, "the pairing link has no copy button");
h.click(copy);
await h.sleep(0);
assert.strictEqual(h.win._copied, "https://relay.example/pair#fdp_c", "the link did not reach the clipboard");
assert.ok(h.$("notice").textContent.includes("Copied"), "nothing said the link was copied: " + h.$("notice").textContent);

h.win.navigator.clipboard.writeText = () => Promise.reject(new Error("denied"));
h.click(h.$("remote-copy"));
await h.sleep(0);
const link = h.$("overlay-body").querySelector(".remote-link");
assert.ok(h.doc.activeElement === link, "a refused clipboard left the link unselected");
assert.strictEqual(link.selectionEnd, link.value.length, "the link is not selected whole");
assert.ok(h.$("notice").classList.contains("error"), "nothing said the copy failed");
`)
}

// Narrowed to nothing, the conversations simply went blank, which reads the
// same as a project with none or a list still being read. It says so.
func TestTheConversationsSayWhenNothingMatches(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-history"));
const conv = (id, summary) => ({ id, summary, ago: "1h", messages: 3, open: false });
const list = { type: "conversations", cwd: "C:/repo", items: [conv("aaaa1111", "Fix the login bug"), conv("bbbb2222", "Write docs")] };
h.recv(list);
const field = h.$("history-filter");
const none = () => h.$("history-none");
assert.ok(!none() || none().hidden, "the list says nothing matches while it shows conversations");
field.value = "zebra";
field.oninput();
assert.ok(none() && !none().hidden, "a filter that matches nothing leaves the list blank with nothing said");
field.value = "docs";
field.oninput();
assert.ok(none().hidden, "the note outlived the filter");
field.value = "zebra";
field.oninput();
h.recv(list);
assert.ok(!h.$("history-none").hidden, "a refresh forgot that nothing matches");
`)
}

// Typing into a fan-out's task list built every row again on each keystroke,
// each row a select holding every model of every agent: twelve tasks over
// three agents of four models were 240 elements a key. Only the line typed
// into is drawn again, and what was chosen on the others stays as it was.
func TestTypingIntoAFanOutDrawsOnlyTheLineTyped(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
h.recv(fixture());
const agents = [
  { id: "claude", name: "Claude Code", models: [{ id: "" }, { id: "opus" }, { id: "sonnet" }, { id: "haiku" }] },
  { id: "codex", name: "Codex", models: [{ id: "gpt-5" }, { id: "gpt-5-mini" }, { id: "o3" }, { id: "o4-mini" }] },
  { id: "gemini", name: "Gemini", models: [{ id: "pro" }, { id: "flash" }, { id: "flash-lite" }, { id: "ultra" }] },
];
const tasks = [];
for (let i = 0; i < 12; i++) tasks.push("task number " + i);
h.press("fanout");
h.recv({ type: "fanoutPreview", paneId: "p1", tasks, isRepo: true, cwd: "C:/repo", agent: "claude", agents });
const body = h.$("overlay-body");
const box = body.querySelector("textarea.fan-tasks");
const rows = () => body.querySelectorAll("div.fan-row");
const first = rows()[0];
const s0 = first.querySelector("select");
s0.value = "codex\no3";
h.dispatch(s0, new h.Ev("change", { target: s0 }));

const before = h.made();
for (const ch of " and more") { box.value += ch; box.oninput(); }
const built = h.made() - before;
console.log("nine keystrokes into a twelve-task fan-out built " + built + " elements");
assert.ok(built < 9 * 40, "every keystroke built the whole list again: " + built + " elements");
assert.ok(rows()[0] === first, "a row whose line was not touched was built again");
assert.strictEqual(rows()[0].querySelector("select").value, "codex\no3", "the choice on an untouched row was lost");
assert.strictEqual(rows().length, 12);
assert.ok(rows()[11].textContent.includes("task number 11 and more"), "the line typed into does not show what was typed");
assert.deepStrictEqual(rows().map((r) => r.querySelector(".fan-row-n").textContent),
  ["1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"]);

// A line taken out takes its row with it, and the rest are numbered again.
box.value = box.value.split(String.fromCharCode(10)).slice(1).join(String.fromCharCode(10));
box.oninput();
assert.strictEqual(rows().length, 11);
assert.strictEqual(rows()[0].querySelector(".fan-row-task").textContent, "task number 1");
assert.strictEqual(rows()[0].querySelector(".fan-row-n").textContent, "1");
body.querySelector("button.primary").onclick();
const sent = h.commands().pop();
assert.strictEqual(sent.tasks.length, 11);
assert.strictEqual(sent.tasks[10], "task number 11 and more");
`)
	t.Log(out)
}

// A tab was named from everything in it, so it said the close button inside
// it too, and each close button said only "Close tab": a strip of a dozen
// read as a dozen of the same, and nothing said closing one stops its agents.
func TestATabAndItsCloseButtonSayWhichTab(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const tab = h.$("tab-t1");
const close = tab.parentElement.querySelector(".close");
assert.strictEqual(tab.getAttribute("aria-label"), "one", "the tab is not named by its title alone");
assert.ok(/one/.test(close.getAttribute("aria-label") || ""), "the close button does not say which tab: " + close.getAttribute("aria-label"));
assert.ok(/stops/.test(close.dataset.tip || ""), "nothing says closing the tab stops its agents: " + close.dataset.tip);
h.recv(fixture({ waiting: 1, tabs: [
  { id: "t1", title: "renamed", focus: "p1", zoom: false, attention: true, root: { id: "n1", pane: "p1", weight: 1 } },
  { id: "t2", title: "two", focus: "p2", zoom: false, attention: false, root: { id: "n2", pane: "p2", weight: 1 } }] }));
assert.ok(tab.getAttribute("aria-label").startsWith("renamed"), "the name did not follow a rename");
assert.ok(/waiting/.test(tab.getAttribute("aria-label")), "the name does not say an agent is waiting");
assert.ok(/renamed/.test(close.getAttribute("aria-label")), "the close button kept the old name");
`)
}

// A dialog redrawn after a press puts the keyboard back on the control it was
// on. Where the press disabled that control - the font stepper reaching its
// limit, Show them again with no hint left to show - the keyboard was put
// back on a disabled button, which a browser does not allow, and dropped
// onto nothing. It goes to the nearest control that can take it.
func TestTheKeyboardLeavesAControlTheRedrawDisabled(t *testing.T) {
	runFrontEnd(t, `
h.hello({ fontSize: 27, dismissedTips: ["palette"] });
h.recv(fixture());
h.press("settings");
const settled = (what) => {
  const at = h.doc.activeElement;
  assert.ok(at && at.isConnected, "the keyboard was left on a control the redraw threw away: " + what);
  assert.ok(!at.disabled, "the keyboard was left on a disabled control: " + what + " (" + at.id + ")");
  assert.ok(h.$("overlay-body").contains(at), "the keyboard left the dialog: " + what);
  return at;
};

h.$("set-tips").focus();
h.key({ key: "Enter" });
assert.ok(h.$("set-tips").disabled, "Show them again is still offered with nothing to show");
settled("Show them again");

h.click(h.$("settings-tab-terminal"));
h.$("set-font-up").focus();
h.key({ key: "Enter" });
assert.ok(h.$("set-font-up").disabled, "Larger is still offered at the largest size");
assert.strictEqual(settled("the stepper at its limit").id, "set-font-down", "the keyboard did not go to the other half of the stepper");
`)
}

// The remote access dialog's own buttons drew it again without keepFocus, so
// the keyboard went with the button pressed: opening the codes left it on
// nothing rather than in the first code, Enter in the relay field threw the
// field away with the keyboard in it, and unpairing a device dropped it.
func TestTheRemoteDialogKeepsTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-remote"));
h.recv({ type: "remoteDevices", enabled: false, devices: [], hosts: [] });
h.$("remote-more").focus();
h.key({ key: "Enter" });
assert.ok(h.doc.activeElement === h.$("remote-join"), "opening the codes did not put the keyboard in the first one");
const relay = h.$("remote-relay");
relay.value = "relay.example";
relay.focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop().cmd, "remoteEnable");
assert.ok(h.doc.activeElement === h.$("remote-relay"), "Enter in the relay field took the keyboard away with the field");

const remote = { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0, since: "2030-01-01T00:00:00Z" };
const hosts = [{ id: "h1", name: "desk", online: true, self: true }];
h.recv(fixture({ remote }));
h.recv({ type: "remoteDevices", enabled: true, hosts,
  devices: [{ id: "d1", name: "phone", created: "2030-01-01T00:00:00Z", lastSeen: "2030-01-01T00:00:00Z" }] });
const unpair = h.$("overlay-body").querySelectorAll("button").find((b) => b.textContent === "Unpair");
unpair.focus();
h.win._confirm = true;
h.key({ key: "Enter" });
h.recv({ type: "remoteDevices", enabled: true, hosts, devices: [] });
assert.ok(h.doc.activeElement === h.$("remote-pair"), "unpairing a device dropped the keyboard out of the dialog");
`)
}

// Saving a key, putting its form away or clearing it takes away what the
// keyboard was on - the field, Cancel, Clear - and the keyboard went with
// it. It goes back to the row's Set or Replace button, across the reply that
// renames that button as well.
func TestTheKeyboardStaysOnAKeysRow(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("settings");
h.click(h.$("settings-tab-keys"));
const keys = (aSet) => ({ type: "keys", items: [
  { agent: "anthropic", name: "Anthropic API", set: aSet, source: aSet ? "store" : "", vars: ["ANTHROPIC_API_KEY"] },
  { agent: "openai", name: "OpenAI API", set: false, source: "", vars: ["OPENAI_API_KEY"] }] });
h.recv(keys(false));
const row = () => h.$("overlay-body").querySelectorAll("div.wt-row")[0];
const setBtn = () => row().querySelector("button");
setBtn().focus();
h.key({ key: "Enter" });
const field = h.$("overlay-body").querySelector("input[type=password]");
assert.ok(field && h.doc.activeElement === field, "the key field did not take the keyboard");
field.value = "sk-test";
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "keySet", id: "anthropic", text: "sk-test" });
assert.ok(h.doc.activeElement === setBtn(), "saving a key dropped the keyboard with the field");
h.recv(keys(true));
assert.strictEqual(setBtn().textContent, "Replace…");
assert.ok(h.doc.activeElement === setBtn(), "the reply took the keyboard off the row when Set became Replace");

row().querySelectorAll("button")[1].focus();
h.key({ key: "Enter" });
assert.deepStrictEqual(h.commands().pop(), { cmd: "keyClear", id: "anthropic" });
h.recv(keys(false));
assert.ok(h.doc.activeElement === setBtn(), "clearing a key dropped the keyboard");

h.key({ key: "Enter" });
const cancel = h.$("overlay-body").querySelectorAll("button").find((b) => b.textContent === "Cancel");
cancel.focus();
h.key({ key: "Enter" });
assert.ok(h.doc.activeElement === setBtn(), "Cancel dropped the keyboard with the form");
`)
}

// Escape in the API key field was meant to put the form away and keep the
// dialog, so a half-typed key did not leave anyone wondering what was saved.
// The window's key handler sees Escape first, and it closed the whole dialog
// before the field's own handler ran.
func TestEscapeInAKeyFieldPutsTheFormAway(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("settings");
h.click(h.$("settings-tab-keys"));
h.recv({ type: "keys", items: [{ agent: "anthropic", name: "Anthropic API", set: false, source: "", vars: [] }] });
const setBtn = () => h.$("overlay-body").querySelectorAll("div.wt-row")[0].querySelector("button");
setBtn().focus();
h.key({ key: "Enter" });
const field = h.$("key-field");
assert.ok(field && h.doc.activeElement === field, "the key field did not take the keyboard");
field.value = "sk-half";
const before = h.commands().length;
h.key({ key: "Escape" });
assert.ok(!h.$("overlay").hidden, "Escape in the key field closed the whole dialog");
assert.ok(!field.isConnected, "the form is still open");
assert.ok(h.doc.activeElement === setBtn(), "the keyboard did not go back to the row");
assert.strictEqual(h.commands().length, before, "a half-typed key was sent");
h.key({ key: "Escape" });
assert.ok(h.$("overlay").hidden, "Escape from the row no longer closes the dialog");
`)
}

// "Use the run's model for every task" hides itself once nothing is left
// routed, and pressed from the keyboard it hid itself with the keyboard on
// it. The keyboard goes to the run's own select, which the button is about.
func TestClearingTheRoutesKeepsTheKeyboardInTheFanOut(t *testing.T) {
	runFrontEnd(t, fanoutRouting+`
h.press("fanout");
h.recv(routedPreview());
const clear = h.$("fan-route-clear");
clear.focus();
h.key({ key: "Enter" });
assert.ok(clear.hidden, "the button is still offered with nothing left to undo");
const run = body.querySelector("div.fan-agent").querySelector("select");
assert.ok(h.doc.activeElement === run, "the keyboard was left on the button that hid itself");
`)
}

// A commit that works answers with a tree that has nothing left to commit,
// and the message box and its buttons go - with the keyboard in them, since
// Ctrl+Enter commits from the box. It goes to Push, which is what comes
// next, or to Refresh where there is no remote.
func TestAfterACommitTheKeyboardIsOnPush(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const commitFrom = (hasRemote) => {
  h.click(h.$("btn-changes"));
  h.recv({ type: "changes", cwd: "C:/repo", branch: "main", upstream: hasRemote ? "origin/main" : "", hasRemote,
    files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
  const box = h.$("commit-message");
  box.value = "webui: something";
  box.oninput();
  box.focus();
  h.key({ key: "Enter", ctrlKey: true });
  assert.strictEqual(h.commands().pop().cmd, "commit");
  h.recv({ type: "notice", text: "committed in repo", error: false });
  h.recv({ type: "changes", cwd: "C:/repo", branch: "main", upstream: hasRemote ? "origin/main" : "", hasRemote,
    ahead: hasRemote ? 1 : 0, files: [] });
};
commitFrom(true);
assert.ok(h.doc.activeElement === h.$("rev-push"), "after a commit the keyboard was left with the message box that went");
h.key({ key: "Escape" });
commitFrom(false);
assert.ok(h.doc.activeElement === h.$("rev-refresh"), "with no remote, the keyboard did not land in the dialog");
`)
}

// The review reads the tree again whenever an agent writes to it, and each
// time it drew the diff on show twice more - once with the dialog, once when
// the same diff came back - three thousand lines each, while it was being
// read. An unchanged diff stays as it is drawn, with the reader's place in it.
func TestARefreshDoesNotDrawTheDiffOnShowAgain(t *testing.T) {
	out := runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const NL = String.fromCharCode(10);
const tree = (n) => ({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "big.go", label: "M", added: 3000, removed: 0 }, { path: "other.go", label: "M", added: n, removed: 0 }] });
h.recv(tree(1));
h.click(h.$("overlay-body").querySelector("div.rev-file"));
const lines = [];
for (let i = 0; i < 3000; i++) lines.push("+line " + i);
const text = lines.join(NL);
h.recv({ type: "diff", cwd: "C:/repo", file: "big.go", text });
const panel = h.$("overlay-body").querySelector("div.rev-diff");
panel.scrollTop = 900;

const before = h.made();
h.recv(tree(2));
h.recv({ type: "diff", cwd: "C:/repo", file: "big.go", text });
const built = h.made() - before;
console.log("a refresh with an unchanged 3,000-line diff on show built " + built + " elements");
assert.ok(built < 200, "the diff on show was drawn again: " + built + " elements");
const now = h.$("overlay-body").querySelector("div.rev-diff");
assert.ok(now === panel, "the diff panel was built again");
assert.strictEqual(now.scrollTop, 900, "the reader's place in the diff was lost");
assert.ok(now.textContent.includes("+line 2999"), "the diff is no longer shown");

// A diff that did change is drawn.
h.recv(tree(3));
h.recv({ type: "diff", cwd: "C:/repo", file: "big.go", text: "@@ -1 +1 @@" + NL + "+changed" });
assert.ok(h.$("overlay-body").querySelector("div.rev-diff").textContent.includes("+changed"), "a changed diff was not drawn");
`)
	t.Log(out)
}

// Find asked for again, once the keyboard has moved to another pane, searches
// that pane instead - and left the first pane's matches marked for good,
// since only the pane being searched is ever cleared.
func TestFindMovedToAnotherPaneClearsTheFirst(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tab = (focus) => ({ id: "t1", title: "pair", focus, zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) });
const panes = { p1: pane("p1", { name: "reviewer" }), p2: pane("p2", { name: "builder" }) };
h.recv(fixture({ tabs: [tab("p1")], panes }));
h.press("findInTerminal");
h.$("search-input").value = "error";
h.key({ key: "Enter" });
const first = h.searchers[0];
assert.ok(first.forward.includes("error"), "the first pane was not searched");
const cleared = first.cleared;
h.recv(fixture({ tabs: [tab("p2")], panes }));
h.press("findInTerminal");
assert.strictEqual(h.$("search-label").textContent, "Find in builder", "Find did not move to the pane with the keyboard");
assert.ok(first.cleared > cleared, "the first pane's matches were left marked");
`)
}

// Every notification carries the one tag, so a newer one replaces the one
// before. A replacement is put in place silently unless it asks to alert
// again, so an agent that stopped to wait while an earlier notification was
// still up made no sound and showed no banner.
func TestANotificationThatReplacesAnotherStillAlerts(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.doc._hasFocus = false;
h.recv(fixture({ waiting: 1, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2") } }));
h.recv(fixture({ waiting: 2, panes: { p1: pane("p1", { status: "waiting" }), p2: pane("p2", { status: "waiting" }) } }));
assert.strictEqual(h.notifications.length, 2, "each agent that stopped was not notified");
for (const n of h.notifications) {
  assert.strictEqual(n.tag, "flockdeck");
  assert.strictEqual(n.renotify, true, "a notification that replaces another is put in place silently");
}
`)
}

// A diff longer than the panel draws is offered whole with Show them, and the
// button goes with its note once pressed - with the keyboard on it, from the
// keyboard. The keyboard goes to the diff, where the arrows scroll the rest.
func TestShowingTheRestOfADiffLeavesTheKeyboardInIt(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "big.go", label: "M", added: 3050, removed: 0 }] });
h.click(h.$("overlay-body").querySelector("div.rev-file"));
const lines = [];
for (let i = 0; i < 3050; i++) lines.push("+" + i);
h.recv({ type: "diff", cwd: "C:/repo", file: "big.go", text: lines.join(String.fromCharCode(10)) });
const panel = h.$("overlay-body").querySelector("div.rev-diff");
const more = panel.querySelectorAll("button").find((b) => b.textContent === "Show them");
assert.ok(more, "a long diff offers no way to see the rest");
more.focus();
h.key({ key: "Enter" });
assert.ok(panel.textContent.includes("+3049"), "the rest was not drawn");
assert.ok(h.doc.activeElement === panel, "showing the rest of the diff dropped the keyboard");
`)
}

// The agents list says how long each pane has been as it is. The server
// words that as "just now" or "5 minutes ago", and the row put "for" in front
// of it, so it read "for just now" and "for 5 minutes ago".
func TestHowLongAPaneHasBeenAsItIsReadsAsASentence(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "a", status: "idle", for: "just now" },
  { paneId: "p2", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "b", status: "waiting", for: "5 minutes ago" },
  { paneId: "p3", tabId: "t1", root: "C:/repo", project: "repo", tab: "one", name: "c", status: "working", for: "1m" },
] });
const whens = h.$("overlay-body").querySelectorAll("span.agent-for").map((s) => s.textContent);
assert.deepStrictEqual(whens, ["just now", "for 5 minutes", "for 1m"]);
`)
}

// A key stored before a variable was exported goes on being held, shadowed by
// the variable. The keys dialog offered Clear only when the key in use was the
// stored one, so that key could no longer be cleared from it. Clear is for the
// stored key, never for the variable, which is the user's own arrangement.
func TestAStoredKeyAVariableShadowsCanBeCleared(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("settings");
h.click(h.$("settings-tab-keys"));
h.recv({ type: "keys", items: [
  { agent: "gateway", name: "Gateway", set: true, source: "env", env: "OPENAI_API_KEY", stored: true, vars: [] },
  { agent: "openai", name: "OpenAI API", set: true, source: "env", env: "OPENAI_API_KEY", vars: ["OPENAI_API_KEY"] }] });
const rows = h.$("overlay-body").querySelectorAll("div.wt-row");
const clearOf = (row) => row.querySelectorAll("button").find((b) => b.textContent === "Clear");
assert.ok(rows[0].textContent.includes("a stored key is kept as well"), "the row does not say a stored key is still held");
const clear = clearOf(rows[0]);
assert.ok(clear, "a stored key the variable shadows has no Clear");
h.click(clear);
assert.deepStrictEqual(h.commands().pop(), { cmd: "keyClear", id: "gateway" });
assert.ok(!clearOf(rows[1]), "a key only from the environment was offered Clear");
assert.ok(!rows[1].textContent.includes("a stored key"), "a key only from the environment says one is stored");
`)
}

// Find pressed again while the bar was up filled the box with the last search,
// which is only kept when the bar closes, so what had just been typed was
// replaced by an older search and its matches were left marked. On the pane it
// is already searching the bar just takes the keyboard back; moved to another
// pane, it carries what was typed.
func TestFindPressedAgainKeepsWhatWasTyped(t *testing.T) {
	runFrontEnd(t, `
h.hello();
const tab = (focus) => ({ id: "t1", title: "one", focus, zoom: false, attention: false,
  root: split("h", [leaf("n1", "p1"), leaf("n2", "p2")]) });
h.recv(fixture({ tabs: [tab("p1")] }));
const input = h.$("search-input");
h.press("findInTerminal");
input.value = "panic";
h.key({ key: "Escape" });

h.press("findInTerminal");
input.value = "deadlock";
h.doc.body.focus();
h.press("findInTerminal");
assert.strictEqual(input.value, "deadlock", "pressing Find again replaced what was typed with the last search");
assert.ok(h.doc.activeElement === input, "the bar did not take the keyboard back");
assert.deepStrictEqual([input.selectionStart, input.selectionEnd], [0, 8], "what was typed is not selected");

h.recv(fixture({ tabs: [tab("p2")] }));
h.doc.body.focus();
h.press("findInTerminal");
assert.strictEqual(input.value, "deadlock", "moving the bar to another pane dropped what was typed");
`)
}

// The disconnected panel covers the whole window, and the shortcuts went on
// running under it: a binding opened the palette or the find bar out of sight,
// and Escape closed the dialog behind the panel and put the keyboard in a
// terminal nobody could see. While it is up, only Tab and Enter do anything,
// and they do it in the panel.
func TestNothingBehindTheDisconnectedPanelAnswersTheKeyboard(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-worktrees"));
h.control.close();
assert.ok(!h.$("disconnected").hidden, "the panel did not come up");
h.terms.forEach((t) => { t.focused = false; });

h.key({ key: "Escape" });
assert.ok(!h.$("overlay").hidden, "Escape closed the dialog behind the disconnected panel");
assert.ok(!h.terms.some((t) => t.focused), "Escape put the keyboard in a terminal behind the panel");
h.press("findInTerminal");
assert.ok(h.$("searchbar").hidden, "a shortcut ran behind the disconnected panel");
assert.ok(h.doc.activeElement === h.$("retry"), "the keyboard left the panel");

const before = h.controls().length;
h.key({ key: "Enter" });
assert.strictEqual(h.controls().length, before + 1, "Enter no longer presses Reconnect");
`)
}

// A line starting with --- or +++ is a file's header only before its first
// hunk. After it, "--- comment" is a removed SQL comment, "----" a removed
// Markdown rule and "+++i;" an added increment, and all three were drawn grey
// as though they were headers, among lines that were plainly removed and added.
func TestADiffLineIsAHeaderOnlyAboveItsFirstHunk(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
         files: [{ path: "a.sql", label: "M", added: 1, removed: 2 }] });
h.click(h.$("overlay-body").querySelector("div.rev-file"));
const NL = String.fromCharCode(10);
const text = [
  "diff --git a/a.sql b/a.sql", "index 1111111..2222222 100644", "--- a/a.sql", "+++ b/a.sql",
  "@@ -1,3 +1,2 @@", "--- comment", "----", "+++i;", " select 1;",
  "diff --git a/b.md b/b.md", "--- a/b.md", "+++ b/b.md", "@@ -1 +1 @@", "-was", "+is",
].join(NL);
h.recv({ type: "diff", cwd: "C:/repo", file: "a.sql", text });
const panel = h.$("overlay-body").querySelector("div.rev-diff");
const kinds = panel.children.map((c) => c.className);
assert.deepStrictEqual(kinds, [
  "meta", "meta", "meta", "meta", "hunk", "del", "del", "add", "",
  "meta", "meta", "meta", "hunk", "del", "add",
]);
`)
}

// The review's file names are elided from the left with direction: rtl, which
// moves a neutral character at either end of a name to the other end. A mark
// led the name, so ".gitignore" stayed put, and nothing followed it, so
// "report (1)" was drawn as "(report (1". The harness lays no text out, so
// this holds the style sheet to a mark on both sides.
func TestAFileNameKeepsThePunctuationAtItsEnd(t *testing.T) {
	runFrontEnd(t, `
const css = h.css();
assert.ok(/\.rev-name::before[^{]*\{[^}]*content:\s*"\\200E"/.test(css), "nothing leads a file name");
assert.ok(/\.rev-name::after[^{]*\{[^}]*content:\s*"\\200E"/.test(css),
  "nothing follows a file name, so a bracket at its end is drawn at its start");
`)
}

// Agents go on writing while somebody reads the review, and the commit staged
// whatever was in the tree when it was pressed, listed or not. The commit says
// which files were listed, and how many more the list left out, so the server
// can refuse a tree that has moved since.
func TestACommitSaysWhichFilesItWasShown(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, omitted: 3,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }, { path: "b.go", label: "A", added: 2, removed: 0 }] });
const box = h.$("commit-message");
box.value = "webui: reviewed";
box.oninput();
h.click(h.$("rev-commit"));
const sent = h.commands().filter((c) => c.cmd === "commit").pop();
assert.deepStrictEqual(sent.files, ["a.go", "b.go"], "the commit does not say which files were listed");
assert.strictEqual(sent.omitted, 3, "the commit does not say how many files the list left out");
`)
}

// The server sends at most two thousand changed files and counts the rest,
// which the page never read: the button said "Commit 2000 files" over a
// commit of every changed file in the tree, and nothing on the dialog said
// any were missing once the server's notice had gone.
func TestTheReviewCountsTheFilesItLeavesOut(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, omitted: 2500,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }, { path: "b.go", label: "A", added: 2, removed: 0 }] });
assert.strictEqual(h.$("rev-commit").textContent, "Commit 2502 files", "the button counts only the files listed");
const note = h.$("overlay-body").querySelector("div.rev-omitted");
assert.ok(note && note.textContent.includes("2,500 more not listed"), "nothing on the dialog says files were left out");

h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false,
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
assert.strictEqual(h.$("rev-commit").textContent, "Commit 1 file");
assert.ok(!h.$("overlay-body").querySelector("div.rev-omitted"), "a list with nothing left out says some were");
`)
}

// A desktop notification was closed only when it was clicked. Answered from
// the window instead, it stayed in the Action Center, and clicking it later
// went to a pane that was no longer waiting. It goes when the window comes to
// the front, and when nothing is waiting any more.
func TestANotificationGoesOnceItIsAnswered(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.doc._hasFocus = false;
const state = (status) => fixture({ waiting: status === "waiting" ? 1 : 0,
  panes: { p1: pane("p1", { status }), p2: pane("p2") } });
h.recv(state("working"));
h.recv(state("waiting"));
assert.strictEqual(h.notifications.length, 1, "no notification was raised");
h.doc._hasFocus = true;
h.win.dispatchEvent(new h.Ev("focus"));
assert.ok(h.notifications[0].closed, "the notification outlived the window coming to the front");

h.doc._hasFocus = false;
h.recv(state("working"));
h.recv(state("waiting"));
assert.strictEqual(h.notifications.length, 2);
h.doc.hidden = false;
h.recv(state("working"));
assert.ok(h.notifications[1].closed, "the notification outlived the wait it was about");
`)
}
