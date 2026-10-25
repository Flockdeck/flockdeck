import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel, rollupOf, labelOf, stuckOf } from "../src/graph/model.js";
import { Tree, rowsOf, totalOf } from "../src/graph/Tree.js";
import { GoalLine } from "../src/graph/GoalLine.js";
import { Deps } from "../src/graph/DepsView.js";
import { Feed } from "../src/graph/Feed.js";
import { diffGraphs, feedStep } from "../src/graph/FeedDiff.js";
import { goalTabsOf, emptyTabs, scopeModel, tabsValueOf } from "../src/graph/GoalTabs.js";
import { planOf, loopsOf, waitsOf, scopeTasks } from "../src/graph/deps.js";

const NOW = Date.parse("2026-10-24T10:00:00Z");
const nd = (pane, o = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, role: "worker", status: "working", activity: "working", waitingOn: { kind: "none" }, since: "2026-10-24T08:00:00Z", children: [], ...o });
const g = (nodes, teams, extra = {}) => ({ type: "graph", root: "/r", project: "p", nodes, teams, edges: [], solo: [], soloNeeds: [], ...extra });
const all = (m, tasks = []) => {
  const o = {};
  o.tree = renderToString(html`<${Tree} model=${m} tasks=${tasks} />`);
  o.goal = renderToString(html`<${GoalLine} model=${m} now=${NOW} />`);
  o.deps = renderToString(html`<${Deps} team=${{ type: "team", tasks }} model=${m} />`);
  o.tabs = goalTabsOf(m, emptyTabs());
  o.rows = rowsOf(m);
  o.total = totalOf(m);
  for (const n of m.nodes) { rollupOf(m, n.pane); labelOf(m, n.pane); stuckOf(n, NOW); }
  for (const t of m.teams) scopeModel(m, t.id);
  planOf(tasks); loopsOf(m); waitsOf(m);
  return o;
};

test("empty graph", () => { all(buildGraphModel(g([], []))); });
test("graph with null fields", () => { all(buildGraphModel({ type: "graph", nodes: null, teams: null })); });
test("team with no manager node", () => {
  const m = buildGraphModel(g([nd("a"), nd("b")], [{ id: "t", manager: "", members: ["a", "b"], goal: "G" }]));
  all(m);
});
test("team manager not in nodes", () => {
  const m = buildGraphModel(g([nd("a")], [{ id: "t", manager: "gone", members: ["gone", "a"], goal: "G" }]));
  all(m);
});
test("team with undefined goal/members", () => {
  all(buildGraphModel(g([nd("a")], [{ id: "t", manager: "a" }])));
});
test("two teams share a member; parent cycle", () => {
  const nodes = [nd("a", { parent: "b", children: ["b"] }), nd("b", { parent: "a", children: ["a"] })];
  all(buildGraphModel(g(nodes, [{ id: "t", manager: "a", members: ["a", "b"], goal: "G" }, { id: "u", manager: "b", members: ["a", "b"], goal: "H" }])));
});
test("duplicate pane ids", () => {
  all(buildGraphModel(g([nd("a"), nd("a", { name: "x" })], [{ id: "t", manager: "a", members: ["a", "a"], goal: "G" }])));
});
test("30 teams, 200 agents", () => {
  const nodes = []; const teams = [];
  for (let t = 0; t < 30; t++) {
    const mid = "m" + t; const members = [mid];
    const kids = [];
    for (let i = 0; i < 7; i++) { const id = `w${t}_${i}`; members.push(id); kids.push(id); nodes.push(nd(id, { parent: mid, depth: 1, name: "worker" })); }
    nodes.push(nd(mid, { children: kids, role: "manager", name: "manager" }));
    teams.push({ id: "t" + t, manager: mid, members, goal: "Goal " + t, needs: [] });
  }
  const m = buildGraphModel(g(nodes, teams));
  const t0 = Date.now(); const o = all(m);
  assert.ok(Date.now() - t0 < 3000, "slow " + (Date.now() - t0));
  assert.equal(o.tabs.tabs.length, 30);
});
test("names: long, rtl, emoji, markup, newline", () => {
  const names = ["x".repeat(500), "مرحبا بالعالم", "🙂🙂🙂", "<script>alert(1)</script>", "a\nb", "‮evil", ""];
  const nodes = names.map((n, i) => nd("p" + i, { name: n, task: { id: "T" + i, title: n } }));
  const m = buildGraphModel(g(nodes, [{ id: "t", manager: "p0", members: nodes.map((n) => n.pane), goal: names[3] }]));
  const o = all(m, [{ id: "T1", title: names[3], state: "planned", depends: [] }]);
  assert.ok(!o.tree.includes("<script>"), "escaped");
  assert.ok(!o.deps.includes("<script>"));
  assert.ok(!o.goal.includes("<script>"));
});
test("tasks: missing team, self-dep, dangling dep, dup id, loop, 5000 chain", () => {
  const m = buildGraphModel(g([nd("a")], [{ id: "t", manager: "a", members: ["a"], goal: "G" }]));
  const tasks = [
    { id: "T1", title: "t", state: "planned", depends: ["T1"] },
    { id: "T2", title: "t", state: "planned", depends: ["T99"] },
    { id: "T2", title: "dup", state: "planned", depends: [] },
    { id: "T3", title: "t", state: "planned", depends: ["T4"], team: "nope" },
    { id: "T4", title: "t", state: "planned", depends: ["T3"] },
    { id: "T5", title: "t", state: "weird", depends: null },
    { id: "", title: "", state: "", depends: [] },
    null,
  ];
  all(m, tasks.filter(Boolean));
  const chain = []; for (let i = 0; i < 5000; i++) chain.push({ id: "C" + i, title: "c", state: "planned", depends: i ? ["C" + (i - 1)] : [] });
  all(m, chain);
});
test("stale and out-of-order pushes through the feed", () => {
  const a = buildGraphModel(g([nd("a")], [{ id: "t", manager: "a", members: ["a"], goal: "G" }]));
  const b = buildGraphModel(g([nd("a", { status: "waiting", activity: "waiting" }), nd("b")], [{ id: "t", manager: "a", members: ["a", "b"], goal: "G" }]));
  let feed = [];
  feed = feedStep(feed, a, b, NOW, 200); feed = feedStep(feed, b, a, NOW - 1000, 200); feed = feedStep(feed, null, a, NOW, 200); feed = feedStep(feed, a, null, NOW, 200);
  renderToString(html`<${Feed} feed=${feed} now=${NOW} />`);
  renderToString(html`<${Feed} feed=${[{}, null, { at: "bad" }]} now=${NOW} />`);
});
