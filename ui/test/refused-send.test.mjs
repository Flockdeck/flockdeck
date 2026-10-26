// A button that answers or asks something sends the text with paneSend and says
// "Sent" only when the desk took it. A refusal (the agent is gone, a shell pane,
// a viewer, a text over 16 KiB) is shown in the desk's words and the button can be
// pressed again. The store runs on a fake socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { useStore } from "../src/lib/use-store.js";
import { buildGraphModel } from "../src/graph/model.js";
import { Decisions } from "../src/graph/DecisionBlock.js";
import { StreamDecision } from "../src/drawer/StreamDecision.js";
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
const REFUSED = "cfg is not taking text now.";
// A button that waits for the desk is aria-disabled, not disabled: a disabled button loses the keyboard focus and is not given it back when a refusal makes it ready again.
const busy = (b) => b.getAttribute("aria-disabled") === "true";

async function start(view) {
  const h = await harness();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  h.store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  function App() {
    const snap = useStore(h.store);
    return view(snap, h.store);
  }
  render(html`<${App} />`, container);
  await settle();
  return { ws, container, done: async () => { render(null, container); await settle(); restore(); } };
}

const questionOf = (labels) => ({ ask: { questions: [{ question: "which?", options: labels.map((label) => ({ label })) }] } });

test("a question's answer is not shown as sent until the desk took it, and a refusal can be tried again", async () => {
  const model = buildGraphModel(graph([need("ask", "cfg", { text: "which?" })]));
  const panes = new Map([["cfg", questionOf(["flags/", "new"])]]);
  const a = await start((snap, store) => html`<${Decisions} model=${model} pane="cfg" ctx=${{ snap, store, panes, manager: { pane: "m", name: "mgr" } }} />`);
  try {
    const buttons = () => a.container.findAll(".dec-btn").filter((b) => b.getAttribute("data-option") !== null);
    const status = () => a.container.find(".dec-status");
    buttons()[0].click();
    await settle();
    const first = a.ws.commands("paneSend");
    assert.equal(first.length, 1);
    assert.equal(status().textContent, "Sending…", "the socket took it, the desk has not answered");
    assert.ok(buttons().every(busy));
    assert.ok(buttons().every((b) => !b.disabled), "a button that waits is not disabled, so it keeps the focus");
    buttons()[1].click();
    await settle();
    assert.equal(a.ws.commands("paneSend").length, 1, "a press while it waits sends nothing");
    // The desk refuses it.
    a.ws.frame({ type: "notice", text: REFUSED, error: true });
    await settle();
    assert.equal(status().textContent, REFUSED);
    assert.equal(status().getAttribute("role"), "alert");
    assert.ok(!/Sent/.test(a.container.textContent), "a refused answer is not shown as sent");
    assert.ok(buttons().every((b) => !busy(b)), "the choices can be pressed again");
    // A second try goes out with a req of its own, and its answer is the one that counts.
    buttons()[1].click();
    await settle();
    const second = a.ws.commands("paneSend");
    assert.equal(second.length, 2);
    assert.notEqual(second[1].req, first[0].req);
    a.ws.frame({ type: "paneSend", id: "cfg", queued: true, shell: false, req: "someone-elses" });
    await settle();
    assert.equal(status().textContent, "Sending…", "an answer to another send is not this one's");
    a.ws.frame({ type: "paneSend", id: "cfg", queued: true, shell: false, req: second[1].req });
    await settle();
    assert.match(status().textContent, /^Sent “new”\. Waiting for the agent\.$/);
    assert.ok(buttons().every(busy));
  } finally {
    await a.done();
  }
});

test("two choices pressed before the card is drawn again send the first, and the card names the first", async () => {
  const model = buildGraphModel(graph([need("ask", "cfg", { text: "which?" })]));
  const panes = new Map([["cfg", questionOf(["flags/", "new"])]]);
  const a = await start((snap, store) => html`<${Decisions} model=${model} pane="cfg" ctx=${{ snap, store, panes, manager: { pane: "m", name: "mgr" } }} />`);
  try {
    const buttons = a.container.findAll(".dec-btn").filter((b) => b.getAttribute("data-option") !== null);
    buttons[0].click();
    buttons[1].click();
    await settle();
    const sent = a.ws.commands("paneSend");
    assert.equal(sent.length, 1);
    assert.equal(sent[0].text, "flags/");
    a.ws.frame({ type: "paneSend", id: "cfg", queued: true, shell: false, req: sent[0].req });
    await settle();
    assert.match(a.container.find(".dec-status").textContent, /^Sent “flags\/”\. Waiting for the agent\.$/, "the card named the second press");
  } finally {
    await a.done();
  }
});

test("a long refusal is shortened where it is drawn", async () => {
  const model = buildGraphModel(graph([need("ask", "cfg", { text: "which?" })]));
  const panes = new Map([["cfg", questionOf(["a"])]]);
  const a = await start((snap, store) => html`<${Decisions} model=${model} pane="cfg" ctx=${{ snap, store, panes }} />`);
  try {
    a.container.findAll(".dec-btn").find((b) => b.getAttribute("data-option") !== null).click();
    await settle();
    a.ws.frame({ type: "notice", text: "cfg is gone. " + "x".repeat(400), error: true });
    await settle();
    const words = a.container.find(".dec-status--error").textContent;
    assert.ok(words.startsWith("cfg is gone."));
    assert.ok(words.length <= 161, "kept to " + words.length);
  } finally {
    await a.done();
  }
});

test("an answer that was sent stays sent when another send is answered after it, or an error comes with no req", async () => {
  const model = buildGraphModel(graph([need("ask", "cfg", { text: "which?" }), need("conflict", "api", { other: "lint", text: "x.yaml" })]));
  const panes = new Map([["cfg", questionOf(["flags/", "new"])]]);
  const a = await start((snap, store) => html`<${Decisions} model=${model} pane="cfg" ctx=${{ snap, store, panes, manager: { pane: "m", name: "mgr" } }} />
    <${Decisions} model=${model} pane="api" ctx=${{ snap, store, manager: { pane: "m", name: "mgr" } }} />`);
  try {
    const choice = () => a.container.findAll(".dec-btn").filter((b) => b.getAttribute("data-option") !== null);
    const status = () => a.container.findAll(".dec-status")[0];
    choice()[0].click();
    await settle();
    const first = a.ws.commands("paneSend")[0];
    a.ws.frame({ type: "paneSend", id: "cfg", queued: true, shell: false, req: first.req });
    await settle();
    assert.match(status().textContent, /^Sent “flags\/”/);
    // Another send is answered, and the snapshot's latest answer is that one's.
    a.container.find('[data-decision="conflict"]').find(".dec-btn--primary").click();
    await settle();
    const second = a.ws.commands("paneSend")[1];
    assert.equal(second.id, "m");
    a.ws.frame({ type: "paneSend", id: "m", queued: true, shell: false, req: second.req });
    await settle();
    assert.match(status().textContent, /^Sent “flags\/”/, "the first answer went back to sending");
    assert.ok(choice().every(busy));
    // An error for something else, with no req, is not a refusal of an answer the desk took.
    a.ws.frame({ type: "notice", text: "something else failed", error: true });
    await settle();
    assert.match(status().textContent, /^Sent “flags\/”/);
    assert.equal(a.ws.commands("paneSend").length, 2, "no duplicate answer was invited");
  } finally {
    await a.done();
  }
});

test("a refused ask to the manager shows why, and the button is ready again", async () => {
  const model = buildGraphModel(graph([need("conflict", "api", { other: "cfg", text: "cfg/limits.yaml" })]));
  const a = await start((snap, store) => html`<${Decisions} model=${model} pane="api" ctx=${{ snap, store, manager: { pane: "m", name: "mgr" } }} />`);
  try {
    const ask = () => a.container.find(".dec-btn--primary");
    ask().click();
    await settle();
    assert.equal(busy(ask()), true);
    a.ws.frame({ type: "notice", text: "mgr is not taking text now.", error: true });
    await settle();
    assert.equal(a.container.find(".dec-status--error").textContent, "mgr is not taking text now.");
    assert.equal(ask().textContent, "Ask mgr to sort it out");
    assert.equal(busy(ask()), false);
    ask().click();
    await settle();
    assert.equal(a.ws.commands("paneSend").length, 2);
  } finally {
    await a.done();
  }
});

test("the card in the conversation says what the desk refused, and keeps its choices", async () => {
  const pane = { id: "cfg", statusSince: new Date(Date.now() - 60000).toISOString(), ...questionOf(["a", "b"]) };
  const a = await start((snap, store) => html`<${StreamDecision} node=${node("cfg")} pane=${pane} snap=${snap} store=${store} />`);
  try {
    const choices = () => a.container.findAll(".prompt-choice");
    choices()[0].click();
    await settle();
    a.ws.frame({ type: "notice", text: REFUSED, error: true });
    await settle();
    assert.equal(a.container.find(".prompt-status").textContent, REFUSED);
    assert.ok(!/Sent/.test(a.container.textContent));
    assert.ok(choices().every((b) => !busy(b)));
  } finally {
    await a.done();
  }
});

test("a socket that is not open refuses the answer in words, not as sent", async () => {
  const model = buildGraphModel(graph([need("ask", "cfg", { text: "which?" })]));
  const panes = new Map([["cfg", questionOf(["a"])]]);
  const a = await start((snap) => html`<${Decisions} model=${model} pane="cfg" ctx=${{ snap, store: { send: () => false }, panes }} />`);
  try {
    a.container.findAll(".dec-btn").find((b) => b.getAttribute("data-option") !== null).click();
    await settle();
    assert.match(a.container.find(".dec-status--error").textContent, /Not connected/);
    assert.ok(!/Sent/.test(a.container.textContent));
  } finally {
    await a.done();
  }
});
