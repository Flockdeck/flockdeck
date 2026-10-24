// Decision blocks: what each of the four kinds says, what its buttons send, and
// that nothing answers from a bare key. The store runs on a fake socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { useStore } from "../assets/lib/use-store.js";
import { buildGraphModel } from "../assets/graph/model.js";
import { Decisions, decisionOf, decisionsOf, isDecision, questionOptions } from "../assets/graph/DecisionBlock.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const node = (pane, over = {}) => ({ pane, kind: "terminal", depth: 0, name: pane, status: "idle", activity: "idle", waitingOn: { kind: "none" }, ...over });
const graph = (needs) => ({
  type: "graph", root: "C:/repo", project: "repo",
  teams: [{ id: "t1", manager: "m", members: ["m", "api", "cfg", "lint"], goal: "g", needs }],
  nodes: [node("m", { name: "mgr", team: "t1", children: ["api", "cfg", "lint"] }), node("api", { team: "t1", parent: "m" }), node("cfg", { team: "t1", parent: "m" }), node("lint", { team: "t1", parent: "m" })],
  edges: [], solo: [], soloNeeds: [],
});
const need = (kind, pane, over = {}) => ({ id: kind + pane, kind, pane, time: new Date(Date.now() - 120000).toISOString(), text: "", ...over });
const items = [
  need("permission", "api", { text: "go test ./... -race", task: "T4" }),
  need("ask", "cfg", { text: "which flag store should T2 use?" }),
  need("conflict", "api", { other: "cfg", text: "cfg/limits.yaml" }),
  need("down", "lint", { text: "exited with code 2", task: "T10" }),
];

async function mount(ctxOver = {}, pane = "api", skip = []) {
  const h = await harness();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  h.store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  const model = buildGraphModel(graph(items));
  function App() {
    const snap = useStore(h.store);
    return html`<${Decisions} model=${model} pane=${pane} skip=${skip} ctx=${{ snap, store: h.store, manager: { pane: "m", name: "mgr" }, ...ctxOver }} />`;
  }
  render(html`<${App} />`, container);
  await settle();
  return { ...h, ws, container, model, done: async () => { render(null, container); await settle(); restore(); } };
}

test("only the four kinds are decisions, and a conflict is drawn under its first pane only", () => {
  const model = buildGraphModel(graph(items));
  assert.ok(isDecision(items[0]) && !isDecision(need("returned", "api")));
  assert.deepEqual(decisionsOf(model, "api").map((n) => n.kind), ["permission", "conflict"]);
  assert.deepEqual(decisionsOf(model, "cfg").map((n) => n.kind), ["ask"], "the other pane carries no block");
});

test("decisionOf words each kind and hides a text that was replaced for the relay", () => {
  const model = buildGraphModel(graph(items));
  const [perm, , conflict, down] = model.needs.length ? items.map((n) => decisionOf(model, model.needs.find((m) => m.id === n.id) || n)) : [];
  assert.equal(perm.command, "go test ./... -race");
  assert.equal(perm.task, "T4");
  assert.match(conflict.line, /^ both change cfg\/limits\.yaml\./);
  assert.equal(conflict.who2, "cfg");
  assert.match(down.line, /exited with code 2/);
  assert.equal(down.tone, "down");
  const hidden = decisionOf(model, { ...model.needs[0], textHidden: true, text: "secret" });
  assert.equal(hidden.command, "");
  assert.ok(!hidden.line.includes("secret"));
});

test("questionOptions reads one single-choice question and nothing else", () => {
  const q = (o) => ({ ask: { questions: [{ question: "x", options: o.map((label) => ({ label })) , ...(o.multi ? { multiSelect: true } : {}) }] } });
  assert.deepEqual(questionOptions(q(["a", "b"])), ["a", "b"]);
  assert.deepEqual(questionOptions({ ask: { questions: [{ options: [{ label: "a" }] }, { options: [{ label: "b" }] }] } }), []);
  assert.deepEqual(questionOptions({ ask: { questions: [{ multiSelect: true, options: [{ label: "a" }] }] } }), []);
  assert.deepEqual(questionOptions(null), []);
});

test("a question's option button sends that label to its pane, once", async () => {
  const panes = new Map([["cfg", { ask: { questions: [{ question: "which?", options: [{ label: "flags/" }, { label: "new" }] }] } }]]);
  const a = await mount({ panes }, "cfg");
  try {
    const btns = a.container.findAll(".dec-btn");
    assert.deepEqual(btns.map((b) => b.textContent), ["flags/", "new"]);
    btns[0].click();
    btns[1].click();
    await settle();
    assert.deepEqual(a.ws.commands("paneSend"), [{ cmd: "paneSend", id: "cfg", text: "flags/" }]);
  } finally {
    await a.done();
  }
});

test("a conflict asks the manager, names both agents, and offers Review", async () => {
  const seen = [];
  const a = await mount({ onSee: (p) => seen.push(p) }, "api");
  try {
    const block = a.container.find('[data-decision="conflict"]');
    assert.match(block.textContent, /api and cfg both change cfg\/limits\.yaml/);
    block.find(".dec-btn--primary").click();
    await settle();
    const sent = a.ws.commands("paneSend");
    assert.equal(sent.length, 1);
    assert.equal(sent[0].id, "m");
    assert.match(sent[0].text, /api and cfg/);
    block.findAll(".dec-btn").find((b) => b.textContent === "See in Review").click();
    assert.deepEqual(seen, ["api"]);
  } finally {
    await a.done();
  }
});

test("a down block restarts through the row's handler and opens the drawer; no handler, no Restart", async () => {
  const calls = [];
  const a = await mount({ onRestart: (p) => calls.push("r" + p), onOpen: (p) => calls.push("o" + p) }, "lint");
  try {
    const b = a.container.findAll(".dec-btn").map((x) => x.textContent);
    assert.deepEqual(b, ["Restart", "Ask mgr to reassign T10", "See terminal"]);
    a.container.find(".dec-btn--primary").click();
    a.container.find(".dec-btn--ghost").click();
    assert.deepEqual(calls, ["rlint", "olint"]);
  } finally {
    await a.done();
  }
  const bare = await mount({}, "lint");
  try {
    assert.deepEqual(bare.container.findAll(".dec-btn").map((b) => b.textContent), ["Ask mgr to reassign T10"], "no Restart without its handler");
  } finally {
    await bare.done();
  }
});

test("a terminal pane's permission has no answer buttons, and says where to answer", async () => {
  const a = await mount({ onOpen: () => {} }, "api", ["conflict"]);
  try {
    const block = a.container.find('[data-decision="permission"]');
    assert.match(block.find(".dec-command").textContent, /go test/);
    assert.match(block.textContent, /Answer in its drawer/);
    assert.equal(block.findAll(".prompt-choice").length, 0);
  } finally {
    await a.done();
  }
});

test("no block listens for a bare key", async () => {
  const a = await mount({ onOpen: () => {} }, "api");
  try {
    const block = a.container.find('[data-decision="permission"]');
    block.keydown("y");
    block.keydown("Enter");
    await settle();
    assert.equal(a.ws.commands("paneSend").length, 0);
    assert.equal(a.ws.commands("permissionAnswer").length, 0);
  } finally {
    await a.done();
  }
});

test("a permission says it asks to run only when there is a command", () => {
  const model = buildGraphModel(graph(items));
  const p = model.needs.find((n) => n.kind === "permission");
  assert.equal(decisionOf(model, p).line, "asks to run");
  assert.equal(decisionOf(model, p, Date.now(), { summary: "Edit mw/limiter.go" }).line, "asks permission", "the card body has the summary");
  assert.equal(decisionOf(model, p, Date.now(), {}).line, "asks permission");
  assert.equal(decisionOf(model, { ...p, text: "" }).line, "asks permission");
});

test("Ask mgr to reassign sends once, then says the manager was asked", async () => {
  const a = await mount({ onRestart() {} }, "lint");
  try {
    const btn = () => a.container.findAll(".dec-btn").find((b) => /reassign|Manager asked/.test(b.textContent));
    btn().click();
    btn().click();
    await settle();
    const sent = a.ws.commands("paneSend");
    assert.equal(sent.length, 1);
    assert.equal(sent[0].id, "m");
    assert.equal(btn().textContent, "Manager asked");
  } finally {
    await a.done();
  }
});

test("Open sits last in the answer row of a question, and a conflict names both agents in bold", async () => {
  const panes = new Map([["cfg", { ask: { questions: [{ question: "which?", options: [{ label: "a" }, { label: "b" }] }] } }]]);
  const a = await mount({ panes, onOpen() {} }, "cfg");
  try {
    assert.deepEqual(a.container.findAll(".dec-btn").map((b) => b.textContent), ["a", "b", "Open cfg"]);
    assert.equal(a.container.findAll(".dec-actions").length, 1);
  } finally {
    await a.done();
  }
  const c = await mount({}, "api", ["permission"]);
  try {
    assert.deepEqual(c.container.find(".dec-line").findAll("b").map((b) => b.textContent), ["api", "cfg"]);
    assert.ok(!/waiting/.test(c.container.find(".dec-age").textContent));
  } finally {
    await c.done();
  }
});

test("a relayed view (no store) draws no answering buttons and says to answer on the desktop", async () => {
  const panes = new Map([["cfg", { ask: { questions: [{ question: "which?", options: [{ label: "a" }, { label: "b" }] }] } }]]);
  for (const [pane, keep] of [["cfg", ["Open cfg"]], ["api", ["See in Review"]], ["lint", ["See terminal"]]]) {
    const a = await mount({ store: null, panes, onOpen() {}, onSee() {}, onRestart() {} }, pane);
    try {
      assert.deepEqual(a.container.findAll(".dec-btn").map((b) => b.textContent).filter((t) => !/Open api/.test(t)), keep, pane);
      assert.match(a.container.textContent, /Answer this on the desktop/);
    } finally {
      await a.done();
    }
  }
});

test("the block's buttons are not tab stops unless the row is", async () => {
  const a = await mount({ tab: false, onOpen() {}, onRestart() {} }, "lint");
  try {
    for (const b of a.container.findAll(".dec-btn")) assert.equal(b.getAttribute("tabindex"), "-1");
  } finally {
    await a.done();
  }
});
