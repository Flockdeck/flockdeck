package webui

import "testing"

// A commit, push, pull or fetch the server will not run until somebody has seen
// what the repository would run is answered with a gitWarn. Continuing sends
// the same command again with the answer, and a second question says whether
// to remember it.
func TestAGitWarningIsAskedAboutAndTheCommandSentAgain(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const state = () => ({ type: "changes", cwd: "C:/repo", branch: "main",
  upstream: "origin/main", hasRemote: true, ahead: 1,
  files: [{ path: "app.js", label: "M", added: 9, removed: 1 }] });
h.recv(state());

// The questions are answered in turn from this list, and what was asked is kept.
let answers = [];
const asked = [];
h.win.confirm = (q) => { asked.push(q); return answers.shift(); };
const warn = (resend) => ({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "commit", resend, first: true,
  items: ["hook pre-commit (in C:/repo/.git/hooks)"], seen: "abc123",
  text: "This repository's git configuration or hooks make git run a program.\n- hook pre-commit (in C:/repo/.git/hooks)" });

// A commit: the message typed, and the list that was looked at, go again.
const box = h.$("commit-message");
box.value = "a change";
box.oninput();
h.click(h.$("rev-commit"));
const first = h.commands().pop();
assert.strictEqual(first.cmd, "commit");

answers = [true, true];
h.recv(warn("commit"));
assert.ok(asked[0].includes("hook pre-commit (in C:/repo/.git/hooks)"), "the question does not say what would run: " + asked[0]);
assert.strictEqual(asked.length, 2, "remembering was not offered");
assert.deepStrictEqual(h.commands().pop(), { ...first, gitAccept: "remember", gitSeen: "abc123" });

// Continuing, but not remembering.
h.recv(state());
answers = [true, false];
h.recv(warn("commit"));
assert.deepStrictEqual(h.commands().pop(), { ...first, gitAccept: "once", gitSeen: "abc123" });

// Cancelling sends nothing, and does not ask about remembering.
h.recv(state());
asked.length = 0;
const sent = h.commands().length;
answers = [false];
h.recv(warn("commit"));
assert.strictEqual(h.commands().length, sent, "a cancelled warning still sent the command");
assert.strictEqual(asked.length, 1);

// A push is sent again as a push, with the path it came from.
h.recv(state());
answers = [true, true];
h.recv(warn("gitPush"));
assert.deepStrictEqual(h.commands().pop(), { cmd: "gitPush", path: "C:/repo", gitAccept: "remember", gitSeen: "abc123" });
`)
}
