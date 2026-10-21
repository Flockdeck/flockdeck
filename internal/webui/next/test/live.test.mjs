// The page wired to a store: the shell, the home screen and the graph, drawn
// from pushes on a fake socket. The five graph fixtures, the states of the
// home screen, a project switch, a reconnect, a window reached through the relay,
// and the end-to-end key on a remote hello.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { Home, usable, sameProject, rosterOf } from "../assets/shell/Home.js";
import { watchRemoteHello } from "../assets/lib/e2e-boot.js";
import { createTerminal } from "../assets/lib/terminal.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver, FakeHost, paneState, waitFor } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures");
const names = readdirSync(dir).filter((f) => f.endsWith(".json")).map((f) => f.slice(0, -5)).sort();
const fixture = (name, root = "/work/a") => ({ ...JSON.parse(readFileSync(join(dir, name + ".json"), "utf8")), root });

const projects = [
  { root: "/work/a", name: "alpha", waiting: 0, working: 0 },
  { root: "/work/b", name: "beta", waiting: 0, working: 0 },
];
const stateMsg = (root = "/work/a", panes = {}) => ({ type: "state", root, projects, panes });

async function mount(extra = {}) {
  const dom = createDom();
  const restore = install(dom);
  const { store, clock } = await harness(extra.store);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const ws = () => FakeWebSocket.last;
  const draw = () => render(html`<${Live} store=${store} window=${dom.window} />`, container);
  draw();
  await settle();
  return {
    dom, store, clock, container, ws,
    // The desktop's side: open the socket and say hello.
    connect: async (over = {}) => {
      ws().open();
      ws().frame(hello(over));
      await settle();
    },
    push: async (msg) => {
      ws().frame(msg);
      await settle();
    },
    main: () => container.find(".main"),
    text: () => container.textContent,
    done: () => { render(null, container); store.stop(); restore(); },
  };
}

test("before a hello the main area says it is connecting", async () => {
  const m = await mount();
  assert.match(m.main().textContent, /Connecting to Flockdeck/);
  assert.equal(m.container.find(".graph"), null);
  m.done();
});

test("a socket that is waiting to retry says how long", async () => {
  const m = await mount();
  m.ws().drop();
  await settle();
  assert.match(m.main().textContent, /Not connected to Flockdeck\. Trying again in 1 s/);
  m.done();
});

for (const name of names) {
  test(`fixture ${name}: hello, state and graph draw the graph, edges and the strip`, async () => {
    const m = await mount();
    await m.connect();
    await m.push(stateMsg());
    assert.deepEqual(m.ws().commands("graphWatch").map((c) => c.root), ["/work/a"], "the watch for the first project");
    assert.match(m.main().textContent, /Reading the team graph/);
    const msg = fixture(name);
    await m.push(msg);
    assert.ok(m.container.find(".graph"), "no graph");
    assert.ok(m.container.find(".needs"), "no needs-you strip");
    for (const n of msg.nodes) assert.ok(m.container.findAll(".gnode").some((g) => g.attributes["data-pane"] === n.pane), `${n.pane} is not drawn`);
    const edgeKinds = new Set(msg.edges.map((e) => e.kind));
    if (edgeKinds.has("message")) assert.ok(m.container.find(".gedge--message"), "a message edge is not drawn");
    assert.equal(m.main().textContent.includes("Reading the team graph"), false);
    m.done();
  });
}

test("fixture 5 draws the amber edge with its words through the page", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture("05-six-helpers-undelivered-message"));
  assert.ok(m.container.find(".gedge--undelivered"));
  assert.match(m.main().textContent, /not told yet/);
  m.done();
});

test("a second graph push updates the picture without remounting it", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  const msg = fixture("01-six-helpers-one-permission");
  await m.push(msg);
  const svg = m.container.find(".graph");
  const pane = msg.nodes[0].pane;
  const box = m.container.findAll(".gnode").find((g) => g.attributes["data-pane"] === pane);
  const next = { ...msg, nodes: msg.nodes.map((n, i) => (i === 0 ? { ...n, live: "A new line from the agent" } : n)) };
  await m.push(next);
  assert.equal(m.container.find(".graph"), svg, "the svg was replaced");
  assert.equal(m.container.findAll(".gnode").find((g) => g.attributes["data-pane"] === pane), box, "a box was replaced");
  assert.match(m.main().textContent, /A new line from the agent/);
  m.done();
});

test("a reconnect sends graphWatch again for the same project", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture("01-six-helpers-one-permission"));
  const first = m.ws();
  first.drop();
  await settle();
  assert.match(m.main().textContent, /Out of date/, "the last picture is kept and called out of date");
  assert.ok(m.container.find(".graph"));
  m.clock.advance(1000);
  assert.notEqual(m.ws(), first);
  await m.connect();
  await m.push(stateMsg());
  assert.deepEqual(m.ws().commands("graphWatch").map((c) => c.root), ["/work/a"]);
  assert.equal(m.main().textContent.includes("Out of date"), false);
  m.done();
});

test("a project switch watches the new project and draws no picture of the old one", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture("01-six-helpers-one-permission", "/work/a"));
  assert.ok(m.container.find(".graph"));
  const rail = m.container.findAll("button").find((b) => b.attributes["data-root"] === "/work/b");
  assert.ok(rail, "no rail button for beta");
  rail.click();
  await settle();
  assert.deepEqual(m.ws().commands("graphWatch").map((c) => c.root), ["/work/a", "/work/b"]);
  // The old project's picture is gone while the new one is read.
  assert.equal(m.container.find(".graph"), null);
  assert.match(m.main().textContent, /Reading the team graph/);
  // A push of the old project that was in flight is not drawn.
  await m.push(fixture("01-six-helpers-one-permission", "/work/a"));
  assert.equal(m.container.find(".graph"), null);
  await m.push(fixture("02-eight-helpers-permission-and-question", "/work/b"));
  assert.ok(m.container.find(".graph"));
  m.done();
});

test("teamGone clears the picture and says the project can no longer be read", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push(fixture("01-six-helpers-one-permission"));
  await m.push({ type: "teamGone", root: "/work/a", error: "that directory is gone" });
  assert.equal(m.container.find(".graph"), null);
  assert.match(m.main().textContent, /This project can no longer be read/);
  assert.match(m.main().textContent, /that directory is gone/);
  m.done();
});

test("no project open says so", async () => {
  const m = await mount();
  await m.connect();
  await m.push({ type: "state", root: "", projects: [], panes: {} });
  assert.match(m.main().textContent, /No project is open/);
  assert.equal(m.ws().commands("graphWatch").length, 0);
  m.done();
});

test("a graph command refused for another reason is said, with the desk's sentence", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  await m.push({ type: "teamResult", ok: false, op: "graphWatch", error: "no project is open at that directory" });
  assert.match(m.main().textContent, /The team graph could not be read/);
  assert.match(m.main().textContent, /no project is open at that directory/);
  m.done();
});

test("a graph push that cannot be read leaves the last good picture", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  const msg = fixture("01-six-helpers-one-permission");
  await m.push(msg);
  const before = m.container.findAll(".gnode").length;
  assert.ok(before > 0);
  await m.push({ type: "graph", root: "/work/a", nodes: "not a list", teams: null, edges: 7 });
  assert.equal(m.container.findAll(".gnode").length, before);
  assert.ok(m.container.find(".graph"));
  // And the next good push is drawn.
  await m.push({ ...msg, nodes: msg.nodes.slice(0, 2) });
  assert.equal(m.container.findAll(".gnode").length, 2);
  m.done();
});

test("a malformed frame does not take the page down", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg());
  m.ws().raw("{not json");
  m.ws().raw(JSON.stringify([1, 2]));
  await settle();
  assert.equal(m.store.getState().dropped, 2);
  assert.match(m.text(), /alpha/);
  m.done();
});

// ------------------------------------------------------------ through the relay

test("a remote hello sends no graph command and shows the roster and the desktop line", async () => {
  const m = await mount();
  await m.connect({ remote: true });
  await m.push(stateMsg("/work/a", {
    p1: { id: "p1", name: "manager", role: "manager", activity: "working" },
    p2: { id: "p2", name: "<img src=x onerror=alert(1)>", activity: "waiting" },
  }));
  assert.equal(m.ws().sent.filter((c) => String(c.cmd).startsWith("graph")).length, 0, "a graph command went to the relay");
  assert.match(m.main().textContent, /The team graph is shown on the desktop/);
  assert.equal(m.container.find(".graph"), null);
  const rows = m.container.findAll(".home-agent");
  assert.equal(rows.length, 2);
  assert.match(rows[0].textContent, /manager/);
  assert.match(rows[1].textContent, /waiting/);
  // The name of an agent is text, not markup.
  assert.equal(m.container.findAll("img").length, 0);
  assert.ok(rows[1].textContent.includes("<img src=x"));
  // A graph push in a window like this is not drawn.
  await m.push(fixture("01-six-helpers-one-permission"));
  assert.equal(m.container.find(".graph"), null);
  m.done();
});

test("a remote window that watches a project afterwards still sends no graph command", async () => {
  const m = await mount();
  await m.connect({ remote: true });
  await m.push(stateMsg());
  const rail = m.container.findAll("button").find((b) => b.attributes["data-root"] === "/work/b");
  rail.click();
  await settle();
  assert.equal(m.ws().sent.filter((c) => String(c.cmd).startsWith("graph")).length, 0);
  m.done();
});

test("the graph is refused by the desk: the same roster and line, and no retry", async () => {
  const m = await mount();
  await m.connect();
  await m.push(stateMsg("/work/a", { p1: { id: "p1", name: "manager", activity: "idle" } }));
  await m.push({ type: "teamResult", ok: false, op: "graphWatch", code: "deskOnly", error: "the graph is shown on the machine Flockdeck runs on" });
  assert.match(m.main().textContent, /The desktop would not send the team graph to this window/);
  assert.equal(m.container.findAll(".home-agent").length, 1);
  assert.equal(m.ws().commands("graphWatch").length, 1);
  m.done();
});

// ------------------------------------------------------------ the end-to-end key

const E2E_KEY = "AAAA";

function keyHarness(over = {}) {
  const calls = { loads: 0, registered: 0, fail: false };
  const loadE2E = async () => {
    calls.loads++;
    if (calls.fail) throw new Error("could not load assets/e2e.js");
    return { ensureRegistered: async () => { calls.registered++; } };
  };
  return { calls, loadE2E, ...over };
}

test("a remote hello registers the key once however many hellos arrive on one load of e2e.js", async () => {
  const { store } = await harness();
  const k = keyHarness();
  // The page's loadE2E shares one load, so the file runs once; here the fake
  // counts asks and registrations.
  watchRemoteHello(store, { loadE2E: k.loadE2E, onError() {} });
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ remote: true, e2ePublicKey: E2E_KEY }));
  await settle(20);
  for (let i = 0; i < 5; i++) ws.frame({ type: "state", root: "", projects: [], panes: {} });
  ws.frame({ type: "prefs", prefs: { a: 1 } });
  await settle(20);
  assert.equal(k.calls.registered, 1, "registered again on a push that was not a hello");
  assert.equal(k.calls.loads, 1);
  store.stop();
});

test("a window on the desktop does not load e2e.js for the key", async () => {
  const { store } = await harness();
  const k = keyHarness();
  watchRemoteHello(store, { loadE2E: k.loadE2E, onError() {} });
  store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  await settle(20);
  assert.equal(k.calls.loads, 0);
  store.stop();
});

test("a failed e2e.js load is reported, and retried on the next hello", async () => {
  const { store, clock } = await harness();
  const k = keyHarness();
  k.calls.fail = true;
  const errors = [];
  watchRemoteHello(store, { loadE2E: k.loadE2E, onError: (e) => errors.push(e.message) });
  store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello({ remote: true, e2ePublicKey: E2E_KEY }));
  await settle(20);
  assert.equal(errors.length, 1);
  assert.equal(k.calls.registered, 0);
  FakeWebSocket.last.frame({ type: "state", root: "", projects: [], panes: {} });
  await settle(20);
  assert.equal(k.calls.loads, 1, "a push retried the load");
  // The control socket reconnects: a new hello, and the load works this time.
  k.calls.fail = false;
  FakeWebSocket.last.drop();
  clock.advance(1000);
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello({ remote: true, e2ePublicKey: E2E_KEY }));
  await settle(20);
  assert.equal(k.calls.loads, 2);
  assert.equal(k.calls.registered, 1);
  store.stop();
});

test("a hello that carries an end-to-end key never opens a plain terminal", async () => {
  PtySocket.reset();
  FakeTerm.reset();
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({ remote: true, e2ePublicKey: E2E_KEY }));
  ws.frame(paneState({ p1: { status: "idle" } }));
  const ctl = createTerminal({
    host: new FakeHost(), paneId: "p1", store, location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" },
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket, isFocused: () => false,
    // The end-to-end layer is not there and cannot be had.
    e2e: null,
  });
  ctl.start();
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(PtySocket.instances.length, 0, "a socket was opened without the end-to-end layer");
  assert.equal(ctl.state.phase, "blocked");
  ctl.dispose();
  store.stop();
});

// ------------------------------------------------------------ the pieces

test("usable, sameProject and rosterOf", () => {
  assert.equal(usable({ type: "graph", nodes: [], teams: [], edges: [] }), true);
  for (const bad of [null, {}, { type: "graph" }, { type: "graph", nodes: {}, teams: [], edges: [] }, { type: "state", nodes: [], teams: [], edges: [] }]) assert.equal(usable(bad), false);
  assert.equal(sameProject("C:\\Work\\A\\", "c:/work/a"), true);
  assert.equal(sameProject("/work/a", "/work/b"), false);
  assert.deepEqual(rosterOf(null), []);
  assert.deepEqual(rosterOf({ panes: { x: { id: "x", name: "n", activity: "idle" }, y: 5 } }), [{ id: "x", name: "n", role: "", doing: "idle" }]);
});

test("Home draws from a snapshot alone", async () => {
  const dom = createDom();
  const restore = install(dom);
  const c = dom.document.createElement("div");
  dom.body.appendChild(c);
  render(html`<${Home} snap=${{ connection: "open", hello: { type: "hello" }, state: stateMsg(), graph: null, graphGone: null, graphRefused: false, graphError: null }} selected="/work/a" />`, c);
  await settle();
  assert.match(c.textContent, /Reading the team graph/);
  render(null, c);
  restore();
});

test("main.js makes the store, registers the key on a remote hello, starts the socket and unwatches on the way out", () => {
  const src = readFileSync(join(here, "..", "assets", "main.js"), "utf8");
  assert.match(src, /createStore\(\{ url: controlUrl\(window\.location\) \}\)/);
  assert.match(src, /watchRemoteHello\(store\)/);
  assert.match(src, /store\.start\(\)/);
  assert.match(src, /addEventListener\("pagehide", \(\) => store\.stop\(\)\)/);
  assert.ok(src.indexOf("watchRemoteHello(store)") < src.indexOf("store.start()"), "the watch must be set before the socket can say hello");
});
