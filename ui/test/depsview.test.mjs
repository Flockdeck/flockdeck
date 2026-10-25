// The Dependencies view: the tree of tasks by what blocks them, the critical path,
// the deadlock block, and the keyboard of a tree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { Deps, glyphOf, NO_TASKS_WORDS, READING_WORDS, breakText } from "../src/graph/DepsView.js";
import { TEAM_RELAY_WORDS } from "../src/lib/team.js";
import { createDom, install, settle } from "./dom.mjs";

const T = (id, state, depends = [], over = {}) => ({ id, title: "title " + id, state, depends, estimateMin: 0, ownerPane: "", owner: "", ...over });
const push = (tasks) => ({ type: "team", root: "/r", project: "api", tasks });
const tasks = (est = true) => [
  T("T1", "done"),
  T("T2", "in-progress", ["T1"], { estimateMin: est ? 20 : 0, ownerPane: "ci" }),
  T("T4", "assigned", ["T2"], { estimateMin: est ? 25 : 0, ownerPane: "api" }),
  T("T7", "planned", ["T4", "T6"], { estimateMin: est ? 20 : 0 }),
  T("T6", "in-progress", ["T1"], { estimateMin: est ? 15 : 0 }),
  T("T11", "planned", ["T1"], { estimateMin: est ? 5 : 0 }),
  T("T12", "planned", ["T6"], { estimateMin: est ? 5 : 0 }),
];
const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, team: "t", name: pane, role: "worker", status: "working", activity: "working", waitingOn: { kind: "none" }, since: "2026-10-24T08:00:00Z", ...over });
const waits = (ref) => ({ kind: "helpers", ref, text: "" });
const model = (nodes) => buildGraphModel({ type: "graph", root: "/r", project: "api", teams: [{ id: "t", manager: "mgr", goal: "Ship the API", members: nodes.map((n) => n.pane), needs: [] }], nodes, edges: [], solo: [], soloNeeds: [] });
const plain = [node("mgr", { role: "manager" }), node("ci"), node("api")];

test("remote and unread states say so and draw no rows", () => {
  assert.ok(renderToString(html`<${Deps} team=${push([])} remote=${true} />`).includes(TEAM_RELAY_WORDS));
  assert.ok(renderToString(html`<${Deps} team=${null} />`).includes(READING_WORDS));
  const empty = renderToString(html`<${Deps} team=${push([])} />`);
  assert.ok(empty.includes(NO_TASKS_WORDS));
});

test("rows: blocked, ready, critical and see above, with times only when the desk sent estimates", () => {
  const out = renderToString(html`<${Deps} team=${push(tasks())} model=${model(plain)} />`);
  assert.ok(out.includes("Critical path"));
  assert.ok(out.includes("blocked by T2"));
  assert.ok(out.includes("see above"), "T6 is wanted by T7 and T12, so it is met again");
  assert.ok(out.includes("1 ready"));
  assert.ok(out.includes("Ask mgr to start it"));
  assert.ok(out.includes("Done in"));
  assert.ok(out.includes("+1h"));
  const none = renderToString(html`<${Deps} team=${push(tasks(false))} model=${model(plain)} />`);
  assert.ok(!none.includes("Done in"));
  assert.ok(!none.includes("left, if nothing waits"));
});

test("a ring of agents gets the deadlock block", () => {
  const m = model([node("mgr", { role: "manager" }), node("api", { waitingOn: waits("tests") }), node("tests", { waitingOn: waits("api") })]);
  const out = renderToString(html`<${Deps} team=${push(tasks())} model=${m} send=${() => true} />`);
  assert.ok(out.includes("Deadlock"));
  assert.ok(out.includes("api waits on tests waits on api. None of them can move."));
  assert.ok(out.includes("2-way loop"));
  assert.ok(out.includes("Ask mgr to break the loop"));
  assert.ok(out.includes("the other half"));
  assert.ok(breakText(["a", "b"]).includes("a, b"));
  assert.ok(/class="tree-c deps-age">\d+[mh]|class="tree-c deps-age">now/.test(out), "the wait rows carry an age");
});

test("an ask is ready again when what it was about changes", async () => {
  const m = await mount();
  try {
    m.box.findAll(".deps-start-btn")[0].click();
    await settle();
    assert.ok(m.box.textContent.includes("Manager asked"));
    render(html`<${Deps} team=${push(tasks().map((t) => (t.id === "T11" ? { ...t, state: "assigned", ownerPane: "api" } : t)))} model=${model(plain)} send=${() => true} />`, m.box);
    await settle();
    assert.ok(!m.box.textContent.includes("Manager asked"));
  } finally {
    m.done();
  }
});

test("glyphOf follows the task's state", () => {
  assert.equal(glyphOf({ state: "done" }), "done");
  assert.equal(glyphOf({ state: "cancelled" }), "done");
  assert.equal(glyphOf({ state: "blocked" }), "waiting");
  assert.equal(glyphOf({ state: "planned" }), "idle");
  assert.equal(glyphOf({ state: "in-progress" }), "working");
});

async function mount(over = {}) {
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  const sent = [];
  const opened = [];
  render(html`<${Deps} team=${push(tasks())} model=${model(plain)} onOpen=${(p) => opened.push(p)} send=${(m) => { sent.push(m); return true; }} ...${over} />`, box);
  await settle();
  const row = (k) => box.findAll(".tree-row").find((e) => e.attributes["data-pane"] === k);
  return { box, row, sent, opened, done: () => { render(null, box); restore(); } };
}

test("one tab stop, arrows move it, Right and Left fold, Enter opens the owner", async () => {
  const m = await mount();
  try {
    assert.equal(m.row("GOAL").attributes.tabindex, "0");
    assert.equal(m.row("T2").attributes.tabindex, "-1");
    const before = m.box.findAll(".tree-row").length;
    m.row("GOAL").keydown("ArrowLeft");
    await settle();
    assert.equal(m.box.findAll(".tree-row").length, 1, "the goal folded away everything");
    m.row("GOAL").keydown("ArrowRight");
    await settle();
    assert.equal(m.box.findAll(".tree-row").length, before);
    m.row("GOAL").keydown("ArrowDown");
    await settle();
    assert.equal(m.box.findAll(".tree-row").filter((e) => e.attributes.tabindex === "0").length, 1);
    m.row("T2").keydown("Enter");
    assert.deepEqual(m.opened, ["ci"]);
  } finally {
    m.done();
  }
});

test("Ask to start sends the manager the task and says so", async () => {
  const m = await mount();
  try {
    const btn = m.box.findAll(".deps-start-btn")[0];
    btn.click();
    await settle();
    assert.equal(m.sent.length, 1);
    assert.equal(m.sent[0].cmd, "paneSend");
    assert.equal(m.sent[0].id, "mgr");
    assert.ok(m.sent[0].text.includes("T11"));
    assert.ok(m.box.textContent.includes("Manager asked"));
  } finally {
    m.done();
  }
});

test("sixty tasks: the idle ones are one row until opened, and a chain of one has no critical path band", async () => {
  const many = [T("A", "in-progress", [], { ownerPane: "ci" })];
  for (let i = 0; i < 59; i++) many.push(T("I" + i, "planned"));
  const m = await mount({ team: push(many) });
  try {
    assert.equal(m.box.findAll(".tree-row").length, 3, "goal, A and the group");
    assert.ok(m.box.textContent.includes("59 tasks not started, no dependencies"));
    assert.ok(!m.box.textContent.includes("Critical path"), "the longest chain is one task");
    m.row("IDLE").click();
    m.box.findAll(".tree-toggle").find((e) => e.attributes["aria-label"] === "Unfold IDLE" || (e.attributes["aria-label"] || "").includes("Unfold")).click();
    await settle();
    assert.ok(m.box.findAll(".tree-row").length > 50);
  } finally {
    m.done();
  }
});

test("with teams named on the tasks, the view shows the open team's tasks and the manager as owner", () => {
  const list = [T("T1", "assigned", [], { team: "mgr", ownerPane: "api" }), T("T2", "planned", ["T1"], { team: "mgr" }), T("X1", "assigned", [], { team: "other", ownerPane: "ci" })];
  const out = renderToString(html`<${Deps} team=${push(list)} model=${model(plain)} />`);
  assert.ok(out.includes("title T1") && out.includes("title T2"));
  assert.ok(!out.includes("title X1"), "another team's task is not in the tree");
  assert.ok(out.includes('data-open="mgr"'), "the manager of the team is the owner of its tasks");
  assert.ok(!out.includes('data-open="api"'), "not the pane the task was given to");
  const bare = renderToString(html`<${Deps} team=${push(list.map(({ team, ...t }) => t))} model=${model(plain)} />`);
  assert.ok(bare.includes("title X1"), "a push with no team named is not scoped");
});

test("a task another team runs is marked with that team's task, not its raw owner", () => {
  const two = buildGraphModel({
    type: "graph", root: "/r", project: "api", solo: [], soloNeeds: [], edges: [],
    teams: [
      { id: "mgr", manager: "mgr", goal: "Ship the API", members: ["mgr", "api"], needs: [] },
      { id: "m2", manager: "m2", goal: "Docs", taskId: "T9", members: ["m2", "docs"], needs: [] },
    ],
    nodes: [node("mgr", { role: "manager" }), node("api"), node("m2", { role: "manager", name: "docs-lead", team: "m2", task: { id: "T9", title: "Write the docs", state: "doing" } }), node("docs", { team: "m2" })],
  });
  const scoped = { ...two, teams: [two.teams[0]] };
  const list = [T("T1", "assigned", [], { team: "mgr", ownerPane: "api" }), T("T9", "assigned", ["T1"], { team: "m2", ownerPane: "docs", title: "Write the docs" })];
  const out = renderToString(html`<${Deps} team=${push(list)} model=${scoped} whole=${two} />`);
  assert.ok(out.includes("T9 · Write the docs"), "the other team is named by its task");
  assert.ok(out.includes("title T1"));
});

test("with the view scoped to a team, open tasks no team runs sit under a folded row of their own", async () => {
  const list = [T("T1", "in-progress", [], { team: "mgr" }), T("T2", "planned", []), T("T3", "done", []), T("T4", "planned", ["T2"])];
  const m = await mount({ team: push(list) });
  try {
    const head = m.row("NOTEAM");
    assert.ok(head, "the row is there");
    assert.match(head.textContent, /Not assigned to a team/);
    assert.match(head.textContent, /2 tasks/);
    assert.equal(m.row("NOTEAM-T2"), undefined, "folded at first");
    head.keydown("ArrowRight");
    await settle();
    assert.ok(m.row("NOTEAM-T2") && m.row("NOTEAM-T4"));
    assert.equal(m.row("NOTEAM-T3"), undefined, "a finished one is not listed");
  } finally {
    m.done();
  }
});

test("when no task names a team nothing is scoped, so there is no unassigned row", async () => {
  const m = await mount();
  try {
    assert.equal(m.row("NOTEAM"), undefined);
  } finally {
    m.done();
  }
});

test("the link chips of the row the keyboard is on are in the tab order, the others are not", async () => {
  const m = await mount();
  try {
    const chips = (k) => m.row(k).findAll(".is-link");
    assert.ok(chips("T4").length > 0, "T4 is blocked by T2 and has a link chip");
    assert.ok(chips("T4").every((c) => c.attributes.tabindex === "-1"));
    m.row("GOAL").keydown("ArrowDown");
    await settle();
    m.row("T7").keydown("ArrowDown");
    await settle();
    assert.ok(chips("T4").every((c) => c.attributes.tabindex === "0"));
    assert.ok(chips("T7").every((c) => c.attributes.tabindex === "-1"));
  } finally {
    m.done();
  }
});
