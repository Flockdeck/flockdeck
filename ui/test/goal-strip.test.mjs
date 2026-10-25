// The goal strip on the page: a tab for each team of the graph and Solo, the view
// switch on a row of its own, and what the person does to a tab (open, rename,
// move, archive, restore), kept in the interface's preferences through uiPrefs.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { labelOf } from "../src/graph/GoalTabs.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const ONE = "0c96cbe9";
const TWO = "t-two";
const MARKUP = '<img src=x onerror="alert(1)">';
const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
  { id: "selectTab", keys: "Alt+1 … Alt+9", label: "Select tab by number", section: "Tabs" },
  { id: "nextTab", keys: "Ctrl+Tab", label: "Next tab", section: "Tabs", noPalette: true },
  { id: "prevTab", keys: "Ctrl+Shift+Tab", label: "Previous tab", section: "Tabs", noPalette: true },
  { id: "moveTabLeft", keys: "Ctrl+Shift+PageUp", label: "Move tab left", section: "Tabs" },
  { id: "moveTabRight", keys: "Ctrl+Shift+PageDown", label: "Move tab right", section: "Tabs" },
];
const API = { major: 1, minor: 1, caps: ["clientHello", "unsupported", "apiMismatch", "uiPrefs"] };

// The fixture has one goal. A second one, with one manager, makes two tabs.
const graph = () => {
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  const base = g.nodes[0];
  g.nodes = [...g.nodes, { ...base, pane: "m9", name: "tester", parent: "", children: [], depth: 0, activity: "idle", status: "idle", team: TWO }];
  g.teams = [...g.teams, { id: TWO, manager: "m9", members: ["m9"], goal: "Fix " + MARKUP + " tests", started: g.teams[0].started, needs: [], caps: [] }];
  return g;
};
const stateOf = (g) => ({
  type: "state", root: "/work/a", projects: [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }], activeTab: "t1", tabs: [{ id: "t1", title: "Main" }],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name }])),
});

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
  const g = graph();
  const m = {
    dom, store, container, ws,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: parts.includes("Alt") });
      await settle();
    },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, api: caps ? API : undefined, prefs: ui ? { ui } : {} }));
      await settle();
      ws().frame(stateOf(g));
      await settle();
      ws().frame(g);
      await settle();
    },
    tabs: () => container.findAll(".goaltab"),
    ids: () => container.findAll(".goaltab").map((t) => t.getAttribute("data-tab")),
    active: () => container.findAll(".goaltab").filter((t) => t.attributes.class.includes("is-active")).map((t) => t.getAttribute("data-tab")),
    rows: () => container.findAll(".tree-row").map((r) => r.getAttribute("data-pane")),
    plus: async () => { if (!container.find(".tabs-menu")) await m.click(container.find(".tabs-plus")); },
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

test("a tab for each goal, then Solo; labels are text, and the first goal that waits is open", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.deepEqual(m.ids(), [ONE, TWO, "solo"]);
    const labels = m.tabs().map((t) => t.find(".tab-title").textContent);
    assert.equal(labels[0], labelOf("Ship 01-six-helpers-one-permission"));
    assert.equal(labels[1], labelOf("Fix " + MARKUP + " tests"), "cut at a word, and still text");
    assert.equal(labels[2], "Solo");
    assert.equal(m.container.findAll("img").length, 0);
    assert.deepEqual(m.active(), [ONE], "the goal with a pane waiting on the person");
    assert.ok(m.tabs()[0].find(".tab-needs"), "its badge counts what waits");
    assert.equal(m.tabs()[2].find(".tab-close"), null, "Solo cannot be archived");
  }));

test("choosing a tab shows that goal's outline only, and Solo a flat list; the desk is not asked", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    assert.equal(m.rows().includes("m9"), false);
    assert.equal(m.rows().includes(ONE), true);
    await m.click(m.tabs()[1].find(".tab-pick"));
    assert.deepEqual(m.rows(), ["m9"]);
    assert.deepEqual(m.active(), [TWO]);
    await m.click(m.tabs()[2].find(".tab-pick"));
    assert.deepEqual(m.rows(), []);
    assert.equal(m.sent("selectTab").length, 0);
  }));

test("the views are on a row of their own, not inside the strip", () =>
  withPage({}, async (m) => {
    await m.connect();
    const bar = m.container.find(".viewbar");
    assert.ok(bar);
    assert.deepEqual(bar.findAll(".tabs-view").map((b) => b.textContent.trim()), ["Team", "Dependencies", "Board", "Changes", "Replay"]);
    assert.equal(m.container.find(".goaltabs").findAll(".tabs-view").length, 0);
    assert.equal(m.container.find(".goaltabs").findAll(".viewbar").length, 0);
    assert.equal(m.container.find(".tabs"), null, "the desk's tab strip is gone");
  }));

test("renaming is a double click; Enter keeps the name in the preferences, Escape does not, empty gives the goal's words back", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    m.tabs()[1].find(".tab-pick").dispatch("dblclick");
    await settle();
    const box = m.container.find(".tab-rename");
    assert.ok(box, "the field opens");
    box.value = "Tests";
    box.dispatch("input");
    await settle();
    box.keydown("Escape");
    await settle();
    assert.equal(m.sent("uiPrefs").length, 0);
    m.tabs()[1].find(".tab-pick").dispatch("dblclick");
    await settle();
    const again = m.container.find(".tab-rename");
    again.value = "  Tests  ";
    again.dispatch("input");
    await settle();
    again.keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("uiPrefs"), [{ cmd: "uiPrefs", ui: { "tabs.names": { [TWO]: "Tests" } } }]);
    assert.equal(m.tabs()[1].find(".tab-title").textContent, "Tests", "shown at once, before the desk answers");
    // Solo has no name to change.
    m.clear();
    m.tabs()[2].find(".tab-pick").dispatch("dblclick");
    await settle();
    assert.equal(m.container.find(".tab-rename"), null);
  }));

test("a rename keeps what was typed when the field loses focus, and Escape then blur does not", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    m.tabs()[1].find(".tab-pick").dispatch("dblclick");
    await settle();
    let box = m.container.find(".tab-rename");
    box.value = "Tests";
    box.dispatch("input");
    box.keydown("Escape");
    box.dispatch("blur");
    await settle();
    assert.equal(m.sent("uiPrefs").length, 0, "Escape was undone by the blur that followed");
    m.tabs()[1].find(".tab-pick").dispatch("dblclick");
    await settle();
    box = m.container.find(".tab-rename");
    box.value = "Tests";
    box.dispatch("input");
    await settle();
    box.dispatch("blur");
    await settle();
    assert.deepEqual(m.sent("uiPrefs"), [{ cmd: "uiPrefs", ui: { "tabs.names": { [TWO]: "Tests" } } }]);
  }));

test("focus goes back to the tab after a rename at the keyboard, and to its neighbour after an archive", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.tabs()[1].find(".tab-pick").dispatch("dblclick");
    await settle();
    m.container.find(".tab-rename").keydown("Escape");
    await settle();
    const doc = m.container.ownerDocument;
    assert.equal(doc.activeElement, m.tabs()[1].find(".tab-pick"), "focus was lost with the field");
    await m.click(m.tabs()[1].find(".tab-close"));
    await settle();
    assert.ok(doc.activeElement && doc.activeElement.getAttribute("role") === "tab", "focus was lost with the archived tab");
  }));

test("the + menu: Home and End go to the first and last item, Tab closes it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.plus();
    const items = () => m.container.findAll('[role="menuitem"]').filter((b) => !b.disabled);
    const doc = m.container.ownerDocument;
    const menu = m.container.find(".tabs-menu");
    assert.ok(items().length > 2);
    menu.keydown("End");
    await settle();
    assert.equal(doc.activeElement, items()[items().length - 1]);
    menu.keydown("Home");
    await settle();
    assert.equal(doc.activeElement, items()[0]);
    menu.keydown("Tab");
    await settle();
    assert.equal(m.container.find(".tabs-menu"), null, "Tab left the menu open");
  }));

test("archiving a middle goal puts focus on its neighbouring tab", () =>
  withPage({}, async (m) => {
    await m.connect();
    const doc = m.container.ownerDocument;
    assert.deepEqual(m.ids(), [ONE, TWO, "solo"]);
    await m.click(m.tabs()[0].find(".tab-close"));
    await settle();
    assert.deepEqual(m.ids(), [TWO, "solo"]);
    assert.equal(doc.activeElement, m.tabs()[0].find('[role="tab"]'), "focus did not go to the tab that took its place");
    await m.click(m.tabs()[0].find(".tab-close"));
    await settle();
    assert.deepEqual(m.ids(), ["solo"]);
    assert.equal(doc.activeElement, m.tabs()[0].find('[role="tab"]'), "focus did not go to Solo, the last tab");
  }));

test("a name and an order kept in the preferences are used", () =>
  withPage({ ui: { tabs: { names: { [TWO]: "Tests" }, order: [TWO, ONE] } } }, async (m) => {
    await m.connect();
    assert.deepEqual(m.ids(), [TWO, ONE, "solo"]);
    assert.equal(m.tabs()[0].find(".tab-title").textContent, "Tests");
  }));

test("× archives a goal's tab; the + menu lists it and brings it back", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.tabs()[1].find(".tab-close"));
    assert.deepEqual(m.sent("uiPrefs"), [{ cmd: "uiPrefs", ui: { "tabs.archived": [TWO] } }]);
    assert.deepEqual(m.ids(), [ONE, "solo"]);
    assert.equal(m.sent("closeTab").length, 0, "nothing is closed on the desk");
    await m.plus();
    const restore = m.container.findAll(".tabs-restore");
    assert.equal(restore.length, 1);
    assert.equal(restore[0].textContent.startsWith("Show Fix"), true);
    m.clear();
    await m.click(restore[0]);
    assert.deepEqual(m.sent("uiPrefs"), [{ cmd: "uiPrefs", ui: { "tabs.archived": null } }], "nothing left to keep removes the key");
    assert.deepEqual(m.ids(), [ONE, TWO, "solo"]);
    assert.deepEqual(m.active(), [TWO], "the restored goal is the one open");
  }));

test("move left and right, and the keys, change the order of goals; Alt+number and Ctrl+Tab choose a tab", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.tabs()[0].find(".tab-pick"));
    await m.plus();
    const names = m.container.findAll(".tabs-btn");
    assert.equal(names.find((b) => b.textContent === "Move left").disabled, true, "first goal cannot go left");
    await m.click(names.find((b) => b.textContent === "Move right"));
    assert.deepEqual(m.sent("uiPrefs"), [{ cmd: "uiPrefs", ui: { "tabs.order": [TWO, ONE] } }]);
    assert.deepEqual(m.ids(), [TWO, ONE, "solo"]);
    m.clear();
    await m.key("Alt+3");
    assert.deepEqual(m.active(), ["solo"]);
    await m.key("Ctrl+Tab");
    assert.deepEqual(m.active(), [TWO], "after Solo, the first");
    await m.key("Ctrl+Shift+Tab");
    assert.deepEqual(m.active(), ["solo"]);
    assert.equal(m.sent("nextTab").length + m.sent("prevTab").length + m.sent("selectTab").length, 0);
  }));

test("the menu starts with Start a team, and keeps the ways to open an agent", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.plus();
    const names = m.container.findAll(".tabs-btn").map((b) => b.textContent.trim());
    assert.equal(names[0], "Start a team…");
    for (const want of ["New agent tab", "Choose agent…", "New shell tab", "Resume…", "Find pane…", "Rename this goal"]) assert.ok(names.includes(want), want);
    m.clear();
    await m.click(m.container.findAll(".tabs-btn").find((b) => b.textContent === "New shell tab"));
    assert.deepEqual(m.sent("newTab").map((c) => c.kind), ["shell"]);
  }));

test("with a desktop that has no uiPrefs the choices still work, for the life of the page", () =>
  withPage({ caps: false }, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.tabs()[1].find(".tab-close"));
    assert.equal(m.sent("uiPrefs").length, 0, "nothing the desktop would not understand is sent");
    assert.deepEqual(m.ids(), [ONE, "solo"]);
  }));

const click = async (m, el) => { await m.click(el); };
const viewBtn = (m, id) => m.container.findAll(".tabs-view").find((b) => b.getAttribute("data-view") === id);
const isOn = (b) => b.attributes.class.includes("is-on");

test("choosing a tab on a project-wide view shows the Team view; on Dependencies it stays", () =>
  withPage({}, async (m) => {
    await m.connect();
    await click(m, viewBtn(m, "board"));
    assert.equal(isOn(viewBtn(m, "board")), true);
    await click(m, m.tabs()[1].find(".tab-pick"));
    assert.equal(isOn(viewBtn(m, "tree")), true, "the tab did something the person can see");
    assert.equal(isOn(viewBtn(m, "board")), false);
    await click(m, viewBtn(m, "deps"));
    await click(m, m.tabs()[0].find(".tab-pick"));
    assert.equal(isOn(viewBtn(m, "deps")), true, "Dependencies follows the tab, so it stays");
    await click(m, viewBtn(m, "changes"));
    await m.key("Alt+2");
    assert.equal(isOn(viewBtn(m, "tree")), true, "the keys do the same");
  }));

test("a new shell tab opens its pane in the drawer, on Solo", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.plus();
    m.clear();
    await click(m, m.container.findAll(".tabs-btn").find((b) => b.textContent === "New shell tab"));
    assert.equal(m.sent("newTab").length, 1);
    const g = graph();
    g.nodes = [...g.nodes, { ...g.nodes[0], pane: "sh1", name: "shell one", parent: "", children: [], depth: 0, team: "", shell: true }];
    g.solo = ["sh1"];
    const st = stateOf(g);
    await m.push(st);
    await m.push(g);
    assert.equal(m.container.find(".drawer-name").textContent, "shell one");
    assert.deepEqual(m.active(), ["solo"]);
    assert.ok(m.rows().includes("sh1"));
  }));

test("opening a pane from elsewhere (an agent started, the feed, the palette) opens the goal that holds it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await click(m, m.tabs()[1].find(".tab-pick"));
    assert.deepEqual(m.active(), [TWO]);
    await m.push({ type: "agentStarted", paneId: ONE, id: "x" });
    assert.deepEqual(m.active(), [ONE]);
  }));

test("an empty Solo says so in one line", () =>
  withPage({}, async (m) => {
    await m.connect();
    await click(m, m.tabs()[2].find(".tab-pick"));
    assert.equal(m.container.find(".solo-empty").textContent, "No panes outside a goal.");
    await click(m, m.tabs()[0].find(".tab-pick"));
    assert.equal(m.container.find(".solo-empty"), null);
  }));

test("archiving says how to get the tab back, and a dot on the + says an archived goal needs you", () =>
  withPage({}, async (m) => {
    await m.connect();
    await click(m, m.tabs()[1].find(".tab-close"));
    assert.match(m.container.find(".notice").textContent, /Archived\. Show it again from the \+ menu\./);
    assert.equal(m.container.find(".tabs-plus-dot"), null);
    await click(m, m.tabs()[0].find(".tab-close"));
    assert.ok(m.container.find(".tabs-plus-dot"), "the first goal has a pane waiting on the person");
    assert.match(m.container.find(".tabs-plus").getAttribute("aria-label"), /an archived goal needs you/);
  }));

test("the older single tabs key is still read, and the next change carries its choices to the three keys and removes it", () =>
  withPage({ ui: { tabs: { names: { [TWO]: "Tests" } } } }, async (m) => {
    await m.connect();
    assert.equal(m.tabs()[1].find(".tab-title").textContent, "Tests", "the old key was not read");
    m.clear();
    await m.click(m.tabs()[0].find(".tab-close"));
    assert.deepEqual(m.sent("uiPrefs"), [{ cmd: "uiPrefs", ui: { "tabs.archived": [ONE], "tabs.names": { [TWO]: "Tests" }, tabs: null } }]);
  }));
