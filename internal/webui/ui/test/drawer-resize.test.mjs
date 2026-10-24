// The border between the main area and the drawer is a handle: dragged, moved with
// the arrow keys, reset by a double click or Home, kept in the interface's
// preferences (layout.drawerW), and only there while the drawer is open and not
// maximised.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";
import { DRAWER_MIN, DRAWER_MAX, prefsOf, setting } from "../assets/lib/prefs.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const KEY = "layout.drawerW";
const API = { major: 1, minor: 1, caps: ["clientHello", "unsupported", "apiMismatch", "uiPrefs"] };

async function mount({ caps = true, ui = null } = {}) {
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
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  const state = {
    type: "state", root: "/work/a", projects: [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }], activeTab: "t1", tabs: [{ id: "t1", title: "Main" }],
    panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name }])),
  };
  const m = {
    container, ws,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    shell: () => container.find(".shell"),
    handle: () => container.find(".drawer-resize"),
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: [], api: caps ? API : undefined, prefs: ui ? { ui } : {} }));
      await settle();
      ws().frame(state);
      await settle();
      ws().frame(g);
      await settle();
    },
    openDrawer: async () => m.click(container.find(".tree-row")),
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

const uiSent = (m) => m.sent("uiPrefs").map((c) => c.ui);

test("the handle is there while the drawer is open, and not when it is closed or maximised", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.equal(m.handle(), null, "no drawer, no handle");
    await m.openDrawer();
    const h = m.handle();
    assert.ok(h);
    assert.equal(h.attributes.role, "separator");
    assert.equal(h.attributes["aria-orientation"], "vertical");
    assert.equal(h.attributes.tabindex, "0");
    assert.equal(h.attributes["aria-valuenow"], "420");
    await m.click(m.container.find(".drawer-max"));
    assert.equal(m.handle(), null, "maximised, the drawer fills the area");
    await m.click(m.container.find(".drawer-max"));
    assert.ok(m.handle());
  }));

test("the arrow keys move the border 16 px, Shift 64 px, within the limits, and Home resets", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openDrawer();
    m.clear();
    m.handle().keydown("ArrowLeft");
    await settle();
    assert.deepEqual(uiSent(m), [{ [KEY]: 436 }]);
    assert.match(m.shell().style.cssText, /--drawer-w:436px/, "drawn at once");
    m.handle().keydown("ArrowLeft", { shiftKey: true });
    await settle();
    assert.deepEqual(uiSent(m).pop(), { [KEY]: 500 });
    m.handle().keydown("ArrowRight");
    await settle();
    assert.deepEqual(uiSent(m).pop(), { [KEY]: 484 });
    for (let i = 0; i < 6; i++) {
      m.handle().keydown("ArrowRight", { shiftKey: true });
      await settle();
    }
    assert.equal(m.handle().attributes["aria-valuenow"], String(DRAWER_MIN), "no narrower than the minimum");
    m.clear();
    m.handle().keydown("ArrowRight");
    await settle();
    assert.equal(m.sent("uiPrefs").length, 0, "nothing changes at the minimum");
    m.handle().keydown("Home");
    await settle();
    assert.deepEqual(uiSent(m), [{ [KEY]: null }], "the default is kept as nothing");
    assert.doesNotMatch(m.shell().style.cssText, /--drawer-w/);
    m.handle().keydown("a");
    await settle();
  }));

test("dragging draws the width as it moves and keeps it when the pointer is let go; left widens", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openDrawer();
    m.clear();
    const h = m.handle();
    h.dispatch("pointerdown", { button: 0, clientX: 600, pointerId: 1 });
    await settle();
    h.dispatch("pointermove", { clientX: 500 });
    await settle();
    assert.match(m.shell().style.cssText, /--drawer-w:520px/);
    assert.equal(m.sent("uiPrefs").length, 0, "nothing is kept while it moves");
    h.dispatch("pointermove", { clientX: 5000 });
    await settle();
    assert.match(m.shell().style.cssText, /--drawer-w:320px/, "held to the narrowest");
    h.dispatch("pointermove", { clientX: -9000 });
    await settle();
    assert.match(m.shell().style.cssText, new RegExp("--drawer-w:" + DRAWER_MAX + "px"), "and the widest");
    h.dispatch("pointermove", { clientX: 450 });
    h.dispatch("pointerup", {});
    await settle();
    assert.deepEqual(uiSent(m), [{ [KEY]: 570 }]);
  }));

test("a double click puts the width back to the default", () =>
  withPage({ ui: { [KEY]: 600 } }, async (m) => {
    await m.connect();
    await m.openDrawer();
    assert.match(m.shell().style.cssText, /--drawer-w:600px/, "the kept width is used");
    m.clear();
    m.handle().dispatch("dblclick");
    await settle();
    assert.deepEqual(uiSent(m), [{ [KEY]: null }]);
  }));

test("a width outside what a drawer can be is ignored", () => {
  assert.equal(prefsOf({ ui: { [KEY]: 100 } }).drawerW, 420);
  assert.equal(prefsOf({ ui: { [KEY]: 99999 } }).drawerW, 420);
  assert.equal(prefsOf({ ui: { [KEY]: "wide" } }).drawerW, 420);
  assert.equal(prefsOf({ ui: { [KEY]: 480 } }).drawerW, 480);
  assert.equal(setting("drawerW", 100, {}, ["uiPrefs"]), null);
  assert.equal(setting("drawerW", 480.5, {}, ["uiPrefs"]), null);
  assert.deepEqual(setting("drawerW", 480, {}, ["uiPrefs"]).command, { cmd: "uiPrefs", ui: { [KEY]: 480 } });
  assert.equal(setting("drawerW", 480, {}, []), null, "nowhere to keep it without uiPrefs");
});

test("with no uiPrefs on the desktop the width still changes, for the life of the page", () =>
  withPage({ caps: false }, async (m) => {
    await m.connect();
    await m.openDrawer();
    m.clear();
    m.handle().keydown("ArrowLeft");
    await settle();
    assert.equal(m.sent("uiPrefs").length, 0);
    assert.match(m.shell().style.cssText, /--drawer-w:436px/);
  }));
