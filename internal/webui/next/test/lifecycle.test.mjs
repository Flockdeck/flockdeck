// Starting agents and the life of a pane (L1): the start dialog, the tab strip,
// and restart, lock, close and zoom, through the real page on a fake control
// socket. The pure parts (the catalog, the commands, the warnings) are tested
// first, and the page after.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { OPEN_AN_AGENT_WORDS } from "../assets/shell/Live.js";
import { WORKTREE_WORDS } from "../assets/shell/Start.js";
import { OTHER_PROJECT_WORDS, CLOSE_WAITING_WORDS } from "../assets/shell/Tabs.js";
import { LOCKED_WORDS } from "../assets/drawer/Lifecycle.js";
import {
  catalogOf, closeWarnings, defaultValue, moveTabCommand, newTabCommand, optionsOf, restartWarnings, splitValue, startAgentCommand,
  startProblem, tabsOf, valueOf,
} from "../assets/lib/lifecycle.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");

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

test("the commands carry what was chosen, and a worktree is never asked for", () => {
  assert.deepEqual(newTabCommand({ kind: "shell", agent: "claude" }), { cmd: "newTab", kind: "shell" });
  assert.deepEqual(newTabCommand({ agent: "claude", model: "opus" }), { cmd: "newTab", kind: "agent", agent: "claude", model: "opus" });
  assert.deepEqual(newTabCommand({}), { cmd: "newTab", kind: "agent" });
  const c = startAgentCommand({ root: "/r", agent: "claude", model: "", task: "do it", role: "worker", worktree: true });
  assert.deepEqual(c, { cmd: "startAgent", root: "/r", task: "do it", worktree: false, agent: "claude", role: "worker" });
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
  assert.match(closeWarnings({ ...clean, structured: true }, null)[0], /not saved/);
  // Nothing an agent wrote is in a warning.
  const text = closeWarnings({ ...clean, name: "NAME-FROM-AGENT", task: { title: "TITLE-FROM-AGENT" }, dirty: 1 }, { name: "NAME-FROM-AGENT" }).join(" ");
  assert.doesNotMatch(text, /FROM-AGENT/);
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
    await m.click(m.byText(".tabs-btn", /^New agent tab$/));
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
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    assert.ok(m.container.find(".dialog"), "no dialog");
    assert.equal(valueIn(m.container.find("#start-agent")), valueOf("claude", "opus"), "starts on the default");
    await choose(m, "#start-agent", valueOf("claude", "sonnet"));
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.deepEqual(m.sent("newTab"), [{ cmd: "newTab", kind: "agent", agent: "claude", model: "sonnet" }]);
    assert.equal(m.container.find(".dialog"), null, "the dialog closes");
    assert.equal(m.sent("startAgent").length, 0);
  }));

test("a task, or a role, starts the agent in the project through startAgent, never in a worktree", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
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
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    await choose(m, "#start-role", "worker");
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.match(m.container.find(".dialog-note--error").textContent, /needs a task/);
    assert.equal(m.ws().sent.length, 0);
    assert.ok(m.container.find(".dialog"), "the dialog stays open");
  }));

test("the worktree option is drawn and switched off, with the reason", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const box = m.container.findAll("input").find((i) => i.attributes.type === "checkbox");
    assert.ok(box, "no checkbox");
    assert.equal(!!box.disabled || box.attributes.disabled !== undefined, true);
    assert.ok(m.container.find(".dialog").textContent.includes(WORKTREE_WORDS));
  }));

test("an agent that is not installed cannot be picked, and its option says so", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const groups = m.container.findAll("optgroup").map((g) => g.attributes.label);
    assert.ok(groups.some((l) => /Codex \(not installed, see npm i -g codex\)/.test(l)), JSON.stringify(groups));
    const disabled = m.container.findAll("option").filter((o) => o.disabled || o.attributes.disabled !== undefined);
    assert.equal(disabled.length, 1);
  }));

test("Escape closes the dialog, Tab stays inside it, and nothing is sent", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    const dialog = m.container.find(".dialog");
    const all = ["select", "textarea", "button"].flatMap((t) => dialog.findAll(t)).filter((e) => !(e.disabled || e.attributes.disabled !== undefined));
    dialog.querySelectorAll = () => all; // the fake DOM has no selector lists
    all[all.length - 1].focus();
    dialog.dispatch("keydown", { key: "Tab", shiftKey: false, ctrlKey: false, altKey: false });
    assert.equal(m.dom.document.activeElement, all[0], "Tab from the last control wraps to the first");
    dialog.dispatch("keydown", { key: "Escape", shiftKey: false, ctrlKey: false, altKey: false });
    await settle();
    assert.equal(m.container.find(".dialog"), null);
    assert.equal(m.ws().sent.length, 0);
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

test("the strip draws the desk's tabs as text and chooses one", () =>
  withPage({ over: { tabs: [{ id: "t1", title: "<img src=x onerror=alert(1)>" }, { id: "t2", title: "Second" }] } }, async (m) => {
    await m.connect();
    const items = m.container.findAll(".tab");
    assert.equal(items.length, 2);
    assert.equal(m.container.find(".tab-title").textContent, "<img src=x onerror=alert(1)>");
    assert.equal(m.container.findAll("img").length, 0);
    assert.equal(items.find((t) => t.attributes.class.includes("is-active")).attributes["data-tab"], "t2");
    m.clear();
    await m.click(items[0].find(".tab-pick"));
    assert.deepEqual(m.sent("selectTab"), [{ cmd: "selectTab", id: "t1" }]);
  }));

test("rename: Enter sends the title, Escape leaves it, empty asks for the automatic one", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.byText(".tabs-btn", /^Rename$/));
    assert.ok(m.container.find(".tab-rename"), "no field");
    assert.equal(valueIn(m.container.find(".tab-rename")), "Second");
    await fill(m, ".tab-rename", "  Release  ");
    m.container.find(".tab-rename").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("renameTab"), [{ cmd: "renameTab", id: "t2", text: "Release" }]);
    assert.equal(m.container.find(".tab-rename"), null);

    m.clear();
    await m.click(m.byText(".tabs-btn", /^Rename$/));
    await fill(m, ".tab-rename", "Never sent");
    m.container.find(".tab-rename").keydown("Escape");
    await settle();
    assert.equal(m.sent("renameTab").length, 0);

    // A double click on a tab opens the same field; an unchanged title sends nothing.
    await m.click(m.container.findAll(".tab-pick")[0]);
    m.container.findAll(".tab-pick")[0].dispatch("dblclick");
    await settle();
    m.clear();
    m.container.find(".tab-rename").keydown("Enter");
    await settle();
    assert.equal(m.sent("renameTab").length, 0, "an unchanged title is not a rename");

    m.container.findAll(".tab-pick")[0].dispatch("dblclick");
    await settle();
    await fill(m, ".tab-rename", "");
    m.container.find(".tab-rename").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("renameTab"), [{ cmd: "renameTab", id: "t1", text: "" }]);
  }));

test("move left and right send the tab it goes in front of, and are off at the ends", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.byText(".tabs-btn", /^Move left$/));
    await m.click(m.byText(".tabs-btn", /^Move right$/));
    assert.deepEqual(m.sent("moveTab"), [{ cmd: "moveTab", id: "t2", target: "t1" }, { cmd: "moveTab", id: "t2", target: "" }]);
    m.clear();
    await m.key("Ctrl+Shift+K");
    // The same through the actions a palette row runs.
    const rows = m.container.findAll(".palette-row");
    await m.click(rows.find((r) => r.attributes["data-action"] === "moveTabLeft"));
    assert.deepEqual(m.sent("moveTab"), [{ cmd: "moveTab", id: "t2", target: "t1" }]);
    // The first tab has nowhere to go left.
    await m.push(stateOf(graph(), {}, { activeTab: "t1" }));
    assert.equal(!!m.byText(".tabs-btn", /^Move left$/).disabled || m.byText(".tabs-btn", /^Move left$/).attributes.disabled !== undefined, true);
  }));

test("closing a tab sends closeTab, and asks first when it is waiting on someone", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.container.findAll(".tab-close")[0]);
    assert.deepEqual(m.sent("closeTab"), [{ cmd: "closeTab", id: "t1" }]);
    m.clear();
    await m.click(m.container.findAll(".tab-close")[2]);
    assert.equal(m.sent("closeTab").length, 0, "a waiting tab is not closed at once");
    assert.ok(m.container.find(".confirm").textContent.includes(CLOSE_WAITING_WORDS));
    await m.click(m.byText(".confirm .dialog-btn", /^Cancel$/));
    assert.equal(m.container.find(".confirm"), null);
    assert.equal(m.sent("closeTab").length, 0);
    await m.click(m.container.findAll(".tab-close")[2]);
    await m.click(m.byText(".confirm .dialog-btn", /^Close tab$/));
    assert.deepEqual(m.sent("closeTab"), [{ cmd: "closeTab", id: "t3" }]);
  }));

test("tabs belong to the desk's project: another project gets a line and no tabs", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.ok(m.container.find(".tab"));
    await m.click(m.container.findAll(".rail-item").find((b) => b.attributes["data-root"] === "/work/b"));
    assert.equal(m.container.find(".tab"), null);
    assert.ok(m.container.find(".tabs").textContent.includes(OTHER_PROJECT_WORDS));
  }));

test("next and previous tab, and a refusal from the desk is shown and can be dismissed", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Tab");
    await m.key("Ctrl+Shift+Tab");
    assert.equal(m.sent("nextTab").length, 1);
    assert.equal(m.sent("prevTab").length, 1);
    await m.push({ type: "notice", text: "A pane in this tab is locked, so the tab was not closed", error: true });
    assert.match(m.container.find(".notice").textContent, /is locked, so the tab was not closed/);
    await m.click(m.container.find(".notice-dismiss"));
    assert.equal(m.container.find(".notice"), null);
    await m.push({ type: "notice", text: "Another", error: false });
    assert.match(m.container.find(".notice").textContent, /Another/);
  }));

// ------------------------------------------------------------- pane lifecycle

const open = async (m, pane) => {
  await m.click(m.box(pane));
  assert.ok(m.container.find(".life"), "no lifecycle row");
};

test("restart of a running terminal agent asks first, and one that has ended restarts at once", () =>
  withPage({}, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click(m.container.find(".life-restart"));
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
    await m.click(m.container.find(".life-restart"));
    assert.deepEqual(m.sent("restartPane"), [{ cmd: "restartPane", id: CART }]);
    assert.equal(m.container.find(".confirm"), null);
  }));

test("restart of a structured agent asks, and says the session starts fresh", () =>
  withPage({ g: graph({ [CART]: { structured: true, kind: "structured" } }), extra: { [CART]: { structured: true } } }, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click(m.container.find(".life-restart"));
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
    await m.click(m.container.find(".life-close"));
    assert.equal(m.sent("closePane").length, 0);
    assert.match(m.container.find(".confirm").textContent, /waiting for your answer/);
    await m.click(m.byText(".confirm .dialog-btn", /^Cancel$/));
    assert.equal(m.container.find(".confirm"), null);
    assert.equal(m.sent("closePane").length, 0);
    await m.click(m.container.find(".life-close"));
    await m.click(m.byText(".confirm .dialog-btn", /^Close pane$/));
    assert.deepEqual(m.sent("closePane"), [{ cmd: "closePane", id: TAX }]);
  }));

test("close pane with uncommitted files says so and that they stay on disk", () =>
  withPage({ g: graph({ [CART]: { dirty: 4, untracked: 1 } }) }, async (m) => {
    await m.connect();
    await open(m, CART);
    await m.click(m.container.find(".life-close"));
    const text = m.container.find(".confirm").textContent;
    assert.match(text, /5 changed files/);
    assert.match(text, /stay on disk/);
  }));

test("a clean agent is closed at once", () =>
  withPage({ g: graph({ [CART]: { dirty: 0, untracked: 0, waitingOn: { kind: "none", text: "", ref: "" } } }) }, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click(m.container.find(".life-close"));
    assert.deepEqual(m.sent("closePane"), [{ cmd: "closePane", id: CART }]);
  }));

test("lock and unlock send lockPane, and a locked pane cannot be closed from the button", () =>
  withPage({}, async (m) => {
    await m.connect();
    await open(m, CART);
    m.clear();
    await m.click(m.container.find(".life-lock"));
    assert.deepEqual(m.sent("lockPane"), [{ cmd: "lockPane", id: CART, locked: true }]);
    await m.push(stateOf(graph(), { [CART]: { locked: true } }));
    assert.equal(m.container.find(".life-lock").textContent.trim(), "Unlock");
    const close = m.container.find(".life-close");
    assert.equal(!!close.disabled || close.attributes.disabled !== undefined, true);
    assert.ok(m.container.find(".life").textContent.includes(LOCKED_WORDS));
    m.clear();
    await m.click(m.container.find(".life-lock"));
    assert.deepEqual(m.sent("lockPane"), [{ cmd: "lockPane", id: CART, locked: false }]);
  }));

test("a question for one pane does not follow the drawer to another", () =>
  withPage({ extra: { [TAX]: { permission: { tool: "Bash", options: [] } } } }, async (m) => {
    await m.connect();
    await open(m, TAX);
    await m.click(m.container.find(".life-close"));
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
    await m.click(m.byText(".tabs-btn", /Choose agent/));
    assert.match(m.container.find(".dialog").textContent, /has not sent the list of agents/);
    await m.click(m.byText(".dialog-btn", /^Start$/));
    assert.equal(m.ws().sent.length, 0);
  }));
