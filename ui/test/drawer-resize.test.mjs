// The border between the main area and the drawer is a handle: dragged, moved with
// the arrow keys, reset by a double click or Home, kept in the interface's
// preferences (layout.drawerW), and only there while the drawer is open and not
// maximised.
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
import { DRAWER_H_MIN, DRAWER_MIN, DRAWER_MAX, ORIENT_KEY, prefsOf, setting } from "../src/lib/prefs.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const KEY = "layout.drawerW";
const API = { major: 1, minor: 1, caps: ["clientHello", "unsupported", "apiMismatch", "uiPrefs"] };

async function mount({ caps = true, ui = null, portrait = false } = {}) {
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
  const matchMedia = (q) => ({ media: q, matches: portrait && q.includes("portrait"), addEventListener() {}, removeEventListener() {} });
  render(html`<${Live} store=${store} window=${dom.window} matchMedia=${matchMedia} terminalOptions=${terminalOptions} />`, container);
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
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: parts.includes("Alt") });
      await settle();
    },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    shell: () => container.find(".shell"),
    handle: () => container.find(".drawer-resize"),
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: [{ id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" }], api: caps ? API : undefined, prefs: ui ? { ui } : {} }));
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

const stacked = (m) => m.shell().attributes.class.includes("shell--stack");
const layoutBtn = (m) => m.container.find(".drawer-layout");

test("a window taller than wide stacks the drawer below until the person chooses; a wide one puts it beside", () =>
  withPage({ portrait: true }, async (m) => {
    await m.connect();
    assert.equal(stacked(m), true);
    await m.openDrawer();
    assert.equal(layoutBtn(m).textContent, "Side by side");
    m.clear();
    await m.click(layoutBtn(m));
    assert.deepEqual(uiSent(m), [{ [ORIENT_KEY]: "side" }]);
    assert.equal(stacked(m), false);
    assert.equal(layoutBtn(m).textContent, "Stack");
  }).then(() => withPage({}, async (m) => {
    await m.connect();
    assert.equal(stacked(m), false);
    await m.openDrawer();
    m.clear();
    await m.click(layoutBtn(m));
    assert.deepEqual(uiSent(m), [{ [ORIENT_KEY]: "stack" }]);
    assert.equal(stacked(m), true);
  })));

test("a kept choice wins over the window's shape, and the palette can flip it", () =>
  withPage({ portrait: true, ui: { [ORIENT_KEY]: "side" } }, async (m) => {
    await m.connect();
    assert.equal(stacked(m), false, "the person chose side by side");
    m.clear();
    await m.key("Ctrl+Shift+K");
    const row = m.container.findAll(".palette-row").find((r) => r.textContent.includes("Layout: stack / side by side"));
    assert.ok(row, "the palette lists it");
    await m.click(row);
    assert.deepEqual(uiSent(m), [{ [ORIENT_KEY]: "stack" }]);
    assert.equal(stacked(m), true);
  }));

test("below the main area the handle is horizontal: Up grows the drawer, Down shrinks it, within 240 px", () =>
  withPage({ ui: { [ORIENT_KEY]: "stack" } }, async (m) => {
    await m.connect();
    await m.openDrawer();
    const h = () => m.handle();
    assert.equal(h().attributes["aria-orientation"], "horizontal");
    assert.equal(h().attributes["aria-label"], "Drawer height");
    assert.equal(h().attributes["aria-valuenow"], "360");
    m.clear();
    h().keydown("ArrowUp");
    await settle();
    assert.deepEqual(uiSent(m), [{ "layout.drawerH": 376 }]);
    assert.match(m.shell().style.cssText, /--drawer-h:376px/);
    h().keydown("ArrowDown", { shiftKey: true });
    await settle();
    assert.deepEqual(uiSent(m).pop(), { "layout.drawerH": 312 });
    for (let i = 0; i < 4; i++) {
      h().keydown("ArrowDown", { shiftKey: true });
      await settle();
    }
    assert.equal(h().attributes["aria-valuenow"], String(DRAWER_H_MIN));
    h().keydown("ArrowLeft");
    await settle();
    assert.equal(h().attributes["aria-valuenow"], String(DRAWER_H_MIN), "Left and Right are for the side handle");
    h().keydown("Home");
    await settle();
    assert.deepEqual(uiSent(m).pop(), { "layout.drawerH": null });
    h().dispatch("pointerdown", { button: 0, clientY: 500, pointerId: 1 });
    h().dispatch("pointermove", { clientY: 400 });
    await settle();
    assert.match(m.shell().style.cssText, /--drawer-h:460px/);
    h().dispatch("pointerup", {});
    await settle();
    assert.deepEqual(uiSent(m).pop(), { "layout.drawerH": 460 });
  }));

test("maximised, stacked or not, the drawer has no handle and no stacked layout", () =>
  withPage({ ui: { [ORIENT_KEY]: "stack" } }, async (m) => {
    await m.connect();
    await m.openDrawer();
    await m.click(m.container.find(".drawer-max"));
    assert.equal(m.handle(), null);
    assert.equal(layoutBtn(m), null, "nothing to move while it fills the area");
    assert.match(m.shell().attributes.class, /shell--max/);
  }));

test("the layout settings are read defensively and kept as nothing at the default", () => {
  assert.equal(prefsOf({ ui: { [ORIENT_KEY]: "sideways" } }).orient, "auto");
  assert.equal(prefsOf({ ui: { [ORIENT_KEY]: "stack" } }).orient, "stack");
  assert.equal(prefsOf({ ui: { "layout.drawerH": 10 } }).drawerH, 360);
  assert.equal(prefsOf({ ui: { "layout.drawerH": 500 } }).drawerH, 500);
  assert.equal(setting("orient", "sideways", {}, ["uiPrefs"]), null);
  assert.deepEqual(setting("orient", "auto", {}, ["uiPrefs"]).command, { cmd: "uiPrefs", ui: { [ORIENT_KEY]: null } });
  assert.equal(setting("drawerH", 100, {}, ["uiPrefs"]), null);
  assert.equal(setting("drawerH", 500, {}, []), null);
});
