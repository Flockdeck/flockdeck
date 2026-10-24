// The Dependencies view: the tree of tasks by what blocks them, the critical path,
// the deadlock block, and the keyboard of a tree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { Deps, glyphOf, NO_TASKS_WORDS, READING_WORDS, breakText } from "../assets/graph/DepsView.js";
import { TEAM_RELAY_WORDS } from "../assets/lib/team.js";
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
