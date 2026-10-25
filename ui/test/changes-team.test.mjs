// The Changes view across the team: the model of a changesTeam push, the store's watch,
// and the list drawn from the built fixtures (api/fixtures/built-changes-*.json).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { attentionOf, changesTeamOf, conflictPanes, isStale, ladderOf, sortBranches, totalsOf } from "../src/lib/changesteam.js";
import { ChangesList } from "../src/changes/ChangesList.js";
import { TeamChanges } from "../src/changes/TeamChanges.js";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", name), "utf8"));
const mixed = () => fixture("built-changes-01-team-mixed.json");
const names = { "5dc771c1": "api", "366c9143": "tax", "6fb40971": "cart", ec776492: "mgr", "1ce36fdc": "search", dccc34a5: "migrate" };

test("a push is read defensively and an unreadable tree keeps its error", () => {
  assert.equal(changesTeamOf(null), null);
  const t = changesTeamOf(mixed());
  assert.equal(t.rev, 7);
  assert.deepEqual(t.partial, { read: 4, total: 5 });
  assert.equal(t.branches.length, 5);
  const gone = t.branches.find((b) => b.pane === "dccc34a5");
  assert.equal(gone.error, "the directory is gone");
  const odd = changesTeamOf({ rev: "x", branches: [null, { pane: 4 }, { pane: "p", files: "x", checks: [{ state: "bogus" }], gate: { state: "nope", blockers: [3] } }] });
  assert.equal(odd.rev, 0);
  assert.equal(odd.branches.length, 1);
  assert.deepEqual(odd.branches[0].files, []);
  assert.deepEqual(odd.branches[0].checks.map((c) => c.state), ["unknown"], "a state this page does not know is kept and not taken for fine");
  assert.equal(odd.branches[0].gate.state, "blocked");
});

test("branches that need a person come first, then the ready ones, and each ranks for its reason", () => {
  const t = changesTeamOf(mixed());
  const by = (p) => t.branches.find((b) => b.pane === p);
  assert.equal(attentionOf(by("5dc771c1")).key, "conflict");
  assert.equal(attentionOf(by("366c9143")).key, "review");
  assert.equal(attentionOf(by("6fb40971")).key, "ready");
  assert.equal(attentionOf(by("ec776492")).key, "rest");
  assert.deepEqual(sortBranches(t.branches).map((b) => b.pane), ["5dc771c1", "366c9143", "6fb40971", "ec776492", "dccc34a5"]);
  assert.deepEqual(conflictPanes(by("5dc771c1")), ["1ce36fdc"]);
  const totals = totalsOf(t.branches);
  assert.equal(totals.branches, 4, "a tree that could not be read is not counted");
  assert.equal(totals.attention, 2);
});

test("the ladder follows how far the work has gone", () => {
  const t = changesTeamOf(mixed());
  const by = (p) => t.branches.find((b) => b.pane === p);
  assert.deepEqual(ladderOf(by("366c9143")), ["done", "now", "todo", "todo"], "committed, three ahead, no upstream");
  assert.deepEqual(ladderOf(by("6fb40971")), ["done", "done", "now", "todo"], "pushed");
  assert.deepEqual(ladderOf(by("ec776492")), ["now", "todo", "todo", "todo"], "the base branch itself");
});

test("an old revision of the same project is skipped", () => {
  const cur = { root: "/r", rev: 7 };
  assert.equal(isStale(cur, { root: "/r", rev: 7 }), true);
  assert.equal(isStale(cur, { root: "/r", rev: 6 }), true);
  assert.equal(isStale(cur, { root: "/r", rev: 8 }), false);
  assert.equal(isStale(cur, { root: "/other", rev: 1 }), false);
  assert.equal(isStale(cur, { root: "/r", rev: 0 }), false, "a push with no revision is always drawn");
  assert.equal(isStale(null, { root: "/r", rev: 1 }), false);
});

test("the list draws a row per branch, attention first, with the words and no markup from the data", () => {
  const t = changesTeamOf(mixed());
  t.branches[0].branch = '<img src=x onerror="alert(1)">';
  const out = renderToString(html`<${ChangesList} branches=${t.branches} names=${names} selected="6fb40971" onPick=${() => {}} />`);
  assert.ok(!out.includes("<img"), "the branch name is text");
  assert.ok(out.includes("Conflicts with search"));
  assert.ok(out.includes("Changes requested"));
  assert.ok(out.includes("Ready"));
  assert.ok(out.includes("the directory is gone"));
  assert.ok(out.includes("4 branches"));
  assert.ok(out.includes("2 need attention"));
  assert.ok(out.includes('aria-current="true"'));
  assert.ok(out.indexOf("api") < out.indexOf("tax") && out.indexOf("tax") < out.indexOf("cart"));
});

test("the view says what it is waiting for, what it was refused and what it found", () => {
  const draw = (props) => renderToString(html`<${TeamChanges} names=${names} ...${props} />`);
  assert.ok(draw({ data: null }).includes("Reading what changed"));
  assert.ok(draw({ data: null, remote: true }).includes("not from a window reached through the relay"));
  assert.ok(draw({ data: null, gone: { error: "project closed" } }).includes("project closed"));
  assert.ok(draw({ data: changesTeamOf(fixture("built-changes-02-team-empty.json")) }).includes("No agent has a branch yet"));
  assert.ok(draw({ data: changesTeamOf(mixed()) }).includes("Read 4 of 5 working trees."));
});

test("the store watches the branches, drops old pushes, and sends nothing from a relay window", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  assert.equal(store.send({ cmd: "changesWatch" }), false, "nothing is sent before the hello");
  ws.frame(hello({ api: { major: 1, minor: 3, caps: ["changesTeam"] } }));
  assert.equal(store.watchChangesTeam("/work/project"), true);
  assert.deepEqual(ws.commands("changesWatch").map((c) => c.root), ["/work/project"]);
  ws.frame(mixed());
  assert.equal(store.getState().changesTeam.branches.length, 5);
  ws.frame({ ...mixed(), rev: 6, branches: [] });
  assert.equal(store.getState().changesTeam.branches.length, 5, "an older revision is not drawn");
  ws.frame({ ...mixed(), rev: 8, branches: [] });
  assert.equal(store.getState().changesTeam.branches.length, 0);
  ws.frame(hello({ api: { major: 1, minor: 3, caps: ["changesTeam"] } }));
  assert.equal(ws.commands("changesWatch").length, 2, "a live watch is sent again with the next hello");
  ws.frame({ type: "changesResult", op: "changesWatch", ok: false, code: "badRequest", error: "no such project" });
  assert.equal(store.getState().changesTeamGone.error, "no such project");
  store.watchChangesTeam("/work/other");
  assert.equal(store.getState().changesTeam, null, "branches of another project are not kept");
  store.unwatchChangesTeam();
  assert.equal(ws.commands("changesUnwatch").length, 1);
  ws.frame(mixed());
  assert.equal(store.getState().changesTeam, null, "a push after the watch was given up is not drawn");
  store.stop();
});

test("a relay window sends none of the Changes commands and says so", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ remote: true }));
  for (const c of ["changesWatch", "changesUnwatch", "changesSnapshot", "filesPage", "diffHunks", "diffContext", "commentAdd", "commentResolve", "commentReopen", "runChecks", "requestReview", "submitReview", "approveMerge", "unapproveMerge", "mergeBranch"]) {
    assert.equal(store.send({ cmd: c }), false, c);
  }
  assert.equal(store.watchChangesTeam("/work/project"), false);
  assert.equal(store.getState().changesTeamRefused, true);
  assert.equal(ws.commands("changesWatch").length, 0);
  ws.frame(mixed());
  assert.equal(store.getState().changesTeam, null);
  store.stop();
});
