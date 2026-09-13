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
const tab = h.$("tabs").children[0];
const close = tab.querySelector(".close");
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
