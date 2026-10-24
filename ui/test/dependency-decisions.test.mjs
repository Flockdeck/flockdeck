// The deadlock and ready-to-start blocks: loop finding and what the buttons send.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { buildGraphModel } from "../src/graph/model.js";
import { Deadlock, ReadyToStart, loopOf } from "../src/graph/DependencyDecisions.js";
import { createDom, install, settle } from "./dom.mjs";

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "working", activity: "idle", waitingOn: { kind: "none" }, ...over });
const wait = (ref, text = "") => ({ kind: "helpers", ref, text });
const model = (nodes) => buildGraphModel({ type: "graph", root: "C:/r", project: "r", teams: [], nodes, edges: [], solo: nodes.map((n) => n.pane), soloNeeds: [] });
const loopy = () => model([node("api", { waitingOn: wait("tests", "a review of mw/limiter.go") }), node("tests", { waitingOn: wait("api", "the Limiter interface") }), node("docs")]);

test("loopOf finds a loop of waits and nothing when the waits end", () => {
  const l = loopOf(loopy());
  assert.deepEqual(l.map((s) => [s.name, s.onName]), [["api", "tests"], ["tests", "api"]]);
  assert.equal(l[0].why, "a review of mw/limiter.go");
  assert.equal(loopOf(model([node("a", { waitingOn: wait("b") }), node("b")])), null);
  assert.equal(loopOf(model([node("a", { waitingOn: wait("a") })])), null, "waiting on itself is no edge");
  assert.equal(loopOf(model([node("a", { waitingOn: wait("zz") })])), null, "a ref that is not a node");
  const tail = model([node("x", { waitingOn: wait("a") }), node("a", { waitingOn: wait("b") }), node("b", { waitingOn: wait("a") })]);
  assert.deepEqual(loopOf(tail).map((s) => s.name), ["a", "b"], "a path leading into a loop is not part of it");
});

async function mount(vnode, ws) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  render(vnode, container);
  await settle();
  return { container, done: async () => { render(null, container); await settle(); restore(); } };
}

test("the deadlock block names the loop and sends once to the manager, and once to the agent", async () => {
  const sent = [];
  const ctx = { manager: { pane: "m", name: "mgr" }, store: { send: (c) => { sent.push(c); return true; } }, onOpen() {} };
  const a = await mount(html`<${Deadlock} model=${loopy()} loop=${loopOf(loopy())} ctx=${ctx} />`);
  try {
    const line = a.container.find(".dec-line");
    assert.match(line.textContent, /api waits on tests for a review of mw\/limiter\.go, and tests waits on api for the Limiter interface\. Neither can move\./);
    const btns = a.container.findAll(".dec-btn");
    assert.deepEqual(btns.map((b) => b.textContent), ["Ask mgr to break the loop", "Tell tests to use a stub", "Open tests"]);
    btns[0].click();
    btns[0].click();
    btns[1].click();
    await settle();
    assert.equal(sent.length, 2);
    assert.deepEqual([sent[0].cmd, sent[0].id], ["paneSend", "m"]);
    assert.deepEqual([sent[1].cmd, sent[1].id], ["paneSend", "tests"]);
  } finally {
    await a.done();
  }
});

test("a loop of three says none of them can move", async () => {
  const m = model([node("a", { waitingOn: wait("b") }), node("b", { waitingOn: wait("c") }), node("c", { waitingOn: wait("a") })]);
  const a = await mount(html`<${Deadlock} model=${m} loop=${loopOf(m)} ctx=${{}} />`);
  try {
    assert.match(a.container.find(".dec-line").textContent, /None of them can move.$/);
  } finally {
    await a.done();
  }
});

test("the ready block asks the manager to start the task, once, and is off with no manager", async () => {
  const sent = [];
  const ctx = { manager: { pane: "m", name: "mgr" }, store: { send: (c) => { sent.push(c); return true; } } };
  const a = await mount(html`<${ReadyToStart} task=${{ id: "T7", title: "Load test harness" }} ctx=${ctx} />`);
  try {
    assert.match(a.container.find(".dec-line").textContent, /T7 Load test harness has no open blockers and nobody on it\./);
    const b = a.container.find(".dec-btn");
    b.click();
    b.click();
    await settle();
    assert.equal(sent.length, 1);
    assert.equal(sent[0].text, "Please start T7: Load test harness");
  } finally {
    await a.done();
  }
  const nomgr = await mount(html`<${ReadyToStart} task=${{ id: "T7", title: "x" }} ctx=${{ store: { send: () => true } }} />`);
  try {
    assert.ok(nomgr.container.find(".dec-btn").disabled || nomgr.container.find(".dec-btn").getAttribute("disabled") !== null, "off with no manager");
  } finally {
    await nomgr.done();
  }
  const relayed = await mount(html`<${ReadyToStart} task=${{ id: "T7", title: "x" }} ctx=${{ manager: { pane: "m" } }} />`);
  try {
    assert.equal(relayed.container.findAll(".dec-btn").length, 0, "no answering button without a store");
    assert.match(relayed.container.textContent, /Answer this on the desktop/);
  } finally {
    await relayed.done();
  }
});
