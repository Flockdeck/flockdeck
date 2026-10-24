// The outline: one row per agent, the rows that need the person first within a
// level, and the keyboard of a tree (one tab stop, arrows, Home and End, Enter).
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { Tree, needsOf, rowsOf, stepOf, ageOf, rankOf, doingOf } from "../assets/graph/Tree.js";
import { createDom, install, settle } from "./dom.mjs";

const n = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, team: "t", name: pane, role: "worker", status: "working", activity: "working", live: "go test", waitingOn: { kind: "none" }, children: [], ...over });
// m has helpers idle, work, ask (asks to run something) and down; work has two helpers.
function team(needs = []) {
  const nodes = [
    n("m", { role: "manager", children: ["idle", "work", "ask", "down"] }),
    n("idle", { parent: "m", depth: 1, status: "idle", activity: "idle", live: "", since: "2026-10-24T08:00:00Z" }),
    n("work", { parent: "m", depth: 1, children: ["w1", "w2"], since: "2026-10-24T08:10:00Z", task: { id: "T4", title: "Limiter", state: "doing" } }),
    n("ask", { parent: "m", depth: 1, activity: "waiting", status: "working", since: "2026-10-24T08:20:00Z" }),
    n("down", { parent: "m", depth: 1, status: "exited", activity: "exited", since: "2026-10-24T08:30:00Z" }),
    n("w1", { parent: "work", depth: 2, status: "idle", activity: "idle", since: "2026-10-24T08:30:00Z" }),
    n("w2", { parent: "work", depth: 2, activity: "waiting", since: "2026-10-24T08:31:00Z" }),
  ];
  return { type: "graph", root: "/r", project: "p", teams: [{ id: "t", manager: "m", goal: "Ship it", spendUSD: 6.1, members: nodes.map((x) => x.pane), needs }], nodes, edges: [], solo: [], soloNeeds: [] };
}
const model = (m = team()) => buildGraphModel(m);
const ask = { id: "a", kind: "permission", pane: "ask", time: "2026-10-24T08:20:00Z", text: "go test ./... -race" };

test("a level lists the pane that asks first, then one that ended, working, then idle, helpers under their parent", () => {
  const rows = rowsOf(model(team([ask])));
  assert.deepEqual(rows.map((r) => r.pane), ["m", "ask", "down", "work", "w2", "w1", "idle"]);
  assert.deepEqual(rows.map((r) => r.depth), [0, 1, 1, 1, 2, 2, 1]);
  assert.equal(rows.find((r) => r.pane === "work").kids, 2);
  assert.equal(rows.find((r) => r.pane === "w1").parent, "work");
});

test("within one state the pane longest in it comes first, and a pane with no time after the rest", () => {
  const m = team();
  m.nodes.push(n("a1", { parent: "m", depth: 1, since: "2026-10-24T07:00:00Z" }), n("a2", { parent: "m", depth: 1, since: "" }));
  m.nodes[0].children.push("a1", "a2");
  m.teams[0].members.push("a1", "a2");
  const order = rowsOf(model(m)).filter((r) => r.parent === "m").map((r) => r.pane);
  assert.ok(order.indexOf("a1") < order.indexOf("work"));
  assert.ok(order.indexOf("work") < order.indexOf("a2"));
});

test("a pane in no team and a member the tree does not reach are rows too, once", () => {
  const m = team();
  m.nodes.push(n("lost", { team: "t", parent: "", depth: 0 }), n("solo", { team: "" }));
  m.teams[0].members.push("lost");
  m.solo = ["solo"];
  const panes = rowsOf(model(m)).map((r) => r.pane);
  assert.equal(new Set(panes).size, panes.length);
  assert.ok(panes.includes("lost") && panes.includes("solo"));
});

test("a message that is not a graph has no rows, and the outline draws nothing", () => {
  assert.deepEqual(rowsOf(buildGraphModel(null)), []);
  assert.equal(renderToString(html`<${Tree} model=${buildGraphModel(null)} />`), "");
});

test("stepOf moves by row, to the ends, to the first helper and to the parent", () => {
  const rows = rowsOf(model(team([ask])));
  assert.equal(stepOf(rows, "m", "ArrowDown"), "ask");
  assert.equal(stepOf(rows, "ask", "ArrowUp"), "m");
  assert.equal(stepOf(rows, "m", "ArrowUp"), "m");
  assert.equal(stepOf(rows, "idle", "ArrowDown"), "idle");
  assert.equal(stepOf(rows, "ask", "Home"), "m");
  assert.equal(stepOf(rows, "ask", "End"), "idle");
  assert.equal(stepOf(rows, "work", "ArrowRight"), "w2");
  assert.equal(stepOf(rows, "ask", "ArrowRight"), "");
  assert.equal(stepOf(rows, "w1", "ArrowLeft"), "work");
  assert.equal(stepOf(rows, "m", "ArrowLeft"), "");
  assert.equal(stepOf(rows, "nobody", "ArrowDown"), "");
  assert.equal(stepOf(rows, "m", "x"), "");
});

test("a row has its level, the one tab stop on the first row, the task and the manager's spend", () => {
  const html_ = renderToString(html`<${Tree} model=${model(team([ask]))} />`);
  assert.ok(html_.includes('role="tree"'));
  assert.equal(html_.match(/role="treeitem"/g).length, 7);
  assert.equal(html_.match(/tabindex="0"/g).length, 1);
  assert.ok(/data-pane="m"[^>]*tabindex="0"|tabindex="0"[^>]*data-pane="m"/.test(html_));
  assert.ok(html_.includes('aria-level="3"'));
  assert.ok(html_.includes("T4"));
  assert.ok(html_.includes("$6.10"));
  assert.ok(html_.includes("tree-row--ask") && html_.includes("tree-row--down"));
  assert.ok(html_.includes("go test"), "the live line is the doing line");
});

test("the pane whose drawer is open is selected", () => {
  const out = renderToString(html`<${Tree} model=${model()} open="work" />`);
  assert.ok(/data-pane="work"[^>]*aria-selected="true"|aria-selected="true"[^>]*data-pane="work"/.test(out));
  assert.equal(out.match(/aria-selected="true"/g).length, 1);
});

test("ageOf, rankOf and doingOf", () => {
  const now = Date.parse("2026-10-24T10:00:00Z");
  assert.equal(ageOf("2026-10-24T10:00:00Z", now), "now");
  assert.equal(ageOf("2026-10-24T09:58:10Z", now), "1m");
  assert.equal(ageOf("2026-10-24T08:30:00Z", now), "1h");
  assert.equal(ageOf("2026-10-20T08:30:00Z", now), "4d");
  assert.equal(ageOf("0001-01-01T00:00:00Z", now), "");
  assert.equal(ageOf("", now), "");
  const m = model(team([ask]));
  assert.equal(rankOf(m.byPane.get("ask"), m.needsByPane.get("ask")), 0);
  assert.equal(rankOf(m.byPane.get("down"), []), 1);
  assert.equal(rankOf(m.byPane.get("work"), []), 2);
  assert.equal(rankOf(m.byPane.get("idle"), []), 3);
  assert.equal(doingOf(m.byPane.get("ask"), m.needsByPane.get("ask")), "go test", "the live line wins");
  assert.ok(doingOf({ live: "", activity: "waiting", status: "waiting" }, m.needsByPane.get("ask")).includes("go test ./... -race"));
  assert.equal(doingOf(m.byPane.get("work"), []), "go test");
  assert.equal(doingOf(m.byPane.get("idle"), []), "idle");
});

// The keyboard on the fake page.
async function mount(over = {}) {
  const dom = createDom();
  const restore = install(dom);
  const box = dom.document.createElement("div");
  dom.body.appendChild(box);
  const opened = [];
  render(html`<${Tree} model=${model(team([ask]))} onOpen=${(p) => opened.push(p)} ...${over} />`, box);
  await settle();
  const row = (p) => box.findAll(".tree-row").find((e) => e.attributes["data-pane"] === p);
  return { dom, box, row, opened, done: () => { render(null, box); restore(); } };
}

test("an arrow moves the keyboard and the one tab stop with it, Enter and Space open the drawer", async () => {
  const m = await mount();
  try {
    m.row("m").focus();
    assert.equal(m.row("m").keydown("ArrowDown").defaultPrevented, true);
    await settle();
    assert.equal(m.dom.document.activeElement, m.row("ask"));
    assert.equal(m.row("ask").attributes.tabindex, "0");
    assert.equal(m.row("m").attributes.tabindex, "-1");
    m.row("ask").keydown("End");
    await settle();
    assert.equal(m.dom.document.activeElement, m.row("idle"));
    m.row("idle").keydown("Home");
    await settle();
    assert.equal(m.dom.document.activeElement, m.row("m"));
    m.row("m").keydown("Enter");
    m.row("m").keydown(" ");
    assert.deepEqual(m.opened, ["m", "m"]);
  } finally {
    m.done();
  }
});

test("a key with a modifier, an unknown key and Esc are left to the page", async () => {
  const m = await mount();
  try {
    m.row("m").focus();
    assert.equal(m.row("m").keydown("ArrowDown", { ctrlKey: true }).defaultPrevented, false);
    assert.equal(m.row("m").keydown("a").defaultPrevented, false);
    assert.equal(m.row("m").keydown("Escape").defaultPrevented, false);
    assert.equal(m.row("m").keydown("ArrowLeft").defaultPrevented, false);
  } finally {
    m.done();
  }
});

import { stuckOf, rollupOf, STUCK_MS } from "../assets/graph/model.js";
import { costOf } from "../assets/graph/Tree.js";

test("a pane's own spend is its cost, and a manager with none shows the team's", () => {
  assert.equal(costOf({ spendUSD: 3.824 }, null), "$3.82");
  assert.equal(costOf({ spendUSD: 0 }, { spendUSD: 6.1 }), "$6.10");
  assert.equal(costOf({ spendUSD: 0 }, null), "");
});

test("stuckOf: working, terminal, no output for ten minutes, with the desk's flag as the fallback", () => {
  const now = Date.parse("2026-10-24T10:00:00Z");
  const w = (over) => ({ activity: "working", status: "working", structured: false, lastOutput: "", stuck: false, ...over });
  assert.deepEqual(stuckOf(w({ lastOutput: "2026-10-24T09:50:00Z" }), now), { minutes: 10 });
  assert.equal(stuckOf(w({ lastOutput: "2026-10-24T09:50:01Z" }), now), null);
  assert.deepEqual(stuckOf(w({ lastOutput: "2026-10-24T09:30:00Z", stuck: false }), now), { minutes: 30 });
  assert.equal(stuckOf(w({ lastOutput: "2026-10-24T09:59:00Z", stuck: true }), now), null, "the time wins over the flag");
  assert.deepEqual(stuckOf(w({ stuck: true }), now), { minutes: 0 });
  assert.equal(stuckOf(w({}), now), null);
  assert.equal(stuckOf(w({ activity: "idle", status: "idle", lastOutput: "2026-10-24T08:00:00Z" }), now), null);
  assert.equal(stuckOf(w({ structured: true, lastOutput: "2026-10-24T08:00:00Z" }), now), null);
  assert.equal(stuckOf(null, now), null);
  assert.equal(STUCK_MS, 600000);
});

test("rollupOf counts the panes below and those that wait on the person or ended, not quiet ones", () => {
  const m = model(team([ask, { id: "q", kind: "stuck", pane: "w1", time: "2026-10-24T08:20:00Z", text: "" }]));
  assert.deepEqual(rollupOf(m, "m"), { below: 6, needYou: 3 }); // ask, down, w2
  assert.deepEqual(rollupOf(m, "work"), { below: 2, needYou: 1 });
  assert.deepEqual(rollupOf(m, "idle"), { below: 0, needYou: 0 });
  assert.deepEqual(rollupOf(m, "nobody"), { below: 0, needYou: 0 });
});

test("rollupOf uses the desk's figures only when no helpers are known here", () => {
  const g = team();
  g.nodes[2].below = 0;
  g.nodes.find((x) => x.pane === "idle").below = 4;
  g.nodes.find((x) => x.pane === "idle").needYou = 2;
  const m = model(g);
  assert.deepEqual(rollupOf(m, "idle"), { below: 4, needYou: 2 });
  assert.deepEqual(rollupOf(m, "m"), { below: 6, needYou: 3 }); // the tree wins over the desk's 0
});

test("a conflict is a row's need only under the pane it names first", () => {
  const c = { id: "c", kind: "conflict", pane: "w1", other: "idle", time: "2026-10-24T08:20:00Z", text: "a.yaml" };
  const m = model(team([c]));
  assert.equal(needsOf(m, "w1").length, 1);
  assert.equal(needsOf(m, "idle").length, 0);
  assert.equal(m.needsByPane.get("idle").length, 1, "the model still has it under both");
  assert.ok(rowsOf(m).findIndex((r) => r.pane === "idle") > 0);
});
