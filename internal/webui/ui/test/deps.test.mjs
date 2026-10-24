// What waits on what: blocked, ready, the critical path, "see above" and times, all
// worked out from the tasks' `depends` and states.
import { test } from "node:test";
import assert from "node:assert/strict";
import { IDLE, idleOf, planOf, depsOf, isReady, openBlockers, finishOf, hasEstimates, sinksOf, minutesWords, GOAL } from "../assets/graph/deps.js";

const T = (id, state, depends = [], over = {}) => ({ id, title: "title " + id, state, depends, estimateMin: 0, ownerPane: "", owner: "", ...over });
// The api-service team of prototype C: T1 merged, the rest hang from it.
const EST = { T1: 0, T2: 20, T3: 8, T4: 25, T5: 15, T6: 15, T7: 20, T8: 20, T9: 15, T10: 10, T11: 5 };
const proto = (est = true) => {
  const dep = { T1: [], T2: ["T1"], T3: [], T4: ["T2"], T5: ["T4"], T6: ["T1"], T7: ["T4", "T6"], T8: ["T4"], T9: ["T7"], T10: [], T11: ["T1"] };
  const state = { T1: "done", T2: "in-progress", T3: "in-review", T4: "assigned", T5: "assigned", T6: "in-progress", T7: "planned", T8: "assigned", T9: "planned", T10: "blocked", T11: "planned" };
  const owner = { T2: "ci", T3: "cfg", T4: "api", T5: "docs", T6: "cdx", T8: "tests", T10: "lint" };
  return Object.keys(dep).map((id) => T(id, state[id], dep[id], { estimateMin: est ? EST[id] : 0, ownerPane: owner[id] || "" }));
};

test("the critical path is the chain that finishes last, and the whole thing is 1h 20m away", () => {
  const p = planOf(proto());
  assert.deepEqual(p.critical.slice().reverse(), ["T2", "T4", "T7", "T9"]);
  assert.equal(p.total, 80);
  assert.equal(minutesWords(p.total), "+1h 20m");
  assert.equal(minutesWords(45), "+45m");
  assert.equal(minutesWords(60), "+1h");
  assert.equal(minutesWords(0), "done");
  assert.equal(p.finish("T6"), 15);
  assert.equal(p.finish("T1"), 0);
});

test("without an estimate the path is still chosen, by the longest chain, and no time is claimed", () => {
  const p = planOf(proto(false));
  assert.equal(p.estimates, false);
  assert.deepEqual(p.critical.slice().reverse(), ["T2", "T4", "T7", "T9"]);
});

test("the tree is the goal, then work by what blocks it, the last to finish first, with a task met again as see above", () => {
  const p = planOf(proto());
  const text = p.rows.map((r) => " ".repeat(r.depth * 2) + r.id + (r.dup ? "~" : "")).join("\n");
  assert.equal(p.rows[0].id, GOAL);
  assert.equal(p.rows[1].id, "T9");
  assert.ok(text.includes("    T7\n      T4\n        T2"), text);
  assert.ok(text.includes("      T6"), "T6 is under T7 beside T4");
  assert.ok(p.rows.some((r) => r.id === "T4" && r.dup), "T4 under T8 and T5 is the second time");
  for (const r of p.rows.filter((x) => x.dup)) assert.equal(r.kids, 0);
  assert.equal(new Set(p.rows.map((r) => r.key)).size, p.rows.length, "keys are unique");
  assert.ok(!p.rows.some((r) => r.id === "T1"), "a finished blocker is not drawn");
});

test("ready: planned, nobody on it, nothing open blocks it", () => {
  const p = planOf(proto());
  assert.deepEqual(p.ready, ["T11"]);
  const idx = depsOf(proto());
  assert.equal(isReady(idx, "T9"), false, "T7 is open");
  assert.equal(isReady(idx, "T4"), false, "it has an owner and is not planned");
  assert.deepEqual(openBlockers(idx, "T7").map((t) => t.id), ["T4", "T6"]);
  assert.deepEqual(openBlockers(idx, "T2"), []);
  const done = proto().map((t) => (t.id === "T2" ? { ...t, state: "done" } : t));
  assert.equal(isReady(depsOf(done), "T4"), false, "T4 is assigned, not planned");
});

test("cancelled counts as finished, and a dependency on a task that is not there or on itself is dropped", () => {
  const idx = depsOf([T("a", "planned", ["b", "ghost", "a"]), T("b", "cancelled")]);
  assert.deepEqual(idx.dep.get("a"), ["b"]);
  assert.equal(isReady(idx, "a"), true);
  assert.equal(hasEstimates(idx), false);
});

test("a loop in the tasks does not run away, and a chain of any length is walked", () => {
  const loop = planOf([T("a", "planned", ["b"]), T("b", "planned", ["a"])]);
  assert.ok(loop.rows.length >= 2 && loop.rows.length < 10);
  const chain = [];
  for (let i = 0; i < 5000; i++) chain.push(T("t" + i, "planned", i ? ["t" + (i - 1)] : []));
  const p = planOf(chain);
  assert.equal(p.rows.length, 5001);
  assert.equal(p.total, 5000, "one unit a task when no estimates");
  assert.equal(finishOf(depsOf(chain), false).get("t4999"), 5000);
  assert.deepEqual(sinksOf(depsOf(chain)), ["t4999"]);
});

test("folding a row leaves its blockers out; no tasks gives only the goal", () => {
  const all = planOf(proto());
  const folded = planOf(proto(), new Set(["T9"]));
  assert.ok(folded.rows.length < all.rows.length);
  assert.equal(folded.rows.find((r) => r.id === "T9").folded, true);
  assert.deepEqual(planOf([]).rows.map((r) => r.id), [GOAL]);
  assert.deepEqual(planOf(null).rows.map((r) => r.id), [GOAL]);
  assert.equal(planOf([]).total, 0);
});

import { buildGraphModel } from "../assets/graph/model.js";
import { loopsOf, waitsOf } from "../assets/graph/deps.js";

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, team: "t", name: pane, role: "worker", status: "working", activity: "working", waitingOn: { kind: "none" }, since: "2026-10-24T08:00:00Z", ...over });
const graph = (nodes, needs = []) => ({ type: "graph", root: "/r", project: "p", teams: [{ id: "t", manager: "m", members: nodes.map((n) => n.pane), needs }], nodes, edges: [], solo: [], soloNeeds: [] });
const waits = (ref, text = "") => ({ kind: "helpers", ref, text });

test("loopsOf finds a ring of two and of three, and leaves out a pane that only waits on a ring", () => {
  const m = buildGraphModel(graph([node("m"), node("api", { waitingOn: waits("tests", "a review") }), node("tests", { waitingOn: waits("api", "the interface") }), node("x", { waitingOn: waits("api") }),
    node("a", { waitingOn: waits("b") }), node("b", { waitingOn: waits("c") }), node("c", { waitingOn: waits("a") }), node("lone", { waitingOn: waits("nobody") })]));
  const rings = loopsOf(m).map((r) => r.slice().sort());
  assert.deepEqual(rings.sort((p, q) => p.length - q.length), [["api", "tests"], ["a", "b", "c"]]);
  assert.deepEqual(loopsOf(null), []);
});

test("waitsOf puts rings first, then what waits on you, then overlaps, then plain waits", () => {
  const needs = [
    { id: "p", kind: "permission", pane: "ci", time: "2026-10-24T08:20:00Z", text: "go test" },
    { id: "c", kind: "conflict", pane: "api", other: "cfg", time: "2026-10-24T08:21:00Z", text: "cfg/limits.yaml" },
  ];
  const m = buildGraphModel(graph([node("m"), node("ci", { activity: "waiting" }), node("cfg", { waitingOn: waits("rev", "review of T3") }), node("rev"),
    node("api", { waitingOn: waits("tests", "a review") }), node("tests", { waitingOn: waits("api", "the interface") })], needs));
  const w = waitsOf(m);
  assert.deepEqual(w.map((x) => x.kind), ["loop", "loop", "you", "overlap", "agent"]);
  assert.deepEqual(w.map((x) => x.from + ">" + x.to), ["api>tests", "tests>api", "ci>you", "api>cfg", "cfg>rev"]);
  assert.equal(w[3].why, "cfg/limits.yaml");
  assert.deepEqual(waitsOf(buildGraphModel(null)), []);
});

test("tasks nobody has, nothing waits on and nothing blocks are one folded group, last", () => {
  const many = [T("A", "in-progress", [], { ownerPane: "x" }), T("B", "planned", ["A"])];
  for (let i = 0; i < 50; i++) many.push(T("I" + i, "planned"));
  many.push(T("C", "planned", [], { owner: "someone" }));
  const idx = depsOf(many);
  assert.equal(idleOf(idx).length, 50);
  const open = planOf(many, new Set([IDLE]));
  const ids = open.rows.map((r) => r.id);
  assert.deepEqual(ids, [GOAL, "B", "A", "C", IDLE], "work first, the group last, folded");
  assert.equal(open.rows.at(-1).kids, 50);
  assert.equal(open.rows.at(-1).folded, true);
  assert.equal(planOf(many, new Set()).rows.length, 5 + 50, "unfolded it lists them under the group");
  assert.deepEqual(open.ready, [], "an idle task is in the group, not counted as ready");
  assert.equal(planOf([T("only", "planned")]).rows.length, 3, "a lone idle task is a group of one");
});

import { scopeTasks, teamOfTask } from "../assets/graph/deps.js";

test("scopeTasks keeps a team's tasks, what blocks them and what waits on them directly", () => {
  const list = [
    T("A", "planned", [], { team: "m1" }), T("B", "planned", ["A"], { team: "m1" }), T("C", "planned", ["B"], { team: "m2" }),
    T("D", "planned", ["C"], { team: "m2" }), T("E", "planned", [], { team: "m2" }), T("F", "planned", ["E"], { team: "m1" }),
  ];
  assert.deepEqual(scopeTasks(list, "m1").map((t) => t.id), ["A", "B", "C", "E", "F"], "own A B F, blocker E of F, dependent C of B; D is two away");
  assert.deepEqual(scopeTasks(list, "m3"), [], "a team with no tasks has none");
  assert.equal(scopeTasks(list, ""), list, "no team: everything");
  const bare = [T("A", "planned"), T("B", "planned", ["A"])];
  assert.equal(scopeTasks(bare, "m1"), bare, "a push that names no team is not scoped");
});

test("a task's team is its own field, else the graph team started for it", () => {
  const m = { ok: true, teams: [{ taskId: "T9", manager: "mgr9" }] };
  assert.equal(teamOfTask({ id: "T9", team: "" }, m), "mgr9");
  assert.equal(teamOfTask({ id: "T9", team: "x" }, m), "x");
  assert.equal(teamOfTask({ id: "T1", team: "" }, m), "");
  assert.equal(teamOfTask({ id: "T9", team: "" }, null), "");
});
