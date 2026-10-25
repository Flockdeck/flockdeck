// The home manager bar and the manager's Conversation tab, on the real page: the
// bar steps aside only for the manager's own box, and keeps what was typed in it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const MANAGER = "0c96cbe9";
const TAX = "366c9143";
const entry = (n) => ({ id: "e" + n, kind: "prompt", ts: "2026-10-21T08:00:00Z", text: "say " + n });
const page = (id) => ({ type: "conversationPage", id, supported: true, agent: "claude", entries: [entry(1)], cursor: "e1", atStart: true });

const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Finding your way", noPalette: true },
  { id: "nextNeedsYou", keys: "Ctrl+Shift+U", label: "Next agent that needs you", section: "Finding your way" },
  { id: "prevNeedsYou", keys: "Ctrl+Shift+J", label: "Previous agent that needs you", section: "Finding your way" },
  { id: "managerBar", keys: "Ctrl+Shift+H", label: "Message the manager", section: "Finding your way" },
];
const pressManagerKey = (dom) => dom.window.dispatch("keydown", { key: "H", ctrlKey: true, shiftKey: true, altKey: false });

async function mount(helloOver = {}, edit = (g) => g) {
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
  const g = edit({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ keys: KEYS, ...helloOver }));
  await settle();
  ws.frame({
    type: "state", root: "/work/a", projects: [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }],
    panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name }])),
  });
  await settle();
  ws.frame(g);
  await settle();
  return {
    dom, ws, container,
    active: () => dom.document.activeElement,
    box: (pane) => container.findAll("[data-pane]").find((e) => String(e.className || e.getAttribute("class")).includes("tree-row") && e.getAttribute("data-pane") === pane),
    tabs: () => container.findAll(".drawer-tab"),
    bar: () => container.find(".mbar"),
    barHidden: () => container.find(".mbar").getAttribute("hidden") !== null,
    open: async (pane) => {
      FakeWebSocket.last.frame(page(pane));
      await settle();
    },
    done: () => { render(null, container); store.stop(); restore(); },
  };
}

async function withPage(fn, over, edit) {
  const m = await mount(over, edit);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

test("the bar is hidden, not removed, while the manager's Conversation tab has its own box, and its draft survives", () =>
  withPage(async (m) => {
    assert.equal(m.barHidden(), false);
    m.bar().find(".mbar-input").enter("tell api to stop");
    await settle();
    m.box(MANAGER).click();
    await settle();
    await m.open(MANAGER);
    m.tabs()[1].click();
    await settle();
    assert.equal(m.barHidden(), true, "two boxes to the manager");
    assert.ok(m.container.find(".structured-input"), "no box in the drawer");
    m.tabs()[0].click();
    await settle();
    assert.equal(m.barHidden(), false, "no box at all on the terminal tab");
    assert.equal(m.bar().find(".mbar-input").value, "tell api to stop", "the draft was lost");
  }));

test("a worker's Conversation tab leaves the bar alone", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    await m.open(TAX);
    m.tabs()[1].click();
    await settle();
    assert.ok(m.container.find(".structured-input"), "the worker has its own box");
    assert.equal(m.barHidden(), false, "the bar to the manager was hidden for a worker's tab");
  }));

test("a viewer keeps the bar: the drawer has no box for it to stand in for", () =>
  withPage(async (m) => {
    m.box(MANAGER).click();
    await settle();
    await m.open(MANAGER);
    m.tabs()[1].click();
    await settle();
    assert.equal(m.barHidden(), false);
  }, { role: "viewer" }));

test("the stylesheet hides a hidden bar and the footer round it", () => {
  const css = readFileSync(join(here, "..", "src", "app.css"), "utf8");
  assert.match(css, /\.mbar\[hidden\]\s*\{\s*display:\s*none/);
  assert.match(css, /\.composer:has\(>\s*\.mbar\[hidden\]\)\s*\{\s*display:\s*none/);
});

test("the manager-bar key goes to the drawer box when that box stands in for the bar, and to the bar otherwise", () =>
  withPage(async (m) => {
    m.box(MANAGER).click();
    await settle();
    await m.open(MANAGER);
    m.tabs()[1].click();
    await settle();
    pressManagerKey(m.dom);
    await settle();
    assert.equal(m.active(), m.container.find(".structured-input"), "the key went to the hidden bar");
    m.tabs()[0].click();
    await settle();
    pressManagerKey(m.dom);
    await settle();
    assert.equal(m.active(), m.container.find(".mbar-input"));
  }));

test("a worker's box does not take the manager-bar key", () =>
  withPage(async (m) => {
    m.box(TAX).click();
    await settle();
    await m.open(TAX);
    m.tabs()[1].click();
    await settle();
    pressManagerKey(m.dom);
    await settle();
    assert.equal(m.active(), m.container.find(".mbar-input"), "'tell the manager' went to the worker");
  }));

test("with two teams the bar and the key belong to the first team's manager only", () =>
  withPage(async (m) => {
    const second = "mgr2";
    const picks = m.container.findAll(".tab-pick");
    assert.ok(picks.length >= 2, "the second team has no tab");
    picks[1].click();
    await settle();
    m.box(second).click();
    await settle();
    await m.open(second);
    m.tabs()[1].click();
    await settle();
    assert.equal(m.barHidden(), false, "another team's manager box hid the bar of the first");
    pressManagerKey(m.dom);
    await settle();
    assert.equal(m.active(), m.container.find(".mbar-input"));
  }, {}, (g) => {
    const t2 = { ...g.teams[0], id: "t2", name: "Second", manager: "mgr2", members: ["mgr2"], needs: [] };
    return { ...g, teams: [...g.teams, t2], nodes: [...g.nodes, { pane: "mgr2", kind: "terminal", name: "mgr2", role: "manager", status: "idle", activity: "idle", waitingOn: { kind: "none" } }] };
  }));
