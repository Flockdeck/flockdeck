// The leftovers of L2 and L3: Claude Code's usage limits, the rail's width and
// whether it is wide, the Keyboard section (recording, clearing and resetting a
// binding), and the help having been seen. The pure parts first, then the page on a
// fake socket.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Live } from "../src/shell/Live.js";
import { chordOf, isBareKey, recorded } from "../src/shell/keys.js";
import { anyChanged, resetAllCommand, resetCommand, rowsOf, setCommand } from "../src/lib/keybindings.js";
import { prefsOf } from "../src/lib/prefs.js";
import { addressCommand, answerOf, defaultCommand, groupsOf, modelsOf } from "../src/lib/agentsettings.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "internal", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");

// ---------------------------------------------------------------- pure parts

const press = (key, over = {}) => ({ key, ctrlKey: false, shiftKey: false, altKey: false, ...over });

test("a keydown is written as the table writes a binding", () => {
  assert.equal(chordOf(press("k", { ctrlKey: true, shiftKey: true })), "Ctrl+Shift+K");
  assert.equal(chordOf(press("ArrowLeft", { altKey: true })), "Alt+←");
  assert.equal(chordOf(press(" ", { ctrlKey: true })), "Ctrl+Space");
  assert.equal(chordOf(press("F5")), "F5");
  assert.equal(chordOf(press("+", { ctrlKey: true, shiftKey: true })), "Ctrl+=", "Ctrl++ and Ctrl+= are one gesture");
  assert.equal(chordOf(press("Shift", { shiftKey: true })), null, "a modifier alone is not a binding");
  assert.equal(chordOf(press("Control", { ctrlKey: true })), null);
});

test("a key that would still reach a terminal is not a binding", () => {
  assert.equal(isBareKey("K"), true);
  assert.equal(isBareKey("Shift+K"), true);
  assert.equal(isBareKey("Ctrl+K"), false);
  assert.equal(isBareKey("Alt+1"), false);
  assert.equal(isBareKey("F5"), false);
  assert.equal(isBareKey("Shift+F6"), false);
  assert.equal(isBareKey("Tab"), false);
  assert.equal(isBareKey("Esc"), false);
});

test("while a binding is recorded, a key is cancel, clear, wait or the chord", () => {
  assert.deepEqual(recorded(press("Escape")), { kind: "cancel" });
  assert.deepEqual(recorded(press("Tab")), { kind: "cancel", pass: true });
  assert.deepEqual(recorded(press("Backspace")), { kind: "clear" });
  assert.deepEqual(recorded(press("Delete")), { kind: "clear" });
  assert.deepEqual(recorded(press("Shift", { shiftKey: true })), { kind: "wait" });
  assert.deepEqual(recorded({ ...press("a"), isComposing: true }), { kind: "wait" });
  assert.deepEqual(recorded(press("y", { ctrlKey: true })), { kind: "chord", chord: "Ctrl+Y" });
});

test("the key table is rows by section, and the commands name the action", () => {
  const table = [
    { id: "a", keys: "Ctrl+A", label: "Alpha", section: "One" },
    { id: "b", keys: "", label: "Beta", section: "One", overridden: true },
    { id: "c", keys: "Alt+1 … Alt+9", label: "Gamma", section: "Two" },
    null,
  ];
  const sections = rowsOf(table);
  assert.deepEqual(sections.map((s) => [s.name, s.rows.map((r) => r.id)]), [["One", ["a", "b"]], ["Two", ["c"]]]);
  assert.equal(sections[1].rows[0].range, true);
  assert.equal(sections[0].rows[1].overridden, true);
  assert.equal(anyChanged(table), true);
  assert.equal(anyChanged([{ id: "a" }]), false);
  assert.deepEqual(setCommand("a", "Ctrl+Y"), { cmd: "setKeybinding", id: "a", text: "Ctrl+Y" });
  assert.deepEqual(resetCommand("a"), { cmd: "resetKeybinding", id: "a" });
  assert.deepEqual(resetAllCommand(), { cmd: "resetKeybindings" });
});

test("the rail, the status line and the help are read from the preferences", () => {
  const d = prefsOf({});
  assert.deepEqual([d.statusLine, d.railExpanded, d.railWidth, d.helpSeen], ["auto", false, 220, false]);
  const p = prefsOf({ spend: { statusLine: "on" }, railExpanded: true, railWidth: 300, helpSeen: true });
  assert.deepEqual([p.statusLine, p.railExpanded, p.railWidth, p.helpSeen], ["on", true, 300, true]);
  const bad = prefsOf({ spend: { statusLine: "maybe" }, railExpanded: "yes", railWidth: 9999, helpSeen: 1 });
  assert.deepEqual([bad.statusLine, bad.railExpanded, bad.railWidth, bad.helpSeen], ["auto", false, 220, false]);
  assert.equal(prefsOf({ spend: "x" }).statusLine, "auto");
});

// ---------------------------------------------------------------- the page

const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
  { id: "settings", keys: "Ctrl+,", label: "Settings", section: "Window" },
  { id: "toggleRail", keys: "Ctrl+B", label: "Toggle the rail", section: "Window" },
  { id: "help", keys: "F1", label: "Help", section: "Window" },
  { id: "selectTab", keys: "Alt+1 … Alt+9", label: "Select tab by number", section: "Tabs" },
  { id: "unbound", keys: "", label: "Something in the palette", section: "Tabs" },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0, active: true }];
const CATALOG = {
  items: [
    { id: "claude", name: "Claude Code", available: true, defaultModel: "sonnet", models: [{ id: "sonnet", name: "Sonnet" }, { id: "opus", name: "Opus" }] },
    { id: "local", name: "My model", available: true, defaultModel: "", addressable: true, address: "http://127.0.0.1:11434/v1" },
    { id: "codex", name: "Codex", available: false, install: "npm i -g codex", defaultModel: "" },
  ],
  roles: [], default: { agent: "claude", model: "sonnet" },
};
const stateOf = (g, agents = { items: [], roles: [], default: {} }) => ({
  type: "state", root: "/work/a", projects, agents, activeTab: "", tabs: [],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured" }])),
});

async function mount({ prefs = {}, remote = false, keys = KEYS, agents } = {}) {
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
    dom, container, ws,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    key: async (spec) => {
      const parts = spec.split("+");
      const name = parts[parts.length - 1];
      dom.window.dispatch("keydown", { key: name === "Comma" ? "," : name, ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: parts.includes("Alt") });
      await settle();
    },
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys, prefs, remote }));
      await settle();
      ws().frame(stateOf(g, agents));
      await settle();
      if (!remote) {
        ws().frame(g);
        await settle();
      }
    },
    openSettings: async (section) => {
      await m.click(container.find(".rail-settings"));
      assert.ok(container.find(".settings-panel"), "the dialog did not open");
      if (section) await m.click(container.findAll(".settings-tab").find((b) => b.attributes["data-section"] === section));
    },
    shell: () => container.find(".shell"),
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

const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const off = (el) => !!el.disabled || el.attributes.disabled !== undefined;
const collapsed = (m) => m.shell().attributes.class.includes("shell--rail-collapsed");
const prefsPush = (over) => ({ type: "prefs", prefs: over });

test("Claude Code's usage limits: the choice is sent and shown, and a relay window cannot change it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("general");
    const list = () => m.container.find("#set-status-line-list");
    assert.equal(valueIn(list()), "auto");
    m.clear();
    list().value = "on";
    list().dispatch("change");
    await settle();
    assert.deepEqual(m.sent("statusLine"), [{ cmd: "statusLine", text: "on" }]);
    assert.equal(valueIn(list()), "on", "shown at once");
    await m.push(prefsPush({ spend: { statusLine: "on" } }));
    assert.equal(valueIn(list()), "on");
    list().value = "auto";
    list().dispatch("change");
    await settle();
    assert.deepEqual(m.sent("statusLine").pop(), { cmd: "statusLine", text: "auto" });
  }));

test("Claude Code's usage limits: a relay window sees the choice and cannot change it", () =>
  withPage({ remote: true, prefs: { spend: { statusLine: "off" } } }, async (m) => {
    await m.connect();
    await m.openSettings("general");
    assert.equal(off(m.container.find("#set-status-line-list")), true);
  }));

test("the rail follows the desktop: narrow until it is made wide, from the key or the button, in every window", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.equal(collapsed(m), true, "the desktop's default is the icon rail");
    m.clear();
    await m.key("Ctrl+B");
    assert.deepEqual(m.sent("railExpanded"), [{ cmd: "railExpanded", kind: "on" }]);
    assert.equal(collapsed(m), false, "shown at once");
    await m.push(prefsPush({ railExpanded: true }));
    assert.equal(collapsed(m), false);
    m.clear();
    await m.click(m.container.find(".rail-toggle"));
    assert.deepEqual(m.sent("railExpanded"), [{ cmd: "railExpanded", kind: "off" }]);
    assert.equal(collapsed(m), true);
    await m.push(prefsPush({}));
    assert.equal(collapsed(m), true, "the desktop keeps it narrow");
    await m.push(prefsPush({ railExpanded: true }));
    assert.equal(collapsed(m), false, "another window made it wide");
  }));

test("the rail's width is a handle moved with the arrows, kept within the desktop's bounds, and drawn at once", () =>
  withPage({ prefs: { railExpanded: true, railWidth: 300 } }, async (m) => {
    await m.connect();
    const handle = () => m.container.find(".rail-resize");
    assert.equal(handle().attributes["aria-valuenow"], "300");
    assert.equal(handle().attributes.role, "separator");
    assert.match(m.shell().style.cssText, /--rail-w:300px/);
    m.clear();
    handle().keydown("ArrowRight");
    await settle();
    assert.deepEqual(m.sent("railWidth"), [{ cmd: "railWidth", size: 316 }]);
    assert.match(m.shell().style.cssText, /--rail-w:316px/);
    handle().keydown("ArrowLeft");
    await settle();
    assert.deepEqual(m.sent("railWidth").pop(), { cmd: "railWidth", size: 300 });
    handle().keydown("End");
    await settle();
    assert.deepEqual(m.sent("railWidth").pop(), { cmd: "railWidth", size: 480 });
    m.clear();
    handle().keydown("ArrowRight");
    await settle();
    assert.equal(m.sent("railWidth").length, 0, "already as wide as it goes");
    handle().keydown("Home");
    await settle();
    assert.deepEqual(m.sent("railWidth").pop(), { cmd: "railWidth", size: 180 });
    handle().keydown("a");
    await settle();
  }));

test("dragging the handle draws the width as it moves and keeps it when the pointer is let go", () =>
  withPage({ prefs: { railExpanded: true, railWidth: 300 } }, async (m) => {
    await m.connect();
    const handle = m.container.find(".rail-resize");
    m.clear();
    handle.dispatch("pointerdown", { button: 0, clientX: 300, pointerId: 1 });
    await settle();
    handle.dispatch("pointermove", { clientX: 340 });
    await settle();
    assert.match(m.shell().style.cssText, /--rail-w:340px/);
    assert.equal(m.sent("railWidth").length, 0, "nothing is kept while it moves");
    handle.dispatch("pointermove", { clientX: 900 });
    await settle();
    assert.match(m.shell().style.cssText, /--rail-w:480px/, "held to the widest");
    handle.dispatch("pointerup", {});
    await settle();
    assert.deepEqual(m.sent("railWidth"), [{ cmd: "railWidth", size: 480 }]);
  }));

test("a collapsed rail has no handle, and a relay window keeps its own rail", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.equal(m.container.find(".rail-resize"), null);
  }).then(() => withPage({ remote: true, prefs: { railExpanded: false } }, async (m) => {
    await m.connect();
    assert.equal(collapsed(m), false, "a relay window starts as it always did");
    m.clear();
    await m.key("Ctrl+B");
    assert.equal(m.sent("railExpanded").length, 0);
    assert.equal(collapsed(m), true);
    assert.equal(m.container.find(".rail-resize"), null);
  })));

const keyTable = (over = {}) => KEYS.map((k) => ({ ...k, ...(over[k.id] || {}) }));
const section = async (m) => m.openSettings("keyboard");
const chordBtn = (m, id) => m.container.find("#set-keybind-" + id);
const askBtn = (m, label) => m.container.findAll(".dialog-btn").find((b) => b.textContent === label);

test("Keyboard: every action is a row, a range is shown and not editable, Reset waits for a change", () =>
  withPage({}, async (m) => {
    await m.connect();
    await section(m);
    assert.equal(m.container.findAll(".keybind-row").length, KEYS.length);
    assert.equal(chordBtn(m, "palette").textContent, "Ctrl+Shift+K");
    assert.equal(chordBtn(m, "unbound").textContent, "No binding");
    assert.equal(chordBtn(m, "selectTab"), null, "a range has no button");
    assert.match(m.container.find('[data-action="selectTab"]').textContent, /Alt\+1 … Alt\+9/);
    assert.equal(off(m.container.find("#set-keybind-reset-palette")), true);
    assert.equal(off(m.container.find("#set-keybind-reset-all")), true);
  }));

test("Keyboard: recording sends the chord, takes the key from the shell, and the table that comes back is shown", () =>
  withPage({}, async (m) => {
    await m.connect();
    await section(m);
    await m.click(chordBtn(m, "unbound"));
    assert.equal(chordBtn(m, "unbound").textContent, "Press a key…");
    m.clear();
    await m.key("Ctrl+Shift+K");
    assert.equal(m.sent("setKeybinding").length, 0, "that is already the palette's");
    assert.match(m.container.find(".keybind-error").textContent, /Ctrl\+Shift\+K is already Command palette/);
    assert.equal(m.container.find(".palette"), null, "and it did not open the palette");
    await m.click(chordBtn(m, "unbound"));
    await m.key("Ctrl+Y");
    assert.deepEqual(m.sent("setKeybinding"), [{ cmd: "setKeybinding", id: "unbound", text: "Ctrl+Y" }]);
    assert.equal(chordBtn(m, "unbound").textContent, "No binding", "until the desktop sends the table");
    await m.push({ type: "keyTable", keys: keyTable({ unbound: { keys: "Ctrl+Y", overridden: true } }) });
    assert.equal(chordBtn(m, "unbound").textContent, "Ctrl+Y");
    assert.equal(off(m.container.find("#set-keybind-reset-unbound")), false);
    assert.equal(off(m.container.find("#set-keybind-reset-all")), false);
    m.clear();
    await m.click(m.container.find("#set-keybind-reset-unbound"));
    assert.deepEqual(m.sent("resetKeybinding"), [{ cmd: "resetKeybinding", id: "unbound" }]);
  }));

test("Keyboard: Escape leaves it alone and keeps the dialog, Backspace takes the binding away, a bare key is refused", () =>
  withPage({}, async (m) => {
    await m.connect();
    await section(m);
    m.clear();
    await m.click(chordBtn(m, "palette"));
    await m.key("Escape");
    assert.equal(chordBtn(m, "palette").textContent, "Ctrl+Shift+K");
    assert.ok(m.container.find(".settings-panel"), "Escape ended the recording, not the dialog");
    assert.equal(m.sent("setKeybinding").length, 0);
    await m.click(chordBtn(m, "palette"));
    await m.key("Backspace");
    assert.deepEqual(m.sent("setKeybinding"), [{ cmd: "setKeybinding", id: "palette", text: "" }]);
    await m.click(chordBtn(m, "palette"));
    m.clear();
    await m.key("q");
    assert.equal(m.sent("setKeybinding").length, 0);
    assert.match(m.container.find(".keybind-error").textContent, /Q would still reach the agent's terminal/);
    await m.click(chordBtn(m, "palette"));
    await m.key("F8");
    assert.deepEqual(m.sent("setKeybinding"), [{ cmd: "setKeybinding", id: "palette", text: "F8" }], "a function key is fine");
  }));

test("Keyboard: a modifier alone waits for the key, and the shell runs its keys again once recording ends", () =>
  withPage({}, async (m) => {
    await m.connect();
    await section(m);
    await m.click(chordBtn(m, "palette"));
    m.clear();
    await m.key("Shift");
    assert.equal(chordBtn(m, "palette").textContent, "Press a key…", "still waiting");
    await m.key("Escape");
    await m.click(askBtn(m, "Close"));
    assert.equal(m.container.find(".settings-panel"), null);
    await m.key("Ctrl+Shift+K");
    assert.ok(m.container.find(".palette") || m.container.find('[role="dialog"]'), "the palette key works again");
  }));

test("Keyboard: Reset every shortcut asks first", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.push({ type: "keyTable", keys: keyTable({ palette: { keys: "Ctrl+Y", overridden: true } }) });
    await section(m);
    m.clear();
    await m.click(m.container.find("#set-keybind-reset-all"));
    assert.equal(m.sent("resetKeybindings").length, 0);
    await m.click(askBtn(m, "Cancel"));
    assert.equal(m.sent("resetKeybindings").length, 0);
    await m.click(m.container.find("#set-keybind-reset-all"));
    await m.click(askBtn(m, "Reset every shortcut"));
    assert.deepEqual(m.sent("resetKeybindings"), [{ cmd: "resetKeybindings" }]);
  }));

test("the help: a person who has not seen it is pointed to it, and opening it tells the desktop once", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.match(m.notice(), /Press F1 for the help\./);
    m.clear();
    await m.key("F1");
    assert.deepEqual(m.sent("helpSeen"), [{ cmd: "helpSeen" }]);
    assert.equal(m.notice(), "", "the pointer goes once it is shown to be done");
    await m.key("F1");
    await m.key("F1");
    assert.equal(m.sent("helpSeen").length, 1);
  }));

test("the help: the pointer can be put away without opening it, and is not shown to a person who has seen it", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.container.find(".notice-dismiss"));
    assert.deepEqual(m.sent("helpSeen"), [{ cmd: "helpSeen" }]);
    assert.equal(m.notice(), "");
  }).then(() => withPage({ prefs: { helpSeen: true } }, async (m) => {
    await m.connect();
    assert.equal(m.notice(), "");
    m.clear();
    await m.key("F1");
    assert.equal(m.sent("helpSeen").length, 0);
  })).then(() => withPage({ remote: true }, async (m) => {
    await m.connect();
    assert.equal(m.notice(), "", "a relay window is not welcomed");
    m.clear();
    await m.key("F1");
    assert.equal(m.sent("helpSeen").length, 0);
  })));

// ---------------------------------------------------------------- agents and models

test("the catalog is read into choices, the two defaults and the agents that take an address", () => {
  const m = modelsOf({ ...CATALOG, project: { agent: "claude", model: "opus" } });
  assert.equal(m.ready, true);
  assert.equal(m.every, "claude\nsonnet");
  assert.equal(m.project, "claude\nopus");
  assert.deepEqual(m.addressable, [{ id: "local", name: "My model", address: "http://127.0.0.1:11434/v1" }]);
  assert.equal(modelsOf(CATALOG).project, "\n", "a project with no choice of its own");
  assert.equal(modelsOf(null).ready, false);
  assert.equal(modelsOf({ err: "no file" }).err, "no file");
  const groups = groupsOf(m.options);
  assert.deepEqual(groups.map((g) => g.name), ["Claude Code", "My model", "Codex (not installed, see npm i -g codex)"]);
  assert.equal(groups[2].items[0].disabled, true);
});

test("the default is sent for every project or for this one, and giving up a choice is a choice of nothing", () => {
  assert.deepEqual(defaultCommand("claude\nopus", true), { cmd: "setAgentDefault", agent: "claude", model: "opus", kind: "all" });
  assert.deepEqual(defaultCommand("claude\nopus", false), { cmd: "setAgentDefault", agent: "claude", model: "opus" });
  assert.deepEqual(defaultCommand("\n", false), { cmd: "setAgentDefault", agent: "", model: "" });
  assert.equal(defaultCommand("\n", true), null, "there must be an agent for every project");
});

test("an address is not sent when it is the one shown, and an answer is read defensively", () => {
  assert.deepEqual(addressCommand("local", "  http://x/v1 ", "http://y/v1"), { cmd: "setAgentAddress", id: "local", text: "http://x/v1" });
  assert.equal(addressCommand("local", " http://y/v1", "http://y/v1"), null);
  assert.deepEqual(addressCommand("local", "", "http://y/v1"), { cmd: "setAgentAddress", id: "local", text: "" });
  assert.deepEqual(answerOf({ id: "local", address: 4, error: "no" }), { id: "local", address: "", error: "no" });
  assert.equal(answerOf({ address: "x" }), null);
});

const pick = async (m, id, value) => {
  const el = m.container.find("#" + id);
  el.value = value;
  el.dispatch("change");
  await settle();
};
const typeInto = async (m, id, value) => {
  const el = m.container.find("#" + id);
  el.value = value;
  el.dispatch("input");
  await settle();
};

test("Agents & models: opening it reads the agents again, and the two choices are sent", () =>
  withPage({ agents: CATALOG }, async (m) => {
    await m.connect();
    m.clear();
    await m.openSettings("agents");
    assert.equal(m.sent("refreshAgents").length, 1);
    assert.equal(valueIn(m.container.find("#set-agent-all-list")), "claude\nsonnet");
    assert.equal(valueIn(m.container.find("#set-agent-project-list")), "\n");
    m.clear();
    await pick(m, "set-agent-all-list", "claude\nopus");
    assert.deepEqual(m.sent("setAgentDefault"), [{ cmd: "setAgentDefault", agent: "claude", model: "opus", kind: "all" }]);
    await pick(m, "set-agent-project-list", "local\n");
    assert.deepEqual(m.sent("setAgentDefault").pop(), { cmd: "setAgentDefault", agent: "local", model: "" });
    await pick(m, "set-agent-project-list", "\n");
    assert.deepEqual(m.sent("setAgentDefault").pop(), { cmd: "setAgentDefault", agent: "", model: "" });
  }));

test("Agents & models: an address is saved from its field, a refusal is shown under it, and an unchanged one sends nothing", () =>
  withPage({ agents: CATALOG }, async (m) => {
    await m.connect();
    await m.openSettings("agents");
    m.clear();
    await m.click(m.container.find("#set-address-save-local"));
    assert.equal(m.sent("setAgentAddress").length, 0, "nothing changed");
    await typeInto(m, "set-address-field-local", "http://192.168.1.5:8080/v1");
    await m.click(m.container.find("#set-address-save-local"));
    assert.deepEqual(m.sent("setAgentAddress"), [{ cmd: "setAgentAddress", id: "local", text: "http://192.168.1.5:8080/v1" }]);
    assert.equal(off(m.container.find("#set-address-field-local")), true, "waiting for the answer");
    await m.push({ type: "agentAddress", id: "local", address: "http://192.168.1.5:8080/v1", error: "that does not look like an address" });
    assert.match(m.container.find(".set-error").textContent, /does not look like an address/);
    assert.equal(off(m.container.find("#set-address-field-local")), false);
    assert.equal(valueIn(m.container.find("#set-address-field-local")), "http://192.168.1.5:8080/v1", "what was typed stays to be put right");
    await typeInto(m, "set-address-field-local", "http://192.168.1.5:8080/v2");
    m.container.find("#set-address-field-local").keydown("Enter");
    await settle();
    assert.equal(m.sent("setAgentAddress").length, 2);
    await m.push({ type: "agentAddress", id: "local", address: "http://192.168.1.5:8080/v2" });
    assert.equal(m.container.find(".set-error"), null);
  }));

test("Agents & models: a relay window reads the choices and sends no address", () =>
  withPage({ agents: CATALOG, remote: true }, async (m) => {
    await m.connect();
    await m.openSettings("agents");
    m.clear();
    assert.equal(off(m.container.find("#set-address-field-local")), true);
    assert.equal(off(m.container.find("#set-address-save-local")), true);
    assert.match(m.container.find("#set-address-local-about").textContent, /machine Flockdeck runs on/);
    m.container.find("#set-address-save-local").click();
    await settle();
    assert.equal(m.sent("setAgentAddress").length, 0);
    assert.equal(m.sent("refreshAgents").length, 0, "the agents are read at the desk");
  }));

test("Agents & models: a window with no catalog says so", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("agents");
    assert.match(m.container.find(".settings-panel").textContent, /Reading the agents/);
  }));
