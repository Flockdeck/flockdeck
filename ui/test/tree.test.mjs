// The outline: one row per agent, the rows that need the person first within a
// level, and the keyboard of a tree (one tab stop, arrows, Home and End, Enter).
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { Tree, rowNameOf, foldOf, needsOf, rowsOf, stepOf, ageOf, rankOf, doingOf, tipOf } from "../src/graph/Tree.js";
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

test("a row has its level, the one tab stop on the first row, the task and no team spend", () => {
  const html_ = renderToString(html`<${Tree} model=${model(team([ask]))} />`);
  assert.ok(html_.includes('role="tree"'));
  assert.equal(html_.match(/role="treeitem"/g).length, 7);
  assert.equal(html_.match(/tabindex="0"/g).length, 1);
  assert.ok(/data-pane="m"[^>]*tabindex="0"|tabindex="0"[^>]*data-pane="m"/.test(html_));
  assert.ok(html_.includes('aria-level="3"'));
  assert.ok(html_.includes("T4"));
  assert.ok(!html_.includes("$6.10"), "the team's spend is not on the manager row");
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
  assert.ok(doingOf(m.byPane.get("ask"), m.needsByPane.get("ask"), now).includes("go test ./... -race"), "a pane that asks shows what it asks");
  assert.equal(doingOf(m.byPane.get("work"), [], now), "go test", "working shows the live line");
});

test("Doing now: idle is just \"idle\" (the Age column has how long), never the last thing it said, which is the tooltip; waiting says what on", () => {
  const now = Date.parse("2026-10-24T10:00:00Z");
  const said = "I replied to flockdeck-92 about the PR and am waiting for its answer";
  const idle = { pane: "i", status: "idle", activity: "idle", live: said, since: "2026-10-24T09:57:00Z", waitingOn: { kind: "none" } };
  assert.equal(doingOf(idle, [], now), "idle");
  assert.equal(doingOf({ ...idle, since: "" }, [], now), "idle");
  assert.equal(tipOf(idle, doingOf(idle, [], now)), said);
  const waits = { ...idle, status: "working", activity: "waiting", live: said, waitingOn: { kind: "helpers", text: "review of T3" } };
  assert.equal(doingOf(waits, [], now), "review of T3");
  assert.equal(tipOf({ live: "go test" }, "go test"), "go test");
  const m = model(team());
  const out = renderToString(html`<${Tree} model=${m} />`);
  assert.ok(!out.includes(said));
});

test("a column with nothing in any row is hidden: Task and Cost", () => {
  const g = team();
  for (const x of g.nodes) { delete x.task; x.spendUSD = 0; }
  const out = renderToString(html`<${Tree} model=${model(g)} />`);
  assert.ok(out.includes("tree--no-task") && out.includes("tree--no-cost"));
  const full = renderToString(html`<${Tree} model=${model(team())} />`);
  assert.ok(full.includes("tree--no-cost") === !team().nodes.some((x) => x.spendUSD > 0));
  assert.ok(!full.includes("tree--no-task"), "T4 is on a row");
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
  } finally {
    m.done();
  }
});

import { stuckOf, rollupOf, STUCK_MS } from "../src/graph/model.js";
import { costOf } from "../src/graph/Tree.js";

test("a pane's own spend is its cost, and a manager does not show the team's", () => {
  assert.equal(costOf({ spendUSD: 3.824 }), "$3.82");
  assert.equal(costOf({ spendUSD: 0 }), "", "a team's spend is not put on its manager");
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

import { chipsOf } from "../src/graph/chips.js";

// The chips of the api-service team of prototype C.
function proto() {
  const g = team([
    { id: "p", kind: "permission", pane: "ask", time: "2026-10-24T08:20:00Z", text: "go test" },
    { id: "c", kind: "conflict", pane: "ask", other: "idle", time: "2026-10-24T08:21:00Z", text: "cfg/limits.yaml" },
    { id: "d", kind: "down", pane: "down", time: "2026-10-24T08:30:00Z", text: "exited with code 2" },
  ]);
  const by = (p) => g.nodes.find((x) => x.pane === p);
  by("w1").waitingOn = { kind: "helpers", text: "Limiter interface", ref: "work" };
  by("w1").undelivered = 1;
  by("idle").waitingOn = { kind: "reviewer", text: "" };
  by("work").lastOutput = "2026-10-24T09:00:00Z";
  by("work").waitingOn = { kind: "task", text: "" };
  return g;
}
const NOW = Date.parse("2026-10-24T09:12:00Z");
const chips = (p) => { const m = model(proto()); return chipsOf(m, m.byPane.get(p), needsOf(m, p), NOW); };
const texts = (p) => chips(p).map((c) => c.text);

test("chips: the ask kind, the overlap on both panes, waiting on, undelivered and stuck, in that order", () => {
  assert.deepEqual(texts("ask"), ["Permission", "overlaps idle"]);
  assert.deepEqual(texts("idle"), ["overlaps ask", "waiting on a reviewer"]);
  assert.deepEqual(texts("w1"), ["waiting on work", "1 undelivered"]);
  assert.deepEqual(texts("work"), ["waiting on a task", "stuck 12m"]);
  assert.deepEqual(texts("down"), ["Down"]);
  assert.deepEqual(texts("m"), []);
  assert.equal(chips("ask")[1].to, "idle");
  assert.equal(chips("idle")[0].to, "ask");
  assert.equal(chips("w1")[0].to, "work");
  assert.equal(chips("idle")[1].to, "", "waiting on a reviewer names no row");
  assert.deepEqual(chips("ask").map((c) => c.kind), ["ask", "overlap"]);
});

test("chips: a pane that waits on a helper that is not a row has a plain chip, and a quiet need makes none", () => {
  const g = proto();
  g.nodes.find((x) => x.pane === "w1").waitingOn = { kind: "helpers", text: "x", ref: "gone" };
  g.teams[0].needs.push({ id: "s", kind: "stale-unread", pane: "w1", time: "2026-10-24T08:20:00Z", text: "" });
  const m = model(g);
  const out = chipsOf(m, m.byPane.get("w1"), needsOf(m, "w1"), NOW);
  assert.deepEqual(out.map((c) => c.text), ["waiting on a helper", "1 undelivered"]);
  assert.equal(out[0].to, "");
});

test("link chips are buttons that name their row, plain chips are not", () => {
  const out = renderToString(html`<${Tree} model=${model(proto())} />`);
  assert.equal((out.match(/<button[^>]*tree-chip/g) || []).length, 3, "overlaps x2 and waiting on work");
  assert.ok(out.includes("tree-chip--ask") && out.includes("tree-chip--down"));
  assert.ok(out.includes("1 undelivered"));
});

test("hovering a link chip lights both rows, leaving it clears them, and a click goes to the row without opening the drawer", async () => {
  const m = await mount({ model: model(proto()) });
  try {
    const chip = m.row("ask").findAll(".tree-chip").find((c) => c.textContent.startsWith("overlaps"));
    assert.ok(chip, "the chip");
    chip.dispatch("mouseenter", {});
    await settle();
    assert.ok((m.row("ask").attributes.class || "").includes("is-lit"));
    assert.ok((m.row("m").attributes.class || "").includes("is-lit") === false);
    chip.dispatch("mouseleave", {});
    await settle();
    assert.ok(!(m.row("ask").attributes.class || "").includes("is-lit"));
    chip.dispatch("click");
    await settle();
    assert.deepEqual(m.opened, []);
  } finally {
    m.done();
  }
});

test("a lone agent has no header and no chevron; a manager with no helpers says so, once", () => {
  const solo = { type: "graph", root: "/r", project: "p", teams: [], nodes: [n("s", { team: "" })], edges: [], solo: ["s"], soloNeeds: [] };
  const a = renderToString(html`<${Tree} model=${model(solo)} />`);
  assert.ok(!a.includes("tree-head") && !a.includes("tree-note") && a.includes("treeitem"));
  const g = team();
  g.nodes = [g.nodes[0]];
  g.nodes[0].children = [];
  g.teams[0].members = ["m"];
  const b = renderToString(html`<${Tree} model=${model(g)} />`);
  assert.ok(b.includes("tree-head"));
  assert.equal((b.match(/No helpers yet\. The manager starts them as it plans\./g) || []).length, 1);
  assert.ok(!renderToString(html`<${Tree} model=${model(team())} />`).includes("No helpers yet"));
});

test("the row tints are not beaten by the hover rule, and the name has a floor", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../src/graph/tree.css", import.meta.url), "utf8");
  assert.ok(css.includes(".tree-row:where(:hover)"));
  assert.ok(css.indexOf(".tree-row:where(:hover)") < css.indexOf(".tree-row--ask"));
  assert.ok(css.includes("max(48px, calc(112px"));
  assert.ok(!css.includes(".tree-view") && !css.includes(".tree-goal"));
});

test("only the stop row's chips are tab stops, and the aria-label names the chips and the other pane", async () => {
  const m = await mount({ model: model(proto()) });
  try {
    const tab = (p) => m.row(p).findAll(".tree-chip").filter((c) => c.attributes.tabindex !== undefined).map((c) => c.attributes.tabindex);
    assert.deepEqual(tab("ask"), ["-1"]);
    m.row("m").focus();
    m.row("m").keydown("ArrowDown");
    await settle();
    assert.deepEqual(tab("ask"), ["0"]);
    assert.ok(m.row("ask").attributes["aria-label"].includes("overlaps idle"));
    const chip = m.row("ask").findAll(".tree-chip").find((c) => c.textContent.startsWith("overlaps"));
    assert.ok(chip.attributes["aria-label"].includes("go to idle"));
  } finally {
    m.done();
  }
});

test("an arrow on a chip moves between rows, Enter on a chip is not the row's", async () => {
  const m = await mount({ model: model(proto()) });
  try {
    m.row("ask").focus();
    await settle();
    const chip = m.row("ask").findAll(".tree-chip").find((c) => c.textContent.startsWith("overlaps"));
    assert.equal(chip.keydown("ArrowDown").defaultPrevented, true);
    await settle();
    assert.equal(m.dom.document.activeElement, m.row("down"));
    chip.keydown("Enter");
    assert.deepEqual(m.opened, []);
  } finally {
    m.done();
  }
});

test("the chips shrink with the row and the note under an empty manager is a disabled treeitem", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../src/graph/tree.css", import.meta.url), "utf8");
  assert.ok(/\.tree-chips \{[^}]*flex: 0 1 auto[^}]*min-width: 0[^}]*overflow: hidden/.test(css));
  const g = team();
  g.nodes = [g.nodes[0]];
  g.nodes[0].children = [];
  g.teams[0].members = ["m"];
  const out = renderToString(html`<${Tree} model=${model(g)} />`);
  assert.ok(/role="treeitem"[^>]*aria-disabled="true"[^>]*>No helpers yet|aria-disabled="true"[^>]*role="treeitem"/.test(out) || out.includes('aria-disabled="true"'));
});

test("a roll-up counts exactly the rows folding the pane hides, even for a helper linked only by parent", () => {
  const g = team([ask]);
  g.nodes.find((x) => x.pane === "work").children = ["w1"]; // w2 names work as its parent but is not listed
  const m = model(g);
  const all = rowsOf(m).length;
  for (const p of ["m", "work", "ask", "idle"]) {
    const hidden = all - rowsOf(m, new Set([p])).length;
    assert.equal(rollupOf(m, p).below, hidden, p);
  }
  assert.equal(rollupOf(m, "work").below, 2);
  assert.equal(rowsOf(m, new Set(["work"])).filter((r) => r.pane === "w2").length, 0, "no stray row for a folded helper");
});

test("the roll-up sits in its own span beside the ellipsised doing text, and the chevron is a button off the tab order", async () => {
  const m = await mount();
  try {
    m.row("work").findAll(".tree-toggle")[0].dispatch("click");
    await settle();
    const cell = m.row("work").findAll(".tree-c--doing")[0];
    assert.ok(cell.findAll(".tree-doing-text").length === 1 && cell.findAll(".tree-roll").length === 1);
    assert.ok(!cell.findAll(".tree-doing-text")[0].textContent.includes("below"));
    const tog = m.row("work").findAll(".tree-toggle")[0];
    assert.equal(tog.localName, "button");
    assert.equal(tog.attributes.tabindex, "-1");
    assert.ok(tog.attributes["aria-label"].startsWith("Unfold"));
  } finally {
    m.done();
  }
});

test("foldOf: Right opens a folded parent, Left folds an open one, a row with no helpers folds nothing", () => {
  const rows = rowsOf(model(team([ask])));
  assert.equal(foldOf(rows, "work", "ArrowLeft"), "collapse");
  assert.equal(foldOf(rows, "work", "ArrowRight"), "");
  assert.equal(foldOf(rows, "idle", "ArrowLeft"), "");
  const folded = rowsOf(model(team([ask])), new Set(["work"]));
  assert.equal(foldOf(folded, "work", "ArrowRight"), "expand");
  assert.equal(foldOf(folded, "work", "ArrowLeft"), "");
  assert.equal(foldOf(folded, "nobody", "ArrowLeft"), "");
  // Right on a folded parent does not move; on an open one it goes to the first helper.
  assert.equal(stepOf(folded, "work", "ArrowRight"), "");
  assert.equal(stepOf(rows, "work", "ArrowRight"), "w2");
  // Left on an open parent folds it, so it does not go to its own parent.
  assert.equal(stepOf(rows, "work", "ArrowLeft"), "");
  assert.equal(stepOf(folded, "work", "ArrowLeft"), "m");
  assert.deepEqual(folded.map((r) => r.pane), ["m", "ask", "down", "work", "idle"]);
});

test("a folded parent says how many are below and how many need you, and a click on the chevron folds it", async () => {
  const m = await mount();
  try {
    assert.ok(!m.row("work").textContent.includes("below"));
    m.row("work").findAll(".tree-toggle")[0].dispatch("click");
    await settle();
    const text = m.row("work").textContent;
    assert.ok(text.includes("2 below"), text);
    assert.ok(text.includes("1 need you"), text);
    assert.equal(m.row("work").attributes["aria-expanded"], "false");
    assert.equal(m.row("w1"), undefined);
    assert.deepEqual(m.opened, [], "the chevron does not open the drawer");
    m.row("work").keydown("ArrowRight");
    await settle();
    assert.equal(m.row("work").attributes["aria-expanded"], "true");
    assert.ok(m.row("w1"));
    m.row("work").keydown("ArrowLeft");
    await settle();
    assert.equal(m.row("w1"), undefined);
  } finally {
    m.done();
  }
});

test("the columns follow the main column and a phone gets two lines", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../src/graph/tree.css", import.meta.url), "utf8");
  assert.ok(css.includes("@container main (max-width: 680px)"));
  assert.ok(/@container main[^{]*\{[^@]*\.tree-c--role, \.tree-c--task, \.tree-c--age \{ display: none; \}/.test(css));
  assert.ok(css.includes("@media (max-width: 560px)"));
});

import { labelOf } from "../src/graph/model.js";

test("panes that share a name read as role and a stable name, never the live line, and stay apart when that is shared too", () => {
  const g = team();
  const live = {
    work: "The test-fix commit is ready, running go vet ./... before I push it",
    ask: "I replied to flockdeck-92 about the PR and am waiting for its answer",
    idle: "Bash",
  };
  for (const p of ["idle", "work", "ask"]) {
    const x = g.nodes.find((y) => y.pane === p);
    x.name = "Bash";
    x.live = live[p];
  }
  g.nodes.find((x) => x.pane === "work").task = { id: "T4", title: "tabs per goal", state: "doing" };
  const m = model(g);
  assert.equal(labelOf(m, "work"), "worker · tabs per goal", "the task title");
  assert.match(labelOf(m, "ask"), /^worker · Bash · [0-9a-z]{3,4}$/, "the pane's name, then the start of its id because the two collide");
  assert.notEqual(labelOf(m, "ask"), labelOf(m, "idle"));
  for (const p of ["idle", "work", "ask"]) assert.ok(!labelOf(m, p).includes("commit") && !labelOf(m, p).includes("replied"), "no live line in " + p);
  assert.equal(labelOf(m, "m"), "m", "a name nobody else has is left alone");
  assert.deepEqual(rowNameOf(m, m.byPane.get("work")), { role: "worker · ", name: "tabs per goal" });
  const out = renderToString(html`<${Tree} model=${m} />`);
  assert.ok(out.includes(">tabs per goal<") && out.includes('class="tree-name-role">worker · <'), "the role is in the name cell, shown where the Role column is gone");
  assert.ok(out.includes("tree--shared-names"));
  assert.ok(!renderToString(html`<${Tree} model=${model(team())} />`).includes("tree--shared-names"));
  assert.equal(labelOf(m, "nobody"), "nobody");
  assert.equal(labelOf(null, "x"), "x");
});

import { decisionOf } from "../src/graph/DecisionBlock.js";
import { loopOf } from "../src/graph/DependencyDecisions.js";
import { overlapsOf } from "../src/graph/OverlapBar.js";

test("decision blocks, the overlap bar and the loop block name panes that share a name by role and a stable name", () => {
  const g = team([ask, { id: "c", kind: "conflict", pane: "ask", other: "work", time: "2026-10-24T08:21:00Z", text: "a.go" }]);
  for (const p of ["ask", "work"]) g.nodes.find((x) => x.pane === p).name = "flockdeck";
  g.nodes.find((x) => x.pane === "ask").live = "reading logs";
  const m = model(g);
  assert.equal(decisionOf(m, ask).who, "worker · flockdeck");
  assert.ok(overlapsOf(m)[0].who.includes("worker · flockdeck and worker · Limiter"));
  const lp = model(team());
  assert.equal(loopOf(lp), null);
});

import { totalOf } from "../src/graph/Tree.js";

test("totalOf is the plain sum of what every pane spent, and nothing when nothing was", () => {
  assert.equal(totalOf(buildGraphModel(team())), "");
  const g = team();
  g.nodes[0].spendUSD = 1.5;
  g.nodes[1].spendUSD = 0.25;
  assert.equal(totalOf(buildGraphModel(g)), "$1.75");
  assert.equal(totalOf(null), "");
});

test("an idle row says only idle: how long is the Age column's", () => {
  const out = renderToString(html`<${Tree} model=${model(team())} />`);
  assert.doesNotMatch(out, /idle \d+[smhd]/);
});

test("on a phone the doing text breaks inside a long unbroken path, so the outline never scrolls sideways", async () => {
  const { readFileSync } = await import("node:fs");
  const css = readFileSync(new URL("../src/graph/tree.css", import.meta.url), "utf8");
  const phone = css.slice(css.indexOf("@media (max-width: 560px)"));
  assert.match(phone.slice(0, phone.indexOf("\n}")), /\.tree-doing-text \{[^}]*overflow-wrap: anywhere/);
});

test("the stuck chip writes its time as the Age column does", () => {
  const m = model(proto());
  const chip = (min) => chipsOf(m, { ...m.byPane.get("work"), lastOutput: new Date(NOW - min * 60000).toISOString() }, [], NOW).find((c) => c.kind === "stuck").text;
  assert.equal(chip(12), "stuck 12m");
  assert.equal(chip(300), "stuck 5h");
  assert.equal(chip(5472), "stuck 3d");
  assert.equal(ageOf(new Date(NOW - 5472 * 60000).toISOString(), NOW), "3d");
});
