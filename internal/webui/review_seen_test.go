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

