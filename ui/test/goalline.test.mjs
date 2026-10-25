// The goal line above the graph: the team's goal, time, spend, working count,
// helper cap and the number that need the person, all from the graph push.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { GoalLine, elapsedOf, helpersOf, summaryOf } from "../src/graph/GoalLine.js";

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
  assert.doesNotMatch(out, /goalline-cost|\$18/, "no spend on the goal line");
  assert.match(out, /Working: 2</);
  assert.match(out, /goalline-helpers is-capped">8 of 8 helpers</);
  assert.match(out, /goalline-needs">1 needs you</);
});

test("what the push does not have is not drawn", () => {
  const out = draw({ started: "", spendUSD: 0, caps: [], needs: [] });
  assert.doesNotMatch(out, /goalline-time|goalline-cost|goalline-helpers|goalline-needs/);
  assert.match(out, /goalline-none">Nothing waits on you</, "with nothing to answer the line says so");
  assert.match(out, /Working: 2/);
  const bare = draw({ goal: "" });
  assert.match(bare, /goalline-goal">Team</, "a team with no goal and no task is still a line, called Team");
  assert.match(bare, /Working: 2/);
});

test("a manager the graph no longer holds is said to be gone, not shown as a pane id", () => {
  const m = msg({ manager: "ghost" });
  const out = renderToString(html`<${GoalLine} model=${buildGraphModel(m)} now=${NOW} />`);
  assert.match(out, /manager gone/);
  assert.doesNotMatch(out, /ghost/);
  assert.match(draw({}), /run by mgr/);
});

test("more helpers than the cap is shown as it is and flagged", () => {
  const model = buildGraphModel(msg({ caps: [{ pane: "m", used: 9, max: 8 }] }));
  assert.equal(summaryOf(model, model.teams[0], NOW).helpers, "9 of 8 helpers, over the cap");
  const out = draw({ caps: [{ pane: "m", used: 9, max: 8 }] });
  assert.match(out, /is-capped is-over/);
  assert.match(out, /9 of 8 helpers, over the cap/);
});

test("the cap below its limit is not marked, and several needs are counted", () => {
  const model = buildGraphModel(msg({ spendPartial: true, caps: [{ pane: "m", used: 3, max: 8 }], needsHidden: 2 }));
  const s = summaryOf(model, model.teams[0], NOW);
  assert.equal(s.spend, undefined);
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

import { GoalStrip, ViewBar } from "../src/shell/GoalStrip.js";

test("a goal's tab shows how many panes in it need you, in its badge and its name", () => {
  const tabs = [
    { id: "g1", kind: "team", label: "Team", title: "Team goal", activity: "waiting", needs: 3 },
    { id: "g2", kind: "team", label: "Other", title: "Other goal", activity: "", needs: 0 },
  ];
  const out = renderToString(html`<${GoalStrip} tabs=${tabs} active="g1" onSelect=${() => {}} canStep=${() => false} />`);
  assert.match(out, /badge badge--waiting tab-needs" aria-hidden="true">3</);
  assert.match(out, /aria-label="Team, waiting, 3 need you"/);
  assert.doesNotMatch(out, /aria-label="Other[^"]*need/);
});

import { HAS_TREE, TREE_VIEW, firstViewOf, viewsOf } from "../src/shell/Views.js";

test("the switch offers Tree first only when the page has it, and the page starts there then", () => {
  assert.equal(HAS_TREE, true, "the outline is built in");
  assert.deepEqual(viewsOf().map(([id]) => id), ["tree", "deps", "graph", "board", "changes", "replay"]);
  assert.deepEqual(viewsOf(false).map(([id]) => id), ["graph", "board", "changes", "replay"]);
  assert.deepEqual(viewsOf(true).map(([id]) => id), ["tree", "deps", "graph", "board", "changes", "replay"]);
  assert.equal(firstViewOf(), TREE_VIEW[0]);
  assert.equal(firstViewOf(false), "graph");
  assert.equal(firstViewOf(true), TREE_VIEW[0]);
  const out = renderToString(html`<${ViewBar} view="tree" onView=${() => {}} views=${viewsOf(true)} />`);
  assert.match(out, /data-view="tree"[^>]*>Team</);
  assert.match(out, /tabs-view is-on" data-view="tree" aria-pressed="true"/);
  assert.ok(out.indexOf('data-view="tree"') < out.indexOf('data-view="graph"'));
});

test("the home column, not .main, is the container the layout asks about", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../src/app.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\.main \{[^}]*container:/, "a container on .main would pin dialogs and toasts inside it");
  const graph = readFileSync(new URL("../src/graph/graph.css", import.meta.url), "utf8");
  assert.match(graph, /\.home-col \{ container: main \/ inline-size;/);
  assert.match(graph, /@container main \(max-width: 560px\)/);
});

import { taskOfTeam, taskLabelOf } from "../src/graph/model.js";

test("a team started for a task says the task, with its title; else the goal text", () => {
  const withTask = (extra = {}, nodeTask = { id: "T4", title: "Limiter middleware", state: "doing" }) => {
    const g = msg({ taskId: "T4", ...extra });
    g.nodes[0].task = nodeTask;
    return buildGraphModel(g);
  };
  const m = withTask();
  assert.deepEqual(taskOfTeam(m, m.teams[0]), { id: "T4", title: "Limiter middleware" });
  assert.equal(taskLabelOf(taskOfTeam(m, m.teams[0])), "T4 · Limiter middleware");
  assert.deepEqual(taskOfTeam(m, m.teams[0], [{ id: "T4", title: "From the card" }]), { id: "T4", title: "From the card" });
  assert.match(renderToString(html`<${GoalLine} model=${m} now=${NOW} />`), /goalline-goal">T4 · Limiter middleware</);
  const untitled = withTask({}, null);
  assert.deepEqual(taskOfTeam(untitled, untitled.teams[0]), { id: "T4", title: "" });
  assert.match(renderToString(html`<${GoalLine} model=${untitled} now=${NOW} />`), /goalline-goal">Add rate limiting</, "no title known: the goal text");
  const bare = withTask({ goal: "" }, null);
  assert.match(renderToString(html`<${GoalLine} model=${bare} now=${NOW} />`), /goalline-goal">T4</, "no goal text either: the id");
  assert.equal(taskOfTeam(buildGraphModel(msg()), buildGraphModel(msg()).teams[0]), null);
  assert.equal(taskOfTeam(null, {}), null);
  assert.equal(taskLabelOf(null), "");
});

test("the goal line says who runs the team", () => {
  const model = buildGraphModel({
    type: "graph",
    nodes: [{ pane: "m1", name: "flockdeck-29", kind: "terminal", children: [], depth: 0, status: "idle", activity: "idle" }],
    teams: [{ id: "t1", manager: "m1", members: ["m1"], goal: "Ship it" }],
    solo: [],
  });
  assert.equal(summaryOf(model, model.teams[0], Date.now()).manager, "flockdeck-29");
  assert.match(renderToString(html`<${GoalLine} model=${model} now=${Date.now()} />`), /run by flockdeck-29/);
});

test("the goal line leaves out the goal text when the tab already shows all of it", () => {
  const model = buildGraphModel({ type: "graph", nodes: [{ pane: "m1", kind: "terminal", children: [], depth: 0, status: "idle", activity: "idle" }], teams: [{ id: "t1", manager: "m1", members: ["m1"], goal: "Fix flaky tests" }], solo: [] });
  const same = renderToString(html`<${GoalLine} model=${model} now=${Date.now()} shownAs="Fix flaky tests" />`);
  assert.doesNotMatch(same, /goalline-goal/);
  assert.match(renderToString(html`<${GoalLine} model=${model} now=${Date.now()} shownAs="Fix flak…" />`), /Fix flaky tests/);
});
