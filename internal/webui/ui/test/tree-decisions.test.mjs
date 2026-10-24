// The decision blocks under the outline's rows: after the row of their agent, in a
// wrapper that is not a treeitem, and not repeated for the row whose drawer is open.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { Tree } from "../assets/graph/Tree.js";

const n = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, team: "t", name: pane, role: "worker", status: "working", activity: "working", live: "go test", waitingOn: { kind: "none" }, children: [], ...over });
const needs = [
  { id: "p", kind: "permission", pane: "api", time: "2026-10-24T08:20:00Z", text: "go test ./...", task: "T4" },
  { id: "c", kind: "conflict", pane: "api", other: "cfg", time: "2026-10-24T08:21:00Z", text: "cfg/limits.yaml" },
];
const model = () => buildGraphModel({
  type: "graph", root: "/r", project: "p",
  teams: [{ id: "t", manager: "m", goal: "g", members: ["m", "api", "cfg"], needs }],
  nodes: [n("m", { role: "manager", children: ["api", "cfg"] }), n("api", { parent: "m", depth: 1 }), n("cfg", { parent: "m", depth: 1 })],
  edges: [], solo: [], soloNeeds: [],
});
const ctx = { manager: { pane: "m", name: "m" }, store: { send: () => true }, onOpen() {} };
const draw = (props) => renderToString(html`<${Tree} model=${model()} ...${props} />`);

test("the blocks follow the row of their agent and sit outside the treeitems", () => {
  const out = draw({ decisions: ctx });
  const at = (s) => out.indexOf(s);
  assert.ok(at('data-pane="api"') > -1);
  assert.ok(at('data-decision="permission"') > at('role="treeitem"'), "after a row");
  assert.match(out, /<div class="tree-decs" role="presentation" data-pane="api">/);
  assert.equal((out.match(/data-decision="conflict"/g) || []).length, 1, "a conflict under its first pane only");
  assert.ok(!/tree-decs[^>]*data-pane="cfg"/.test(out));
});

test("the open row does not repeat its permission, but keeps the conflict", () => {
  const out = draw({ decisions: ctx, open: "api" });
  assert.ok(!out.includes('data-decision="permission"'));
  assert.ok(out.includes('data-decision="conflict"'));
});

test("without a ctx the outline draws no blocks", () => {
  assert.ok(!draw({}).includes("data-decision"));
});
