package webui

import "testing"

// A shortcut being recorded takes every key the window sees. Closing Settings
// with the pointer while a row was still waiting for its key left that
// recording going with nothing on screen to say so, and the whole window
// swallowed what was typed.
func TestClosingSettingsEndsAKeyRecording(t *testing.T) {
	runFrontEnd(t, relayPromise+`
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("settings-tab-keybindings"));
h.click(h.$("set-keybind-closePane"));
h.click(h.$("overlay-close"));
assert.ok(h.$("overlay").hidden, "the dialog did not close");
const before = h.commands().length;
const ev = h.key({ key: "x" });
assert.ok(!ev.defaultPrevented, "a key typed after the dialog closed was still being taken by the recording");
h.key({ key: "w", ctrlKey: true, altKey: true });
assert.strictEqual(h.commands().slice(before).filter((c) => c.cmd === "setKeybinding").length, 0,
  "a key typed after the dialog closed was saved as a shortcut");
`)
}

// A release note with a heading line that carries U+2028 (a line separator
// JavaScript's "." and "$" treat as a line end but split("\n") does not) is
// neither a heading nor a paragraph as renderNotes read it, and the loop never
// moved past it: opening the update dialog hung the window.
func TestReleaseNotesWithALineSeparatorDoNotHangTheUpdateDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9", notes: "# Title\u2028more\n\n- item" } }));
h.click(h.$("btn-update"));
assert.ok(h.$("overlay-body").textContent.includes("Title"), "the note was not drawn");
assert.ok(h.$("overlay-body").textContent.includes("item"), "the list after it was not drawn");
`)
}

// A terminal socket that had held for a good while and then dropped reset the
// retry backoff, as it should; but connectedAt stayed on that old open, so
// every failed retry after it, which never opened, looked like it followed a
// good connection too and reset the backoff again: an outage was retried
// every 250ms for as long as it lasted, instead of backing off.
func TestAPaneKeepsBackingOffWhileItsSocketCannotConnect(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const ptySockets = () => h.sockets.filter((s) => s.url.includes("/ws/pty?id=p1"));
ptySockets()[0].onopen();
await h.sleep(1100); // long enough to count as a connection that held
ptySockets()[0].close();
await h.sleep(350);
assert.strictEqual(ptySockets().length, 2, "the first retry did not come after 250ms");
ptySockets()[1].close(); // refused without ever opening
await h.sleep(350);
assert.strictEqual(ptySockets().length, 2, "a second failure retried after 250ms again instead of backing off");
await h.sleep(300);
assert.strictEqual(ptySockets().length, 3, "the backed-off retry never came");
`)
}

// Every save of a todo is answered with todoSaved, including a step's own
// text edit and a drag to reorder, and the handler reopened the Todos dialog
// for each: over whatever the person had moved on to, or over the terminals
// after the dialog was closed (an edit is saved as its field loses the
// keyboard, which closing the dialog with the pointer is what does).
func TestASavedTodoDoesNotReopenTheTodosDialog(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const todo = { id: "t1", title: "Ship it", steps: [{ id: "s1", text: "one", done: false }] };
h.click(h.$("btn-todos"));
h.recv({ type: "todos", root: "C:/repo", items: [todo] });
h.click(h.$("overlay-close"));
assert.ok(h.$("overlay").hidden, "the dialog did not close");
h.recv({ type: "todoSaved", todo });
assert.ok(h.$("overlay").hidden, "a step saved as the dialog closed brought it back");

// Still open, a save is drawn in place rather than reset to the loading line.
h.click(h.$("btn-todos"));
h.recv({ type: "todos", root: "C:/repo", items: [todo] });
const sent = h.commands().length;
h.recv({ type: "todoSaved", todo: { ...todo, title: "Ship it now" } });
assert.ok(h.$("overlay-body").textContent.includes("Ship it now"), "the saved todo was not drawn");
assert.ok(!h.$("overlay-body").textContent.includes("Reading saved todos"), "the dialog was reset to loading");
assert.strictEqual(h.commands().slice(sent).filter((c) => c.cmd === "todos").length, 0, "the list was asked for again");

// Another dialog is left alone.
h.click(h.$("btn-settings"));
h.recv({ type: "todoSaved", todo });
assert.strictEqual(h.$("overlay-title").textContent.includes("Todos"), false, "a save replaced the dialog on screen");
`)
}

// Asking for the prompt bar while it was already up put back the sentence left
// at its last close in place of the one being written.
func TestAskingForThePromptBarAgainKeepsTheSentence(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.press("promptAll");
h.$("prompt-input").value = "refactor the parser and";
h.$("btn-settings").focus(); // clicked into a pane: the bar stays up
h.press("promptAll");
assert.strictEqual(h.$("prompt-input").value, "refactor the parser and", "asking again threw away what was typed");
`)
}

// A download chosen in the version picker is answered with a notice alone, and
// nothing redrew the row, so after a failed download its button read
// "Downloading…" and stayed disabled: a retry meant closing the dialog.
func TestAFailedVersionDownloadReleasesItsButton(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
h.click(h.$("set-versions"));
h.recv({ type: "versions", items: [{ version: "v1.6.0", relation: "newer" }, { version: "v1.5.0", relation: "current" }] });
const install = () => h.$("version-install-v1.6.0");
h.click(install());
assert.deepStrictEqual(h.commands().pop(), { cmd: "installVersion", text: "v1.6.0" });
assert.ok(install().disabled, "the button did not wait for the download");
h.recv({ type: "notice", text: "the checksum did not match", error: true });
assert.ok(!install().disabled, "the button stayed disabled after the download failed");
assert.strictEqual(install().textContent, "Update to…", "the button still reads as downloading");
`)
}

// The overview's view tabs were found again after a redraw by their class and
// wording, and choosing one changes its class ("sel"), so the tab pressed
// from the keyboard was never found and the keyboard fell out of the dialog.
func TestChoosingAnAgentViewKeepsTheKeyboardOnItsTab(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("summary"));
h.recv({ type: "agents", items: [
  { paneId: "p1", tabId: "t1", root: "C:/repo", project: "repo", tab: "planner", name: "planner", status: "waiting" },
] });
const project = h.$("overlay-body").querySelectorAll("button.agent-view-tab")[1];
project.focus();
h.key({ key: "Enter" });
const now = h.doc.activeElement;
assert.ok(now && now.isConnected && now.textContent === "By project", "the keyboard left the tab that was pressed: " + (now && now.tagName));
`)
}

// A pull request being opened when the connection dropped never heard back, and
// nothing released the form: its button read "Opening…" and stayed disabled,
// and the next pull request shown was taken for the answer to it. The
// reconnect already releases what a dialog was waiting on.
func TestAPullRequestLostToADropDoesNotLeaveTheFormWaiting(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-github"));
h.recv({ type: "ghStatus", cwd: "", installed: true, loggedIn: true, account: "octocat", host: "github.com" });
h.recv({ type: "ghPRs", cwd: "C:/repo", items: [] });
const body = () => h.$("overlay-body");
const button = (text) => [...body().querySelectorAll("button")].find((b) => b.textContent === text);
h.click(button("New pull request"));
body().querySelector("input").value = "Add things";
body().querySelector("input").oninput();
h.click(button("Open pull request"));
assert.ok(button("Opening…") && button("Opening…").disabled, "the form did not wait for the reply");

h.controls().pop().onopen(); // the connection went and came back; the reply did not
assert.ok(button("Open pull request") && !button("Open pull request").disabled, "the form is still waiting for a reply that was lost");
`)
}
