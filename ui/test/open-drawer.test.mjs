// The drawer in the real page: the shell over a store on a fake control socket,
// with the terminal on a fake pty socket. A click or Enter on a graph box opens
// the drawer for that pane, Esc closes it and the keyboard returns to the box,
// the header follows later pushes, and nothing resizes the pty. The page also
// starts again, and asks for its graph again, when it comes back from the
// back-forward cache.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render, options } from "../src/vendor/preact.mjs";
import { Drawer } from "../src/drawer/Drawer.js";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { Home } from "../src/shell/Home.js";
import { TerminalChrome } from "../src/lib/terminal.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";
import { fakeKeys } from "./util.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const projects = [
  { root: "/work/a", name: "alpha", waiting: 0, working: 0 },
  { root: "/work/b", name: "beta", waiting: 0, working: 0 },
];

// The graph of fixture 1, with a change to a node where a test wants one. The
// fixture's permission need is for pane TAX, so the needs-you strip has a row
// that carries the same pane id as that pane's box.
const graph = (change = {}) => {
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  g.nodes = g.nodes.map((n) => ({ ...n, ...(change[n.pane] || {}) }));
  return g;
};
const MANAGER = "0c96cbe9";
const CART = "6fb40971";
const TAX = "366c9143";

const stateOf = (g, extra = {}) => ({
  type: "state", root: "/work/a", projects,
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", ...(extra[n.pane] || {}) }])),
});

async function mount({ keys = [], g = graph() } = {}) {
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
    box: (pane) => container.findAll("[data-pane]").find((e) => String(e.className || e.getAttribute("class")).includes("tree-row") && e.getAttribute("data-pane") === pane),
    bodies: () => container.findAll("[data-drawer-pane]"),
    name: () => { const e = container.find(".drawer-name"); return e ? e.textContent : ""; },
    active: () => dom.document.activeElement,
    push: async (msg) => { ws().frame(msg); await settle(); },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys }));
      await settle();
      ws().frame(stateOf(g));
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

test("a click on a box opens the drawer for that pane, and Enter on another replaces it", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.equal(m.bodies().length, 0);
    m.box(CART).click();
    await settle();
    assert.equal(m.name(), "cart");
    assert.equal(m.bodies()[0].getAttribute("data-drawer-pane"), CART);
    const ev = m.box(TAX).keydown("Enter");
    assert.equal(ev.defaultPrevented, true);
    await settle();
    assert.equal(m.name(), "tax");
    assert.equal(m.bodies().length, 1, "two drawers");
    assert.equal(m.container.findAll(".drawer").length, 1);
  }));

test("Esc closes the drawer and the keyboard goes to the box, not to the needs-you row with the same pane id", () =>
  withPage({}, async (m) => {
    await m.connect();
    const rows = m.container.findAll("[data-pane]").filter((e) => !String(e.className || e.getAttribute("class")).includes("tree-row") && e.getAttribute("data-pane") === TAX);
    assert.ok(rows.length > 0, "the strip has no row for the pane, so this test would prove nothing");
    m.box(TAX).click();
    await settle();
    assert.equal(m.active().getAttribute("data-drawer-pane"), TAX);
    m.bodies()[0].keydown("Escape");
    await settle();
    assert.equal(m.bodies().length, 0);
    assert.equal(m.active(), m.box(TAX), "the keyboard did not return to the box");
  }));

test("focus returns to the box after a push that drew the graph again", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.box(CART).click();
    await settle();
    await m.push(graph({ [CART]: { status: "idle", activity: "idle", branch: "cart-2" } }));
    await m.push(graph({ [CART]: { status: "working", activity: "working", branch: "cart-3" } }));
    m.bodies()[0].keydown("Escape");
    await settle();
    assert.equal(m.active(), m.box(CART));
    assert.equal(m.active().getAttribute("aria-selected"), "false");
  }));

test("the header follows later pushes while the drawer is open", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.box(CART).click();
    await settle();
    assert.match(m.container.find(".fact--status").textContent, /working/);
    await m.push(graph({ [CART]: { status: "waiting", activity: "waiting", branch: "cart-9" } }));
    assert.match(m.container.find(".fact--status").textContent, /waiting/);
    assert.match(m.container.find(".fact--branch").textContent, /cart-9/);
    assert.equal(m.name(), "cart");
    await m.push({ ...stateOf(graph(), { [CART]: { model: "opus-5", recording: true } }) });
    assert.match(m.container.find(".fact--model").textContent, /opus-5/);
    assert.ok(m.container.find(".fact--recording"));
  }));

test("opening, maximising, restoring and closing the drawer send no pty resize", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.box(CART).click();
    await settle();
    const pty = PtySocket.last;
    assert.ok(pty, "no pty socket for a terminal pane");
    pty.open();
    m.container.find(".drawer-max").click();
    await settle();
    m.container.find(".drawer-max").click();
    await settle();
    m.bodies()[0].keydown("Escape");
    await settle();
    m.box(CART).click();
    await settle();
    for (const ws of PtySocket.instances) {
      assert.equal(ws.plainJson.filter((x) => x.resize).length, 0, "a resize was sent");
    }
  }));

test("a pane that cannot have a terminal shows the structured view and opens no pty socket", () =>
  withPage({ g: graph({ [CART]: { kind: "structured" } }) }, async (m) => {
    await m.connect();
    m.box(CART).click();
    await settle();
    assert.ok(m.container.find(".drawer-stub").textContent.includes("Loading the agent"));
    assert.equal(PtySocket.instances.length, 0);
    assert.equal(FakeTerm.instances.length, 0);
  }));

test("a pane that leaves the picture closes its drawer", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.box(CART).click();
    await settle();
    const g = graph();
    g.nodes = g.nodes.filter((n) => n.pane !== CART);
    g.teams = g.teams.map((t) => ({ ...t, members: t.members.filter((x) => x !== CART) }));
    g.edges = g.edges.filter((e) => e.from !== CART && e.to !== CART);
    await m.push(g);
    assert.equal(m.bodies().length, 0);
  }));

test("the shell keeps its key table through a disconnect", () =>
  withPage({ keys: fakeKeys }, async (m) => {
    await m.connect();
    m.ws().drop();
    await settle();
    // The store has emptied its hello; the shell still reads the keys it had.
    assert.equal(m.store.getState().hello, null);
    const ev = m.container.find("main").keydown("K", { ctrlKey: true, shiftKey: true });
    assert.equal(ev.defaultPrevented, true, "the palette key stopped working while disconnected");
    await settle();
    assert.ok(m.container.find(".palette"));
  }));

test("a page that is stopped and started again asks for its graph once more, and only once", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.equal(m.ws().commands("graphWatch").length, 1);
    m.store.stop();
    await settle();
    m.store.start();
    await settle();
    const second = m.ws();
    second.open();
    second.frame(hello());
    await settle();
    second.frame(stateOf(graph()));
    await settle();
    assert.equal(second.commands("graphWatch").length, 1, "the graph was not asked for again, or was asked for twice");
  }));

test("a graph command that failed is said above the picture that stays", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.push({ type: "teamResult", ok: false, op: "graphSnapshot", error: "the project is busy" });
    assert.match(m.container.find(".home").textContent, /The team graph could not be updated: the project is busy/);
    assert.ok(m.container.find(".tree-row"), "the picture went");
  }));

test("a local window the desk refused the graph for is not told it is the relay", () => {
  const snap = { connection: "open", hello: { type: "hello" }, remote: false, state: { projects, panes: {} }, graph: null, graphRefused: true };
  const out = renderToString(html`<${Home} snap=${snap} selected="/work/a" />`);
  assert.match(out, /The desktop would not send the team graph to this window/);
  const remote = renderToString(html`<${Home} snap=${{ ...snap, remote: true }} selected="/work/a" />`);
  assert.match(remote, /The team graph is shown on the desktop\./);
});

test("the terminal cover offers Try again for a failed library load and not for a blocked terminal", () => {
  const chrome = (phase) => renderToString(html`<${TerminalChrome} state=${{ phase, reason: "why", ptySize: null, sizeNote: null }} hostRef=${{ current: null }} />`);
  assert.match(chrome("load-failed"), /Try again/);
  assert.ok(!chrome("blocked").includes("Try again"));
  assert.match(chrome("refused"), /Try again/);
});

test("after a switch the drawer is never drawn for the old project's pane, even for one frame", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.box(CART).click();
    await settle();
    assert.equal(m.name(), "cart");
    // Every commit of the drawer, with the props it was given. A commit with the
    // props it had before is Preact skipping an unchanged drawer, not a new draw.
    const drawn = [];
    const before = options.diffed;
    options.diffed = (vnode) => {
      if (vnode.type === Drawer) drawn.push(vnode.props);
      if (before) before(vnode);
    };
    let known = null;
    try {
      await m.push(graph());
      known = drawn[drawn.length - 1];
      drawn.length = 0;
      const rail = m.container.findAll("button").find((b) => b.attributes["data-root"] === "/work/b");
      rail.click();
      await settle();
    } finally {
      options.diffed = before;
    }
    const fresh = drawn.filter((p) => p !== known).map((p) => p.node && p.node.pane);
    assert.ok(!fresh.includes(CART), "the drawer was drawn for project B with project A's pane");
    assert.equal(m.bodies().length, 0);
  }));
