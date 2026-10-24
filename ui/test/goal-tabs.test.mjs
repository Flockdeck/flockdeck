// The goal tabs' data: labels from goals, the person's names, order and archive,
// and the value written to the preferences.
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildGraphModel } from "../src/graph/model.js";
import {
  SOLO, TABS_KEY, activeTabOf, archiveTab, canStep, emptyTabs, goalTabsOf, labelOf, moveTab, renameTab, restoreTab, stepTab, tabsStateOf, tabsValueOf, teamOfTab,
} from "../src/graph/GoalTabs.js";

const node = (pane, over = {}) => ({ pane, kind: "terminal", children: [], depth: 0, status: "idle", activity: "idle", ...over });
const graph = () =>
  buildGraphModel({
    type: "graph",
    nodes: [
      node("m1", { children: ["w1"] }), node("w1", { depth: 1, parent: "m1", activity: "working" }),
      node("m2"), node("s1"), node("s2", { activity: "waiting", status: "waiting" }),
    ],
    teams: [
      { id: "t1", manager: "m1", members: ["m1", "w1"], goal: "Goal: Ship the billing page for the new plans before Friday\nsecond line", needs: [{ id: "n1", kind: "permission", pane: "w1" }] },
      { id: "t2", manager: "m2", members: ["m2"], goal: "Fix flaky tests" },
    ],
    solo: ["s1", "s2"],
  });
const ids = (r) => r.tabs.map((t) => t.id);

test("a label is the goal's first line, cut at a word", () => {
  assert.equal(labelOf("Fix flaky tests"), "Fix flaky tests");
  assert.equal(labelOf("Goal: Ship the billing page for the new plans"), "Ship the billing page…");
  assert.equal(labelOf("  \n\n  Two   spaces\nnext"), "Two spaces");
  assert.equal(labelOf(""), "Goal");
  assert.equal(labelOf(undefined), "Goal");
  assert.equal(labelOf("Goal:"), "Goal");
  assert.equal(labelOf("x".repeat(60)).length, 24);
  assert.equal(labelOf("<img src=x onerror=1>"), "<img src=x onerror=1>", "text, never markup");
  assert.equal(labelOf("ab cd", 3), "ab…");
});

test("there is a tab for each goal and Solo last, with what each waits on", () => {
  const r = goalTabsOf(graph(), emptyTabs());
  assert.deepEqual(ids(r), ["t1", "t2", SOLO]);
  assert.equal(r.tabs[0].label, "Ship the billing page…");
  assert.equal(r.tabs[0].title.startsWith("Goal: Ship"), true, "the full goal is the tooltip");
  assert.deepEqual(r.tabs.map((t) => [t.needs, t.activity]), [[1, "waiting"], [0, ""], [0, "waiting"]]);
  assert.equal(r.tabs[2].kind, "solo");
  assert.deepEqual(r.archived, []);
});

test("Solo is listed with no goals, and with no graph", () => {
  assert.deepEqual(ids(goalTabsOf(buildGraphModel({ type: "graph", nodes: [], teams: [], solo: [] }), emptyTabs())), [SOLO]);
  assert.deepEqual(ids(goalTabsOf(buildGraphModel(null), null)), [SOLO]);
});

test("a name the person gave wins over the goal, and an empty one gives the goal back", () => {
  let s = renameTab(emptyTabs(), "t2", "  Tests \n now ");
  const r = goalTabsOf(graph(), s);
  assert.equal(r.tabs[1].label, "Tests now");
  assert.equal(r.tabs[1].named, true);
  s = renameTab(s, "t2", "   ");
  assert.equal(goalTabsOf(graph(), s).tabs[1].label, "Fix flaky tests");
  assert.equal(renameTab(s, SOLO, "x"), s, "Solo has no name to change");
  assert.equal(renameTab(emptyTabs(), "t1", "y".repeat(500)).names.t1.length, 80);
});

test("the order is the person's, goals they have not placed follow, and Solo stays last", () => {
  const model = graph();
  const s = { names: {}, order: ["t2", "gone"], archived: [] };
  assert.deepEqual(ids(goalTabsOf(model, s)), ["t2", "t1", SOLO]);
});

test("moving writes the whole order of the open goals and leaves archived ones after them", () => {
  const model = graph();
  const open = goalTabsOf(model, emptyTabs()).tabs;
  const s = moveTab(emptyTabs(), open, "t1", 1);
  assert.deepEqual(s.order, ["t2", "t1"]);
  assert.deepEqual(ids(goalTabsOf(model, s)), ["t2", "t1", SOLO]);
  assert.equal(moveTab(s, goalTabsOf(model, s).tabs, "t1", 1), s, "to where it is");
  assert.equal(moveTab(s, open, "nope", 0), s);
  assert.equal(moveTab(s, open, SOLO, 0), s, "Solo does not move");
  assert.deepEqual(moveTab(emptyTabs(), open, "t2", -5).order, ["t2", "t1"], "clamped");
  const withHidden = moveTab({ names: {}, order: ["t1", "old"], archived: ["old"] }, open, "t2", 0);
  assert.deepEqual(withHidden.order, ["t2", "t1", "old"]);
});

test("stepping moves one place and says when it cannot", () => {
  const open = goalTabsOf(graph(), emptyTabs()).tabs;
  assert.equal(canStep(open, "t1", -1), false);
  assert.equal(canStep(open, "t1", 1), true);
  assert.equal(canStep(open, "t2", 1), false, "Solo is not a goal to step past");
  assert.equal(canStep(open, SOLO, -1), false);
  assert.deepEqual(stepTab(emptyTabs(), open, "t1", 1).order, ["t2", "t1"]);
  assert.deepEqual(stepTab(emptyTabs(), open, "t1", -1), emptyTabs());
});

test("archiving hides a goal, lists it apart, and restoring brings it back in place", () => {
  const model = graph();
  let s = archiveTab(emptyTabs(), "t1");
  let r = goalTabsOf(model, s);
  assert.deepEqual(ids(r), ["t2", SOLO]);
  assert.deepEqual(r.archived.map((t) => t.id), ["t1"]);
  assert.equal(archiveTab(s, "t1"), s);
  assert.equal(archiveTab(s, SOLO), s, "Solo cannot be archived");
  s = restoreTab(s, "t1");
  assert.deepEqual(ids(goalTabsOf(model, s)), ["t1", "t2", SOLO]);
  assert.equal(restoreTab(s, "t1"), s);
});

test("the open tab is the one chosen, else the first goal that waits, else the first", () => {
  const t = (id, kind, needs) => ({ id, kind, needs });
  const tabs = [t("a", "team", 0), t("b", "team", 2), t(SOLO, "solo", 5)];
  assert.equal(activeTabOf(tabs, "a"), "a");
  assert.equal(activeTabOf(tabs, "gone"), "b");
  assert.equal(activeTabOf([t("a", "team", 0), t(SOLO, "solo", 5)], ""), "a");
  assert.equal(activeTabOf([], "x"), "");
});

test("a tab's team is found, and Solo or a gone tab has none", () => {
  const model = graph();
  assert.equal(teamOfTab(model, "t2").goal, "Fix flaky tests");
  assert.equal(teamOfTab(model, SOLO), null);
  assert.equal(teamOfTab(model, "gone"), null);
});

test("the choices are read from the preferences, keeping only what is well formed", () => {
  assert.deepEqual(tabsStateOf(undefined), emptyTabs());
  assert.deepEqual(tabsStateOf({ [TABS_KEY]: "x" }), emptyTabs());
  assert.deepEqual(tabsStateOf({ [TABS_KEY]: [1] }), emptyTabs());
  const s = tabsStateOf({ [TABS_KEY]: { names: { a: " Hi ", b: 3, c: "  ", [SOLO]: "no" }, order: ["a", "a", 5, "", SOLO, "b"], archived: "x", extra: 1 } });
  assert.deepEqual(s, { names: { a: "Hi" }, order: ["a", "b"], archived: [] });
});

test("the value written drops goals that are gone, keeps the rest sorted, and is null when empty", () => {
  const model = graph();
  const s = { names: { t2: "Tests", gone: "Old", t1: "Billing" }, order: ["t2", "gone", "t1"], archived: ["gone", "t1"] };
  const v = tabsValueOf(s, model);
  assert.deepEqual(v, { names: { t1: "Billing", t2: "Tests" }, order: ["t2", "t1"], archived: ["t1"] });
  assert.deepEqual(Object.keys(v.names), ["t1", "t2"], "sorted, so a pending value matches the desk's");
  assert.equal(tabsValueOf(emptyTabs(), model), null);
  assert.equal(tabsValueOf({ names: { gone: "x" }, order: [], archived: [] }, model), null);
  // With no graph nothing is known, so nothing is dropped.
  assert.deepEqual(tabsValueOf(s, buildGraphModel(null)).order, ["t2", "gone", "t1"]);
  assert.deepEqual(tabsStateOf({ [TABS_KEY]: v }), { names: v.names, order: v.order, archived: v.archived });
});

test("a goal started for a task is labelled with the task's title, and the tooltip has the id", () => {
  const model = buildGraphModel({
    type: "graph",
    nodes: [node("m1", { task: { id: "T4", title: "Limiter middleware" } }), node("m2")],
    teams: [
      { id: "t1", manager: "m1", members: ["m1"], goal: "Add rate limiting", taskId: "T4" },
      { id: "t2", manager: "m2", members: ["m2"], goal: "", taskId: "T9" },
    ],
    solo: [],
  });
  const [a, b] = goalTabsOf(model, emptyTabs()).tabs;
  assert.equal(a.label, "Limiter middleware");
  assert.equal(a.title, "T4 · Limiter middleware · Add rate limiting");
  assert.equal(b.label, "T9", "no title and no goal: the id");
  const named = goalTabsOf(model, { names: { t1: "Mine" }, order: [], archived: [] }).tabs[0];
  assert.equal(named.label, "Mine", "the person's name wins");
});
