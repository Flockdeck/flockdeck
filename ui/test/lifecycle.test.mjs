// Starting agents and the life of a pane (L1): the start dialog, the tab strip,
// and restart, lock, close and zoom, through the real page on a fake control
// socket. The pure parts (the catalog, the commands, the warnings) are tested
// first, and the page after.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { OPEN_AN_AGENT_WORDS } from "../src/shell/Live.js";
import { WORKTREE_WORDS } from "../src/shell/Start.js";
import { Header } from "../src/drawer/Header.js";
import { renderToString } from "./vendor/render-to-string.mjs";
import { LOCKED_WORDS } from "../src/drawer/Lifecycle.js";
import {
  catalogOf, closeTabWarnings, closeWarnings, defaultValue, moveTabCommand, newTabCommand, optionsOf, restartWarnings, splitValue, startAgentCommand,
  startProblem, tabsOf, valueOf,
} from "../src/lib/lifecycle.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";


// openPlus opens the tab strip's "+" menu, where the tab actions are.
const openPlus = async (m) => {
  if (!m.container.find(".tabs-menu")) await m.click(m.container.find(".tabs-plus"));
};

// The pane's actions are behind the drawer header's More button: open it when the
// button asked for is not there yet.
const pickMore = async (m, sel) => {
  if (!m.container.find(sel) && m.container.find(".drawer-more")) await m.click(m.container.find(".drawer-more"));
  return m.container.find(sel);
};

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");

const CATALOG = {
  items: [
    { id: "claude", name: "Claude Code", available: true, defaultModel: "sonnet", models: [{ id: "sonnet", name: "Sonnet" }, { id: "opus", name: "Opus" }] },
    { id: "codex", name: "Codex", available: false, install: "npm i -g codex", defaultModel: "", models: [] },
    { id: "opencode-acp", name: "opencode (structured)", available: true, defaultModel: "", models: [] },
  ],
  roles: [{ id: "manager", name: "Manager" }, { id: "worker", name: "Worker" }],
  default: { agent: "claude", model: "opus" },
};

// ---------------------------------------------------------------- pure parts

test("the catalog of a state push is read defensively", () => {
  assert.deepEqual(catalogOf(null).items, []);
  assert.deepEqual(catalogOf({ agents: { items: [null, 3, { id: "" }, { id: "a" }], roles: "x" } }).items.map((a) => a.id), ["a"]);
  assert.deepEqual(catalogOf({ agents: { roles: "x" } }).roles, []);
  assert.equal(catalogOf({}).default.agent, "");
});

test("an agent that is not installed is listed and cannot be chosen", () => {
  const options = optionsOf(catalogOf({ agents: CATALOG }));
  const codex = options.filter((o) => o.group.startsWith("Codex"));
  assert.equal(codex.length, 1);
  assert.match(codex[0].group, /not installed, see npm i -g codex/);
  assert.equal(codex[0].disabled, true);
  assert.equal(options.find((o) => o.value === valueOf("claude", "opus")).disabled, false);
  assert.ok(options.find((o) => o.value === valueOf("opencode-acp", "")), "an agent with no models stands as one entry");
});

test("the picker starts on the project's choice, then the overall one, then the first usable agent", () => {
  const cat = catalogOf({ agents: CATALOG });
  assert.equal(defaultValue(cat), valueOf("claude", "opus"));
  assert.equal(defaultValue({ ...cat, project: { agent: "opencode-acp", model: "" } }), valueOf("opencode-acp", ""));
  // A choice that is not installed is passed over.
  assert.equal(defaultValue({ ...cat, project: { agent: "codex", model: "" } }), valueOf("claude", "opus"));
  assert.equal(defaultValue({ ...cat, default: { agent: "gone", model: "" } }), valueOf("claude", "sonnet"));
  assert.equal(defaultValue(catalogOf({})), "");
  assert.deepEqual(splitValue(valueOf("a", "m")), ["a", "m"]);
});

test("the commands carry what was chosen, and a worktree only when it is asked for", () => {
  assert.deepEqual(newTabCommand({ kind: "shell", agent: "claude" }), { cmd: "newTab", kind: "shell" });
  assert.deepEqual(newTabCommand({ agent: "claude", model: "opus" }), { cmd: "newTab", kind: "agent", agent: "claude", model: "opus" });
  assert.deepEqual(newTabCommand({}), { cmd: "newTab", kind: "agent" });
  assert.deepEqual(newTabCommand({ kind: "shell", path: "/wt/x", title: "x" }), { cmd: "newTab", kind: "shell", text: "x", path: "/wt/x" });
  const c = startAgentCommand({ root: "/r", agent: "claude", model: "", task: "do it", role: "worker" });
  assert.deepEqual(c, { cmd: "startAgent", root: "/r", task: "do it", worktree: false, agent: "claude", role: "worker" });
  assert.equal(startAgentCommand({ root: "/r", task: "do it", worktree: true }).worktree, true);
  assert.equal(startAgentCommand({ root: "/r", task: "do it", worktree: "yes" }).worktree, false);
});

test("a start that cannot go says why", () => {
  assert.match(startProblem({ mode: "team", task: " ", root: "/r", picked: true }), /goal/);
  assert.match(startProblem({ mode: "team", task: "x", root: "", picked: true }), /no project/);
  assert.match(startProblem({ mode: "team", task: "x", root: "/r", picked: false }), /no agent/);
  assert.match(startProblem({ mode: "agent", task: "", role: "worker", root: "/r", picked: true }), /needs a task/);
  assert.match(startProblem({ mode: "agent", task: "x", root: "", picked: true }), /no project/);
  assert.equal(startProblem({ mode: "agent", task: "", role: "", root: "", picked: true }), "");
});

test("closing warns about a request waiting, uncommitted files and a structured conversation", () => {
  const clean = { pane: "p", dirty: 0, untracked: 0, waitingOn: { kind: "none" } };
  assert.deepEqual(closeWarnings(clean, null), []);
  assert.equal(closeWarnings({ ...clean, waitingOn: { kind: "user" } }, null).length, 1);
  assert.equal(closeWarnings(clean, { permission: {} }).length, 1);
  const dirty = closeWarnings({ ...clean, dirty: 2, untracked: 1 }, null);
  assert.match(dirty[0], /3 changed files/);
  assert.match(dirty[0], /stay on disk/);
  assert.match(closeWarnings({ ...clean, dirty: 1 }, null)[0], /1 changed file /);
  assert.match(closeWarnings({ ...clean, structured: true }, null)[0], /does not keep/);
  // Nothing an agent wrote is in a warning.
  const text = closeWarnings({ ...clean, name: "NAME-FROM-AGENT", task: { title: "TITLE-FROM-AGENT" }, dirty: 1 }, { name: "NAME-FROM-AGENT" }).join(" ");
  assert.doesNotMatch(text, /FROM-AGENT/);
});

test("closing warns about unread mail that closing drops", () => {
  const clean = { pane: "p", dirty: 0, untracked: 0, waitingOn: { kind: "none" } };
  assert.match(closeWarnings(clean, { unread: 3 }).join(" "), /3 unread messages for this agent will be dropped/);
  assert.match(closeWarnings(clean, { unread: 1 }).join(" "), /1 unread message for this agent/);
  assert.deepEqual(closeWarnings(clean, { unread: 0 }), []);
});

test("closing a tab warns over every pane in it, and counts, never names", () => {
  const tab = { id: "t", panes: ["a", "b", "c", "gone"] };
  const nodes = new Map([
    ["a", { pane: "a", status: "working", dirty: 2, untracked: 0, waitingOn: { kind: "none" }, name: "NAME-FROM-AGENT" }],
    ["b", { pane: "b", status: "waiting", structured: true, dirty: 0, untracked: 1, waitingOn: { kind: "user" } }],
    ["c", { pane: "c", status: "exited", dirty: 0, untracked: 0, waitingOn: { kind: "none" } }],
  ]);
  const panes = { a: { unread: 2 }, b: { unread: 1, permission: {} } };
  const words = closeTabWarnings(tab, nodes, panes);
  const text = words.join(" ");
  assert.match(text, /ends 2 running agents/);
  assert.match(text, /1 agent in it is waiting/);
  assert.match(text, /3 changed files/);
  assert.match(text, /stay on disk/);
  assert.match(text, /1 structured agent/);
  assert.match(text, /3 unread messages/);
  assert.doesNotMatch(text, /FROM-AGENT/);
  // Only shells and ended agents: nothing to ask.
  assert.deepEqual(closeTabWarnings({ panes: ["c"] }, nodes, {}), []);
  assert.deepEqual(closeTabWarnings({ panes: ["s"] }, new Map(), { s: { shell: true, status: "idle" } }), []);
  assert.deepEqual(closeTabWarnings({ panes: [] }, nodes, panes), []);
  assert.deepEqual(closeTabWarnings(null, null, null), []);
});

test("a tab lists the panes of its layout", () => {
  const tabs = tabsOf({ tabs: [{ id: "a", root: { id: "n", children: [{ id: "x", pane: "p1" }, { id: "y", children: [{ id: "z", pane: "p2" }] }] } }, { id: "b" }] });
  assert.deepEqual(tabs[0].panes, ["p1", "p2"]);
  assert.deepEqual(tabs[1].panes, []);
});

test("restarting a structured agent says the session starts fresh", () => {
  const structured = restartWarnings({ pane: "p", structured: true, status: "idle" }, null);
  assert.match(structured.join(" "), /new session/);
  assert.match(structured.join(" "), /earlier conversation stays/);
  assert.match(restartWarnings({ pane: "p", status: "working" }, null).join(" "), /stopped and started again/);
  assert.deepEqual(restartWarnings({ pane: "p", status: "exited" }, null), []);
  assert.deepEqual(restartWarnings({ pane: "p", shell: true, status: "idle" }, null), []);
  assert.equal(restartWarnings({ pane: "p", status: "waiting", waitingOn: { kind: "user" } }, null).length, 2);
});

test("tabs are read from the state push and a tab moves one place at a time", () => {
  const tabs = tabsOf({ activeTab: "b", tabs: [{ id: "a", title: "One" }, { id: "b", title: "Two", named: true, activity: "waiting" }, { id: "c", title: "Three" }, null, { title: "no id" }] });
  assert.deepEqual(tabs.map((t) => [t.id, t.active, t.named, t.activity]), [["a", false, false, ""], ["b", true, true, "waiting"], ["c", false, false, ""]]);
  assert.deepEqual(moveTabCommand(tabs, "a", -1), null);
  assert.deepEqual(moveTabCommand(tabs, "c", 1), null);
  assert.deepEqual(moveTabCommand(tabs, "b", -1), { cmd: "moveTab", id: "b", target: "a" });
  assert.deepEqual(moveTabCommand(tabs, "a", 1), { cmd: "moveTab", id: "a", target: "c" });
  assert.deepEqual(moveTabCommand(tabs, "b", 1), { cmd: "moveTab", id: "b", target: "" });
  assert.equal(moveTabCommand(tabs, "zz", 1), null);
  assert.deepEqual(tabsOf(null), []);
});

// ---------------------------------------------------------------- the page

const projects = [
  { root: "/work/a", name: "alpha", waiting: 0, working: 0 },
  { root: "/work/b", name: "beta", waiting: 0, working: 0 },
];
const MANAGER = "0c96cbe9";
const CART = "6fb40971";
const TAX = "366c9143";

const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
  { id: "newAgentTab", keys: "Ctrl+Shift+T", label: "New agent tab", section: "Tabs" },
  { id: "newAgentTabChoose", keys: "", label: "New agent tab (choose agent)…", section: "Tabs" },
  { id: "newShellTab", keys: "Ctrl+Shift+N", label: "New shell tab", section: "Tabs" },
  { id: "startTeam", keys: "", label: "Start a team — a manager agent that starts the rest", short: "Start a team", section: "Agents" },
  { id: "nextTab", keys: "Ctrl+Tab", label: "Next tab", section: "Tabs", noPalette: true },
  { id: "prevTab", keys: "Ctrl+Shift+Tab", label: "Previous tab", section: "Tabs", noPalette: true },
  { id: "moveTabLeft", keys: "", label: "Move tab left", section: "Tabs" },
  { id: "moveTabRight", keys: "", label: "Move tab right", section: "Tabs" },
  { id: "zoomPane", keys: "Ctrl+Shift+Z", label: "Zoom pane", section: "Panes" },
  { id: "restartPane", keys: "", label: "Restart pane", section: "Panes" },
  { id: "lockPane", keys: "", label: "Lock pane", section: "Panes" },
  { id: "closePane", keys: "Ctrl+Shift+W", label: "Close pane", section: "Panes" },
];

const graph = (change = {}) => {
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  g.nodes = g.nodes.map((n) => ({ ...n, ...(change[n.pane] || {}) }));
  return g;
};
const TABS = [{ id: "t1", title: "First" }, { id: "t2", title: "Second", named: true }, { id: "t3", title: "Third", activity: "waiting" }];
const stateOf = (g, extra = {}, over = {}) => ({
  type: "state", root: "/work/a", projects, agents: CATALOG, activeTab: "t2", tabs: TABS,
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", ...(extra[n.pane] || {}) }])),
  ...over,
});

async function mount({ g = graph(), extra = {}, over = {}, helloOver = {} } = {}) {
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
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: parts.includes("Alt") });
      await settle();
    },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    byText: (sel, re) => {
      const [outer, inner] = sel.split(" ");
      const scope = inner ? container.find(outer) : container;
      return scope ? scope.findAll(inner || outer).find((e) => re.test(e.textContent)) : undefined;
    },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, ...helloOver }));
      await settle();
      ws().frame(stateOf(g, extra, over));
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

const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const fill = async (m, sel, value) => {
  const el = m.container.find(sel);
  assert.ok(el, "no " + sel);
  el.value = value;
  el.dispatch("input");
  await settle();
};
const choose = async (m, sel, value) => {
  const el = m.container.find(sel);
  el.value = value;
  el.dispatch("change");
  await settle();
};

test("a new agent tab and a new shell tab are one press from the strip and from the keys", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /^New agent tab$/));
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /^New shell tab$/));
    await m.key("Ctrl+Shift+T");
    await m.key("Ctrl+Shift+N");
    assert.deepEqual(m.sent("newTab").map((c) => c.kind), ["agent", "shell", "agent", "shell"]);
    assert.equal(m.sent("newTab").every((c) => c.agent === undefined), true, "the plain new tab names no agent: the project's default is used");
  }));

test("the start dialog opens an agent tab on the agent chosen when there is no task", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    assert.ok(m.container.find(".dialog"), "no dialog");
    assert.equal(valueIn(m.container.find("#start-agent")), valueOf("claude", "opus"), "starts on the default");
    await choose(m, "#start-agent", valueOf("claude", "sonnet"));
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.deepEqual(m.sent("newTab"), [{ cmd: "newTab", kind: "agent", agent: "claude", model: "sonnet" }]);
    assert.equal(m.container.find(".dialog"), null, "the dialog closes");
    assert.equal(m.sent("startAgent").length, 0);
  }));

test("a task, or a role, starts the agent in the project itself through startAgent unless a worktree is asked for", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    await fill(m, "#start-task", "  write the tests  ");
    await choose(m, "#start-role", "worker");
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.deepEqual(m.sent("startAgent"), [{ cmd: "startAgent", root: "/work/a", task: "write the tests", worktree: false, agent: "claude", model: "opus", role: "worker" }]);
    assert.equal(m.sent("newTab").length, 0);
  }));

test("a role with no task is refused in the dialog and nothing is sent", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    await choose(m, "#start-role", "worker");
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.match(m.container.find(".dialog-note--error").textContent, /needs a task/);
    assert.deepEqual(m.ws().sent.filter((c) => c.cmd !== "refreshAgents"), []); // opening the dialog asks for the agent lists again
    assert.ok(m.container.find(".dialog"), "the dialog stays open");
  }));

test("the worktree option is drawn, says what it does, needs a task, and starts the agent in a fresh worktree", () =>
  withPage({}, async (m) => {
    await m.connect();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const box = m.container.find("#start-worktree");
    assert.ok(box, "no checkbox");
    assert.equal(!!box.disabled || box.attributes.disabled !== undefined, false);
    assert.ok(m.container.find(".dialog").textContent.includes(WORKTREE_WORDS));
    box.checked = true;
    box.dispatch("change");
    await settle();
    m.clear();
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.match(m.container.find(".dialog-note--error").textContent, /Write the task first/);
    assert.equal(m.ws().sent.length, 0, "no worktree without a task");
    await fill(m, "#start-task", "fix the login");
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.deepEqual(m.sent("startAgent"), [{ cmd: "startAgent", root: "/work/a", task: "fix the login", worktree: true, agent: "claude", model: "opus" }]);
    assert.equal(m.sent("newTab").length, 0);
  }));

test("an agent that is not installed cannot be picked, and its option says so", () =>
  withPage({}, async (m) => {
    await m.connect();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const groups = m.container.findAll("optgroup").map((g) => g.attributes.label);
    assert.ok(groups.some((l) => /Codex \(not installed, see npm i -g codex\)/.test(l)), JSON.stringify(groups));
    const disabled = m.container.findAll("option").filter((o) => o.disabled || o.attributes.disabled !== undefined);
    assert.equal(disabled.length, 1);
  }));

// The walk of Tab through the start dialog as the real selector finds it. The fake
// DOM matches the selector list itself, so a control dropped from the list in
// Start.js drops out of the walk here.
async function walk(m, from, shiftKey, steps) {
  const doc = m.dom.document;
  doc.activeElement === from || from.focus();
  const seen = [];
  for (let i = 0; i < steps; i++) {
    const ev = doc.activeElement.dispatch("keydown", { key: "Tab", shiftKey, ctrlKey: false, altKey: false });
    assert.ok(ev.defaultPrevented, "Tab was left to the browser");
    seen.push(doc.activeElement.attributes.id || doc.activeElement.textContent);
  }
  return seen;
}

test("Tab walks the start dialog's controls in order and wraps, Shift+Tab walks back", () =>
  withPage({}, async (m) => {
    await m.connect();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const dialog = m.container.find(".dialog");
    const agent = dialog.find("#start-agent");
    // Select, select, textarea, the worktree box, then the buttons, then round to the first.
    assert.deepEqual(await walk(m, agent, false, 7), ["start-role", "start-task", "start-worktree", "Start", "Cancel", "start-agent", "start-role"]);
    assert.deepEqual(await walk(m, dialog.find("#start-role"), true, 3), ["start-agent", "Cancel", "Start"]);
  }));

test("Escape closes the dialog, other keys are left alone, and nothing is sent", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const dialog = m.container.find(".dialog");
    const cancel = m.byText(".dialog-btn", /^Cancel$/);
    cancel.focus();
    for (const key of ["Enter", " ", "a"]) {
      const ev = cancel.dispatch("keydown", { key, shiftKey: false, ctrlKey: false, altKey: false });
      assert.ok(!ev.defaultPrevented, JSON.stringify(key) + " was taken");
      assert.equal(m.dom.document.activeElement, cancel);
    }
    dialog.dispatch("keydown", { key: "Escape", shiftKey: false, ctrlKey: false, altKey: false });
    await settle();
    assert.equal(m.container.find(".dialog"), null);
    assert.deepEqual(m.ws().sent.filter((c) => c.cmd !== "refreshAgents"), []); // opening the dialog asks for the agent lists again
  }));

test("the key table's team and choose actions open their dialogs from the palette", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+K");
    const palette = m.container.find(".palette");
    assert.ok(palette, "no palette");
    // Both are implemented now, so neither row says it is not in this window.
    const rows = m.container.findAll(".palette-row");
    for (const id of ["startTeam", "newAgentTabChoose", "restartPane", "lockPane", "moveTabLeft"]) {
      const row = rows.find((r) => r.attributes["data-action"] === id);
      assert.ok(row, id + " is not in the palette");
      assert.equal(/not in this window yet/.test(row.textContent), false, id);
    }
    await m.click(rows.find((r) => r.attributes["data-action"] === "startTeam"));
    assert.ok(m.container.find(".dialog"), "the team dialog did not open");
    assert.match(m.container.find("#start-title").textContent, /Start a team/);
    // No goal: refused.
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.match(m.container.find(".dialog-note--error").textContent, /Write the goal first/);
    assert.equal(m.sent("startAgent").length, 0);
    await fill(m, "#start-task", "ship the release");
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.deepEqual(m.sent("startAgent"), [{ cmd: "startAgent", root: "/work/a", task: "ship the release", worktree: false, agent: "claude", model: "opus", role: "manager" }]);
  }));

test("an agent the desktop started opens in the drawer once the graph has it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.push({ type: "agentStarted", paneId: CART, id: "x" });
    assert.equal(m.container.find(".drawer-name").textContent, graph().nodes.find((n) => n.pane === CART).name);
    // A pane the graph does not have yet waits for it.
    const g2 = graph();
    const extra = { ...g2.nodes[0], pane: "newpane", name: "fresh" };
    await m.push({ type: "agentStarted", paneId: "newpane", id: "y" });
    assert.equal(m.container.find(".drawer-name").textContent, graph().nodes.find((n) => n.pane === CART).name);
    await m.push({ ...g2, nodes: [...g2.nodes, extra] });
    assert.equal(m.container.find(".drawer-name").textContent, "fresh");
  }));

// -------------------------------------------------------------------- tabs

test("while the start dialog is open no key reaches an action under it", () =>
  withPage({ extra: { [TAX]: { permission: { tool: "Bash", options: [] } } } }, async (m) => {
    await m.connect();
    await open(m, TAX);
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    m.clear();
    await m.key("Ctrl+Shift+W");
    await m.key("Ctrl+Shift+K");
    await m.key("Ctrl+Shift+T");
    assert.equal(m.container.find(".confirm"), null, "no question opened behind the dialog");
    assert.equal(m.container.find(".palette"), null);
    assert.equal(m.ws().sent.length, 0);
    m.container.find(".dialog").dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    await m.key("Ctrl+Shift+K");
    assert.ok(m.container.find(".palette"), "the keys work again once it is closed");
  }));

// ------------------------------------------------------------- pane lifecycle

const open = async (m, pane) => {
  await m.click(m.box(pane));
  assert.ok(m.container.find(".drawer-more"), "no More button");
};

test("restart of a running terminal agent asks first, and one that has ended restarts at once", () =>
  withPage({}, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click((await pickMore(m, ".life-restart")));
    assert.equal(m.sent("restartPane").length, 0);
    assert.match(m.container.find(".confirm").textContent, /stopped and started again/);
    await m.click(m.byText(".confirm .dialog-btn", /^Restart$/));
    assert.deepEqual(m.sent("restartPane"), [{ cmd: "restartPane", id: CART }]);
  }));

test("restart of an agent that has ended is sent at once", () =>
  withPage({ g: graph({ [CART]: { status: "exited" } }) }, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click((await pickMore(m, ".life-restart")));
    assert.deepEqual(m.sent("restartPane"), [{ cmd: "restartPane", id: CART }]);
    assert.equal(m.container.find(".confirm"), null);
  }));

test("restart of a structured agent asks, and says the session starts fresh", () =>
  withPage({ g: graph({ [CART]: { structured: true, kind: "structured" } }), extra: { [CART]: { structured: true } } }, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click((await pickMore(m, ".life-restart")));
    assert.equal(m.sent("restartPane").length, 0);
    const text = m.container.find(".confirm").textContent;
    assert.match(text, /new session/);
    assert.match(text, /earlier conversation stays/);
    await m.click(m.byText(".confirm .dialog-btn", /^Restart$/));
    assert.deepEqual(m.sent("restartPane"), [{ cmd: "restartPane", id: CART }]);
    assert.equal(m.container.find(".confirm"), null);
  }));

test("close pane asks when the agent holds a request, and Cancel sends nothing", () =>
  withPage({ extra: { [TAX]: { permission: { tool: "Bash", options: [] } } } }, async (m) => {
    await m.connect();
    await open(m, TAX);
    m.clear();
    await m.click((await pickMore(m, ".life-close")));
    assert.equal(m.sent("closePane").length, 0);
    assert.match(m.container.find(".confirm").textContent, /waiting for your answer/);
    await m.click(m.byText(".confirm .dialog-btn", /^Cancel$/));
    assert.equal(m.container.find(".confirm"), null);
    assert.equal(m.sent("closePane").length, 0);
    await m.click((await pickMore(m, ".life-close")));
    await m.click(m.byText(".confirm .dialog-btn", /^Close pane$/));
    assert.deepEqual(m.sent("closePane"), [{ cmd: "closePane", id: TAX }]);
  }));

test("close pane with uncommitted files says so and that they stay on disk", () =>
  withPage({ g: graph({ [CART]: { dirty: 4, untracked: 1 } }) }, async (m) => {
    await m.connect();
    await open(m, CART);
    await m.click((await pickMore(m, ".life-close")));
    const text = m.container.find(".confirm").textContent;
    assert.match(text, /5 changed files/);
    assert.match(text, /stay on disk/);
  }));

test("a clean agent is closed at once", () =>
  withPage({ g: graph({ [CART]: { dirty: 0, untracked: 0, waitingOn: { kind: "none", text: "", ref: "" } } }) }, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click((await pickMore(m, ".life-close")));
    assert.deepEqual(m.sent("closePane"), [{ cmd: "closePane", id: CART }]);
  }));

test("lock and unlock send lockPane, and a locked pane cannot be closed from the button", () =>
  withPage({}, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click((await pickMore(m, ".life-lock")));
    assert.deepEqual(m.sent("lockPane"), [{ cmd: "lockPane", id: CART, locked: true }]);
    await m.push(stateOf(graph(), { [CART]: { locked: true } }));
    assert.equal((await pickMore(m, ".life-lock")).textContent.trim(), "Unlock");
    const close = (await pickMore(m, ".life-close"));
    assert.equal(!!close.disabled || close.attributes.disabled !== undefined, true);
    assert.ok((await pickMore(m, ".life")).textContent.includes(LOCKED_WORDS));
    m.clear();
    await m.click((await pickMore(m, ".life-lock")));
    assert.deepEqual(m.sent("lockPane"), [{ cmd: "lockPane", id: CART, locked: false }]);
  }));

test("a question for one pane does not follow the drawer to another", () =>
  withPage({ extra: { [TAX]: { permission: { tool: "Bash", options: [] } } } }, async (m) => {
    await m.connect();
    await open(m, TAX);
    await m.click((await pickMore(m, ".life-close")));
    assert.ok(m.container.find(".confirm"));
    await m.click(m.box(CART));
    assert.equal(m.container.find(".confirm"), null);
    await m.click(m.box(TAX));
    assert.equal(m.container.find(".confirm"), null, "it was dropped when the pane changed");
  }));

test("the keys act on the agent in the drawer, and say so when none is open", () =>
  withPage({ extra: { [TAX]: { permission: { tool: "Bash", options: [] } } } }, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+W");
    assert.ok(m.container.find(".notice").textContent.includes(OPEN_AN_AGENT_WORDS));
    await m.key("Ctrl+Shift+Z");
    assert.equal(m.sent("closePane").length, 0);

    await open(m, TAX);
    assert.equal(m.container.find(".drawer-body--max"), null);
    await m.key("Ctrl+Shift+Z");
    assert.ok(m.container.find(".drawer-body--max"), "zoom maximises the drawer");
    await m.key("Ctrl+Shift+Z");
    assert.equal(m.container.find(".drawer-body--max"), null);

    m.clear();
    await m.key("Ctrl+Shift+W");
    assert.ok(m.container.find(".confirm"), "the close key asks first for a pane holding a request");
    assert.equal(m.sent("closePane").length, 0);
    m.container.find(".confirm").dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(m.container.find(".confirm"), null);
  }));

test("restart and lock run from the palette on the agent in the drawer", () =>
  withPage({}, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    for (const id of ["restartPane", "lockPane"]) {
      await m.key("Ctrl+Shift+K");
      await m.click(m.container.findAll(".palette-row").find((r) => r.attributes["data-action"] === id));
      if (id === "restartPane") await m.click(m.byText(".confirm .dialog-btn", /^Restart$/));
    }
    assert.deepEqual(m.sent("restartPane"), [{ cmd: "restartPane", id: CART }]);
    assert.deepEqual(m.sent("lockPane"), [{ cmd: "lockPane", id: CART, locked: true }]);
  }));

test("the drawer's own close is now called Hide, and still only closes the drawer", () =>
  withPage({}, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    const hide = m.container.find(".drawer-close");
    assert.equal(hide.textContent.trim(), "Hide");
    await m.click(hide);
    assert.equal(m.container.find(".drawer-body"), null);
    assert.equal(m.sent("closePane").length, 0);
  }));

test("a window with no catalog says no agent can be started and sends nothing", () =>
  withPage({ over: { agents: undefined } }, async (m) => {
    await m.connect();
    m.clear();
    await openPlus(m);
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    assert.match(m.container.find(".dialog").textContent, /has not sent the list of agents/);
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.deepEqual(m.ws().sent.filter((c) => c.cmd !== "refreshAgents"), []); // opening the dialog asks for the agent lists again
  }));

test("the team dialog says what it will start, with the helper cap, and a preset fills the goal without discarding what was typed", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+K");
    await m.click(m.container.findAll(".palette-row").find((r) => r.attributes["data-action"] === "startTeam"));
    const note = m.container.find("#start-explain").textContent;
    assert.match(note, /This will start: one agent, a Manager \(claude · opus\)/);
    assert.match(note, /at most 8 open at a time/);
    await choose(m, "#start-preset", "bug");
    assert.match(valueIn(m.container.find("#start-task")), /^Find and fix this bug/);
    await choose(m, "#start-preset", "docs");
    assert.match(valueIn(m.container.find("#start-task")), /^Bring the documentation/, "a bare brief is replaced by the next");
    await fill(m, "#start-task", "Bring the docs up to date for the new rail");
    await choose(m, "#start-preset", "feature");
    assert.equal(valueIn(m.container.find("#start-task")), "Bring the docs up to date for the new rail", "typed words are kept");
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.equal(m.sent("startAgent").length, 1);
    assert.equal(m.sent("startAgent")[0].role, "manager");
  }));

test("a solo agent's header offers Start a team, and an agent in a team does not", () => {
  const solo = renderToString(html`<${Header} node=${{ pane: "p", name: "solo", team: "" }} onClose=${() => {}} onToggleMax=${() => {}} onStartTeam=${() => {}} />`);
  assert.match(solo, /drawer-team[^>]*>Start a team</);
  const none = renderToString(html`<${Header} node=${{ pane: "p", name: "solo", team: "" }} onClose=${() => {}} onToggleMax=${() => {}} />`);
  assert.doesNotMatch(none, /Start a team/);
});
