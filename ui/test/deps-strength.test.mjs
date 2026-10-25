// Cases for graph/deps.js that a mutation run found nothing failing on: how far a
// team's scope reaches, which tasks are idle, what a messy task list is read as,
// and how long a task is from done.
import { test } from "node:test";
import assert from "node:assert/strict";
import { depsOf, finishOf, idleOf, scopeTasks } from "../src/graph/deps.js";

const T = (id, state, depends = [], over = {}) => ({ id, title: "title " + id, state, depends, estimateMin: 0, ownerPane: "", owner: "", ...over });

test("a team's scope reaches the blockers of its blockers, and the tasks that wait on its own directly", () => {
  const tasks = [
    T("A", "planned", ["B"], { team: "x" }),
    T("B", "planned", ["C"], { team: "y" }),
    T("C", "planned", ["D"], { team: "y" }),
    T("D", "planned", [], { team: "y" }),
    T("E", "planned", ["A"], { team: "y" }),
    T("F", "planned", ["E"], { team: "y" }),
    T("G", "planned", [], { team: "y" }),
  ];
  assert.deepEqual(scopeTasks(tasks, "x").map((t) => t.id), ["A", "B", "C", "D", "E"]);
});

test("an open task with no dependencies and no owner is idle only while it is planned", () => {
  const idx = depsOf([T("plain", "planned"), T("busy", "in-progress"), T("blocked", "blocked"), T("done", "done"), T("cancelled", "cancelled")]);
  assert.deepEqual(idleOf(idx), ["plain"]);
});

test("a task someone has is not idle, by pane or by name", () => {
  const idx = depsOf([T("plain", "planned"), T("pane", "planned", [], { ownerPane: "p1" }), T("named", "planned", [], { owner: "Ada" })]);
  assert.deepEqual(idleOf(idx), ["plain"]);
});

test("a task that waits on another, or is waited on, is not idle", () => {
  const idx = depsOf([T("plain", "planned"), T("waits", "planned", ["wanted"]), T("wanted", "planned")]);
  assert.deepEqual(idleOf(idx), ["plain"]);
});

test("depsOf reads a dependency listed twice once, and leaves out one that is not an id", () => {
  const idx = depsOf([T("a", "planned"), T("b", "planned", ["a", "a", 7, null, {}, ["a"], "b", "nope"])]);
  assert.deepEqual(idx.dep.get("b"), ["a"]);
  assert.deepEqual(idx.dep.get("a"), []);
});

test("depsOf keeps the first of two tasks with one id, and skips a task with no id or none at all", () => {
  const idx = depsOf([T("a", "planned", [], { title: "first" }), T("a", "done", ["a"], { title: "second" }), { title: "nameless", state: "planned" }, T("", "planned"), null, undefined]);
  assert.deepEqual([...idx.byId.keys()], ["a"]);
  assert.equal(idx.byId.get("a").title, "first");
  assert.deepEqual(idx.dep.get("a"), []);
  assert.deepEqual([...depsOf(null).byId.keys()], []);
});

test("a finished task is 0 from done whatever it was estimated at, and whatever it waits on", () => {
  const idx = depsOf([T("open", "planned", [], { estimateMin: 50 }), T("done", "done", ["open"], { estimateMin: 40 }), T("cancelled", "cancelled", [], { estimateMin: 30 })]);
  for (const estimates of [true, false]) {
    const fin = finishOf(idx, estimates);
    assert.equal(fin.get("done"), 0, "done " + estimates);
    assert.equal(fin.get("cancelled"), 0, "cancelled " + estimates);
  }
  assert.equal(finishOf(idx, true).get("open"), 50);
});

test("a finished blocker adds nothing to the task that waited on it", () => {
  const idx = depsOf([T("gone", "done", ["open"], { estimateMin: 90 }), T("open", "planned", [], { estimateMin: 5 }), T("next", "planned", ["gone"], { estimateMin: 10 })]);
  assert.equal(finishOf(idx, true).get("next"), 10);
  assert.equal(finishOf(idx, false).get("next"), 1);
});

test("a task is from done by the longest of its blockers on top of its own time, and a shared blocker is counted once", () => {
  const idx = depsOf([
    T("a", "planned", ["b", "c"], { estimateMin: 3 }),
    T("b", "planned", ["d"], { estimateMin: 10 }),
    T("c", "planned", ["d"], { estimateMin: 20 }),
    T("d", "planned", [], { estimateMin: 5 }),
  ]);
  const fin = finishOf(idx, true);
  assert.deepEqual([...["d", "b", "c", "a"]].map((id) => fin.get(id)), [5, 15, 25, 28]);
  const units = finishOf(idx, false);
  assert.deepEqual([...["d", "b", "c", "a"]].map((id) => units.get(id)), [1, 2, 2, 3]);
});

test("a task reached first as someone's blocker has the same time as one reached as a root", () => {
  // z is listed first, so d is first met on the way down from z; y then reads it from the memo.
  const idx = depsOf([T("z", "planned", ["d"], { estimateMin: 1 }), T("y", "planned", ["d"], { estimateMin: 2 }), T("d", "planned", [], { estimateMin: 7 })]);
  const fin = finishOf(idx, true);
  assert.deepEqual([fin.get("d"), fin.get("z"), fin.get("y")], [7, 8, 9]);
});
