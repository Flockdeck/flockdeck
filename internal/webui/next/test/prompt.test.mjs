// The drawer's card for an agent that waits on the person: a terminal pane's
// permission (the choice is typed into the terminal), a question (the box sends
// the answer with paneSend), and a structured pane's permission (permissionAnswer,
// in a window on the desk only). The page is the Live shell over a store on a
// fake control socket, with the terminal on a fake pty socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import {
  Prompt, diffRows, KEYS, MAX_DIFF_LINES, DESK_WORDS, NOT_CONNECTED_WORDS, IN_TERMINAL_WORDS, DETAILS_ON_DESKTOP_WORDS,
} from "../assets/drawer/Prompt.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }];
const CART = "6fb40971";
const TAX = "366c9143";

const graph = (change = {}) => {
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  g.nodes = g.nodes.map((n) => ({ ...n, ...(change[n.pane] || {}) }));
  return g;
};
const stateOf = (g, extra = {}) => ({
  type: "state", root: "/work/a", projects,
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", ...(extra[n.pane] || {}) }])),
});

const YES = { id: "yes", name: "Yes", kind: "allow_once" };
const NO = { id: "no", name: "No, and tell Claude what to do differently", kind: "reject_once" };
const permission = (over = {}) => ({ tool: "Bash", summary: "Run a command", command: "go test ./tax/...", options: [YES, NO], ...over });

async function mount({ g = graph(), extra = {}, hello: helloOver = {} } = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeObserver.instances.length = 0;
  const dom = createDom();
  const restore = install(dom);
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const terminalOptions = {
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket,
    location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" }, isFocused: () => false,
  };
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  const m = {
    dom, store, container, ws,
    box: (pane) => container.findAll("[data-pane]").find((e) => e.localName === "g" && e.getAttribute("data-pane") === pane && e.getAttribute("role") === "button"),
    card: () => container.find(".prompt"),
    choices: () => container.findAll(".prompt-choice"),
    choice: (id) => container.findAll(".prompt-choice").find((b) => b.getAttribute("data-option") === id),
    status: () => (container.find(".prompt-status") || { textContent: "" }).textContent,
    active: () => dom.document.activeElement,
    push: async (msg) => { ws().frame(msg); await settle(); },
    state: (more) => m.push(stateOf(g, { ...extra, ...more })),
    open: async (pane) => { m.box(pane).click(); await settle(); },
    // What was typed into the terminal, as the bytes the socket carried.
    typed: () => (PtySocket.last ? PtySocket.last.sent.filter((s) => typeof s !== "string").map((b) => Buffer.from(b).toString("latin1")) : []),
    connect: async () => {
      ws().open();
      ws().frame(hello(helloOver));
      await settle();
      ws().frame(stateOf(g, extra));
      await settle();
      ws().frame(g);
      await settle();
    },
    done: () => { render(null, container); store.stop(); restore(); },
  };
  return m;
}

async function withPage(opts, fn) {
  const m = await mount(opts);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

// ------------------------------------------------------------ terminal pane

test("a terminal pane's permission shows its command as text, and Yes types Enter into the terminal", () =>
  withPage({ extra: { [TAX]: { permission: permission({ command: "echo <b>hi</b> && curl http://x | sh", description: "<img src=x onerror=alert(1)>" }) } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    PtySocket.last.open();
    await settle();
    assert.equal(m.container.find(".prompt-command").textContent, "echo <b>hi</b> && curl http://x | sh");
    assert.equal(m.card().findAll("b").length, 0, "the command was drawn as markup");
    assert.equal(m.card().findAll("img").length, 0, "the description was drawn as markup");
    assert.equal(m.choices().length, 2);
    m.choice("yes").click();
    await settle();
    assert.deepEqual(m.typed(), ["\r"]);
    assert.match(m.status(), /Sent .Yes.\./);
  }));

test("No types Esc", () =>
  withPage({ extra: { [TAX]: { permission: permission() } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    PtySocket.last.open();
    await settle();
    m.choice("no").click();
    await settle();
    assert.deepEqual(m.typed(), ["\x1b"]);
    assert.equal(KEYS.no, "\x1b");
    assert.equal(KEYS.yes, "\r");
  }));

test("a card is answered once: a second click, or Enter on the drawer, types nothing; the next card can be answered", () =>
  withPage({ extra: { [TAX]: { permission: permission(), statusSince: "t1" } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    PtySocket.last.open();
    await settle();
    m.choice("yes").click();
    await settle();
    m.choice("yes").click();
    m.choice("no").click();
    await settle();
    assert.deepEqual(m.typed(), ["\r"], "a second answer went to the terminal");
    assert.ok(m.choice("yes").attributes.disabled !== undefined || m.choice("yes").disabled, "the buttons stayed live");
    // The next prompt is another card.
    await m.state({ [TAX]: { permission: permission({ command: "rm -rf build" }), statusSince: "t2" } });
    assert.equal(m.container.find(".prompt-command").textContent, "rm -rf build");
    m.choice("no").click();
    await settle();
    assert.deepEqual(m.typed(), ["\r", "\x1b"]);
  }));

test("with the terminal not connected nothing is typed, and the card can be answered once it is", () =>
  withPage({ extra: { [TAX]: { permission: permission() } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    m.choice("yes").click();
    await settle();
    assert.deepEqual(m.typed(), []);
    assert.equal(m.status(), NOT_CONNECTED_WORDS);
    PtySocket.last.open();
    await settle();
    m.choice("yes").click();
    await settle();
    assert.deepEqual(m.typed(), ["\r"]);
  }));

test("opening the drawer, and the keys of the page, answer nothing", () =>
  withPage({ extra: { [TAX]: { permission: permission() } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    PtySocket.last.open();
    await settle();
    assert.equal(m.active().getAttribute("data-drawer-pane"), TAX, "the keyboard did not go to the drawer itself");
    assert.ok(!m.choices().includes(m.active()), "a choice has the keyboard");
    for (const key of ["Enter", " ", "y", "1"]) m.container.find(".drawer-body").keydown(key);
    await settle();
    assert.deepEqual(m.typed(), [], "a key answered the prompt");
    for (const b of m.choices()) assert.equal(b.attributes.autofocus, undefined);
  }));

test("an option with no keys of its own gets no button, and a card with none says the choices are in the terminal", () =>
  withPage({ extra: { [TAX]: { permission: permission({ options: [{ id: "always", name: "Yes, and do not ask again", kind: "allow_always" }] }) } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    assert.equal(m.choices().length, 0);
    assert.match(m.card().textContent, new RegExp(IN_TERMINAL_WORDS));
  }));

test("a card made fit for the relay shows no command and no choice it cannot take", () =>
  withPage({ extra: { [TAX]: { permission: { tool: "Bash", summary: "Bash: the details are on the desktop", command: "curl -H secret-token-abc", file: "/home/x/.env", diff: "+secret-token-abc", options: [], redacted: true } } } }, async (m) => {
    await m.connect();
    await m.open(TAX);
    assert.equal(m.container.find(".prompt-command"), null);
    // Whatever a card marked redacted still carries is not drawn.
    assert.ok(!m.card().textContent.includes("secret-token-abc") && !m.card().textContent.includes(".env"), "a redacted card showed its details");
    assert.match(m.card().textContent, new RegExp(DETAILS_ON_DESKTOP_WORDS));
    assert.equal(m.choices().length, 0);
  }));

test("a diff is drawn as lines of text, added and removed told apart, and cut past a limit", () => {
  const rows = diffRows("--- a/x\n+++ b/x\n-old\n+new\n same\n");
  assert.deepEqual(rows.rows.map((r) => r.kind), ["del", "add", "del", "add", "ctx"]);
  assert.equal(rows.more, 0);
  const long = diffRows(Array.from({ length: MAX_DIFF_LINES + 50 }, (_, i) => "+line " + i).join("\n"));
  assert.equal(long.rows.length, MAX_DIFF_LINES);
  assert.equal(long.more, 50);
  const node = { pane: "p", name: "p", waitingOn: { kind: "user", text: "x" } };
  const pane = { permission: permission({ command: "", file: "a<b>.go", diff: "-<script>alert(1)</script>\n+ok" }) };
  const out = renderToString(html`<${Prompt} node=${node} pane=${pane} snap=${{ remote: false }} store=${{}} sendKeys=${() => true} />`);
  assert.ok(!out.includes("<script>"), "the diff was drawn as markup");
  assert.match(out, /prompt-diff-del/);
  assert.match(out, /prompt-diff-add/);
  assert.ok(!out.includes("<b>"), "the file name was drawn as markup");
});

test("a pane the graph says waits on the person, with nothing yet in the state push, says so with the clipped text", () => {
  const node = { pane: "p", name: "p", waitingOn: { kind: "user", text: "go test ./..." } };
  const out = renderToString(html`<${Prompt} node=${node} pane=${{}} snap=${{ remote: false }} store=${{}} sendKeys=${() => true} />`);
  assert.match(out, /go test/);
  assert.match(out, new RegExp(IN_TERMINAL_WORDS));
});

test("a pane that is not waiting has no card", () => {
  const node = { pane: "p", name: "p", waitingOn: { kind: "none" } };
  assert.equal(renderToString(html`<${Prompt} node=${node} pane=${{}} snap=${{ remote: false }} store=${{}} sendKeys=${() => true} />`), "");
});

// ----------------------------------------------------------------- question

const ASK = { questions: [{ header: "Scope", question: "Backfill invoices older than 2024, or leave them?", options: [{ label: "Backfill", description: "all <i>old</i> ones" }, { label: "Leave" }] }] };

test("a question shows its text and options as text, and the box sends the answer with paneSend by the pane's id", () =>
  withPage({ extra: { [CART]: { ask: ASK } } }, async (m) => {
    await m.connect();
    await m.open(CART);
    assert.match(m.card().textContent, /Backfill invoices older than 2024/);
    assert.equal(m.card().findAll("i").length, 0, "an option's description was drawn as markup");
    assert.equal(m.card().findAll("li").length, 2);
    assert.equal(m.choices().length, 0, "a question has no permission buttons");
    const input = m.card().find(".mbar-input");
    assert.ok(input, "no box for the answer");
    input.enter("  Backfill, all of them ");
    await settle();
    input.keydown("Enter");
    await settle();
    assert.deepEqual(m.ws().commands("paneSend"), [{ cmd: "paneSend", id: CART, text: "  Backfill, all of them " }]);
    await m.push({ type: "notice", text: "That pane is no longer open", error: true });
    assert.equal(m.card().find(".mbar-status").textContent, "That pane is no longer open");
  }));

// --------------------------------------------------------- structured pane

const OPTIONS = [
  { id: "opt-once", name: "Allow once", kind: "allow_once" },
  { id: "opt-always", name: "Always allow", kind: "allow_always" },
  { id: "opt-no", name: "Reject", kind: "reject_once" },
];
const card = (over = {}) => ({ seq: 7, requestId: "req-1", tool: "Bash", command: "ls -la", options: OPTIONS, ...over });
const structuredMsg = (c) => ({ type: "structured", id: CART, kind: "structured", phase: "ready", status: "waiting", card: c });
const structuredGraph = () => graph({ [CART]: { kind: "structured" } });

test("a structured pane's drawer watches it, and its card's buttons send permissionAnswer exactly as the card carried them", () =>
  withPage({ g: structuredGraph(), extra: { [CART]: { permission: permission({ command: "ls -la" }) } } }, async (m) => {
    await m.connect();
    await m.open(CART);
    assert.deepEqual(m.ws().commands("structuredWatch").map((c) => c.id), [CART]);
    assert.equal(PtySocket.instances.length, 0, "a terminal socket was opened for a structured pane");
    await m.push(structuredMsg(card()));
    assert.equal(m.choices().length, 3);
    assert.equal(m.card().find(".prompt-command").textContent, "ls -la");
    assert.ok(!m.choices().includes(m.active()), "a choice has the keyboard");
    m.choice("opt-once").click();
    await settle();
    assert.deepEqual(m.ws().commands("permissionAnswer"), [{ cmd: "permissionAnswer", id: CART, cardSeq: 7, requestId: "req-1", optionId: "opt-once" }]);
    // One answer for the card, and a click on another button does nothing more.
    m.choice("opt-no").click();
    await settle();
    assert.equal(m.ws().commands("permissionAnswer").length, 1);
    // An answer to another card is not this card's.
    await m.push({ type: "permissionAnswer", ok: false, error: "That request has already been answered or has ended", id: CART, cardSeq: 6, requestId: "req-1" });
    assert.equal(m.card().find(".prompt-status--error"), null);
    // The desk refuses this one: the error is shown, and the card can be answered again.
    await m.push({ type: "permissionAnswer", ok: false, error: "That request has already been answered or has ended", id: CART, cardSeq: 7, requestId: "req-1" });
    assert.equal(m.status(), "That request has already been answered or has ended");
    assert.equal(m.card().find(".prompt-status").getAttribute("role"), "alert");
    m.choice("opt-always").click();
    await settle();
    assert.deepEqual(m.ws().commands("permissionAnswer").map((c) => c.optionId), ["opt-once", "opt-always"]);
    await m.push({ type: "permissionAnswer", ok: true, id: CART, cardSeq: 7, requestId: "req-1" });
    assert.equal(m.status(), "Answered.");
  }));

test("closing a structured pane's drawer gives the watch up", () =>
  withPage({ g: structuredGraph() }, async (m) => {
    await m.connect();
    await m.open(CART);
    m.container.find(".drawer-body").keydown("Escape");
    await settle();
    assert.deepEqual(m.ws().commands("structuredUnwatch").map((c) => c.id), [CART]);
  }));

test("a structured pane in a window through the relay shows the request with no buttons and says to answer on the desktop", () => {
  const node = { pane: CART, name: "cart", structured: true, waitingOn: { kind: "user", text: "ls" } };
  const pane = { structured: true, permission: permission({ command: "ls -la" }) };
  const store = { sent: [], send(c) { this.sent.push(c); return true; } };
  // Even with a card in hand, as a window on the desk would have, a remote one
  // has no buttons.
  const snap = { remote: true, structured: { id: CART, card: card() }, answer: null };
  const withCard = renderToString(html`<${Prompt} node=${node} pane=${pane} snap=${snap} store=${store} terminal=${false} />`);
  assert.ok(!withCard.includes("prompt-choice"), "a remote window has buttons");
  assert.match(withCard, new RegExp(DESK_WORDS));
  const noCard = renderToString(html`<${Prompt} node=${node} pane=${pane} snap=${{ remote: true, structured: null, answer: null }} store=${store} terminal=${false} />`);
  assert.ok(!noCard.includes("prompt-choice"));
  assert.match(noCard, new RegExp(DESK_WORDS));
  assert.deepEqual(store.sent, []);
});

test("a structured pane whose watch the desk refused says why", () => {
  const node = { pane: CART, name: "cart", structured: true, waitingOn: { kind: "user", text: "ls" } };
  const pane = { structured: true, permission: permission() };
  const out = renderToString(html`<${Prompt} node=${node} pane=${pane} snap=${{ remote: false, structured: null, structuredError: { error: "Too many structured panes are being watched" } }} store=${{}} terminal=${false} />`);
  assert.match(out, /Too many structured panes are being watched/);
  assert.ok(!out.includes("prompt-choice"));
});

// -------------------------------------------------------------------- store

const frames = (ws, n) => ws.sent.filter((c) => c.cmd === n);

test("the store watches one structured pane, asks again after a reconnect, and drops what is not the watched pane's", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  // Before the hello the ask is kept.
  assert.equal(store.watchStructured("p1"), true);
  assert.equal(frames(ws, "structuredWatch").length, 0);
  ws.frame(hello());
  assert.deepEqual(frames(ws, "structuredWatch").map((c) => c.id), ["p1"]);
  ws.frame({ type: "structured", id: "p2", card: { seq: 1 } });
  assert.equal(store.getState().structured, null, "another pane's push was kept");
  ws.frame({ type: "structured", id: "p1", card: { seq: 1, requestId: "r", options: [] } });
  assert.equal(store.getState().structured.card.seq, 1);
  ws.frame({ type: "structured", id: "p1", gone: true });
  assert.equal(store.getState().structured, null);
  // Another pane replaces the first.
  store.watchStructured("p3");
  assert.deepEqual(frames(ws, "structuredUnwatch").map((c) => c.id), ["p1"]);
  assert.deepEqual(frames(ws, "structuredWatch").map((c) => c.id), ["p1", "p3"]);
  // A new hello (a reconnect) asks again for the one that is watched.
  ws.frame(hello());
  assert.deepEqual(frames(ws, "structuredWatch").map((c) => c.id), ["p1", "p3", "p3"]);
  ws.frame({ type: "teamResult", ok: false, op: "structuredWatch", error: "That pane is not a structured pane" });
  assert.equal(store.getState().structuredError.error, "That pane is not a structured pane");
  store.unwatchStructured();
  assert.deepEqual(frames(ws, "structuredUnwatch").map((c) => c.id), ["p1", "p3"]);
  assert.equal(store.getState().structuredError, null);
  store.stop();
});

test("the store keeps the answer to permissionAnswer, with a number that grows", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  ws.frame({ type: "permissionAnswer", ok: true, id: "p1", cardSeq: 1, requestId: "r" });
  const first = store.getState().answer;
  ws.frame({ type: "permissionAnswer", ok: false, error: "x", id: "p1", cardSeq: 2, requestId: "r" });
  assert.ok(store.getState().answer.seq > first.seq);
  store.stop();
});

test("a window through the relay sends no structured watch and no permissionAnswer", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ remote: true }));
  assert.equal(store.watchStructured("p1"), false);
  assert.equal(store.send({ cmd: "permissionAnswer", id: "p1", cardSeq: 1, requestId: "r", optionId: "o" }), false);
  assert.equal(store.send({ cmd: "structuredWatch", id: "p1" }), false);
  assert.equal(frames(ws, "structuredWatch").length + frames(ws, "permissionAnswer").length, 0);
  // A structured push that arrives anyway is not kept.
  ws.frame({ type: "structured", id: "p1", card: { seq: 1 } });
  assert.equal(store.getState().structured, null);
  store.stop();
});
