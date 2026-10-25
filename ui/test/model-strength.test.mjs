// Cases for graph/model.js that a mutation run found nothing failing on: the
// rounding of the quiet time, the times it will not believe, the state it falls
// back to, the pane that needs the person, the roll-up's guards, the label's
// fallbacks and the task of a team.
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildGraphModel, emptyModel, stuckOf, needsYouOf, rollupOf, labelOf, shownName, taskOfTeam, STUCK_MS } from "../src/graph/model.js";

const n = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, team: "t", name: pane, role: "worker", status: "working", activity: "working", live: "", waitingOn: { kind: "none" }, children: [], ...over });
const graph = (nodes, over = {}) => ({
  type: "graph",
  root: "/r",
  project: "p",
  teams: [{ id: "t", manager: nodes[0].pane, goal: "g", members: nodes.map((x) => x.pane), needs: [], ...over }],
  nodes,
  edges: [],
  solo: [],
  soloNeeds: [],
});
const NOW = Date.parse("2026-10-24T12:00:00Z");
const agoIso = (ms) => new Date(NOW - ms).toISOString();
// A frame as the team's tree holds it, for the tests that build a tree the model would not.
const frame = (pane, ...children) => ({ pane, depth: 0, children });
const one = (over) => buildGraphModel(graph([n("a", over)])).byPane.get("a");

test("stuckOf counts whole minutes and rounds down", () => {
  const node = one({ lastOutput: agoIso((12 * 60 + 50) * 1000) });
  assert.deepEqual(stuckOf(node, NOW), { minutes: 12 });
});

test("stuckOf waits for STUCK_MS exactly", () => {
  assert.equal(stuckOf(one({ lastOutput: agoIso(STUCK_MS - 1) }), NOW), null);
  assert.deepEqual(stuckOf(one({ lastOutput: agoIso(STUCK_MS) }), NOW), { minutes: 10 });
});

test("stuckOf does not believe a time before 2000 or one that is not a time", () => {
  for (const lastOutput of ["1970-01-01T00:00:00Z", "1999-12-31T23:59:59Z", "garbage"]) {
    assert.deepEqual(stuckOf(one({ lastOutput, stuck: true }), NOW), { minutes: 0 }, lastOutput);
    assert.equal(stuckOf(one({ lastOutput }), NOW), null, lastOutput);
  }
});

test("stuckOf believes the first moment of 2000", () => {
  const since = Date.UTC(2000, 0, 1);
  assert.deepEqual(stuckOf(one({ lastOutput: "2000-01-01T00:00:00Z" }), since + 11 * 60000), { minutes: 11 });
});

test("stuckOf is null for a missing, structured or not working pane", () => {
  assert.equal(stuckOf(null, NOW), null);
  assert.equal(stuckOf(one({ kind: "structured", lastOutput: agoIso(STUCK_MS * 3) }), NOW), null);
  assert.equal(stuckOf(one({ activity: "idle", status: "idle", lastOutput: agoIso(STUCK_MS * 3) }), NOW), null);
});

test("an activity of unknown falls back to the status", () => {
  const old = agoIso(STUCK_MS * 2);
  assert.deepEqual(stuckOf(one({ activity: "unknown", status: "working", lastOutput: old }), NOW), { minutes: 20 });
  assert.deepEqual(stuckOf(one({ activity: "", status: "working", lastOutput: old }), NOW), { minutes: 20 });
  assert.equal(stuckOf(one({ activity: "unknown", status: "idle", lastOutput: old }), NOW), null);
  // A real activity wins over the status.
  assert.equal(stuckOf(one({ activity: "idle", status: "working", lastOutput: old }), NOW), null);
});

test("needsYouOf is false for a pane that is not there and true for the states that wait or ended", () => {
  const m = buildGraphModel(
    graph([
      n("m", { children: ["w", "b", "f", "x", "ok", "i"] }),
      n("w", { parent: "m", activity: "waiting" }),
      n("b", { parent: "m", activity: "blocked" }),
      n("f", { parent: "m", activity: "failed" }),
      n("x", { parent: "m", activity: "exited" }),
      n("ok", { parent: "m" }),
      n("i", { parent: "m", activity: "idle", status: "idle" }),
    ]),
  );
  assert.equal(needsYouOf(m, "nobody"), false);
  for (const id of ["w", "b", "f", "x"]) assert.equal(needsYouOf(m, id), true, id);
  for (const id of ["m", "ok", "i"]) assert.equal(needsYouOf(m, id), false, id);
});

test("needsYouOf counts a need that is not quiet and not one that is", () => {
  const need = (kind, pane) => ({ id: kind, kind, pane, time: "2026-10-24T08:00:00Z", text: "" });
  const m = buildGraphModel(graph([n("m", { children: ["p", "q"] }), n("p", { parent: "m" }), n("q", { parent: "m" })], { needs: [need("permission", "p"), need("stuck", "q"), need("stale-unread", "m")] }));
  assert.equal(needsYouOf(m, "p"), true);
  assert.equal(needsYouOf(m, "q"), false);
  assert.equal(needsYouOf(m, "m"), false);
});

// buildGraphModel's treeOf already drops a repeat, so these two hand the roll-up a tree
// that holds one, which only rollupOf's own guard can count once. (A true loop is left
// out: without the guard it would not stop.)
test("rollupOf counts a pane that two branches hold once", () => {
  const m = buildGraphModel(graph([n("m"), n("a"), n("b"), n("c")]));
  m.teams[0].tree = frame("m", frame("a", frame("c")), frame("b", frame("c")));
  assert.deepEqual(rollupOf(m, "m"), { below: 3, needYou: 0 });
});

test("rollupOf counts a pane below once however it is reached, and the ones that need the person", () => {
  const m = buildGraphModel(graph([n("m"), n("a"), n("b", { activity: "waiting" }), n("c", { activity: "blocked" })]));
  m.teams[0].tree = frame("m", frame("a", frame("c")), frame("b", frame("c")));
  assert.deepEqual(rollupOf(m, "m"), { below: 3, needYou: 2 });
});

test("rollupOf does not count the pane itself when a branch below holds it again", () => {
  const m = buildGraphModel(graph([n("m"), n("a")]));
  m.teams[0].tree = frame("m", frame("a", frame("m")));
  assert.deepEqual(rollupOf(m, "m"), { below: 1, needYou: 0 });
});

test("rollupOf leaves out a child that is not a pane of the model", () => {
  const m = buildGraphModel(graph([n("m", { children: ["a", "gone"] }), n("a", { parent: "m" }), n("gone", { parent: "m" })]));
  // The tree holds both; the model then loses one before anything reads it.
  m.byPane.delete("gone");
  assert.deepEqual(rollupOf(m, "m"), { below: 1, needYou: 0 });
});

test("rollupOf gives a pane that is not a node nothing", () => {
  assert.deepEqual(rollupOf(buildGraphModel(graph([n("m")])), "nobody"), { below: 0, needYou: 0 });
});

test("rollupOf uses the desk's figures only when the tree has nothing below", () => {
  const lone = buildGraphModel(graph([n("m", { below: 9, needYou: 2 })]));
  assert.deepEqual(rollupOf(lone, "m"), { below: 9, needYou: 2 });
  const tree = buildGraphModel(graph([n("m", { children: ["a"], below: 9, needYou: 2 }), n("a", { parent: "m" })]));
  assert.deepEqual(rollupOf(tree, "m"), { below: 1, needYou: 0 });
});

test("rollupOf reads a team without a tree, and keeps the first tree of a pane two teams hold", () => {
  const msg = graph([n("m", { children: ["a"] }), n("a", { parent: "m" })]);
  msg.teams.push({ id: "orphan", manager: "ghost", goal: "", members: ["a"], needs: [] }, { id: "again", manager: "m", goal: "", members: ["m", "a"], needs: [] });
  const m = buildGraphModel(msg);
  assert.equal(m.teams[1].tree, null);
  // The second team holds m with nothing below it; the first team's m, with a, is the one that counts.
  m.teams[2].tree = frame("m");
  assert.deepEqual(rollupOf(m, "m"), { below: 1, needYou: 0 });
});

test("labelOf shows the pane id for a pane with no name", () => {
  const m = buildGraphModel(graph([n("p1", { name: "" })]));
  assert.equal(labelOf(m, "p1"), "p1");
  assert.equal(shownName(m.byPane.get("p1")), "p1");
});

test("two panes with no name are told apart by their ids, not taken for one shared name", () => {
  const m = buildGraphModel(graph([n("p1", { name: "" }), n("p2", { name: "", parent: "p1" })]));
  assert.equal(labelOf(m, "p1"), "p1");
  assert.equal(labelOf(m, "p2"), "p2");
});

test("labelOf gives panes with the same name and nothing else to tell them by the start of their ids", () => {
  const m = buildGraphModel(graph([n("aaaa1111", { name: "dev", role: "" }), n("bbbb2222", { name: "dev", role: "", parent: "aaaa1111" })]));
  assert.equal(labelOf(m, "aaaa1111"), "dev · aaaa");
  assert.equal(labelOf(m, "bbbb2222"), "dev · bbbb");
});

test("labelOf tells panes of one name apart by role and task title", () => {
  const m = buildGraphModel(
    graph([
      n("p1", { name: "dev", role: "worker", task: { id: "T4", title: "Limiter", state: "doing" } }),
      n("p2", { name: "dev", role: "reviewer", parent: "p1" }),
      n("p3", { name: "solo" }),
    ]),
  );
  assert.equal(labelOf(m, "p1"), "worker · Limiter");
  assert.equal(labelOf(m, "p2"), "reviewer · dev");
  assert.equal(labelOf(m, "p3"), "solo");
});

// The labels are worked out once for each model on purpose: rows ask for one per render, and a
// label that changed under a row would move it. Do not remove the cache and this test together.
test("labelOf keeps the first answer for a model", () => {
  const m = buildGraphModel(graph([n("p1", { name: "one" })]));
  assert.equal(labelOf(m, "p1"), "one");
  m.byPane.get("p1").name = "changed";
  m.nodes[0].name = "changed";
  assert.equal(labelOf(m, "p1"), "one");
});

test("labelOf gives the pane id for a pane the nodes do not list, a pane that is not there and no model", () => {
  const m = buildGraphModel(graph([n("p1")]));
  m.byPane.set("ghost", n("ghost", { name: "spirit" }));
  assert.equal(labelOf(m, "ghost"), "ghost");
  assert.equal(labelOf(m, "nobody"), "nobody");
  assert.equal(labelOf(null, "x"), "x");
});

test("taskOfTeam is null without a model that is ok, a team or a task id", () => {
  const team = { taskId: "T4", manager: "m" };
  assert.equal(taskOfTeam(emptyModel(), team), null);
  const m = buildGraphModel(graph([n("m")]));
  assert.equal(taskOfTeam(null, team), null);
  assert.equal(taskOfTeam(m, null), null);
  assert.equal(taskOfTeam(m, { manager: "m", taskId: "" }), null);
});

test("taskOfTeam reads the title from a helper when the manager carries another task", () => {
  const m = buildGraphModel(
    graph([n("m", { role: "manager", children: ["h"], task: { id: "T9", title: "Other", state: "doing" } }), n("h", { parent: "m", task: { id: "T4", title: "Limiter", state: "doing" } })]),
  );
  assert.deepEqual(taskOfTeam(m, { taskId: "T4", manager: "m" }), { id: "T4", title: "Limiter" });
});

test("taskOfTeam prefers the manager's title to a helper's", () => {
  // The helper is listed first, so only the step that reads the manager's own pane gives its title.
  const m = buildGraphModel(
    graph([n("h", { parent: "m", task: { id: "T4", title: "Helper's", state: "doing" } }), n("m", { task: { id: "T4", title: "Manager's", state: "doing" } })], { manager: "m" }),
  );
  assert.deepEqual(taskOfTeam(m, { taskId: "T4", manager: "m" }), { id: "T4", title: "Manager's" });
});

test("taskOfTeam prefers a task card of the same id, and passes by a card of another id or with no title", () => {
  const m = buildGraphModel(graph([n("m", { task: { id: "T4", title: "Pane's", state: "doing" } })]));
  const team = { taskId: "T4", manager: "m" };
  assert.equal(taskOfTeam(m, team, [{ id: "T4", title: "Card's" }]).title, "Card's");
  assert.equal(taskOfTeam(m, team, [null, { id: "T4", title: "Card's" }]).title, "Card's");
  assert.equal(taskOfTeam(m, team, [{ id: "T5", title: "Not this" }]).title, "Pane's");
  assert.equal(taskOfTeam(m, team, [{ id: "T4", title: "" }]).title, "Pane's");
});

test("taskOfTeam has an empty title when nothing carries the task", () => {
  const m = buildGraphModel(graph([n("m", { task: { id: "T9", title: "Other", state: "doing" } })]));
  assert.deepEqual(taskOfTeam(m, { taskId: "T4", manager: "m" }), { id: "T4", title: "" });
  assert.deepEqual(taskOfTeam(m, { taskId: "T4", manager: "m" }, "not a list"), { id: "T4", title: "" });
});
