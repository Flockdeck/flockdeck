// The layout is a pure function, so it is tested with the server's five graph
// fixtures and with generated trees from a seeded random source.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { buildGraphModel } from "../assets/graph/model.js";
import { layoutGraph, DEFAULTS } from "../assets/graph/layout.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures");
const names = readdirSync(dir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.slice(0, -5))
  .sort();
const load = (name) => JSON.parse(readFileSync(join(dir, name + ".json"), "utf8"));

// Two boxes overlap when they share area; boxes that only touch do not.
function overlapping(boxes) {
  const sorted = boxes.slice().sort((a, b) => a.x - b.x);
  const bad = [];
  for (let i = 0; i < sorted.length; i++) {
    for (let j = i + 1; j < sorted.length; j++) {
      const a = sorted[i];
      const b = sorted[j];
      if (b.x >= a.x + a.w) break;
      if (b.y < a.y + a.h && a.y < b.y + b.h) bad.push([a.pane, b.pane]);
    }
  }
  return bad;
}

function inside(inner, outer) {
  return inner.x >= outer.x && inner.y >= outer.y && inner.x + inner.w <= outer.x + outer.w && inner.y + inner.h <= outer.y + outer.h;
}

// The invariants every layout keeps.
function check(model, lay, label) {
  assert.equal(lay.ok, true, label);
  assert.deepEqual(overlapping(lay.nodes), [], label + ": boxes overlap");
  // Every pane has exactly one box.
  assert.equal(lay.nodes.length, model.nodes.length, label + ": box count");
  assert.equal(new Set(lay.nodes.map((b) => b.pane)).size, model.nodes.length, label + ": a pane has two boxes");
  for (const n of model.nodes) assert.ok(lay.byPane.has(n.pane), label + ": no box for " + n.pane);
  // Everything is inside the page, and a team's boxes inside its frame.
  for (const b of lay.nodes) {
    assert.ok(b.x >= 0 && b.y >= 0 && b.x + b.w <= lay.width && b.y + b.h <= lay.height, label + ": " + b.pane + " is off the page");
  }
  for (const f of lay.teams) {
    for (const b of lay.nodes.filter((n) => n.team === f.id)) assert.ok(inside(b, f), label + ": " + b.pane + " is outside its team's frame");
  }
  assert.deepEqual(overlapping(lay.teams.map((f) => ({ ...f, pane: f.id }))), [], label + ": team frames overlap");
  // The manager is the topmost box of its team.
  for (const t of model.teams) {
    const mine = lay.nodes.filter((b) => b.team === t.id);
    const mgr = lay.byPane.get(t.manager);
    if (!mgr || t.tree === null) continue;
    for (const b of mine) assert.ok(mgr.y <= b.y, `${label}: manager ${mgr.pane} is below ${b.pane}`);
    assert.equal(mgr.depth, 0);
  }
  // A helper is below its parent.
  for (const b of lay.nodes) {
    const n = model.byPane.get(b.pane);
    const p = n.parent && lay.byPane.get(n.parent);
    if (p && p.team === b.team && b.depth > 0) assert.ok(b.y >= p.y + p.h, `${label}: ${b.pane} is not below its parent`);
  }
  // The solo strip is below every team.
  if (lay.solo) {
    for (const f of lay.teams) assert.ok(lay.solo.y >= f.y + f.h, label + ": the strip is not below the teams");
    for (const b of lay.nodes.filter((n) => n.team === "")) assert.ok(b.y >= lay.solo.y, label + ": a solo box is above the strip");
  }
}

for (const name of names) {
  test(`fixture ${name}: no overlaps, manager on top, one box per pane`, () => {
    const model = buildGraphModel(load(name));
    check(model, layoutGraph(model), name);
  });

  test(`fixture ${name}: the layout is the same every time`, () => {
    const a = layoutGraph(buildGraphModel(load(name)));
    const b = layoutGraph(buildGraphModel(load(name)));
    assert.deepEqual(a, b);
  });
}

test("a nested helper is deeper than its parent", () => {
  const model = buildGraphModel(load("03-nested-helper-question-and-returned-task"));
  const lay = layoutGraph(model);
  const nested = model.nodes.find((n) => n.depth === 2);
  assert.ok(nested, "the fixture has a nested helper");
  assert.ok(lay.byPane.get(nested.pane).y > lay.byPane.get(nested.parent).y);
});

function flatTeam(helpers) {
  const nodes = [{ pane: "m", kind: "terminal", depth: 0, team: "m", children: [], name: "m", role: "manager", status: "working", activity: "working" }];
  for (let i = 0; i < helpers; i++) {
    nodes.push({
      pane: "h" + i,
      parent: "m",
      depth: 1,
      team: "m",
      name: "h" + i,
      role: "worker",
      status: "idle",
      activity: "idle",
      task: { id: "T" + i, title: "t", state: "in-progress" },
    });
    nodes[0].children.push("h" + i);
  }
  return buildGraphModel({ type: "graph", teams: [{ id: "m", manager: "m", members: nodes.map((n) => n.pane) }], nodes, edges: [], solo: [] });
}

test("eleven helpers wrap into rows and stay readable", () => {
  const model = flatTeam(11);
  const lay = layoutGraph(model);
  check(model, lay, "eleven helpers");
  assert.ok(lay.width <= 4 * (DEFAULTS.nodeW + DEFAULTS.gapX) + 2 * DEFAULTS.pad, "wider than four boxes: " + lay.width);
});

test("a team of twelve panes is one frame", () => {
  const model = flatTeam(11);
  const lay = layoutGraph(model);
  check(model, lay, "twelve");
  assert.equal(lay.teams.length, 1);
});

// A small seeded generator, so a failure can be reproduced.
function rng(seed) {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

// A random message: some teams, each a random tree, some solo panes, and now and
// then a child its parent does not list.
function randomGraph(seed, maxNodes) {
  const r = rng(seed);
  const total = 1 + Math.floor(r() * maxNodes);
  const nodes = [];
  const teams = [];
  const solo = [];
  let made = 0;
  while (made < total) {
    const left = total - made;
    if (r() < 0.2) {
      const id = "s" + made;
      nodes.push({ pane: id, kind: "terminal", depth: 0, name: id, shell: true, status: "idle", activity: "idle" });
      solo.push(id);
      made++;
      continue;
    }
    const size = 1 + Math.floor(r() * Math.min(left, 25));
    const ids = [];
    for (let i = 0; i < size; i++) ids.push(`t${teams.length}n${i}`);
    teams.push({ id: ids[0], manager: ids[0], members: ids.slice() });
    const group = ids.map((id) => ({
      pane: id,
      kind: "terminal",
      depth: 0,
      team: ids[0],
      children: [],
      name: id,
      role: "worker",
      status: ["working", "idle", "waiting", "exited"][Math.floor(r() * 4)],
      activity: "",
      waitingOn: { kind: "none" },
    }));
    for (let i = 1; i < size; i++) {
      // A deep chain now and then, otherwise any earlier node.
      const p = r() < 0.3 ? i - 1 : Math.floor(r() * i);
      group[i].parent = ids[p];
      group[i].depth = group[p].depth + 1;
      // One time in twenty the parent does not list the child.
      if (r() > 0.05) group[p].children.push(ids[i]);
    }
    for (const n of group) nodes.push(n);
    made += size;
  }
  return { type: "graph", teams, nodes, edges: [], solo };
}

test("generated trees of up to 60 nodes never overlap", () => {
  for (let seed = 1; seed <= 300; seed++) {
    const msg = randomGraph(seed, 60);
    const model = buildGraphModel(msg);
    const lay = layoutGraph(model);
    check(model, lay, "seed " + seed);
    assert.deepEqual(lay, layoutGraph(buildGraphModel(msg)), "seed " + seed + ": not stable");
  }
});

test("a team whose manager is not a node still shows its members", () => {
  const nodes = [
    { pane: "a", parent: "gone", depth: 1, team: "gone", name: "a", status: "idle", activity: "idle" },
    { pane: "b", parent: "gone", depth: 1, team: "gone", name: "b", status: "idle", activity: "idle" },
  ];
  const model = buildGraphModel({ type: "graph", teams: [{ id: "gone", manager: "gone", members: ["a", "b"] }], nodes, edges: [], solo: [] });
  check(model, layoutGraph(model), "unlinked");
});

test("a node that no team or solo list names goes in the strip", () => {
  const nodes = [{ pane: "x", depth: 0, name: "x", status: "idle", activity: "idle" }];
  const model = buildGraphModel({ type: "graph", teams: [], nodes, edges: [], solo: [] });
  const lay = layoutGraph(model);
  check(model, lay, "stray");
  assert.ok(lay.solo);
});

test("a chain of 20000 panes is laid out", () => {
  const nodes = [];
  const members = [];
  for (let i = 0; i < 20000; i++) {
    nodes.push({
      pane: "p" + i,
      parent: i ? "p" + (i - 1) : undefined,
      children: i < 19999 ? ["p" + (i + 1)] : [],
      depth: i,
      team: "p0",
      name: "p" + i,
      status: "idle",
      activity: "idle",
    });
    members.push("p" + i);
  }
  const model = buildGraphModel({ type: "graph", teams: [{ id: "p0", manager: "p0", members }], nodes, edges: [], solo: [] });
  const lay = layoutGraph(model);
  assert.equal(lay.nodes.length, 20000);
  assert.deepEqual(overlapping(lay.nodes), []);
});

test("a message that is not a graph gives an empty layout", () => {
  const lay = layoutGraph(buildGraphModel({ type: "other" }));
  assert.equal(lay.ok, false);
  assert.deepEqual(lay.nodes, []);
});
