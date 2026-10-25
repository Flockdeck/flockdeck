// The store's side of the diff viewer: reads that do not take the command slot, the head each
// diff was read at, the list of answers, and the session that changes when the connection drops.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { applyReply, readOf, diffKey, MAX_DIFFS } from "../src/lib/changesdiffs.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
const team = () => fixture("01-team-mixed");
const hunks = () => fixture("04-diff-hunks");
const context = () => fixture("09-diff-context");

async function up(opts = {}) {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  const ws = FakeWebSocket.last;
  ws.frame(hello(opts));
  h.store.watchChangesTeam("/work/project");
  ws.frame(team());
  return { ...h, ws };
}
const API = "5dc771c1";
const FILE = "internal/limit/limit.go";
const headOf = (store, pane) => store.getState().changesTeam.branches.find((b) => b.pane === pane).headSha;

test("a diff read is answered with the head of the listing it was asked at, even when a newer listing lands first", async () => {
  const { store, ws } = await up();
  const asked = headOf(store, API);
  const req = store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE });
  assert.ok(req, "the read is sent");
  const newer = team();
  newer.rev = 8;
  newer.branches[0].headSha = "ffff0000ffff0000";
  ws.frame(newer);
  ws.frame({ ...hunks(), req });
  const entry = store.getState().changesDiffs[diffKey(API, FILE)];
  assert.ok(entry, "kept");
  assert.equal(entry.drawnSha, asked, "the head of the listing at send time, not the newer one");
  assert.notEqual(entry.drawnSha, headOf(store, API));
  assert.equal(entry.hunks.path, FILE);
});

test("unchanged lines are added to the gap they were asked for, and an answer to nothing is ignored", async () => {
  const { store, ws } = await up();
  const r1 = store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE });
  ws.frame({ ...hunks(), req: r1 });
  const r2 = store.readChangesDiff({ cmd: "diffContext", id: API, file: FILE, side: "new", from: 59, to: 61 }, "g1");
  ws.frame({ ...context(), req: r2 });
  const r3 = store.readChangesDiff({ cmd: "diffContext", id: API, file: FILE, side: "new", from: 62, to: 64 }, "g1");
  ws.frame({ ...context(), req: r3 });
  const got = store.getState().changesDiffs[diffKey(API, FILE)].context.g1;
  assert.equal(got.lines.length, 6);
  assert.equal(got.eof, false);
  const before = store.getState().changesDiffs;
  ws.frame({ ...context(), req: "nobody" });
  assert.equal(store.getState().changesDiffs, before);
});

test("a read the desk refuses as a result is the read's, not the view's last command", async () => {
  const { store, ws } = await up();
  const req = store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE });
  ws.frame({ type: "changesResult", op: "diffHunks", ok: false, code: "badRequest", error: "not one of the branch's files", req });
  const entry = store.getState().changesDiffs[diffKey(API, FILE)];
  assert.match(entry.hunks.error, /not one of the branch's files/);
  assert.equal(store.getState().changesTeamResult, null);
  assert.deepEqual(store.getState().changesTeamResults, []);
});

test("every answer to a command is kept in a list, and the latest is still changesTeamResult", async () => {
  const { store, ws } = await up();
  ws.frame({ type: "changesResult", op: "runChecks", ok: true, req: "gt1" });
  ws.frame({ type: "changesResult", op: "commentResolve", ok: true, req: "cm2" });
  const list = store.getState().changesTeamResults;
  assert.deepEqual(list.map((x) => x.req), ["gt1", "cm2"]);
  assert.equal(store.getState().changesTeamResult.req, "cm2");
});

test("a dropped connection forgets the reads in flight", async () => {
  const { store, ws } = await up();
  const read = store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE });
  ws.close();
  ws.frame({ ...hunks(), req: read });
  assert.deepEqual(store.getState().changesDiffs, {}, "an answer to a read of a connection that has gone is not taken");
});

test("a relay window, or no watch, reads nothing; other projects start clean", async () => {
  const r = await up({ remote: true });
  assert.equal(r.store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE }), "");
  const { store, ws } = await up();
  assert.equal(store.readChangesDiff({ cmd: "gitPush", id: API, file: FILE }), "", "only the two reads");
  const req = store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE });
  ws.frame({ ...hunks(), req });
  assert.equal(Object.keys(store.getState().changesDiffs).length, 1);
  store.unwatchChangesTeam();
  assert.deepEqual(store.getState().changesDiffs, {});
  assert.equal(store.readChangesDiff({ cmd: "diffHunks", id: API, file: FILE }), "", "not watching");
});

test("the model keeps the newest files only", () => {
  let diffs = {};
  for (let i = 0; i < MAX_DIFFS + 5; i++) diffs = applyReply(diffs, readOf({ cmd: "diffHunks", id: "p", file: "f" + i }, "h"), { type: "diffHunks", path: "f" + i, hunks: [] }, i + 1);
  assert.equal(Object.keys(diffs).length, MAX_DIFFS);
  assert.equal(diffs[diffKey("p", "f0")], undefined);
  assert.ok(diffs[diffKey("p", "f" + (MAX_DIFFS + 4))]);
});
