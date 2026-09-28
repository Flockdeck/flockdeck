package webui

import "testing"

// A folder that is not a repository but holds some is answered with the error
// and a list of them; picking one asks again about that repository, and the
// worktrees window keeps asking about it.
func TestGitWindowsOfAFolderOfReposOfferAPicker(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const repos = [{ name: "alpha", path: "C:/src/alpha" }, { name: "beta", path: "C:/src/beta" }];
const rows = () => Array.from(h.$("overlay-body").querySelectorAll("button.repo-choice"));

h.click(h.$("btn-changes"));
h.recv({ type: "changes", cwd: "C:/src", error: "C:/src is not a git repository", repos });
assert.deepStrictEqual(rows().map((b) => b.textContent), ["alpha", "beta"]);
h.click(rows()[1]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "changes", path: "C:/src/beta" });

h.click(h.$("btn-worktrees"));
h.recv({ type: "worktrees", root: "C:/src", error: "C:/src is not a git repository", repos });
assert.deepStrictEqual(rows().map((b) => b.textContent), ["alpha", "beta"]);
h.click(rows()[0]);
assert.deepStrictEqual(h.commands().pop(), { cmd: "worktrees", root: "C:/src/alpha" });

// Without any inside, it is only the error.
h.recv({ type: "worktrees", root: "C:/src", error: "C:/src is not a git repository" });
assert.strictEqual(rows().length, 0);
`)
}
