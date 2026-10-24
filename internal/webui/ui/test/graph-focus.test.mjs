// What the graph shows around one pane: its lines only, the panes it has no line
// to faded, the pane it waits on outlined, and a faint count of messages on each box.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { layoutGraph } from "../assets/graph/layout.js";
import { Graph } from "../assets/graph/Graph.js";
import { countsLine } from "../assets/graph/Node.js";
import { Edges, countsOf, edgesOf, focusOf } from "../assets/graph/Edges.js";

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "idle", activity: "idle", waitingOn: { kind: "none" }, ...over });
const msg = (from, to, delivery, id = 1) => ({ kind: "message", from, to, id, time: "2026-10-21T08:00:00Z", text: "hello", delivery });
const graph = (nodes, edges = []) => ({ type: "graph", root: "/r", project: "p", teams: [], nodes, edges, solo: nodes.map((n) => n.pane), soloNeeds: [] });

// a tells b, c tells a (not told yet), d waits on b, e is alone.
const M = graph(
  [node("a"), node("b"), node("c"), node("d", { activity: "waiting", waitingOn: { kind: "helpers", ref: "b", text: "the review" } }), node("e")],
  [msg("a", "b", "read", 1), msg("c", "a", "queued", 2)],
);

function parts() {
  const model = buildGraphModel(M);
  const lay = layoutGraph(model);
  return { model, lay, edges: edgesOf(model, lay) };
}

test("focusOf names the panes at the other end of a pane's lines and leaves out the pane and the rest", () => {
  const { edges } = parts();
  assert.deepEqual([...focusOf(edges, "a").related].sort(), ["b", "c"]);
  assert.deepEqual([...focusOf(edges, "b").related].sort(), ["a", "d"]);
  assert.equal(focusOf(edges, "e").related.size, 0);
});

test("focusOf gives the pane a waiting pane waits on, and none to any other", () => {
  const { edges } = parts();
  assert.equal(focusOf(edges, "d").target, "b");
  assert.equal(focusOf(edges, "b").target, "");
  assert.equal(focusOf(edges, "a").target, "");
});

test("focusOf with no pane shows nothing", () => {
  const { edges } = parts();
  const f = focusOf(edges, "");
  assert.equal(f.related.size, 0);
  assert.equal(f.target, "");
});

test("only the lines at the focused pane are marked to be drawn", () => {
  const { edges } = parts();
  const on = (focus) => (renderToString(html`<svg><${Edges} e=${edges} focus=${focus} /></svg>`).match(/gedge--on/g) || []).length;
  assert.equal(on(""), 0);
  assert.equal(on("a"), 2);
  assert.equal(on("b"), 2);
  assert.equal(on("c"), 1);
  assert.equal(on("e"), 0);
});

test("countsOf counts what came to a pane and what it sent, and the sent ones not told yet", () => {
  const model = buildGraphModel(M);
  const c = countsOf(model);
  assert.deepEqual(c.get("a"), { in: 1, out: 1, undelivered: 0 });
  assert.deepEqual(c.get("b"), { in: 1, out: 0, undelivered: 0 });
  assert.deepEqual(c.get("c"), { in: 0, out: 1, undelivered: 1 });
  assert.equal(c.has("e"), false);
  assert.equal(countsLine(c.get("a")), "1 in, 1 out");
});

test("a message a pane sent to itself is not counted, nor one to a pane that is not a node", () => {
  const model = buildGraphModel(graph([node("a")], [msg("a", "a", "read", 1), msg("a", "gone", "read", 2)]));
  const c = countsOf(model);
  assert.deepEqual(c.get("a"), { in: 0, out: 1, undelivered: 0 });
});

test("the drawn graph has the count on a box with messages and not on one without", () => {
  const svg = renderToString(html`<${Graph} model=${buildGraphModel(M)} />`);
  assert.equal((svg.match(/class="gnode-msgs"/g) || []).length, 3);
  assert.match(svg, /1 undelivered/);
});

test("the open pane's lines are the ones drawn, and the others fade", () => {
  const model = buildGraphModel(M);
  const svg = renderToString(html`<${Graph} model=${model} open="d" />`);
  assert.match(svg, /class="gnode-rel"/);
  assert.equal((svg.match(/gnode--dim/g) || []).length, 3, "a, c and e have no line to d");
  assert.equal((svg.match(/gedge--on/g) || []).length, 1);
});

test("with nothing open or under the pointer no box fades and none is outlined", () => {
  const svg = renderToString(html`<${Graph} model=${buildGraphModel(M)} />`);
  assert.equal(svg.includes("gnode--dim"), false);
  assert.equal(svg.includes("gnode-rel"), false);
  assert.equal(svg.includes("gedge--on"), false);
});
