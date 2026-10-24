// The leftovers of L1: the Alt+number tab key, Close finished panes (from a key and
// from the Agents dialog), the desk's focus following the drawer, the agent lists
// asked for again when a dialog that chooses from them opens, and closing panes
// from the Team dialog. The pure parts first, then the page on a fake socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { rangeAction, rangesFrom } from "../assets/shell/keys.js";
import { finishedOf, resultOf, resultWords, teamCloseCommand, teamOf } from "../assets/lib/team.js";
import { tabsOf } from "../assets/lib/lifecycle.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const MARKUP = '<img src=x onerror="alert(1)">';

// ---------------------------------------------------------------- pure parts

const TABLE = [
  { id: "selectTab", keys: "Alt+1 … Alt+9", label: "Select tab by number" },
  { id: "other", keys: "Ctrl+Shift+K" },
  { id: "backwards", keys: "Alt+9 … Alt+1" },
  { id: "mixed", keys: "Alt+1 … Ctrl+9" },
  { id: "long", keys: "Alt+10 … Alt+19" },
];
const press = (key, over = {}) => ({ key, ctrlKey: false, shiftKey: false, altKey: true, ...over });

test("a run of keys in the table is read, and a key of it is its number", () => {
  const ranges = rangesFrom(TABLE);
  assert.deepEqual(ranges.map((r) => r.id), ["selectTab"], "a backwards run, mixed modifiers and a run of long names are not read");
  const on = ["selectTab"];
  assert.deepEqual(rangeAction(ranges, press("1"), on), { id: "selectTab", n: 1 });
  assert.deepEqual(rangeAction(ranges, press("9"), on), { id: "selectTab", n: 9 });
  assert.equal(rangeAction(ranges, press("0"), on), null, "outside the run");
  assert.equal(rangeAction(ranges, press("1", { altKey: false }), on), null, "the modifier is part of the key");
  assert.equal(rangeAction(ranges, press("1", { ctrlKey: true }), on), null);
  assert.equal(rangeAction(ranges, press("1"), []), null, "a window that does not run the action leaves the key alone");
  assert.equal(rangeAction(ranges, { ...press("1"), isComposing: true }, on), null, "a key that belongs to an input method");
  assert.equal(rangeAction(ranges, press("Enter"), on), null);
  assert.deepEqual(rangesFrom(null), []);
});

test("a tab carries the pane the desk has focused in it", () => {
  const tabs = tabsOf({ activeTab: "t1", tabs: [{ id: "t1", focus: "p2", root: { pane: "p1" } }, { id: "t2", root: { pane: "p3" } }] });
  assert.deepEqual(tabs.map((t) => [t.id, t.focus, t.active]), [["t1", "p2", true], ["t2", "", false]]);
});

test("the roster says which panes Close finished would close, and the answer says what it did", () => {
  const t = teamOf({ roster: [
    { pane: "a", name: "one", finished: true }, { pane: "b", name: "two", finished: true, locked: true }, { pane: "c", name: "three", activity: "working" }, { pane: 5 },
  ] });
  assert.deepEqual(t.roster.map((r) => [r.pane, r.finished, r.locked]), [["a", true, false], ["b", true, true], ["c", false, false]]);
  assert.deepEqual(finishedOf(t.roster).map((r) => r.pane), ["a"], "a locked pane is never offered");
  assert.deepEqual(teamCloseCommand("/work/a", ["a", "b"], "u1"), { cmd: "teamClose", root: "/work/a", req: "u1", panes: ["a", "b"] });
  assert.equal(teamCloseCommand("/work/a", [], "u1"), null);
  const r = resultOf({ op: "teamClose", ok: true, closed: 1, skipped: ["two: locked", 7] });
  assert.deepEqual(r.skipped, ["two: locked"]);
  assert.equal(resultWords(r), "Closed 1 pane. Left open: two: locked.");
  assert.equal(resultWords(resultOf({ op: "teamClose", ok: true })), "No pane was closed.");
  assert.equal(resultWords(resultOf({ op: "teamClose", ok: true, closed: 3 })), "Closed 3 panes.");
});

// ---------------------------------------------------------------- the page

const KEYS = [
  { id: "selectTab", keys: "Alt+1 … Alt+9", label: "Select tab by number", section: "Tabs" },
  { id: "closeFinishedPanes", keys: "Ctrl+Shift+F", label: "Close finished panes", section: "Agents" },
  { id: "team", keys: "Ctrl+Shift+M", label: "Team", section: "Agents" },
  { id: "agents", keys: "Ctrl+Shift+A", label: "Agents", section: "Agents" },
  { id: "newAgentTabChoose", keys: "Ctrl+Shift+N", label: "New agent tab", section: "Tabs" },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0, active: true }];

async function mount({ helloOver = {}, tabs = [], activeTab = "" } = {}) {
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
  const stateOf = (over = {}) => ({
    type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {} }, activeTab, tabs,
    panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", status: "working" }])), ...over,
  });
  const m = {
    dom, store, container, ws, g, stateOf,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: parts.includes("Alt") });
      await settle();
    },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, ...helloOver }));
      await settle();
      ws().frame(stateOf());
      await settle();
      if (!helloOver.remote) {
        ws().frame(g);
        await settle();
      }
    },
    notice: () => (container.find(".notice") ? container.find(".notice").textContent : ""),
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

const TABS = [
  { id: "t1", title: "one", root: { pane: "x1" } },
  { id: "t2", title: "two", root: { pane: "x2" } },
  { id: "t3", title: "three", root: { pane: "x3" } },
];
const confirmBtn = (m, label) => m.container.findAll(".dialog-btn").find((b) => b.textContent === label);
const isOff = (b) => !!b.disabled || b.attributes.disabled !== undefined;

test("Alt+number chooses that tab, and says so when there is none", () =>
  withPage({ tabs: TABS, activeTab: "t1" }, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Alt+2");
    assert.deepEqual(m.sent("selectTab"), [{ cmd: "selectTab", id: "t2" }]);
    m.clear();
    await m.key("Alt+3");
    assert.deepEqual(m.sent("selectTab"), [{ cmd: "selectTab", id: "t3" }]);
    m.clear();
    await m.key("Alt+7");
    assert.equal(m.sent("selectTab").length, 0);
    assert.match(m.notice(), /There is no tab 7\./);
    await m.key("Alt+0");
    assert.equal(m.sent("selectTab").length, 0, "0 is not in the run");
  }));

test("Alt+number does nothing under a dialog", () =>
  withPage({ tabs: TABS, activeTab: "t1" }, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+A");
    assert.ok(m.container.find("#agents-title"));
    m.clear();
    await m.key("Alt+2");
    assert.equal(m.sent("selectTab").length, 0);
  }));

test("Close finished panes asks first, from the key and from the Agents dialog, and sends nothing on Cancel", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+F");
    assert.ok(m.container.find(".confirm"), "the key asks");
    assert.equal(m.sent("closeFinishedPanes").length, 0);
    await m.click(confirmBtn(m, "Cancel"));
    assert.equal(m.container.find(".confirm"), null);
    assert.equal(m.sent("closeFinishedPanes").length, 0);
    await m.key("Ctrl+Shift+A");
    await m.click(m.container.find("#ag-close-finished"));
    assert.ok(m.container.find(".confirm"), "the dialog's button asks as well");
    await m.click(confirmBtn(m, "Close finished panes"));
    assert.deepEqual(m.sent("closeFinishedPanes"), [{ cmd: "closeFinishedPanes" }]);
    assert.equal(m.container.find(".confirm"), null);
  }));

test("a pane opened in the drawer is focused on the desk, once, when the desk does not have it", () =>
  withPage({}, async (m) => {
    const first = m.g.nodes.find((n) => !n.shell).pane;
    const tabs = [{ id: "t1", title: "one", focus: "elsewhere", root: { pane: first } }];
    await m.connect();
    await m.push(m.stateOf({ tabs, activeTab: "t1" }));
    await m.key("Ctrl+Shift+A");
    await m.push({ type: "agents", items: [{ paneId: first, tabId: "t1", root: "/work/a", project: "alpha", name: "boss", activity: "working" }] });
    m.clear();
    await m.click(m.container.find(".ag-row"));
    assert.deepEqual(m.sent("focusPane"), [{ cmd: "focusPane", id: first }]);
    m.clear();
    await m.push(m.stateOf({ tabs: [{ ...tabs[0], focus: first }], activeTab: "t1" }));
    assert.equal(m.sent("focusPane").length, 0, "a state push does not send it again");
  }));

test("a pane the desk already has focused is not sent", () =>
  withPage({}, async (m) => {
    const first = m.g.nodes.find((n) => !n.shell).pane;
    await m.connect();
    await m.push(m.stateOf({ tabs: [{ id: "t1", title: "one", focus: first, root: { pane: first } }], activeTab: "t1" }));
    await m.key("Ctrl+Shift+A");
    await m.push({ type: "agents", items: [{ paneId: first, tabId: "t1", root: "/work/a", project: "alpha", name: "boss", activity: "working" }] });
    m.clear();
    await m.click(m.container.find(".ag-row"));
    assert.equal(m.sent("focusPane").length, 0);
  }));

test("the agent lists are asked for again when the start dialog opens", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+N");
    assert.equal(m.sent("refreshAgents").length, 1);
  }));

test("a relay window sends neither refreshAgents nor focusPane", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+N");
    assert.equal(m.sent("refreshAgents").length, 0);
    assert.equal(m.sent("focusPane").length, 0);
  }));

const teamPush = (roster) => ({
  type: "team", root: "/work/a", project: "alpha", hasManager: true, roster,
  counts: { panes: roster.length, live: roster.length, tasks: {}, open: 0, messages: 0, unread: 0, unseen: 0 }, tasks: [], messages: [],
});

test("Team: the Agents tab closes finished panes after asking, and says what was left open", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush([
      { pane: "a", name: MARKUP, role: "worker", status: "idle", finished: true },
      { pane: "b", name: "locked one", status: "idle", finished: true, locked: true },
      { pane: "c", name: "busy", status: "working", activity: "working" },
    ]));
    assert.match(m.container.find("#team-tab-agents").textContent, /Agents \(3\)/);
    await m.click(m.container.find("#team-tab-agents"));
    assert.ok(m.container.find("#team-agents").textContent.includes(MARKUP));
    assert.equal(m.container.findAll("img").length, 0, "a name is text");
    assert.equal(isOff(m.container.find("#team-close-a")), false);
    assert.equal(isOff(m.container.find("#team-close-b")), true, "a locked pane");
    assert.equal(isOff(m.container.find("#team-close-c")), true, "a pane at work");
    assert.match(m.container.find("#team-close-finished").textContent, /\(1\)/);
    m.clear();
    await m.click(m.container.find("#team-close-finished"));
    assert.equal(m.sent("teamClose").length, 0, "it asks first");
    await m.click(confirmBtn(m, "Cancel"));
    assert.equal(m.sent("teamClose").length, 0);
    await m.click(m.container.find("#team-close-a"));
    await m.click(confirmBtn(m, "Close 1 pane"));
    const sent = m.sent("teamClose");
    assert.deepEqual(sent.map(({ req, ...c }) => c), [{ cmd: "teamClose", root: "/work/a", panes: ["a"] }]);
    await m.push({ type: "teamResult", op: "teamClose", ok: true, req: sent[0].req, closed: 0, skipped: ["one: not finished any more"] });
    assert.match(m.container.find("#team-said").textContent, /No pane was closed\. Left open: one: not finished any more\./);
  }));

test("Team: a project with nothing to close has the button off", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+M");
    await m.push(teamPush([{ pane: "c", name: "busy", status: "working", activity: "working" }]));
    await m.click(m.container.find("#team-tab-agents"));
    assert.equal(isOff(m.container.find("#team-close-finished")), true);
  }));
