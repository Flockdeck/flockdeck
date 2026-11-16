// The tab strip's marks and the numbers behind them (T255): what the "+" names,
// what an archived entry counts, what the dot and the tab say in words, that the
// goal line's Working is the rail's activity, and that one goal's needs-you count
// is one number on the tab, the goal line, the Teams line and the list, with and
// without asked overlaps.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { attentionOf, emptyTabs, goalTabsOf, plusNeedsOf, plusWords, scopeModel, archiveTab } from "../src/graph/GoalTabs.js";
import { summaryOf as lineOf } from "../src/graph/GoalLine.js";
import { summaryOf as teamsOf } from "../src/graph/Teams.js";
import { NeedsYou } from "../src/graph/NeedsYou.js";
import { GoalStrip, PlusMenu } from "../src/shell/GoalStrip.js";

const node = (pane, over = {}) => ({ pane, kind: "terminal", children: [], depth: 0, status: "idle", activity: "idle", name: pane, ...over });
const NOW = Date.now();

// Four goals: t1 has a permission item; t2 has a waiting pane with no prompt (so no item);
// t3 is quiet; t4 has an idle pane with background work, which the rail counts as working.
const graph = () =>
  buildGraphModel({
    type: "graph",
    nodes: [
      node("m1", { team: "t1" }), node("w1", { team: "t1", activity: "waiting", status: "waiting" }),
      node("m2", { team: "t2", activity: "waiting", status: "waiting" }),
      node("m3", { team: "t3" }),
      node("m4", { team: "t4", status: "idle", activity: "working" }), node("w4", { team: "t4", status: "working", activity: "working" }),
    ],
    teams: [
      { id: "t1", manager: "m1", members: ["m1", "w1"], goal: "Docs drift", needs: [{ id: "n1", kind: "permission", pane: "w1", text: "Bash" }] },
      { id: "t2", manager: "m2", members: ["m2"], goal: "Flaky tests" },
      { id: "t3", manager: "m3", members: ["m3"], goal: "Quiet one" },
      { id: "t4", manager: "m4", members: ["m4", "w4"], goal: "Background work" },
    ],
    solo: [],
  });

const archive = (ids) => ids.reduce((s, id) => archiveTab(s, id), emptyTabs());

test("an archived goal's entry counts its items, else its waiting panes, else nothing", () => {
  const r = goalTabsOf(graph(), archive(["t1", "t2", "t3"]));
  const by = Object.fromEntries(r.archived.map((t) => [t.id, attentionOf(t)]));
  assert.deepEqual(by.t1, { n: 1, words: "1 item needs you" });
  assert.deepEqual(by.t2, { n: 1, words: "1 agent waiting for you" }, "a waiting pane with no prompt has no item, and is still counted");
  assert.equal(by.t3, null);
});

test("the + names only the archived goals with something for the person", () => {
  const r = goalTabsOf(graph(), archive(["t1", "t2", "t3"]));
  const list = plusNeedsOf(r.archived);
  assert.deepEqual(list.map((g) => g.id), ["t1", "t2"]);
  assert.equal(plusWords(list), "Archived goals need you: Docs drift (1), Flaky tests (1)");
  assert.equal(plusWords(plusNeedsOf(goalTabsOf(graph(), archive(["t1"])).archived)), "Archived goal needs you: Docs drift (1)");
  assert.equal(plusWords(plusNeedsOf(goalTabsOf(graph(), archive(["t3"])).archived)), "", "with none, no words and no dot");
  assert.deepEqual(plusNeedsOf([]), []);
});

test("the + button: a dot, the goal and its count in the name and the tooltip; none without", () => {
  const draw = (waiting) => renderToString(html`<${PlusMenu} waiting=${waiting}>${() => null}<//>`);
  const out = draw(plusNeedsOf(goalTabsOf(graph(), archive(["t1", "t3"])).archived));
  assert.match(out, /aria-label="New tab and tab actions. Archived goal needs you: Docs drift \(1\)" title="New tab and tab actions. Archived goal needs you: Docs drift \(1\)"/);
  assert.match(out, /tabs-plus-dot/);
  const none = draw(plusNeedsOf(goalTabsOf(graph(), archive(["t3"])).archived));
  assert.match(none, /aria-label="New tab and tab actions" title="New tab and tab actions"/);
  assert.ok(!none.includes("tabs-plus-dot"));
});

test("a tab carries the rail's counts for its panes: background work is working", () => {
  const r = goalTabsOf(graph(), emptyTabs());
  const t = Object.fromEntries(r.tabs.map((x) => [x.id, x]));
  assert.deepEqual([t.t1.waiting, t.t1.working], [1, 0]);
  assert.deepEqual([t.t4.waiting, t.t4.working], [0, 2], "an idle pane with background work counts, as it does on the rail");
  assert.equal(t.t4.activity, "working");
  const blocked = goalTabsOf(buildGraphModel({ type: "graph", nodes: [node("b", { team: "x", activity: "blocked", status: "blocked" })], teams: [{ id: "x", manager: "b", members: ["b"], goal: "g" }], solo: [] }), emptyTabs());
  assert.deepEqual([blocked.tabs[0].waiting, blocked.tabs[0].activity], [1, "waiting"], "blocked is waiting, as on the rail");
});

test("the goal line's Working is the same number as the tab's and the rail's", () => {
  const model = graph();
  const tabs = goalTabsOf(model, emptyTabs()).tabs;
  for (const team of model.teams) {
    const tab = tabs.find((x) => x.id === team.id);
    assert.equal(lineOf(model, team, NOW).working, tab.working, team.id);
  }
  assert.equal(lineOf(model, model.teams[3], NOW).working, 2);
});

test("a tab's tooltip adds its counts after the goal, and its dot says what it is", () => {
  const model = graph();
  const { tabs } = goalTabsOf(model, emptyTabs());
  const out = renderToString(html`<${GoalStrip} tabs=${tabs} active="t1" onSelect=${() => {}} canStep=${() => false} />`);
  assert.match(out, /title="Docs drift\. 1 agent waiting for you"/);
  assert.match(out, /title="Flaky tests\. 1 agent waiting for you"/);
  assert.match(out, /title="Background work\. 2 working"/);
  assert.match(out, /tab-dot tab-dot--waiting" aria-hidden="true" title="Waiting for you"/);
  assert.match(out, /tab-dot tab-dot--working" aria-hidden="true" title="Working"/);
  // The dot stands in for the badge only; a goal with items has the badge.
  const t1 = out.slice(out.indexOf('data-tab="t1"'), out.indexOf('data-tab="t2"'));
  assert.ok(t1.includes("tab-needs") && !t1.includes("tab-dot"), "no dot on the tab that has the badge");
});

// A project with overlaps. a and b are in t1, c and d in t2; the conflict a-c is listed under both teams.
const teamsGraph = (extra = {}) => ({
  type: "graph",
  nodes: [node("m1", { team: "t1" }), node("a", { team: "t1" }), node("b", { team: "t1" }), node("m2", { team: "t2" }), node("c", { team: "t2" }), node("d", { team: "t2" }), node("s", { activity: "waiting", status: "waiting" })],
  teams: [
    { id: "t1", manager: "m1", members: ["m1", "a", "b"], goal: "One", needs: [{ id: "conflict:a:b", kind: "conflict", pane: "a", other: "b", text: "x.go" }, { id: "conflict:a:c", kind: "conflict", pane: "a", other: "c", text: "y.go" }] },
    { id: "t2", manager: "m2", members: ["m2", "c", "d"], goal: "Two", needs: [{ id: "conflict:a:c", kind: "conflict", pane: "a", other: "c", text: "y.go" }, { id: "p:d", kind: "permission", pane: "d", text: "Bash" }] },
  ],
  solo: ["s"],
  soloNeeds: [{ id: "a:s", kind: "ask", pane: "s", text: "Which?" }],
  ...extra,
});
const rec = (id, path) => ({ [id]: { paths: [path], more: 0 } });

const scenarios = [
  ["no overlap asked", {}],
  ["one overlap asked in a team", rec("conflict:a:b", "x.go")],
  ["both overlaps asked", { ...rec("conflict:a:b", "x.go"), ...rec("conflict:a:c", "y.go") }],
  ["the overlap between two teams asked", rec("conflict:a:c", "y.go")],
  ["an asked overlap that grew", { "conflict:a:b": { paths: [], more: 0 } }],
];

for (const [name, asked] of scenarios) {
  test("one goal, one number: " + name, () => {
    const model = buildGraphModel(teamsGraph(), asked);
    const { tabs } = goalTabsOf(model, emptyTabs());
    const teams = teamsOf(model);
    for (const team of model.teams) {
      // The items of the model that touch this team's panes, each once.
      const members = new Set(team.members);
      const mine = new Set(model.needs.filter((n) => members.has(n.pane) || (n.other && members.has(n.other))).map((n) => n.id));
      const tab = tabs.find((t) => t.id === team.id);
      assert.equal(tab.needs, mine.size, team.id + ": tab");
      assert.equal(lineOf(model, team, NOW).needs, mine.size, team.id + ": goal line");
      assert.equal(teams.find((s) => s.id === team.id).needs, mine.size, team.id + ": Teams line");
      const list = renderToString(html`<${NeedsYou} model=${scopeModel(model, team.id)} />`);
      const head = /Needs you \((\d+)\)/.exec(list);
      assert.equal(head ? Number(head[1]) : 0, mine.size, team.id + ": list heading");
      assert.equal((list.match(/class="needs-row /g) || []).length, mine.size, team.id + ": list rows");
    }
    // Solo has its own item, and is the same everywhere.
    const solo = tabs.find((t) => t.id === "solo");
    assert.equal(solo.needs, 1);
    const soloList = renderToString(html`<${NeedsYou} model=${scopeModel(model, "solo")} />`);
    assert.match(soloList, /Needs you \(1\)/);
  });
}

test("an asked overlap is out of every count of the team that asked, and only that overlap", () => {
  const model = buildGraphModel(teamsGraph(), { ...rec("conflict:a:b", "x.go") });
  const t = Object.fromEntries(goalTabsOf(model, emptyTabs()).tabs.map((x) => [x.id, x.needs]));
  assert.deepEqual([t.t1, t.t2], [1, 2], "t1 has the a-c overlap left; t2 has the same one and a permission");
});
