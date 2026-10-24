// Dependency chips on the outline's rows: what a task waits on, from the task board.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { dependencyChipsOf } from "../src/graph/chips.js";
import { Tree } from "../src/graph/Tree.js";

const node = (pane, task) => ({ pane, kind: "terminal", children: [], depth: 0, status: "working", activity: "working", task });
const tasks = [
  { id: "T1", title: "Schema", state: "done", depends: [], estimateMin: 0 },
  { id: "T2", title: "Store", state: "in-progress", depends: ["T1"], estimateMin: 20 },
  { id: "T3", title: "API", state: "planned", depends: ["T1", "T2", "gone"], estimateMin: 0 },
  { id: "T4", title: "UI", state: "planned", depends: ["T2", "T3", "T5"], estimateMin: 45 },
  { id: "T5", title: "Docs", state: "planned", depends: [], estimateMin: 0 },
];
const n = (id) => ({ task: { id } });

test("a row waits on the open tasks it depends on, not the finished ones", () => {
  assert.deepEqual(dependencyChipsOf(tasks, n("T3")).map((c) => c.text), ["blocked by T2"]);
  assert.deepEqual(dependencyChipsOf(tasks, n("T2")).map((c) => c.text), ["est 20m"]);
});

test("more than two blockers are counted, and the tooltip lists them all", () => {
  const [blocked, est] = dependencyChipsOf(tasks, n("T4"));
  assert.equal(blocked.text, "blocked by T2, T3 +1");
  assert.match(blocked.title, /T5 Docs \(planned\)/);
  assert.equal(est.text, "est 45m");
});

test("nothing for a finished task, an unknown task, no task or no board", () => {
  assert.deepEqual(dependencyChipsOf(tasks, n("T1")), []);
  assert.deepEqual(dependencyChipsOf(tasks, n("T9")), []);
  assert.deepEqual(dependencyChipsOf(tasks, { pane: "p" }), []);
  assert.deepEqual(dependencyChipsOf(null, n("T3")), []);
  assert.deepEqual(dependencyChipsOf([], n("T3")), []);
});

test("the tree draws the chip on the row of the pane that has the task", () => {
  const model = buildGraphModel({ type: "graph", nodes: [node("p1", { id: "T3", title: "API" })], teams: [], solo: ["p1"] });
  const on = renderToString(html`<${Tree} model=${model} tasks=${tasks} />`);
  assert.match(on, /tree-chip--blocked[^>]*>blocked by T2</);
  assert.doesNotMatch(renderToString(html`<${Tree} model=${model} />`), /blocked by/);
});
