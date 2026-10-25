// The store's side of the task panel (requestTask and the taskGet answer) and of
// the Changes view (the changes, diff and gitWarn messages and the commands that
// answer them).
import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

async function up(opts = {}) {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  const ws = FakeWebSocket.last;
  h.store.watchGraph("C:/repo");
  ws.frame(hello(opts));
  ws.frame({ type: "graph", root: "C:/repo", project: "repo", teams: [], nodes: [], edges: [], solo: [], soloNeeds: [] });
  return { ...h, ws };
}

const task = (over = {}) => ({ id: "T9", title: "Basket", state: "in-progress", notes: 0, noteList: [], ...over });

// ------------------------------------------------------------------ tasks

test("requestTask sends taskGet with the project root and a token, and the answer with that token fills it", async () => {
  const { store, ws } = await up();
  assert.equal(store.requestTask("T9"), true);
  const [cmd] = ws.commands("taskGet");
  assert.equal(cmd.id, "T9");
  assert.equal(cmd.root, "C:/repo");
  assert.ok(cmd.req);
  assert.equal(store.getState().taskDetail.state, "loading");
  ws.frame({ type: "teamResult", op: "taskGet", ok: true, req: cmd.req, task: task() });
  assert.equal(store.getState().taskDetail.state, "ok");
  assert.equal(store.getState().taskDetail.task.title, "Basket");
});

test("an answer with another token, or for another task, is not taken", async () => {
  const { store, ws } = await up();
  store.requestTask("T9");
  const [cmd] = ws.commands("taskGet");
  ws.frame({ type: "teamResult", op: "taskGet", ok: true, req: "other", task: task() });
  assert.equal(store.getState().taskDetail.state, "loading");
  ws.frame({ type: "teamResult", op: "taskGet", ok: true, req: cmd.req, task: task({ id: "T1" }) });
  assert.equal(store.getState().taskDetail.state, "error", "a task with another id was shown");
  assert.equal(store.getState().taskDetail.task, null);
});

test("a refusal or a failed read is an error with its words, and clearTask lets go", async () => {
  const { store, ws } = await up();
  store.requestTask("T9");
  const [cmd] = ws.commands("taskGet");
  ws.frame({ type: "teamResult", op: "taskGet", ok: false, req: cmd.req, error: "no such task" });
  assert.equal(store.getState().taskDetail.state, "error");
  assert.equal(store.getState().taskDetail.error, "no such task");
  store.clearTask();
  assert.equal(store.getState().taskDetail, null);
});

test("a remote window, or one with no project, asks for nothing", async () => {
  const r = await up({ remote: true });
  assert.equal(r.store.requestTask("T9"), false);
  assert.equal(r.ws.commands("taskGet").length, 0);
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  assert.equal(h.store.requestTask("T9"), false, "asked with no project on screen");
  assert.equal(h.store.requestTask(""), false);
});

// ---------------------------------------------------------------- changes

const listing = (over = {}) => ({
  type: "changes", cwd: "C:/repo", branch: "main", upstream: "origin/main", ahead: 0, behind: 0, hasRemote: true, omitted: 0,
  files: [{ path: "a.go", status: "M", label: "modified", added: 2, removed: 1, stamp: "s1" }, { path: "b.txt", status: "?", label: "new", untracked: true, stamp: "s2" }],
  ...over,
});

test("a changes message is kept without its type and ends the busy state", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  const c = store.getState().changes;
  assert.equal(c.cwd, "C:/repo");
  assert.equal(c.type, undefined);
  assert.equal(c.files.length, 2);
  assert.equal(store.getState().changesBusy, "");
});

test("requestChanges and requestDiff send the commands, and a diff is kept for the file asked about", async () => {
  const { store, ws } = await up();
  assert.equal(store.requestDiff("a.go"), false, "asked for a diff before any listing");
  assert.equal(store.requestChanges("C:/repo/sub", true), true);
  assert.deepEqual(ws.commands("changes").map((c) => [c.path, c.follow]), [["C:/repo/sub", true]]);
  ws.frame(listing());
  assert.equal(store.requestDiff("a.go"), true);
  assert.deepEqual(ws.commands("diff").map((c) => [c.path, c.text]), [["C:/repo", "a.go"]]);
  assert.equal(store.getState().diff.loading, true);
  ws.frame({ type: "diff", cwd: "C:/repo", file: "b.txt", text: "wrong file" });
  assert.equal(store.getState().diff.loading, true, "a diff for another file was taken");
  ws.frame({ type: "diff", cwd: "C:/repo", file: "a.go", text: "@@ -1 +1 @@\n-a\n+b" });
  assert.equal(store.getState().diff.text, "@@ -1 +1 @@\n-a\n+b");
  assert.equal(store.getState().diff.loading, undefined);
});

test("a commit with an empty message is refused with a reason and sends nothing", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  assert.equal(store.commitChanges("   "), "A commit message is required");
  assert.equal(ws.commands("commit").length, 0);
  assert.equal(store.getState().changesBusy, "");
});

test("a commit sends the listed files and their stamps, and waits for the next listing", async () => {
  const { store, ws } = await up();
  ws.frame(listing({ omitted: 3 }));
  assert.equal(store.commitChanges(" Fix it ", true), "");
  const [cmd] = ws.commands("commit");
  assert.deepEqual({ path: cmd.path, text: cmd.text, push: cmd.push, files: cmd.files, omitted: cmd.omitted, stamps: cmd.stamps },
    { path: "C:/repo", text: "Fix it", push: true, files: ["a.go", "b.txt"], omitted: 3, stamps: { "a.go": "s1", "b.txt": "s2" } });
  assert.equal(store.getState().changesBusy, "commit and push");
  assert.equal(store.commitChanges("again"), "Wait for the last action to finish");
  assert.equal(ws.commands("commit").length, 1);
  ws.frame(listing({ files: [] }));
  assert.equal(store.getState().changesBusy, "");
});

test("push, pull and fetch send their commands for the listed tree, one at a time", async () => {
  const { store, ws } = await up();
  assert.equal(store.remoteChanges("push"), false, "pushed with no listing");
  ws.frame(listing());
  assert.equal(store.remoteChanges("push"), true);
  assert.equal(store.remoteChanges("pull"), false, "a second action went while one was out");
  ws.frame(listing());
  assert.equal(store.remoteChanges("pull"), true);
  ws.frame(listing());
  assert.equal(store.remoteChanges("fetch"), true);
  assert.equal(store.remoteChanges("bogus"), false);
  assert.deepEqual([ws.commands("gitPush").length, ws.commands("gitPull").length, ws.commands("gitFetch").length], [1, 1, 1]);
  assert.equal(ws.commands("gitPush")[0].path, "C:/repo");
});

test("a failed push is kept as the panel's note with git's words", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  store.remoteChanges("push");
  ws.frame({ type: "notice", text: "remote: Permission denied <b>x</b>", error: true });
  const note = store.getState().changesNote;
  assert.equal(note.text, "remote: Permission denied <b>x</b>");
  assert.equal(note.error, true);
  assert.equal(store.getState().changesBusy, "");
});

test("a follow's reply changes the listing only; a gitwarn reply keeps the question; any other reply ends what was out", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  ws.frame({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "commit", resend: "commit", intro: "x", items: ["hook"], text: "x", seen: "abc" });
  assert.ok(store.getState().gitWarn);
  ws.frame(listing({ branch: "feat", reason: "follow" }));
  assert.equal(store.getState().changes.branch, "feat", "the listing was not taken");
  assert.ok(store.getState().gitWarn, "a follow's reply took the question away");
  // The tree sent with the question keeps it open.
  ws.frame(listing({ reason: "gitwarn" }));
  assert.ok(store.getState().gitWarn, "the listing sent with the question closed it");
  // A listing that was asked for, with the question still up, closes it, as it always did.
  ws.frame(listing({ reason: "asked" }));
  assert.equal(store.getState().gitWarn, null);
});

test("a follow in flight does not end a commit or a push; the action's own reply does", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  assert.equal(store.commitChanges("Fix it", false), "");
  assert.equal(store.getState().changesBusy, "commit");
  ws.frame(listing({ reason: "follow" }));
  assert.equal(store.getState().changesBusy, "commit", "a follow ended the commit's busy state");
  ws.frame(listing({ reason: "committed" }));
  assert.equal(store.getState().changesBusy, "");
  // A push: the follow's reply comes first, then the push's own with no reason.
  assert.equal(store.remoteChanges("push"), true);
  ws.frame(listing({ reason: "follow" }));
  assert.equal(store.getState().changesBusy, "push");
  ws.frame(listing({ ahead: 0 }));
  assert.equal(store.getState().changesBusy, "", "the push's own reply left the panel busy for good");
});

test("a follow nobody has answered, then a push, then the push's reasonless reply: busy clears", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  assert.equal(store.requestChanges("C:/repo", true), true);
  assert.equal(store.remoteChanges("push"), true);
  ws.frame(listing({ ahead: 0 }));
  assert.equal(store.getState().changesBusy, "", "an unanswered follow made the push's reply count as quiet");
});

test("a refused push: the desk sends the question, then its listing, and the question stays", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  assert.equal(store.remoteChanges("push"), true);
  ws.frame({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "push", resend: "gitPush", intro: "", items: ["hook"], text: "", seen: "z" });
  ws.frame(listing({ reason: "gitwarn" }));
  assert.ok(store.getState().gitWarn, "the question was closed by the listing sent with it");
  assert.equal(store.getState().changesBusy, "");
});

test("a commit that went through and then asked about its push leaves that question open", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  assert.equal(store.commitChanges("Fix it", true), "");
  ws.frame({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "push", resend: "gitPush", intro: "", items: [], text: "", seen: "z" });
  ws.frame(listing({ reason: "committed" }));
  assert.ok(store.getState().gitWarn, "the push question was closed by the commit's listing");
  assert.equal(store.getState().changesBusy, "");
});

test("a re-read of the diff on screen keeps its text until the new one comes", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  store.requestDiff("a.go");
  ws.frame({ type: "diff", cwd: "C:/repo", file: "a.go", text: "old" });
  store.requestDiff("a.go", true);
  assert.deepEqual([store.getState().diff.text, store.getState().diff.loading], ["old", true]);
  store.requestDiff("b.txt");
  assert.equal(store.getState().diff.text, "", "another file's diff started from the old text");
});

test("a gitWarn is kept, and answering sends the same commit again with the answer and what was shown", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  store.commitChanges("Fix it");
  ws.frame({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "commit", resend: "commit", intro: "x", items: ["hook"], text: "x", seen: "abc" });
  assert.equal(store.getState().gitWarn.seen, "abc");
  assert.equal(store.getState().changesBusy, "");
  assert.equal(store.answerGitWarn("remember"), true);
  const sent = ws.commands("commit");
  assert.equal(sent.length, 2);
  assert.equal(sent[1].gitAccept, "remember");
  assert.equal(sent[1].gitSeen, "abc");
  assert.equal(sent[1].text, "Fix it");
  assert.equal(store.getState().gitWarn, null);
});

test("declining a gitWarn sends nothing, and a push warning resends the push", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  store.remoteChanges("push");
  ws.frame({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "push", resend: "gitPush", seen: "z", items: [], intro: "", text: "" });
  assert.equal(store.answerGitWarn("no"), false);
  assert.equal(ws.commands("gitPush").length, 1);
  assert.equal(store.getState().gitWarn, null);
  store.remoteChanges("push");
  ws.frame({ type: "gitWarn", cwd: "C:/repo", path: "C:/repo", action: "push", resend: "gitPush", seen: "z", items: [], intro: "", text: "" });
  store.answerGitWarn("once");
  const again = ws.commands("gitPush").at(-1);
  assert.deepEqual([again.gitAccept, again.gitSeen, again.path], ["once", "z", "C:/repo"]);
});

test("clearChanges lets go of the listing, the diff, the question and the note", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  store.requestDiff("a.go");
  store.clearChanges();
  const s = store.getState();
  assert.deepEqual([s.changes, s.diff, s.gitWarn, s.changesBusy, s.changesNote], [null, null, null, "", null]);
});

test("a diff answer for another directory is not taken for the file asked about", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  store.requestDiff("a.go");
  ws.frame({ type: "diff", cwd: "C:/other", file: "a.go", text: "wrong tree" });
  assert.equal(store.getState().diff.loading, true);
  assert.equal(store.getState().diff.text, "");
  ws.frame({ type: "diff", cwd: "C:/repo", file: "a.go", text: "right tree" });
  assert.equal(store.getState().diff.text, "right tree");
});

test("a changes reply for another directory than the one last asked about is not taken", async () => {
  const { store, ws } = await up();
  store.requestChanges("C:/repo", false);
  store.requestChanges("C:/other", false);
  ws.frame(listing({ cwd: "C:/repo", path: "C:/repo", branch: "stale" }));
  assert.equal(store.getState().changes, null, "an answer to a replaced question was kept");
  ws.frame(listing({ cwd: "C:/other", path: "C:/other", branch: "fresh" }));
  assert.equal(store.getState().changes.branch, "fresh");
  assert.equal(store.getState().changes.path, undefined);
  // A folder inside the checkout is answered with the checkout's root.
  store.requestChanges("C:/other/sub", true);
  ws.frame(listing({ cwd: "C:/other", path: "C:/other/sub", branch: "sub" }));
  assert.equal(store.getState().changes.branch, "sub");
  // The listing again after an action in the tree on screen is taken.
  ws.frame(listing({ cwd: "C:/other", path: "C:/other", branch: "after" }));
  assert.equal(store.getState().changes.branch, "after");
});

test("an error notice leaves the panel alone unless it has an action out", async () => {
  const { store, ws } = await up();
  ws.frame(listing());
  ws.frame({ type: "notice", text: "something else failed", error: true });
  assert.equal(store.getState().changesNote, null);
  assert.equal(store.remoteChanges("push"), true);
  ws.frame({ type: "notice", text: "push failed", error: true });
  assert.equal(store.getState().changesNote.text, "push failed");
  assert.equal(store.getState().changesBusy, "");
});
