// The graph fits the width it is given: as many boxes in a row as fit, boxes as
// tall as what they hold, no frame round a lone team, and the lines that join a
// pane to its helpers.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { layoutGraph, fitLayout, DEFAULTS, MAX_COLUMNS } from "../assets/graph/layout.js";
import { linksOf } from "../assets/graph/Edges.js";
import { Graph } from "../assets/graph/Graph.js";

function team(helpers, needs = [], extra = {}) {
  const nodes = [{ pane: "m", depth: 0, team: "t", name: "m", status: "working", activity: "working", children: [] }];
  for (let i = 0; i < helpers; i++) {
    nodes.push({ pane: "h" + i, parent: "m", depth: 1, team: "t", name: "h" + i, status: "idle", activity: "idle" });
    nodes[0].children.push("h" + i);
  }
  return { type: "graph", root: "/r", project: "p", teams: [{ id: "t", manager: "m", members: nodes.map((n) => n.pane), needs }], nodes, edges: [], solo: [], ...extra };
}
const need = (kind, pane) => ({ id: kind + pane, kind, pane, time: "2026-10-21T08:00:00Z", text: "ls" });
const model = (m) => buildGraphModel(m);
// The most helpers in one row.
const columns = (lay) => {
  const rows = new Map();
  for (const b of lay.nodes.filter((x) => x.pane !== "m")) rows.set(b.y, (rows.get(b.y) || 0) + 1);
  return Math.max(...rows.values());
};

test("nine helpers fit a column of 810 px with no sideways scroll", () => {
  const lay = fitLayout(model(team(9)), 810);
  assert.ok(lay.width <= 810, "wide: " + lay.width);
  assert.equal(columns(lay), 3);
});

test("a wider column gets more boxes in a row, up to the most there is", () => {
  const widths = [440, 810, 1100, 1400, 2400].map((w) => columns(fitLayout(model(team(12)), w)));
  assert.deepEqual(widths, [1, 3, 4, 5, MAX_COLUMNS]);
});

test("a column too narrow for one box still gives one box in a row", () => {
  const lay = fitLayout(model(team(4)), 100);
  assert.equal(columns(lay), 1);
});

test("with no width the layout is the default", () => {
  const m = model(team(9));
  assert.deepEqual(fitLayout(m, 0), layoutGraph(m));
  assert.deepEqual(fitLayout(m, undefined), layoutGraph(m));
});

test("fewer helpers than columns take only the room they need", () => {
  const lay = fitLayout(model(team(2)), 2000);
  assert.ok(lay.width < 700, "wide: " + lay.width);
});

test("a box is as tall as what it holds: quiet, or loud with a request block", () => {
  const m = model(team(3, [need("permission", "h1"), need("stale-unread", "h2"), need("down", "h0")]));
  const lay = layoutGraph(m);
  assert.equal(lay.byPane.get("h1").h, DEFAULTS.loudH);
  for (const p of ["m", "h0", "h2"]) assert.equal(lay.byPane.get(p).h, DEFAULTS.nodeH, p);
  assert.ok(DEFAULTS.nodeH < DEFAULTS.loudH);
});

test("boxes of different heights in one row do not overlap the row below", () => {
  const m = model(team(8, [need("permission", "h1")]));
  const lay = fitLayout(m, 810);
  const rows = new Map();
  for (const b of lay.nodes.filter((x) => x.pane !== "m")) rows.set(b.y, Math.max(rows.get(b.y) || 0, b.h));
  const ys = [...rows.keys()].sort((a, b) => a - b);
  for (let i = 1; i < ys.length; i++) assert.ok(ys[i] >= ys[i - 1] + rows.get(ys[i - 1]), "row " + i + " starts inside the row above");
});

test("a lone team has no frame and a team beside another has one", () => {
  const lone = renderToString(html`<${Graph} model=${model(team(2))} />`);
  assert.ok(!lone.includes("gteam-frame"));
  assert.ok(lone.includes("gteam--bare"));
  assert.ok(lone.includes("gteam-title"), "its goal is still its title");
  const m = team(2);
  m.nodes.push({ pane: "s", depth: 0, name: "s", status: "idle", activity: "idle" });
  m.solo = ["s"];
  const two = renderToString(html`<${Graph} model=${model(m)} />`);
  assert.ok(two.includes("gteam-frame"));
});

test("the lines to helpers run from the pane's foot to a bar over the row and down to each helper", () => {
  const m = model(team(3));
  const lay = layoutGraph(m);
  const d = linksOf(m, lay);
  const manager = lay.byPane.get("m");
  assert.ok(d.startsWith(`M${manager.x + manager.w / 2} ${manager.y + manager.h}V`), d.slice(0, 40));
  for (const p of ["h0", "h1", "h2"]) {
    const b = lay.byPane.get(p);
    assert.ok(d.includes(`M${b.x + b.w / 2} ${b.y - 12}V${b.y}`), p + " has no line down to it");
  }
  assert.ok(!/NaN|undefined/.test(d));
});

test("helpers in several rows hang from a trunk at the left, so no line crosses a box", () => {
  const m = model(team(8));
  const lay = fitLayout(m, 810);
  const d = linksOf(m, lay);
  const kids = lay.nodes.filter((b) => b.pane !== "m");
  const trunk = Math.min(...kids.map((b) => b.x)) - 9;
  assert.ok(d.includes(`H${trunk}V`), "no trunk");
  // Every vertical piece of line lies outside every box.
  for (const v of d.matchAll(/M(-?[\d.]+) (-?[\d.]+)V(-?[\d.]+)/g)) {
    const x = Number(v[1]);
    const y0 = Math.min(Number(v[2]), Number(v[3]));
    const y1 = Math.max(Number(v[2]), Number(v[3]));
    if (x === trunk) continue;
    for (const b of lay.nodes) {
      const through = x > b.x && x < b.x + b.w && y0 < b.y + b.h && y1 > b.y;
      assert.ok(!through, `a line at x=${x} from ${y0} to ${y1} goes through ${b.pane}`);
    }
  }
});

test("a model with no tree has no lines to draw", () => {
  assert.equal(linksOf(null, null), "");
  const m = model({ type: "graph", teams: [], nodes: [{ pane: "a", depth: 0, name: "a", status: "idle", activity: "idle" }], edges: [], solo: ["a"] });
  assert.equal(linksOf(m, layoutGraph(m)), "");
});
