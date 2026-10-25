// Cases for graph/GoalTabs.js that a mutation run found nothing failing on: the
// edges of a label, the most of each list kept, a name's length, repeated ids,
// what a tab says it is doing, and the ids and indexes it will not move.
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildGraphModel } from "../src/graph/model.js";
import { SOLO, NAMES_KEY, ORDER_KEY, ARCHIVED_KEY, archiveTab, canStep, emptyTabs, goalTabsOf, labelOf, moveTab, renameTab, stepTab, tabsStateOf, teamOfTab } from "../src/graph/GoalTabs.js";

const node = (pane, over = {}) => ({ pane, kind: "terminal", children: [], depth: 0, status: "idle", activity: "idle", ...over });
const teamsGraph = (ids) =>
  buildGraphModel({
    type: "graph",
    nodes: ids.map((id) => node("m-" + id)),
    teams: ids.map((id) => ({ id, manager: "m-" + id, members: ["m-" + id], goal: "goal " + id })),
    solo: [],
  });
const range = (n, prefix = "g") => Array.from({ length: n }, (_, i) => prefix + i);

test("a label of exactly the longest length is kept whole, and one more is cut", () => {
  const exact = "aaaa bbbb cccc dddd eeee";
  assert.equal([...exact].length, 24);
  assert.equal(labelOf(exact), exact);
  assert.equal(labelOf(exact + "f"), "aaaa bbbb cccc dddd…");
  assert.equal(labelOf("x".repeat(25)), "x".repeat(23) + "…");
});

test("a label is cut at a word only when the space is in the second half", () => {
  // The cut text is 23 characters; half of 24 is 12.
  const early = "abcde " + "f".repeat(34);
  assert.equal(labelOf(early), "abcde " + "f".repeat(17) + "…");
  const eleven = "a".repeat(11) + " " + "b".repeat(30);
  assert.equal(labelOf(eleven), "a".repeat(11) + " " + "b".repeat(11) + "…");
  const twelve = "a".repeat(12) + " " + "b".repeat(30);
  assert.equal(labelOf(twelve), "a".repeat(12) + "…");
});

test("each list read from the preferences keeps its first 200", () => {
  const names = {};
  for (const id of range(250)) names[id] = "name " + id;
  const got = tabsStateOf({ [NAMES_KEY]: names, [ORDER_KEY]: range(250), [ARCHIVED_KEY]: range(250, "a") });
  assert.equal(Object.keys(got.names).length, 200);
  assert.ok("g199" in got.names && !("g200" in got.names));
  assert.deepEqual(got.order, range(200));
  assert.deepEqual(got.archived, range(200, "a"));
});

test("archiving a goal past 200 drops the oldest", () => {
  const state = { ...emptyTabs(), archived: range(200, "a") };
  const got = archiveTab(state, "new");
  assert.equal(got.archived.length, 200);
  assert.equal(got.archived[0], "a1");
  assert.equal(got.archived[199], "new");
});

test("a name read from the preferences is cut at 80 characters, and one of spaces is left out", () => {
  const got = tabsStateOf({ [NAMES_KEY]: { a: "n".repeat(200), b: "   ", c: "  padded  ", [SOLO]: "no" } });
  assert.equal(got.names.a, "n".repeat(80));
  assert.equal("b" in got.names, false);
  assert.equal(got.names.c, "padded");
  assert.equal(SOLO in got.names, false);
});

test("a name given to a tab is cut at 80 characters and has its spaces gathered", () => {
  assert.equal(renameTab(emptyTabs(), "a", "n".repeat(200)).names.a, "n".repeat(80));
  assert.equal(renameTab(emptyTabs(), "a", "  two \n  words ").names.a, "two words");
});

test("an id listed twice is read once", () => {
  const got = tabsStateOf({ [ORDER_KEY]: ["a", "a", "b", "a"], [ARCHIVED_KEY]: ["a", "a"] });
  assert.deepEqual(got.order, ["a", "b"]);
  assert.deepEqual(got.archived, ["a"]);
});

test("a goal with one working pane and no needs is working, and one with nothing going on is not", () => {
  const m = buildGraphModel({
    type: "graph",
    nodes: [node("m1", { activity: "working", status: "working" }), node("m2"), node("s1", { activity: "working", status: "working" })],
    teams: [
      { id: "t1", manager: "m1", members: ["m1"], goal: "busy" },
      { id: "t2", manager: "m2", members: ["m2"], goal: "quiet" },
    ],
    solo: ["s1"],
  });
  const r = goalTabsOf(m, emptyTabs());
  const by = Object.fromEntries(r.tabs.map((t) => [t.id, t]));
  assert.equal(by.t1.needs, 0);
  assert.equal(by.t1.activity, "working");
  assert.equal(by.t2.activity, "");
  assert.equal(by[SOLO].activity, "working");
});

test("two teams with one id make one tab, the first", () => {
  const m = buildGraphModel({
    type: "graph",
    nodes: [node("m1"), node("m2")],
    teams: [
      { id: "same", manager: "m1", members: ["m1"], goal: "first" },
      { id: "same", manager: "m2", members: ["m2"], goal: "second" },
    ],
    solo: [],
  });
  const r = goalTabsOf(m, emptyTabs());
  assert.deepEqual(r.tabs.map((t) => t.id), ["same", SOLO]);
  assert.equal(r.tabs[0].label, "first");
});

test("the needs the graph left out are added to a tab's count, and to Solo's", () => {
  const ask = { id: "n", kind: "permission", pane: "m1" };
  const m = buildGraphModel({
    type: "graph",
    nodes: [node("m1"), node("s1")],
    teams: [{ id: "t1", manager: "m1", members: ["m1"], goal: "g", needs: [ask], needsHidden: 2 }],
    solo: ["s1"],
    soloNeeds: [{ id: "s", kind: "ask", pane: "s1" }],
    soloNeedsHidden: 2,
  });
  const r = goalTabsOf(m, emptyTabs());
  assert.equal(r.tabs[0].needs, 3);
  assert.equal(r.tabs[0].activity, "waiting");
  assert.equal(r.tabs[1].needs, 3);
  assert.equal(r.tabs[1].activity, "waiting");
});

test("Solo has no team, even when a team has the id solo", () => {
  const m = teamsGraph(["solo", "t2"]);
  assert.equal(teamOfTab(m, SOLO), null);
  assert.equal(teamOfTab(m, "t2").id, "t2");
  assert.equal(teamOfTab(m, "nobody"), null);
});

test("moveTab does nothing for an index that is not a whole number", () => {
  const tabs = goalTabsOf(teamsGraph(["a", "b", "c"]), emptyTabs()).tabs;
  const state = emptyTabs();
  for (const to of ["1", 1.5, NaN, undefined, null, Infinity]) assert.equal(moveTab(state, tabs, "a", to), state, String(to));
  assert.deepEqual(moveTab(state, tabs, "a", 2).order, ["b", "c", "a"]);
});

test("stepTab and canStep do nothing for a tab that is not listed, or at an end", () => {
  const tabs = goalTabsOf(teamsGraph(["a", "b", "c"]), emptyTabs()).tabs;
  const state = emptyTabs();
  assert.equal(stepTab(state, tabs, "nobody", 1), state);
  assert.equal(stepTab(state, tabs, SOLO, -1), state);
  assert.equal(canStep(tabs, "nobody", 1), false);
  assert.equal(canStep(tabs, "a", -1), false);
  assert.equal(canStep(tabs, "a", 1), true);
  assert.equal(canStep(tabs, "c", 1), false);
  assert.equal(canStep(tabs, "c", -1), true);
  assert.equal(stepTab(state, tabs, "a", -1), state);
  assert.equal(stepTab(state, tabs, "c", 1), state);
  assert.deepEqual(stepTab(state, tabs, "a", 1).order, ["b", "a", "c"]);
});
