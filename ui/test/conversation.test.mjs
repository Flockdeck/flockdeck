// The drawer's conversation tab: the store's watch of one pane's conversation
// (pages, appends, older, close, the refusal), the rows, and the tab in the page.
// Elements are compared with assert.ok(a === b), never equal or deepEqual: a
// failing deepEqual on the fake DOM prints the whole tree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Conversation, Row, toolKind, NO_ENTRIES_WORDS, MAX_TEXT, MAX_REPLY } from "../src/drawer/Conversation.js";
import { Live } from "../src/shell/Live.js";
import { VIEWER_REFUSED_PHRASE, REMOTE_CONVERSATION_LINE, MAX_ENTRIES } from "../src/lib/store.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");

const entry = (n, over = {}) => ({ id: "e" + n, kind: "prompt", ts: "2026-10-21T08:00:00Z", text: "say " + n, ...over });
const range = (from, to) => Array.from({ length: to - from + 1 }, (_, i) => entry(from + i));
const page = (id, entries, over = {}) => ({ type: "conversationPage", id, supported: true, agent: "claude", entries, cursor: entries.length ? entries[entries.length - 1].id : "", ...over });
const ids = (c) => c.entries.map((e) => e.id);

async function up() {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  const ws = FakeWebSocket.last;
  ws.frame(hello());
  return { ...h, ws };
}

test("watching a pane sends conversationOpen once, and a page fills the list", async () => {
  const { store, ws } = await up();
  assert.equal(store.watchConversation("p1"), true);
  assert.equal(store.watchConversation("p1"), false, "the same pane twice");
  assert.deepEqual(ws.commands("conversationOpen"), [{ cmd: "conversationOpen", id: "p1" }]);
  assert.equal(store.getState().conversation.supported, null);
  ws.frame(page("p1", range(1, 50), { atStart: false }));
  const c = store.getState().conversation;
  assert.equal(c.supported, true);
  assert.deepEqual(ids(c), range(1, 50).map((e) => e.id));
  assert.equal(c.atStart, false);
});

test("an append adds entries, and an entry sent again replaces the one with its id", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame(page("p1", [entry(1), entry(2, { kind: "tool", name: "Bash", label: "go test", status: "running" })], { atStart: true }));
  ws.frame({ type: "conversationAppend", id: "p1", entries: [entry(2, { kind: "tool", name: "Bash", label: "go test", status: "ok" }), entry(3)], cursor: "e3" });
  const c = store.getState().conversation;
  assert.deepEqual(ids(c), ["e1", "e2", "e3"]);
  assert.equal(c.entries[1].status, "ok");
  ws.frame({ type: "conversationAppend", id: "other", entries: [entry(9)], cursor: "e9" });
  assert.deepEqual(ids(store.getState().conversation), ["e1", "e2", "e3"], "an append for another pane");
});

test("Older asks for the page before the first entry, once at a time, and the page goes above", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame(page("p1", range(51, 100), { atStart: false }));
  assert.equal(store.olderConversation(), true);
  assert.equal(store.olderConversation(), false, "asked again while the first is out");
  assert.deepEqual(ws.commands("conversationOlder"), [{ cmd: "conversationOlder", id: "p1", before: "e51" }]);
  assert.equal(store.getState().conversation.older, "loading");
  ws.frame(page("p1", range(1, 50), { atStart: true }));
  const c = store.getState().conversation;
  assert.equal(c.entries.length, 100);
  assert.equal(c.entries[0].id, "e1");
  assert.equal(c.entries[99].id, "e100");
  assert.equal(c.older, "idle");
  assert.equal(c.atStart, true);
  assert.equal(store.olderConversation(), false, "nothing is older than the start");
});

test("a page with reset replaces the list", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame(page("p1", range(1, 3), { atStart: true }));
  ws.frame(page("p1", [entry(10)], { atStart: true, reset: true }));
  assert.deepEqual(ids(store.getState().conversation), ["e10"]);
});

test("a pane with no stored conversation is unsupported, and has no entries", async () => {
  const { store, ws } = await up();
  store.watchConversation("sh");
  ws.frame({ type: "conversationPage", id: "sh", supported: false, entries: [] });
  const c = store.getState().conversation;
  assert.equal(c.supported, false);
  assert.equal(c.entries.length, 0);
  assert.equal(store.olderConversation(), false);
});

test("closing sends conversationClose, and so does watching another pane", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  store.watchConversation("p2");
  assert.deepEqual(ws.commands("conversationClose"), [{ cmd: "conversationClose", id: "p1" }]);
  ws.frame(page("p1", [entry(1)]));
  assert.equal(store.getState().conversation.supported, null, "a page for the pane that was closed");
  store.unwatchConversation();
  assert.deepEqual(ws.commands("conversationClose").map((c) => c.id), ["p1", "p2"]);
  assert.equal(store.getState().conversation, null);
  store.unwatchConversation();
  assert.equal(ws.commands("conversationClose").length, 2, "closed twice");
  ws.frame({ type: "conversationAppend", id: "p2", entries: [entry(1)] });
  assert.equal(store.getState().conversation, null);
});

test("stopping the store closes the conversation", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  store.stop();
  assert.deepEqual(ws.commands("conversationClose"), [{ cmd: "conversationClose", id: "p1" }]);
});

test("a refusal is kept on the conversation once, and the open is not repeated", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame({ type: "notice", text: VIEWER_REFUSED_PHRASE + ". Ask for it to be made a full device", error: true });
  assert.ok(store.getState().conversation.refused.startsWith(VIEWER_REFUSED_PHRASE));
  assert.equal(store.getState().conversation.supported, null);
  assert.equal(ws.commands("conversationOpen").length, 1, "no retry on the refusal");
  assert.equal(store.olderConversation(), false);
  ws.frame({ type: "notice", text: "Something else", error: true });
  assert.ok(store.getState().conversation.refused.startsWith(VIEWER_REFUSED_PHRASE), "another notice");
});

test("after a reconnect the open asks only for what came since, and a refusal is tried again", async () => {
  const { store, ws, clock } = await up();
  store.watchConversation("p1");
  ws.frame(page("p1", range(1, 3), { atStart: true }));
  ws.drop();
  clock.advance(30000);
  const ws2 = FakeWebSocket.last;
  assert.ok(ws2 !== ws, "no new socket");
  ws2.open();
  ws2.frame(hello());
  assert.deepEqual(ws2.commands("conversationOpen"), [{ cmd: "conversationOpen", id: "p1", after: "e3" }]);
  ws2.frame(page("p1", [entry(4)]));
  const c = store.getState().conversation;
  assert.deepEqual(ids(c), ["e1", "e2", "e3", "e4"]);
  assert.equal(c.atStart, true, "the start is kept");
});

test("the entries held are bounded", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame(page("p1", range(1, 50), { atStart: true }));
  ws.frame({ type: "conversationAppend", id: "p1", entries: range(51, MAX_ENTRIES + 20), cursor: "x" });
  const c = store.getState().conversation;
  assert.equal(c.entries.length, MAX_ENTRIES);
  assert.equal(c.entries[c.entries.length - 1].id, "e" + (MAX_ENTRIES + 20));
  assert.equal(c.atStart, false);
});

test("the refusal phrase is declared in the store", () => {
  assert.ok(VIEWER_REFUSED_PHRASE.length > 10);
});

// --- the rows -----------------------------------------------------------------

test("each kind of entry is a row, and text is text", () => {
  const evil = "<img src=x onerror=alert(1)> **bold**";
  const rows = [
    entry(1, { text: evil }),
    entry(2, { kind: "reply", markdown: evil }),
    entry(3, { kind: "thinking", seconds: 4 }),
    entry(4, { kind: "tool", name: "Bash", label: "go test ./...", status: "error" }),
    entry(5, { kind: "subagent", description: "tests", status: "running" }),
    entry(6, { kind: "compaction", turns: 12 }),
    entry(7, { kind: "notice", text: "heads up" }),
    entry(8, { kind: "image", mediaType: "image/png", width: 10, height: 20 }),
    entry(9, { kind: "strange" }),
  ];
  const out = renderToString(html`<ol>${rows.map((e) => html`<${Row} key=${e.id} entry=${e} />`)}</ol>`);
  assert.ok(!out.includes("<img"), "markup in text was drawn");
  assert.ok(out.includes("&lt;img src=x"));
  assert.ok(out.includes("**bold**"), "Markdown was interpreted");
  assert.ok(out.includes("Thought for 4 seconds"));
  assert.ok(out.includes("go test ./..."));
  assert.ok(out.includes("conv-tool--error"));
  assert.ok(out.includes("Helper: tests"));
  assert.ok(out.includes("Conversation compacted (12 turns)"));
  assert.ok(out.includes("heads up"));
  assert.ok(out.includes("Image (image/png, 10 by 20)"));
  assert.equal((out.match(/<li /g) || []).length, 9);
});

test("a tool row has an icon for its kind", () => {
  assert.equal(toolKind("Bash"), "run");
  assert.equal(toolKind("Edit"), "edit");
  assert.equal(toolKind("Read"), "read");
  assert.equal(toolKind("Grep"), "search");
  assert.equal(toolKind("WebFetch"), "web");
  assert.equal(toolKind("mcp__x"), "other");
  const out = renderToString(html`<${Row} entry=${entry(1, { kind: "tool", name: "Bash", label: "ls", status: "ok" })} />`);
  assert.ok(out.includes("conv-icon--run"));
});

test("the list says so when it is empty, has Older until the start, and the refusal in place of a list", () => {
  const base = { id: "p1", supported: true, entries: [], atStart: true, older: "idle", refused: "" };
  assert.ok(renderToString(html`<${Conversation} conv=${base} />`).includes(NO_ENTRIES_WORDS));
  assert.ok(!renderToString(html`<${Conversation} conv=${base} />`).includes("conv-older"));
  assert.ok(renderToString(html`<${Conversation} conv=${{ ...base, atStart: false, entries: [entry(1)] }} />`).includes("conv-older"));
  const loading = renderToString(html`<${Conversation} conv=${{ ...base, atStart: false, older: "loading", entries: [entry(1)] }} />`);
  assert.ok(loading.includes("disabled"));
  const refused = renderToString(html`<${Conversation} conv=${{ ...base, supported: null, refused: VIEWER_REFUSED_PHRASE + "." }} />`);
  assert.ok(refused.includes(VIEWER_REFUSED_PHRASE));
  assert.ok(!refused.includes("conv-list"));
});

// --- in the page --------------------------------------------------------------

const TAX = "366c9143";
const CART = "6fb40971";

async function mount() {
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
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  await settle();
  ws.frame({
    type: "state", root: "/work/a", projects: [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }],
    panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name }])),
  });
  await settle();
  ws.frame(g);
  await settle();
  return {
    dom, ws, store, container,
    box: (pane) => container.findAll("[data-pane]").find((e) => String(e.className || e.getAttribute("class")).includes("tree-row") && e.getAttribute("data-pane") === pane),
    tabs: () => container.findAll(".drawer-tab"),
    panel: (id) => container.find("#drawer-panel-" + id),
    done: () => { render(null, container); store.stop(); restore(); },
  };
}

async function withPage(fn) {
  const m = await mount();
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

test("opening a drawer asks for the pane's conversation, and it has a tab once the desk says there is one", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    assert.deepEqual(m.ws.commands("conversationOpen"), [{ cmd: "conversationOpen", id: TAX }]);
    assert.equal(m.tabs().length, 1, "a tab before the desk answered");
    m.ws.frame(page(TAX, [entry(1, { text: "fix the rounding" }), entry(2, { kind: "reply", markdown: "On it." })], { atStart: true }));
    await settle();
    assert.equal(m.tabs().length, 2);
    assert.equal(m.tabs()[1].textContent, "Conversation");
    assert.ok(m.panel("conversation").getAttribute("hidden") !== null, "the conversation is shown before it is chosen");
    m.tabs()[1].click();
    await settle();
    assert.ok(m.panel("conversation").getAttribute("hidden") === null);
    assert.ok(m.panel("terminal").getAttribute("hidden") !== null, "the terminal panel is not hidden");
    assert.equal(m.tabs()[1].getAttribute("aria-selected"), "true");
    assert.equal(m.container.findAll(".conv-row").length, 2);
    m.ws.frame({ type: "conversationAppend", id: TAX, entries: [entry(3, { kind: "tool", name: "Bash", label: "go test", status: "running" })], cursor: "e3" });
    await settle();
    assert.equal(m.container.findAll(".conv-row").length, 3, "the list did not follow the append");
    assert.equal(m.ws.commands("conversationClose").length, 0);
    assert.equal(PtySocket.instances.length <= 1, true, "the terminal reopened");
  }));

test("a pane with no conversation has no tab, and closing the drawer sends conversationClose", () =>
  withPage(async (m) => {
    m.box(CART).click();
    await settle();
    m.ws.frame({ type: "conversationPage", id: CART, supported: false, entries: [] });
    await settle();
    assert.equal(m.tabs().length, 1);
    m.container.find("[data-drawer-pane]").keydown("Escape");
    await settle();
    assert.deepEqual(m.ws.commands("conversationClose"), [{ cmd: "conversationClose", id: CART }]);
  }));

test("opening another pane closes the first one's conversation and returns to the terminal tab", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    m.ws.frame(page(TAX, [entry(1)], { atStart: true }));
    await settle();
    m.tabs()[1].click();
    await settle();
    m.box(CART).click();
    await settle();
    assert.deepEqual(m.ws.commands("conversationClose"), [{ cmd: "conversationClose", id: TAX }]);
    assert.deepEqual(m.ws.commands("conversationOpen").map((c) => c.id), [TAX, CART]);
    assert.equal(m.tabs().length, 1);
    assert.ok(m.panel("terminal").getAttribute("hidden") === null);
  }));

test("Older in the page asks for the page before and the entries go above", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    m.ws.frame(page(TAX, range(51, 100), { atStart: false }));
    await settle();
    m.tabs()[1].click();
    await settle();
    const older = m.container.find(".conv-older");
    assert.ok(older, "no Older button");
    older.click();
    await settle();
    assert.deepEqual(m.ws.commands("conversationOlder"), [{ cmd: "conversationOlder", id: TAX, before: "e51" }]);
    m.ws.frame(page(TAX, range(1, 50), { atStart: true }));
    await settle();
    assert.equal(m.container.findAll(".conv-row").length, 100);
    assert.ok(!m.container.find(".conv-older"), "Older at the start");
  }));

test("a device that may only view sees the refusal on the tab, once", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    m.ws.frame({ type: "notice", text: VIEWER_REFUSED_PHRASE + ". Ask for it to be made a full device on the machine Flockdeck runs on", error: true });
    await settle();
    assert.equal(m.tabs().length, 2);
    m.tabs()[1].click();
    await settle();
    const said = m.container.find(".conv-refused");
    assert.ok(said && said.textContent.startsWith(VIEWER_REFUSED_PHRASE));
    assert.equal(m.ws.commands("conversationOpen").length, 1);
  }));

test("Left and Right move between the tabs", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    m.ws.frame(page(TAX, [entry(1)], { atStart: true }));
    await settle();
    m.tabs()[0].focus();
    const ev = m.tabs()[0].keydown("ArrowRight");
    assert.equal(ev.defaultPrevented, true);
    await settle();
    assert.equal(m.tabs()[1].getAttribute("aria-selected"), "true");
    assert.ok(m.dom.document.activeElement === m.tabs()[1], "the keyboard did not follow");
    m.tabs()[1].keydown("ArrowLeft");
    await settle();
    assert.equal(m.tabs()[0].getAttribute("aria-selected"), "true");
  }));

test("long text on a row is cut", () => {
  const out = renderToString(html`<${Row} entry=${entry(1, { text: "a".repeat(MAX_TEXT + 500) })} />`);
  assert.ok(out.includes("a".repeat(100)));
  assert.ok(!out.includes("a".repeat(MAX_TEXT + 1)), "the whole text was drawn");
});

test("a refusal after the conversation was shown does not replace it", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame(page("p1", range(1, 3), { atStart: true }));
  // A refused paneSend, say, comes back as the same notice.
  ws.frame({ type: "notice", text: VIEWER_REFUSED_PHRASE + ". Ask for it to be made a full device", error: true });
  const c = store.getState().conversation;
  assert.equal(c.supported, true);
  assert.equal(c.refused, "");
  assert.deepEqual(ids(c), ["e1", "e2", "e3"]);
});

test("long reply text is cut, from the markdown field and from the text field", () => {
  for (const field of ["markdown", "text"]) {
    const out = renderToString(html`<${Row} entry=${entry(1, { kind: "reply", text: "", markdown: "", [field]: "b".repeat(MAX_REPLY + 500) })} />`);
    assert.ok(out.includes("b".repeat(100)), field + " was not drawn");
    assert.ok(!out.includes("b".repeat(MAX_REPLY + 1)), "the whole " + field + " was drawn");
    assert.ok(out.includes("500 more characters are not shown."), field + " did not say it was cut");
  }
});

test("a hidden drawer panel is not displayed, whatever display the panel sets", () => {
  const css = readFileSync(join(here, "..", "src", "drawer", "drawer.css"), "utf8");
  const panel = css.indexOf(".drawer-panel {");
  const hidden = css.search(/\.drawer-panel\[hidden\]\s*\{[^}]*display:\s*none/);
  assert.ok(panel >= 0 && /\.drawer-panel \{[^}]*display:\s*flex/.test(css), "the panel no longer sets display, so this test proves nothing");
  assert.ok(hidden > panel, "a hidden panel would show beside the shown one");
});

test("a remote window is not sent conversation text: nothing is asked and the tab says where it is", async () => {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  const ws = FakeWebSocket.last;
  // Asked for before the hello is known, as the drawer does on a reload.
  h.store.watchConversation("p1");
  ws.frame(hello({ remote: true }));
  assert.equal(ws.commands("conversationOpen").length, 0, "an open was sent to the desktop");
  assert.equal(h.store.getState().conversation.refused, REMOTE_CONVERSATION_LINE);
  // And when the pane is asked for after the hello.
  h.store.watchConversation("p2");
  assert.equal(ws.commands("conversationOpen").length, 0);
  assert.equal(h.store.getState().conversation.refused, REMOTE_CONVERSATION_LINE);
  assert.equal(h.store.olderConversation(), false);
  assert.equal(ws.commands("conversationOlder").length, 0);
});

test("the notice a remote window is sent for a conversation command is shown on the tab", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  ws.frame({ type: "notice", text: REMOTE_CONVERSATION_LINE, error: true });
  assert.equal(store.getState().conversation.refused, REMOTE_CONVERSATION_LINE);
});
