// The goal line above the graph: the team's goal, time, spend, working count,
// helper cap and the number that need the person, all from the graph push.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { GoalLine, elapsedOf, helpersOf, summaryOf } from "../assets/graph/GoalLine.js";

const NOW = Date.parse("2026-10-21T10:14:00Z");
const msg = (team = {}) => ({
  type: "graph", root: "/work/a", project: "a", edges: [], solo: [], soloNeeds: [],
  teams: [{
    id: "m", manager: "m", members: ["m", "h1", "h2"], goal: "Add rate limiting", started: "2026-10-21T08:00:00Z", spendUSD: 18.4,
    caps: [{ pane: "m", used: 8, max: 8 }],
    needs: [{ id: "permission:h1", kind: "permission", pane: "h1", time: "2026-10-21T10:00:00Z", text: "go test" }],
    ...team,
  }],
  nodes: [
    { pane: "m", children: ["h1", "h2"], depth: 0, team: "m", name: "mgr", role: "manager", status: "working" },
    { pane: "h1", parent: "m", depth: 1, team: "m", name: "api", role: "worker", status: "working" },
    { pane: "h2", parent: "m", depth: 1, team: "m", name: "docs", role: "worker", status: "idle" },
  ],
});
const draw = (team) => renderToString(html`<${GoalLine} model=${buildGraphModel(msg(team))} now=${NOW} />`);

test("elapsed time is hours and minutes, and nothing for a time that is not known", () => {
  assert.equal(elapsedOf("2026-10-21T08:00:00Z", NOW), "2h 14m");
  assert.equal(elapsedOf("2026-10-21T10:00:00Z", NOW), "14m");
  assert.equal(elapsedOf("2026-10-21T10:13:50Z", NOW), "under 1m");
  assert.equal(elapsedOf("2026-10-18T10:14:00Z", NOW), "3d 0h");
  assert.equal(elapsedOf("0001-01-01T00:00:00Z", NOW), "");
  assert.equal(elapsedOf("2026-10-21T11:00:00Z", NOW), "", "a time ahead of now");
  assert.equal(elapsedOf("", NOW), "");
});

test("the line has the goal, time, cost, working count, helper cap and what needs you", () => {
  const out = draw();
  assert.match(out, /goalline-goal">Add rate limiting</);
  assert.match(out, /goalline-time">2h 14m</);
  assert.match(out, /goalline-cost">\$18\.40</);
  assert.match(out, /Working: 2</);
  assert.match(out, /goalline-helpers is-capped">8 of 8 helpers</);
  assert.match(out, /goalline-needs">1 needs you</);
});

test("what the push does not have is not drawn", () => {
  const out = draw({ started: "", spendUSD: 0, caps: [], needs: [] });
  assert.doesNotMatch(out, /goalline-time|goalline-cost|goalline-helpers|goalline-needs/);
  assert.match(out, /goalline-none">Nothing waits on you</, "with nothing to answer the line says so");
  assert.match(out, /Working: 2/);
  assert.equal(draw({ goal: "" }), "", "a team with no goal has no line");
});

test("a partial cost says so, the cap below its limit is not marked, and several needs are counted", () => {
  const model = buildGraphModel(msg({ spendPartial: true, caps: [{ pane: "m", used: 3, max: 8 }], needsHidden: 2 }));
  const s = summaryOf(model, model.teams[0], NOW);
  assert.equal(s.spend, "$18.40 partial");
  assert.equal(s.helpers, "3 of 8 helpers");
  assert.equal(s.needs, 3);
  assert.equal(s.capped, false);
  const full = buildGraphModel(msg({ caps: [{ pane: "m", used: 8, max: 8 }] }));
  assert.equal(summaryOf(full, full.teams[0], NOW).capped, true, "the model says so; the words are not searched");
  const none = buildGraphModel(msg({ caps: [] }));
  assert.equal(summaryOf(none, none.teams[0], NOW).capped, false, "no cap known is not a full cap");
  assert.equal(helpersOf({ caps: [{ used: 1, max: 2 }, { used: 2, max: 2 }] }), "3 of 4 helpers");
});

test("a goal is text, never markup", () => {
  const out = draw({ goal: '<img src=x onerror="alert(1)">' });
  assert.ok(!out.includes("<img"));
  assert.ok(out.includes("&lt;img"));
});

import { Tabs } from "../assets/shell/Tabs.js";

test("a tab shows how many items in it need you, in its badge and its name", () => {
  const tabs = [{ id: "t1", title: "Team", activity: "waiting", active: true, panes: ["h1"] }, { id: "t2", title: "Other", activity: "", active: false, panes: [] }];
  const out = renderToString(html`<${Tabs} tabs=${tabs} send=${() => {}} needs=${{ t1: 3 }} />`);
  assert.match(out, /badge badge--waiting tab-needs" aria-hidden="true">3</);
  assert.match(out, /aria-label="Team, waiting, 3 need you"/);
  assert.doesNotMatch(out, /aria-label="Other[^"]*need/);
});

import { HAS_TREE, TREE_VIEW, firstViewOf, viewsOf } from "../assets/shell/Views.js";

test("the switch offers Tree first only when the page has it, and the page starts there then", () => {
  assert.equal(HAS_TREE, true, "the outline is built in");
  assert.deepEqual(viewsOf().map(([id]) => id), ["tree", "deps", "graph", "board", "changes", "replay"]);
  assert.deepEqual(viewsOf(false).map(([id]) => id), ["graph", "board", "changes", "replay"]);
  assert.deepEqual(viewsOf(true).map(([id]) => id), ["tree", "deps", "graph", "board", "changes", "replay"]);
  assert.equal(firstViewOf(), TREE_VIEW[0]);
  assert.equal(firstViewOf(false), "graph");
  assert.equal(firstViewOf(true), TREE_VIEW[0]);
  const tabs = [{ id: "t1", title: "Team", activity: "", active: true, panes: [] }];
  const out = renderToString(html`<${Tabs} tabs=${tabs} send=${() => {}} view="tree" onView=${() => {}} views=${viewsOf(true)} />`);
  assert.match(out, /data-view="tree"[^>]*>Team</);
  assert.match(out, /tabs-view is-on" data-view="tree" aria-pressed="true"/);
  assert.ok(out.indexOf('data-view="tree"') < out.indexOf('data-view="graph"'));
});

test("the home column, not .main, is the container the layout asks about", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../assets/app.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\.main \{[^}]*container:/, "a container on .main would pin dialogs and toasts inside it");
  const graph = readFileSync(new URL("../assets/graph/graph.css", import.meta.url), "utf8");
  assert.match(graph, /\.home-col \{ container: main \/ inline-size;/);
  assert.match(graph, /@container main \(max-width: 560px\)/);
});
