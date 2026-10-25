// The narrow layout and the window the graph is not sent to: below a width the
// graph is a list (the strip of what needs the person first, then the teams as
// indented rows), and a window reached through the relay has the roster of the
// state push in the same list, with no graph command sent. The page is the Live
// shell over a store on a fake socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { needsOfRoster } from "../src/graph/List.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures");
const fixture = (name) => ({ ...JSON.parse(readFileSync(join(dir, name + ".json"), "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }];
const stateMsg = (panes = {}) => ({ type: "state", root: "/work/a", projects, panes });
const fakeMedia = (narrow) => () => ({ matches: narrow, addEventListener() {}, removeEventListener() {} });

async function mount({ narrow, remote = false }) {
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
  render(html`<${Live} store=${store} window=${dom.window} matchMedia=${fakeMedia(narrow)} terminalOptions=${terminalOptions} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  return {
    dom, store, container, ws,
    connect: async () => {
      ws().open();
      ws().frame(hello({ remote }));
      await settle();
    },
    push: async (msg) => {
      ws().frame(msg);
      await settle();
    },
    // The needs-you strip as [pane, kind] in the order it is drawn.
    strip: () => container.findAll(".needs-row").map((r) => [r.getAttribute("data-pane"), r.getAttribute("data-kind")]),
    rows: () => container.findAll(".list-row"),
    row: (pane) => container.findAll(".list-row").find((r) => r.getAttribute("data-pane") === pane),
    active: () => dom.document.activeElement,
    graphCommands: () => ws().sent.filter((c) => String(c.cmd).startsWith("graph")),
    drawer: () => container.find("[data-drawer-pane]"),
    done: () => { render(null, container); store.stop(); restore(); },
  };
}

async function withPage(opts, fn) {
  const m = await mount(opts);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

// ----------------------------------------------------------- the relay window

const ROSTER = {
  p1: { id: "p1", name: "manager", role: "manager", activity: "working" },
  p2: { id: "p2", name: "<img src=x onerror=alert(1)>", activity: "waiting", permission: { tool: "Bash", summary: "Bash: the details are on the desktop", redacted: true, options: [] } },
  p3: { id: "p3", name: "docs", activity: "waiting", ask: { questions: [{ question: "Which heading?", options: [] }] } },
};

for (const narrow of [true, false]) {
  test(`a remote window (${narrow ? "narrow" : "wide"}) sends no graph command and shows the roster, the strip and the desktop line`, async () => {
    await withPage({ narrow, remote: true }, async (m) => {
      await m.connect();
      await m.push(stateMsg(ROSTER));
      await m.push(fixture("01-six-helpers-one-permission"));
      assert.deepEqual(m.graphCommands(), [], "a graph command went to the relay");
      assert.match(m.container.find(".main").textContent, /The team graph is shown on the desktop/);
      assert.ok(m.container.find(".graph") === null, "a graph was drawn");
      assert.equal(m.container.findAll(".home-agent").length, 3);
      // Those waiting on the person are in the strip, in the push's order, with their words.
      assert.deepEqual(m.strip(), [["p2", "permission"], ["p3", "ask"]]);
      assert.match(m.container.find(".needs").textContent, /Which heading\?/);
      // Names are text.
      assert.equal(m.container.findAll("img").length, 0);
      assert.ok(m.container.textContent.includes("<img src=x"));
    });
  });
}

test("a remote window's roster row opens the drawer for that agent, and Esc brings the keyboard back", async () => {
  await withPage({ narrow: true, remote: true }, async (m) => {
    await m.connect();
    await m.push(stateMsg(ROSTER));
    const pick = () => m.container.findAll(".list-pick").find((b) => b.parentNode.getAttribute("data-pane") === "p1");
    pick().click();
    await settle();
    assert.ok(m.drawer(), "the row did not open the drawer");
    assert.equal(m.drawer().getAttribute("data-drawer-pane"), "p1");
    assert.match(m.container.find(".drawer-name").textContent, /manager/);
    // The drawer still asks for no graph, and no structured watch goes out for a terminal pane.
    assert.deepEqual(m.graphCommands(), []);
    m.drawer().keydown("Escape");
    await settle();
    assert.ok(m.drawer() === null);
    assert.ok(m.active() === pick(), "the keyboard did not return to the roster row");
  });
});

test("a pane that leaves the roster closes its drawer", async () => {
  await withPage({ narrow: true, remote: true }, async (m) => {
    await m.connect();
    await m.push(stateMsg(ROSTER));
    m.container.findAll(".list-pick").find((b) => b.parentNode.getAttribute("data-pane") === "p3").click();
    await settle();
    assert.ok(m.drawer());
    const { p3, ...rest } = ROSTER;
    await m.push(stateMsg(rest));
    assert.ok(m.drawer() === null, "the drawer stayed open for an agent that is gone");
    // It does not come back when the agent does.
    await m.push(stateMsg(ROSTER));
    assert.ok(m.drawer() === null, "the drawer came back with the agent");
  });
});

test("needsOfRoster reads the push as the relay makes it, and skips what is not a pane", () => {
  const model = needsOfRoster({ panes: { ...ROSTER, bad: 5, none: { name: "no id" } } });
  assert.deepEqual(model.needs.map((n) => [n.pane, n.kind, n.text]), [["p2", "permission", "Bash: the details are on the desktop"], ["p3", "ask", "Which heading?"]]);
  assert.deepEqual(needsOfRoster(null).needs, []);
});

// ----------------------------------------------------------- what the list must not lose

test("the roster row of the open agent says it is expanded", async () => {
  await withPage({ narrow: true, remote: true }, async (m) => {
    await m.connect();
    await m.push(stateMsg(ROSTER));
    const expanded = (id) => m.container.findAll(".list-pick").find((b) => b.parentNode.getAttribute("data-pane") === id).getAttribute("aria-expanded");
    assert.equal(expanded("p1"), "false");
    m.container.findAll(".list-pick").find((b) => b.parentNode.getAttribute("data-pane") === "p1").click();
    await settle();
    assert.equal(expanded("p1"), "true");
    assert.equal(expanded("p2"), "false");
  });
});

test("an entry without an id is no row, and an id that comes twice is one row", async () => {
  await withPage({ narrow: true, remote: true }, async (m) => {
    await m.connect();
    await m.push(stateMsg({ a: { id: "p1", name: "one", activity: "working" }, b: { name: "no id" }, c: { id: "p1", name: "again", activity: "working" }, d: { id: "p2", name: "two" } }));
    assert.deepEqual(m.container.findAll(".home-agent").map((r) => r.getAttribute("data-pane")), ["p1", "p2"]);
  });
  const model = needsOfRoster({ panes: {
    a: { id: "p1", permission: { summary: "first" } }, b: { permission: { summary: "no id" } }, c: { id: "p1", permission: { summary: "second" } },
  } });
  assert.deepEqual(model.needs.map((n) => [n.pane, n.text]), [["p1", "first"]]);
});

test("a remote window's roster row for an agent that runs without asking opens a drawer with the warning", async () => {
  await withPage({ narrow: true, remote: true }, async (m) => {
    await m.connect();
    await m.push(stateMsg({ ...ROSTER, p1: { ...ROSTER.p1, structured: true, runsUnasked: true } }));
    m.container.findAll(".list-pick").find((b) => b.parentNode.getAttribute("data-pane") === "p1").click();
    await settle();
    assert.match(m.container.find(".drawer-warn").textContent, /may run commands and edit files without asking you first: in this repository Flockdeck could not make it ask/);
    // Another agent's drawer has none.
    m.drawer().keydown("Escape");
    await settle();
    m.container.findAll(".list-pick").find((b) => b.parentNode.getAttribute("data-pane") === "p3").click();
    await settle();
    assert.equal(m.container.find(".drawer-warn"), null);
  });
});

