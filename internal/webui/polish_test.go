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
