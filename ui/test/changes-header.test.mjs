// The Changes view across the team: the branch header, the drawer's merge gate mounted in
// the detail, and the store's fetch, pull and push for a branch.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { changesTeamOf } from "../src/lib/changesteam.js";
import { BranchHeader, distanceWords } from "../src/changes/BranchHeader.js";
import { TeamChanges } from "../src/changes/TeamChanges.js";
import { ChangesView, reviewersOf } from "../src/shell/Views.js";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", name), "utf8"));
const mixed = () => fixture("built-changes-01-team-mixed.json");
const names = { "5dc771c1": "api", "366c9143": "tax", "6fb40971": "cart", ec776492: "mgr" };
const branches = () => changesTeamOf(mixed()).branches;

test("the header says how far the branch is, with the remote actions and what the last one said", () => {
  const [api, , cart] = branches();
  assert.equal(distanceWords(api), "2 behind of origin/agent/rate-limit-api");
  assert.equal(distanceWords(cart), "level with origin/agent/cart-totals");
  const note = { action: "pull", pane: api.pane, note: { text: "Already up to date", error: false } };
  const out = renderToString(html`<${BranchHeader} branch=${api} names=${names} git=${note} onRemote=${() => {}} onDismiss=${() => {}} />`);
  assert.ok(out.includes("#412 open"));
  assert.ok(out.includes("1 file not committed"));
  assert.ok(out.includes("Already up to date"));
  assert.ok(out.includes('data-action="push"'));
  const busy = renderToString(html`<${BranchHeader} branch=${api} names=${names} git=${{ action: "pull", pane: api.pane, note: null }} onRemote=${() => {}} onDismiss=${() => {}} />`);
  assert.ok(busy.includes("Pull..."));
  assert.ok(busy.includes("disabled"));
  const other = renderToString(html`<${BranchHeader} branch=${cart} names=${names} git=${{ action: "pull", pane: api.pane, note: { text: "ELSEWHERE", error: false } }} onRemote=${() => {}} onDismiss=${() => {}} />`);
  assert.ok(!other.includes("ELSEWHERE"), "the note belongs to the branch it was for, even when another pane shares the directory");
});

test("the detail mounts the drawer's merge gate, with its buttons only for a full desk that offers the caps", () => {
  const data = changesTeamOf(mixed());
  const draw = (props) => renderToString(html`<${TeamChanges} data=${data} names=${names} reviewers=${[{ pane: "ec776492", name: "mgr" }]} ...${props} />`);
  const full = draw({ caps: ["changesTeam", "changesGate", "changesChecks"], store: {} });
  assert.ok(full.includes("Merge gate for"));
  assert.ok(full.includes("Run again"));
  assert.ok(full.includes("Approve merge") || full.includes("blocking"));
  const old = draw({ caps: ["changesTeam"] });
  assert.ok(old.includes("Read-only here"), "without the gate cap nothing is sent");
  assert.ok(!old.includes("Run again"));
  const viewer = draw({ caps: ["changesTeam", "changesGate", "changesChecks"], viewer: true });
  assert.ok(!viewer.includes("Merge gate for"), "a viewer window is told it needs full access");
});

test("ChangesView hands the store and the window's caps down", () => {
  const snap = { hello: { api: { caps: ["changesTeam", "changesGate"] } }, remote: false, connection: "open", changesTeam: changesTeamOf(mixed()), changesTeamGone: null, changesTeamResult: null, branchGit: null };
  const out = renderToString(html`<${ChangesView} nodes=${[{ pane: "5dc771c1", name: "api" }, { pane: "ec776492", name: "mgr" }]} statePanes=${{}} openPane="" snap=${snap} store=${{ send: () => true }} />`);
  assert.ok(out.includes("Merge gate for"));
  assert.ok(out.includes("Fetch") && out.includes("Push"));
});

test("a fetch, pull or push of a branch is one at a time, keeps what git said, and says when the connection dropped", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  assert.equal(store.branchRemote("push", "p1", ""), false);
  assert.equal(store.branchRemote("rebase", "p1", "/w/api"), false);
  assert.equal(store.branchRemote("pull", "p1", "/w/api"), true);
  assert.deepEqual(ws.commands("gitPull").map((c) => c.path), ["/w/api"]);
  assert.equal(store.branchRemote("push", "p2", "/w/api"), false, "one is out already");
  ws.frame({ type: "notice", text: "Already up to date", error: false });
  assert.equal(store.getState().branchGit.pane, "p1");
  assert.equal(store.getState().branchGit.note.text, "Already up to date");
  store.clearBranchGit();
  assert.equal(store.getState().branchGit, null);
  assert.equal(store.branchRemote("fetch", "p1", "/w/api"), true);
  ws.close();
  assert.match(store.getState().branchGit.note.text, /check the branch before trying again/);
  store.stop();
});

test("the answer to a Changes command is kept as it came, and a relay window sends none", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ api: { major: 1, minor: 3, caps: ["changesTeam", "changesGate"] } }));
  assert.equal(store.send({ cmd: "approveMerge", id: "6fb40971", expectSha: "b2c3", req: "gt1" }), true);
  ws.frame({ ...fixture("built-changes-11-result-head-moved.json"), req: "gt1" });
  assert.equal(store.getState().changesTeamResult.req, "gt1");
  assert.equal(store.getState().changesTeamResult.code, "headMoved");
  store.stop();
  const again = await harness();
  again.store.start();
  const rw = FakeWebSocket.last;
  rw.open();
  rw.frame(hello({ remote: true }));
  for (const c of ["approveMerge", "unapproveMerge", "runChecks", "requestReview", "commentAdd", "mergeBranch"]) assert.equal(again.store.send({ cmd: c }), false, c);
  assert.equal(again.store.branchRemote("push", "p1", "/w/api"), false);
  again.store.stop();
});

test("only agent panes can be asked to review, the manager first, and the session changes with each hello", async () => {
  const nodes = [{ pane: "sh", name: "build", shell: true }, { pane: "w1", name: "api", role: "worker" }, { pane: "m", name: "mgr", role: "manager" }];
  assert.deepEqual(reviewersOf(nodes).map((r) => r.pane), ["m", "w1"]);
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  const first = store.getState().session;
  ws.frame(hello({}));
  assert.notEqual(store.getState().session, first);
  store.stop();
});
