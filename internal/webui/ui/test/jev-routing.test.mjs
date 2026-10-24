// L8, the controls that guard safety and security: a pane's auto-approval of
// read-only commands, TypeSafe's Jev (its key and the switch that sends terminal
// output to a third party), and model routing. The pure parts first, then the store,
// then the page on a fake control socket, with a window reached through the relay
// held to what the desktop allows it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import { jevClearCommand, jevGetCommand, jevOf, jevSetCommand, jevWords } from "../assets/lib/jev.js";
import { overriddenWords, policyOf, routingCommand, routingOf } from "../assets/lib/routing.js";
import { autoReviewCommand } from "../assets/drawer/Tools.js";
import { prefsOf, setting } from "../assets/lib/prefs.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

// The pane's actions are behind the drawer header's More button: open it when the
// button asked for is not there yet.
const pickMore = async (m, sel) => {
  if (!m.container.find(sel) && m.container.find(".drawer-more")) await m.click(m.container.find(".drawer-more"));
  return m.container.find(sel);
};

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const MARKUP = '<img src=x onerror="alert(1)">';

// ---------------------------------------------------------------- pure parts

test("jev: the key's state is read from a flag pair and said in words, and a key is trimmed", () => {
  assert.equal(jevOf(null), null);
  assert.deepEqual(jevOf({ set: true, env: "yes", key: "sk-secret" }), { set: true, env: false });
  assert.equal(jevWords(null, false), "Checking…");
  assert.equal(jevWords({ set: false, env: false }, false), "Not set.");
  assert.equal(jevWords({ set: true, env: false }, false), "Set.");
  assert.match(jevWords({ set: false, env: true }, false), /TYPESAFE_API_KEY from the environment is used/);
  assert.match(jevWords({ set: true, env: true }, false), /used in place of TYPESAFE_API_KEY/);
  assert.match(jevWords(null, true), /machine Flockdeck runs on/, "a relay window is not even told whether one is set");
  assert.deepEqual(jevGetCommand(), { cmd: "jevKey", kind: "get" });
  assert.deepEqual(jevSetCommand("  sk-1 "), { cmd: "jevKey", kind: "set", text: "sk-1" });
  assert.equal(jevSetCommand("  "), null);
  assert.equal(jevSetCommand(undefined), null);
  assert.deepEqual(jevClearCommand(), { cmd: "jevKey", kind: "clear" });
});

test("jev and auto review: the setting and the pane switch send what the desktop reads", () => {
  assert.equal(prefsOf({ jevStatus: true }).jevStatus, true);
  assert.equal(prefsOf({ jevStatus: "yes" }).jevStatus, false);
  assert.deepEqual(setting("jevStatus", true).command, { cmd: "jevStatus", kind: "on" });
  assert.deepEqual(setting("jevStatus", false).command, { cmd: "jevStatus", kind: "off" });
  assert.deepEqual(setting("jevStatus", true).patch, { jevStatus: true });
  assert.deepEqual(autoReviewCommand("p1", true), { cmd: "autoReview", id: "p1", autoReview: true });
  assert.deepEqual(autoReviewCommand("p1", 0), { cmd: "autoReview", id: "p1", autoReview: false });
});

test("routing: the policy is read defensively, and the project's own replaces every project's", () => {
  assert.equal(routingOf(null), null);
  assert.equal(routingOf({}), null);
  assert.equal(routingOf({ routing: "x" }), null);
  const r = routingOf({
    routing: {
      every: { mode: "auto", floor: "mid", strategy: "cost" },
      project: { mode: "weird", floor: "huge", strategy: 4 },
      rules: [{ name: "docs", choice: "small model", when: "the task is docs", kept: 3, overridden: 1 }, null, { name: 5 }, { name: "x", kept: -2, overridden: 2.5 }],
      crossAgent: true, builtIn: true, note: "n", config: "/home/u/agents.json",
    },
  });
  assert.deepEqual(r.every, { mode: "auto", floor: "mid", strategy: "cost" });
  assert.deepEqual(r.project, { mode: "off", floor: "", strategy: "" }, "values the desktop does not know read as the defaults");
  assert.equal(policyOf(r), r.project);
  assert.equal(policyOf({ ...r, project: null }), r.every);
  assert.deepEqual(r.rules.map((x) => x.name), ["docs", "x"]);
  assert.equal(overriddenWords(r.rules[0]), "overridden 25% of the time (1 of 4)");
  assert.equal(overriddenWords(r.rules[1]), "");
  assert.equal(r.crossAgent, true);
  assert.equal(routingOf({ routing: {} }).every.mode, "off");
});

test("routing: a change names its target, goes to every project or the active one, and a value the desktop would refuse is not sent", () => {
  assert.deepEqual(routingCommand("mode", "auto", true), { cmd: "setRouting", kind: "all", target: "mode", text: "auto" });
  assert.deepEqual(routingCommand("mode", "", false), { cmd: "setRouting", target: "mode", text: "" }, "same as every project");
  assert.equal(routingCommand("mode", "", true), null, "every project has nothing to be the same as");
  assert.deepEqual(routingCommand("floor", "mid", false), { cmd: "setRouting", target: "floor", text: "mid" });
  assert.deepEqual(routingCommand("strategy", "cost", true), { cmd: "setRouting", kind: "all", target: "strategy", text: "cost" });
  assert.deepEqual(routingCommand("crossAgent", "true", false), { cmd: "setRouting", target: "crossAgent", text: "true" });
  assert.equal(routingCommand("floor", "enormous", true), null);
  assert.equal(routingCommand("rules", "x", true), null);
  assert.equal(routingCommand("crossAgent", "yes", true), null);
});

// ---------------------------------------------------------------- the store

test("the store keeps the Jev answer, a relay window cannot set the key or turn Jev on, and can turn it off", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello({}));
  ws.frame({ type: "jevKey", set: true, env: false });
  ws.frame({ type: "jevKey", set: false, env: true });
  assert.deepEqual([store.getState().jevKey.set, store.getState().jevKey.seq], [false, 2]);
  for (const c of [{ cmd: "jevKey", kind: "get" }, { cmd: "jevKey", kind: "set", text: "k" }, { cmd: "jevKey", kind: "clear" }, { cmd: "jevStatus", kind: "on" }, { cmd: "jevStatus", kind: "off" }]) {
    assert.equal(store.send(c), true, c.cmd + " " + c.kind + " goes from the desk");
  }
  store.stop();
  const { store: rs } = await harness();
  rs.start();
  const rws = FakeWebSocket.last;
  rws.open();
  rws.frame(hello({ remote: true }));
  for (const c of [{ cmd: "jevKey", kind: "get" }, { cmd: "jevKey", kind: "set", text: "k" }, { cmd: "jevKey", kind: "clear" }, { cmd: "jevStatus", kind: "on" }]) {
    assert.equal(rs.send(c), false, c.cmd + " " + c.kind + " is not sent from a relay window");
  }
  assert.equal(rs.send({ cmd: "jevStatus", kind: "off" }), true, "switching it off is always allowed");
  rs.stop();
});

// ---------------------------------------------------------------- the page

const KEYS = [{ id: "settings", keys: "Ctrl+,", label: "Settings", section: "The window" }];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0, active: true }];
const ROUTING = {
  every: { mode: "auto", floor: "", strategy: "" },
  rules: [{ name: "docs", choice: "a small model", when: "the task is docs", kept: 3, overridden: 1 }],
  builtIn: true, config: "/home/u/agents.json",
};
const stateOf = (g, { agents = {}, panes = {}, extra = {} } = {}) => ({
  type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {}, ...agents }, activeTab: "", tabs: [],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", status: "working", ...(panes[n.pane] || {}) }])),
  ...extra,
});

async function mount({ prefs = {}, helloOver = {}, state = {} } = {}) {
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
    dom, store, container, ws, g,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    box: (pane) => container.findAll("[data-pane]").find((e) => e.localName === "g" && e.getAttribute("data-pane") === pane && e.getAttribute("role") === "button"),
    connect: async (over) => {
      ws().open();
      ws().frame(hello({ keys: KEYS, prefs, ...helloOver }));
      await settle();
      ws().frame(over || stateOf(g, state));
      await settle();
      if (!helloOver.remote) {
        ws().frame(g);
        await settle();
      }
    },
    openSettings: async (section) => {
      await m.click(container.find(".rail-settings"));
      assert.ok(container.find(".settings-panel"), "the dialog did not open");
      if (section) await m.click(container.findAll(".settings-tab").find((b) => b.attributes["data-section"] === section));
    },
    text: () => (container.find(".settings-panel") ? container.find(".settings-panel").textContent : ""),
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

const on = (el) => el.attributes["aria-checked"] === "true";
const isDisabled = (el) => !!el.disabled || el.attributes.disabled !== undefined;
const pick = async (m, id, value) => {
  const el = m.container.find(id);
  el.value = value;
  el.dispatch("change");
  await settle();
};

test("auto review: a pane's drawer shows its state, and the switch sends the opposite", () =>
  withPage({}, async (m) => {
    const [a, b] = m.g.nodes.filter((n) => !n.shell).slice(0, 2).map((n) => n.pane);
    await m.connect(stateOf(m.g, { panes: { [a]: { autoReview: true } } }));
    await m.click(m.box(a));
    const btn = (await pickMore(m, ".tools-review"));
    assert.equal(btn.attributes["aria-pressed"], "true");
    assert.match(btn.textContent, /Auto-approve read-only: on/);
    assert.match(btn.attributes.title, /Only a command Flockdeck is confident is read-only/);
    m.clear();
    await m.click(btn);
    assert.deepEqual(m.sent("autoReview"), [{ cmd: "autoReview", id: a, autoReview: false }]);
    await m.click(m.box(b));
    const other = (await pickMore(m, ".tools-review"));
    assert.equal(other.attributes["aria-pressed"], "false");
    m.clear();
    await m.click(other);
    assert.deepEqual(m.sent("autoReview"), [{ cmd: "autoReview", id: b, autoReview: true }]);
    // The desktop's state says it changed.
    await m.push(stateOf(m.g, { panes: { [b]: { autoReview: true } } }));
    assert.equal((await pickMore(m, ".tools-review")).attributes["aria-pressed"], "true");
  }));

test("auto review: a relay window may switch it (the desktop decides), and a shell has none", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    assert.equal(m.store.send(autoReviewCommand("p", true)), true);
  }));

test("Status detection: the switch sends jevStatus, says what is sent and to whom, and starts off", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("status");
    assert.deepEqual(m.sent("jevKey"), [{ cmd: "jevKey", kind: "get" }]);
    assert.match(m.text(), /SENT TO TYPESAFE, a third party/);
    assert.match(m.text(), /last 30 lines \(at most 2,000 bytes\)/);
    assert.match(m.text(), /Status: checking\./);
    assert.equal(on(m.container.find("#set-jev-status-switch")), false);
    await m.push({ type: "jevKey", set: true, env: false });
    assert.match(m.text(), /Status: set\./);
    m.clear();
    await m.click(m.container.find("#set-jev-status-switch"));
    assert.deepEqual(m.sent("jevStatus"), [{ cmd: "jevStatus", kind: "on" }]);
    assert.equal(on(m.container.find("#set-jev-status-switch")), true, "shown at once");
    await m.push({ type: "prefs", prefs: { jevStatus: true } });
    await m.click(m.container.find("#set-jev-status-switch"));
    assert.deepEqual(m.sent("jevStatus").pop(), { cmd: "jevStatus", kind: "off" });
  }));

test("Status detection: a relay window can see it is off, cannot turn it on, and can turn it off", async () => {
  await withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.openSettings("status");
    assert.equal(isDisabled(m.container.find("#set-jev-status-switch")), true);
    assert.match(m.text(), /can only be turned on on the machine Flockdeck runs on/);
    assert.match(m.text(), /Status: set on the machine Flockdeck runs on\./);
    assert.deepEqual(m.sent("jevKey"), [], "not even asked whether one is set");
  });
  await withPage({ helloOver: { remote: true }, prefs: { jevStatus: true } }, async (m) => {
    await m.connect();
    await m.openSettings("status");
    const sw = m.container.find("#set-jev-status-switch");
    assert.equal(isDisabled(sw), false, "on already, so it can be turned off");
    m.clear();
    await m.click(sw);
    assert.deepEqual(m.sent("jevStatus"), [{ cmd: "jevStatus", kind: "off" }]);
  });
});

test("the TypeSafe key: typed in a password field, sent trimmed, emptied at once, replaced and cleared", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    await m.push({ type: "keys", items: [] });
    await m.push({ type: "jevKey", set: false, env: true });
    assert.match(m.container.find("#set-jev-key-status").textContent, /TYPESAFE_API_KEY from the environment is used/);
    const field = m.container.find("#set-jev-key-field");
    assert.equal(field.attributes.type, "password");
    assert.equal(m.container.find("#set-jev-key-clear"), null, "nothing to clear");
    m.clear();
    field.value = "  ts-secret  ";
    field.keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("jevKey"), [{ cmd: "jevKey", kind: "set", text: "ts-secret" }]);
    assert.equal(field.value, "");
    assert.equal(m.text().includes("ts-secret"), false);
    await m.push({ type: "jevKey", set: true, env: false });
    assert.equal(m.container.find("#set-jev-key-save").textContent, "Replace");
    m.clear();
    await m.click(m.container.find("#set-jev-key-clear"));
    assert.deepEqual(m.sent("jevKey"), [{ cmd: "jevKey", kind: "clear" }]);
    // An empty field sends nothing.
    await m.click(m.container.find("#set-jev-key-save"));
    assert.deepEqual(m.sent("jevKey").filter((c) => c.kind === "set"), []);
  }));

test("the TypeSafe key: a relay window has no field and sends nothing", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    await m.openSettings("keys");
    assert.equal(m.container.find("#set-jev-key-field"), null);
    assert.match(m.container.find("#set-jev-key-status").textContent, /Set on the machine Flockdeck runs on/);
    assert.equal(m.store.send({ cmd: "jevKey", kind: "set", text: "k" }), false);
    assert.deepEqual(m.sent("jevKey"), []);
  }));

test("Routing: every project's policy is shown and each control sends its change to every project", () =>
  withPage({ state: { agents: { routing: ROUTING } } }, async (m) => {
    await m.connect();
    await m.openSettings("routing");
    assert.equal(m.container.find("#set-route-all-list").value ?? m.container.find("#set-route-all-list").attributes.value, "auto");
    assert.match(m.container.find("#set-route-project-label").textContent, /This project, alpha/);
    assert.match(m.container.find("#set-route-project-about").textContent, /Routed as every project is/);
    assert.match(m.text(), /The built-in rules\. The first to match a task decides\. They are edited in agents\.json, at \/home\/u\/agents\.json/);
    assert.match(m.container.find(".route-rule").textContent, /docs.*a small model.*when the task is docs.*overridden 25% of the time \(1 of 4\)/);
    m.clear();
    await pick(m, "#set-route-all-list", "off");
    await pick(m, "#set-route-floor-list", "mid");
    await pick(m, "#set-route-strategy-list", "cost");
    const box = m.container.find("#set-route-cross-box");
    box.checked = true;
    box.dispatch("change");
    await settle();
    assert.deepEqual(m.sent("setRouting"), [
      { cmd: "setRouting", kind: "all", target: "mode", text: "off" },
      { cmd: "setRouting", kind: "all", target: "floor", text: "mid" },
      { cmd: "setRouting", kind: "all", target: "strategy", text: "cost" },
      { cmd: "setRouting", kind: "all", target: "crossAgent", text: "true" },
    ]);
    m.clear();
    await m.click(m.container.find("#set-route-clear-log"));
    assert.deepEqual(m.sent("clearRoutingLog"), [{ cmd: "clearRoutingLog" }]);
  }));

test("Routing: a project with its own policy changes that one, and can go back to every project's", () =>
  withPage({ state: { agents: { routing: { ...ROUTING, project: { mode: "auto", floor: "mid", strategy: "" } } } } }, async (m) => {
    await m.connect();
    await m.openSettings("routing");
    assert.match(m.container.find("#set-route-project-about").textContent, /This project's own policy, which replaces/);
    assert.match(m.container.find("#set-route-floor-about").textContent, /for this project/);
    m.clear();
    await pick(m, "#set-route-floor-list", "top");
    await pick(m, "#set-route-project-list", "");
    assert.deepEqual(m.sent("setRouting"), [
      { cmd: "setRouting", target: "floor", text: "top" },
      { cmd: "setRouting", target: "mode", text: "" },
    ]);
  }));

test("Routing: while the desktop has sent none it says so, and a rule's words are text", async () => {
  await withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("routing");
    assert.match(m.text(), /Reading the agents/);
  });
  await withPage({ state: { agents: { err: "agents.json is damaged" } } }, async (m) => {
    await m.connect();
    await m.openSettings("routing");
    assert.match(m.text(), /agents\.json is damaged/);
  });
  await withPage({ state: { agents: { routing: { ...ROUTING, note: MARKUP, rules: [{ name: MARKUP, choice: MARKUP, when: MARKUP }] } } } }, async (m) => {
    await m.connect();
    await m.openSettings("routing");
    assert.equal(m.container.findAll("img").length, 0);
    assert.ok(m.text().includes(MARKUP));
  });
});
