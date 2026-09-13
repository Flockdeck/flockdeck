package webui

import "testing"

// The review reads the tree again by itself whenever an agent moves it, and
// the commit sent whatever the latest reading held: a file an agent added a
// moment before the press joined the commit with nobody having looked at it.
// The list the commit sends is the one looked at, stamps and all; what moved
// since is marked; and the server's refusal answers with the tree as it is,
// which is then the list to commit.
func TestARowAddedByItselfIsMarkedAndNotCommittedUnseen(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const tree = (reason, files) => ({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, reason, files });
const looked = [{ path: "a.go", label: "M", added: 1, removed: 0, stamp: "1:1:644" }];
const moved = [{ path: "a.go", label: "M", added: 2, removed: 0, stamp: "2:2:644" },
  { path: "b.go", label: "A", added: 1, removed: 0, stamp: "3:3:644" }];
const marks = () => Array.from(h.$("overlay-body").querySelectorAll("div.rev-file"))
  .map((r) => { const m = r.querySelector("span.rev-mark"); return m ? m.textContent : ""; });
const commit = () => {
  const box = h.$("commit-message");
  box.value = "reviewed";
  box.oninput();
  h.click(h.$("rev-commit"));
  return h.commands().pop();
};

h.recv(tree("asked", looked));
assert.deepStrictEqual(marks(), [""], "a file in the list somebody asked to see is marked");

// An agent wrote to a.go and added b.go, and the panel read the tree again.
h.recv(tree(undefined, moved));
assert.deepStrictEqual(marks(), ["edited", "new"], "what moved since the list was looked at is not marked");
assert.ok(/changed since you looked/.test(h.$("overlay-body").textContent), "nothing says the list has moved");
assert.deepStrictEqual(commit(), { cmd: "commit", path: "C:/repo", text: "reviewed", push: false,
  files: ["a.go"], omitted: 0, stamps: { "a.go": "1:1:644" } });

// Refused: the tree as it is comes back, still marked, and is what the next
// commit sends.
h.recv({ type: "notice", text: "nothing was committed: 2 files changed since the list was read", error: true });
h.recv(tree("refused", moved));
assert.deepStrictEqual(marks(), ["edited", "new"], "the refusal took the marks away");
assert.deepStrictEqual(commit(), { cmd: "commit", path: "C:/repo", text: "reviewed", push: false,
  files: ["a.go", "b.go"], omitted: 0, stamps: { "a.go": "2:2:644", "b.go": "3:3:644" } });

// Refresh is a list looked at afresh.
h.recv(tree("asked", moved));
assert.deepStrictEqual(marks(), ["", ""], "the rows stayed marked after a refresh");
`)
}

// One draft served every checkout, so a message written for one worktree's
// review turned up in the review of another.
func TestACommitDraftBelongsToItsCheckout(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const tree = (cwd) => ({ type: "changes", cwd, branch: "main", hasRemote: false, reason: "asked",
  files: [{ path: "a.go", label: "M", added: 1, removed: 0 }] });
h.recv(tree("C:/repo"));
const box = () => h.$("commit-message");
box().value = "for the repo";
box().oninput();

h.recv(tree("C:/other"));
assert.strictEqual(box().value, "", "the other checkout's review was given this one's draft");
box().value = "for the other";
box().oninput();

h.recv(tree("C:/repo"));
assert.strictEqual(box().value, "for the repo", "the draft written for this checkout was lost");
`)
}

// A rebase or a bisect runs on a detached HEAD. The review showed the branch it
// would go back to with "no upstream yet", and a Push that could only fail.
func TestADetachedOrRebasingCheckoutOffersNoPush(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
const files = [{ path: "a.go", label: "M", added: 1, removed: 0 }];
const branch = () => h.$("overlay-body").querySelector("span.rev-branch").textContent;

h.recv({ type: "changes", cwd: "C:/repo", branch: "main", detached: true, operation: "rebasing", hasRemote: true, files });
assert.strictEqual(branch(), "rebasing main");
assert.ok(!h.$("rev-push") && !h.$("rev-commit-push"), "a rebase in progress was offered a push");
assert.ok(!/no upstream yet/.test(h.$("overlay-body").textContent), "a rebase in progress was said to have no upstream yet");

h.recv({ type: "changes", cwd: "C:/repo", branch: "", detached: true, head: "abc1234", hasRemote: true, files });
assert.strictEqual(branch(), "detached at abc1234");
assert.ok(!h.$("rev-push") && !h.$("rev-commit-push"), "a detached HEAD was offered a push");
assert.ok(h.$("rev-commit"), "a detached HEAD cannot be committed to from the panel");
`)
}

// Arrowing down the list asked for the diff of every row it passed.
func TestTheDiffIsAskedForOnlyWhereTheSelectionStops(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/repo", branch: "main", hasRemote: false, files: [
  { path: "a.go", label: "M", added: 1, removed: 0 },
  { path: "b.go", label: "M", added: 1, removed: 0 },
  { path: "c.go", label: "M", added: 1, removed: 0 },
] });
const diffs = () => h.commands().filter((c) => c.cmd === "diff").map((c) => c.text);
const rows = h.$("overlay-body").querySelectorAll("div.rev-file");
h.click(rows[0]);
h.click(rows[1]);
h.click(rows[2]);
assert.deepStrictEqual(diffs(), ["a.go"], "a row chosen on its own was not asked about at once, or the rows passed were");
await h.sleep(250);
assert.deepStrictEqual(diffs(), ["a.go", "c.go"], "the row the selection stopped on was not asked about");
`)
}
