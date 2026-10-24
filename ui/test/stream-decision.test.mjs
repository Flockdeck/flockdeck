// The decision card in the conversation: what it shows of a question and what its
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
// buttons send. The store runs on a fake socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { useStore } from "../src/lib/use-store.js";
import { StreamDecision } from "../src/drawer/StreamDecision.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const node = { pane: "m", name: "mgr", kind: "terminal", waitingOn: { kind: "user" } };
const since = () => new Date(Date.now() - 12 * 60000).toISOString();
const paneWith = (questions) => ({ id: "m", statusSince: since(), ask: { questions } });

async function mount(pane, over = {}) {
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
    return html`<${StreamDecision} node=${node} pane=${pane} snap=${over.remote ? { ...snap, remote: true } : snap} store=${h.store} />`;
  }
  render(html`<${App} />`, container);
  await settle();
  return { ws, container, done: async () => { render(null, container); await settle(); restore(); } };
}

test("the card says who, how long it has waited, and the question", async () => {
  const a = await mount(paneWith([{ question: "Which flag store?", options: [{ label: "flags/" }, { label: "new" }] }]));
  try {
    assert.match(a.container.find(".prompt-label").textContent, /mgr, decision for you/);
    assert.equal(a.container.find(".prompt-title").textContent, "Waiting 12m");
    assert.equal(a.container.find(".prompt-text").textContent, "Which flag store?");
    assert.deepEqual(a.container.findAll(".prompt-choice").map((b) => b.textContent), ["flags/", "new"]);
    assert.ok(/primary/.test(a.container.findAll(".prompt-choice")[0].getAttribute("class")));
  } finally {
    await a.done();
  }
});

test("a choice is sent to the pane as text, once", async () => {
  const a = await mount(paneWith([{ question: "q", options: [{ label: "flags/" }, { label: "new" }] }]));
  try {
    const b = a.container.findAll(".prompt-choice");
    b[1].click();
    b[0].click();
    await settle();
    assert.deepEqual(a.ws.commands("paneSend"), [{ cmd: "paneSend", id: "m", text: "new" }]);
    assert.match(a.container.find(".prompt-status").textContent, /Sent “new”/);
  } finally {
    await a.done();
  }
});

test("a question with no single set of choices keeps the answer box, and a remote card has no buttons", async () => {
  const free = await mount(paneWith([{ question: "Why?" }]));
  try {
    assert.equal(free.container.findAll(".prompt-choice").length, 0);
    assert.ok(free.container.find(".mbar-input"));
  } finally {
    await free.done();
  }
  const remote = await mount(paneWith([{ question: "q", options: [{ label: "a" }] }]), { remote: true });
  try {
    assert.equal(remote.container.findAll(".prompt-choice").length, 0);
    assert.equal(remote.container.findAll(".mbar-input").length, 0);
  } finally {
    await remote.done();
  }
});

test("the card is keyed by its question in the drawer, so a new question has live buttons", () => {
  const src = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "src", "drawer", "Drawer.js"), "utf8");
  assert.match(src, /<\$\{StreamDecision\} key=\$\{[^}]*statusSince[^}]*questions/);
});
