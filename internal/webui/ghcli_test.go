package webui

import "testing"

// TestGitHubSettingsWalksInstallAndSignIn covers requirement one end to end
// on the front end: gh missing, offered to install, streamed while it
// installs, then offered a sign-in that streams the one-time code.
func TestGitHubSettingsWalksInstallAndSignIn(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));

// Jump straight to the section rather than search for it: the section
// list is covered elsewhere, and this case is about what is in it.
const tab = [...h.$("settings-tabs").querySelectorAll("button")].find((b) => b.textContent === "GitHub");
assert.ok(tab, "the GitHub section is listed");
h.click(tab);
assert.deepStrictEqual(h.commands().pop(), { cmd: "ghStatus", path: "" });

h.recv({ type: "ghStatus", cwd: "", installed: false, installManager: "winget", installUrl: "https://cli.github.com" });
const host = () => h.$("settings-host");
assert.ok(/not on this machine/.test(host().textContent), "says gh is missing");
const install = h.$("gh-install");
assert.ok(install, "an install button is offered");
assert.strictEqual(install.textContent, "Install with winget");
h.click(install);
assert.deepStrictEqual(h.commands().pop(), { cmd: "ghInstall" });

h.recv({ type: "ghProgress", kind: "install", line: "Downloading gh 100%, done." });
assert.ok(/Downloading gh 100%, done\./.test(host().textContent), "the install's own output is shown as it runs");

h.recv({ type: "ghProgress", kind: "install", done: true });
h.recv({ type: "ghStatus", cwd: "", installed: true, loggedIn: false });
assert.ok(/not signed in/.test(host().textContent), "installed, not yet signed in");
const signin = h.$("gh-signin");
assert.ok(signin, "a sign-in button is offered once gh is installed");
h.click(signin);
assert.deepStrictEqual(h.commands().pop(), { cmd: "ghLogin" });

h.recv({ type: "ghProgress", kind: "login", code: "1234-ABCD" });
assert.ok(/1234-ABCD/.test(host().textContent), "the one-time code is shown as soon as gh has one");

h.recv({ type: "ghProgress", kind: "login", done: true });
h.recv({ type: "ghStatus", cwd: "", installed: true, loggedIn: true, account: "octocat", host: "github.com" });
assert.ok(/octocat/.test(host().textContent), "signed in as octocat");
`)
}

// TestGitHubPanelListsOpensAndComments covers requirement two: a pull
// request is listed, opened, read and commented on without leaving the
// dialog the panel opened in.
func TestGitHubPanelListsOpensAndComments(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-github"));
const cmds = h.commands();
assert.deepStrictEqual(cmds[0], { cmd: "ghStatus", path: "" });
assert.deepStrictEqual(cmds[1], { cmd: "ghPRs", path: "", ghState: "" });

h.recv({ type: "ghStatus", cwd: "", installed: true, loggedIn: true, account: "octocat", host: "github.com" });
h.recv({
  type: "ghPRs", cwd: "C:/repo",
  items: [{ number: 5, title: "Add gh support", state: "OPEN", isDraft: false,
    author: { login: "octocat" }, updatedAt: "2024-01-01T00:00:00Z" }],
});
const body = () => h.$("overlay-body");
assert.ok(/#5 Add gh support/.test(body().textContent), "the open pull request is listed");

const open = [...body().querySelectorAll("button.gh-open")].find((b) => /#5/.test(b.textContent));
assert.ok(open, "the row opens the pull request");
h.click(open);
assert.deepStrictEqual(h.commands().pop(), { cmd: "ghPR", path: "C:/repo", ghNumber: 5 });

h.recv({
  type: "ghPR",
  item: { number: 5, title: "Add gh support", body: "please review", state: "OPEN", isDraft: false,
    author: { login: "octocat" }, url: "https://github.com/acme/widgets/pull/5",
    headRefName: "feature", baseRefName: "main", comments: [] },
});
assert.ok(/please review/.test(body().textContent), "the pull request's own body is shown");

const box = [...body().querySelectorAll("textarea")][0];
box.value = "looks good";
box.oninput();
const post = [...body().querySelectorAll("button")].find((b) => b.textContent === "Comment");
h.click(post);
assert.deepStrictEqual(h.commands().pop(),
  { cmd: "ghPRComment", path: "C:/repo", ghNumber: 5, ghBody: "looks good" });

h.recv({
  type: "ghPR",
  item: { number: 5, title: "Add gh support", body: "please review", state: "OPEN", isDraft: false,
    author: { login: "octocat" }, comments: [{ author: { login: "reviewer" }, body: "looks good", createdAt: "2024-01-02T00:00:00Z" }] },
});
assert.ok(/looks good/.test(body().textContent), "the panel re-reads the pull request after commenting");
`)
}

// TestGitHubChecksTabShowsCIStatus covers requirement three: CI status for
// the current branch and its pull request, drawn from a checks summary and
// the branch's own recent Actions runs.
func TestGitHubChecksTabShowsCIStatus(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-github"));
h.recv({ type: "ghStatus", cwd: "", installed: true, loggedIn: true, account: "octocat", host: "github.com" });
h.recv({ type: "ghPRs", cwd: "C:/repo", items: [] });

const body = () => h.$("overlay-body");
const tabs = () => [...body().querySelectorAll("button.gh-tab")];
const checksTab = tabs().find((b) => b.textContent === "Checks");
h.click(checksTab);
assert.deepStrictEqual(h.commands().pop(), { cmd: "ghChecks", path: "C:/repo" });

h.recv({
  type: "ghChecks", cwd: "C:/repo", branch: "feature",
  pr: { number: 5, title: "Add gh support" },
  checks: [{ name: "build", state: "SUCCESS", bucket: "pass" }, { name: "lint", state: "FAILURE", bucket: "fail" }],
  summary: { pending: 0, passing: 1, failing: 1, overall: "failing" },
  runs: [{ workflowName: "CI", displayTitle: "Add gh support", status: "completed", conclusion: "failure",
    headBranch: "feature", url: "https://github.com/acme/widgets/actions/runs/1", updatedAt: "2024-01-01T00:00:00Z" }],
});
assert.ok(/feature/.test(body().textContent), "the branch is named");
assert.ok(/1 passing/.test(body().textContent) && /1 failing/.test(body().textContent), "the checks are summarised");
assert.ok(/CI/.test(body().textContent), "the branch's own run history is shown");
`)
}
