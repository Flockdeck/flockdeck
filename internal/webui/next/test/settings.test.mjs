// Settings (L2): the preferences read defensively, put into effect on the page
// and the terminals, and changed from the dialog, through the real page on a fake
// control socket. The pure parts come first.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live } from "../assets/shell/Live.js";
import {
  ACCENTS, DEFAULT_FONT, FONT_DEFAULT, applyLook, fontStack, prefsOf, reducedMotion, addPending, latestOf, settledPending, setting, termOptionsOf, WHY,
} from "../assets/lib/prefs.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver } from "./termfakes.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");
const CART = "6fb40971";

// ---------------------------------------------------------------- pure parts

test("a corrupt or missing stored value reads as the default", () => {
  const base = prefsOf(undefined);
  assert.deepEqual(prefsOf(null), base);
  assert.deepEqual(prefsOf("text"), base);
  assert.equal(base.theme, "dark");
  assert.equal(base.accent, "");
  assert.equal(base.fontSize, FONT_DEFAULT);
  assert.equal(base.scrollback, 10000);
  assert.equal(base.cursorStyle, "block");
  assert.equal(base.fontFamily, "");
  const bad = prefsOf({
    theme: "neon", accentColor: "red", fontSize: 99, scrollback: 5, cursorStyle: "zigzag", fontFamily: "x".repeat(300),
    cursorSteady: "yes", screenReader: 1, autoReviewDefault: "true", dismissedTips: "no",
  });
  assert.deepEqual(bad, base);
  assert.equal(prefsOf({ fontSize: 13.5 }).fontSize, FONT_DEFAULT);
  assert.equal(prefsOf({ fontSize: 3 }).fontSize, FONT_DEFAULT, "below the smallest");
  assert.equal(prefsOf({ fontSize: 7 }).fontSize, FONT_DEFAULT);
  assert.equal(prefsOf({ fontSize: 8 }).fontSize, 8);
  assert.equal(prefsOf({ scrollback: 5000.5 }).scrollback, 10000, "not a whole number of lines");
  assert.equal(prefsOf({ scrollback: 200001 }).scrollback, 10000, "above the most");
  assert.equal(prefsOf({ scrollback: 999 }).scrollback, 10000);
  assert.equal(prefsOf({ scrollback: 200000 }).scrollback, 200000);
  assert.equal(prefsOf({ fontSize: "14" }).fontSize, FONT_DEFAULT);
  assert.equal(prefsOf({ accentColor: "teal" }).accent, "", "teal was the default's old name");
  assert.equal(prefsOf({ dismissedTips: ["a", 3, "b"] }).dismissedTips, 2);
});

test("a stored value in range is kept", () => {
  const p = prefsOf({
    theme: "system", accentColor: "pink", fontSize: 20, scrollback: 50000, cursorStyle: "underline", fontFamily: "  Fira Code  ",
    cursorSteady: true, screenReader: true, autoReviewDefault: true,
  });
  assert.deepEqual(p, {
    theme: "system", accent: "pink", fontSize: 20, fontFamily: "Fira Code", scrollback: 50000, cursorStyle: "underline",
    cursorSteady: true, screenReader: true, autoReviewDefault: true, dismissedTips: 0,
    notificationsOff: false, jevStatus: false, conflictRadar: false, push: { off: false, anonymous: false, delaySeconds: 30 },
    statusLine: "auto", railExpanded: false, railWidth: 220, helpSeen: false,
  });
  assert.equal(prefsOf({ theme: "light" }).theme, "light");
});

test("each setting round-trips: the command it sends and the value the desktop keeps read back the same", () => {
  const cases = [
    ["theme", "light", { cmd: "theme", text: "light" }, (p) => p.theme === "light"],
    ["theme", "dark", { cmd: "theme", text: "dark" }, (p) => p.theme === "dark"],
    ["theme", "system", { cmd: "theme", text: "system" }, (p) => p.theme === "system"],
    ["accent", "green", { cmd: "accentColor", text: "green" }, (p) => p.accent === "green"],
    ["accent", "", { cmd: "accentColor", text: "" }, (p) => p.accent === ""],
    ["fontSize", 17, { cmd: "fontSize", size: 17 }, (p) => p.fontSize === 17],
    ["fontFamily", "  Menlo ", { cmd: "fontFamily", text: "Menlo" }, (p) => p.fontFamily === "Menlo"],
    ["fontFamily", "", { cmd: "fontFamily", text: "" }, (p) => p.fontFamily === ""],
    ["scrollback", 25000, { cmd: "scrollback", size: 25000 }, (p) => p.scrollback === 25000],
    ["cursorStyle", "bar", { cmd: "cursorStyle", text: "bar" }, (p) => p.cursorStyle === "bar"],
    ["cursorStyle", "block", { cmd: "cursorStyle", text: "block" }, (p) => p.cursorStyle === "block"],
    ["cursorSteady", true, { cmd: "cursorBlink", kind: "off" }, (p) => p.cursorSteady === true],
    ["cursorSteady", false, { cmd: "cursorBlink", kind: "on" }, (p) => p.cursorSteady === false],
    ["screenReader", true, { cmd: "screenReader", kind: "on" }, (p) => p.screenReader === true],
    ["autoReviewDefault", true, { cmd: "autoReviewDefault", kind: "on" }, (p) => p.autoReviewDefault === true],
    ["statusLine", "on", { cmd: "statusLine", text: "on" }, (p) => p.statusLine === "on"],
    ["statusLine", "off", { cmd: "statusLine", text: "off" }, (p) => p.statusLine === "off"],
    ["statusLine", "auto", { cmd: "statusLine", text: "auto" }, (p) => p.statusLine === "auto"],
    ["railExpanded", true, { cmd: "railExpanded", kind: "on" }, (p) => p.railExpanded === true],
    ["railExpanded", false, { cmd: "railExpanded", kind: "off" }, (p) => p.railExpanded === false],
    ["railWidth", 300, { cmd: "railWidth", size: 300 }, (p) => p.railWidth === 300],
    ["helpSeen", true, { cmd: "helpSeen" }, (p) => p.helpSeen === true],
  ];
  for (const [name, value, command, ok] of cases) {
    const s = setting(name, value);
    assert.ok(s, name + " " + value);
    assert.deepEqual(s.command, command, name + " " + value);
    assert.ok(ok(prefsOf(s.patch)), name + " " + value + " did not read back");
  }
  assert.deepEqual(setting("resetTips", true).command, { cmd: "resetTips" });
  assert.equal(prefsOf({ dismissedTips: ["a"] , ...setting("resetTips", true).patch}).dismissedTips, 0);
});

test("a value the desktop would refuse is not sent", () => {
  for (const [name, value] of [
    ["theme", "neon"], ["accent", "red"], ["accent", "teal"], ["fontSize", 7], ["fontSize", 29], ["fontSize", 13.5], ["fontSize", "13"],
    ["fontFamily", "x".repeat(201)], ["fontFamily", 4], ["scrollback", 999], ["scrollback", 200001], ["scrollback", 1000.5],
    ["cursorStyle", "zigzag"], ["nothing", 1], ["statusLine", "sometimes"], ["railWidth", 179], ["railWidth", 481], ["railWidth", "300"],
  ]) assert.equal(setting(name, value), null, name + " " + value);
  assert.ok(setting("fontSize", 8) && setting("fontSize", 28) && setting("scrollback", 1000) && setting("scrollback", 200000));
  assert.ok(setting("fontFamily", "x".repeat(200)));
});

test("pending changes: kept in order, answered in order, and the latest is shown", () => {
  let p = addPending({}, { fontSize: 14 });
  p = addPending(p, { fontSize: 15, theme: "light" });
  p = addPending(p, { fontSize: 16 });
  assert.deepEqual(p, { fontSize: [14, 15, 16], theme: ["light"] });
  assert.deepEqual(latestOf(p), { fontSize: 16, theme: "light" });
  // A stale answer to the first command leaves the later ones waiting.
  const a = settledPending(p, { fontSize: 14 }, { fontSize: 13 });
  assert.deepEqual(a.fontSize, [15, 16]);
  assert.deepEqual(a.theme, ["light"], "the theme was unchanged by that push, so it still waits");
  const b = settledPending(a, { fontSize: 15 }, { fontSize: 14 });
  assert.deepEqual(b.fontSize, [16]);
  assert.deepEqual(settledPending(b, { fontSize: 16, theme: "light" }, { fontSize: 15 }), {});
  // An answer that skips ahead settles what it answers and the ones before it.
  assert.deepEqual(settledPending({ fontSize: [14, 15, 16] }, { fontSize: 15 }, { fontSize: 13 }), { fontSize: [16] });
  // A value sent twice: the first answer settles the first, not the whole list.
  assert.deepEqual(settledPending({ fontSize: [14, 15, 14] }, { fontSize: 14 }, { fontSize: 13 }), { fontSize: [15, 14] });
  // A value none of ours matches, moved from where it was, is another window's word.
  assert.deepEqual(settledPending({ fontSize: [14, 15] }, { fontSize: 20 }, { fontSize: 13 }), {});
  // A push that left it as it was is about something else.
  const wait = { theme: ["light"] };
  assert.equal(settledPending(wait, { fontSize: 14 }, {}), wait, "nothing settled keeps the same object");
  assert.deepEqual(settledPending({ theme: [""] }, {}, {}), {}, "dark is the empty value");
  assert.deepEqual(settledPending({ fontSize: [20] }, null, null), { fontSize: [20] });
});

test("the terminal's options come from the settings", () => {
  const p = prefsOf({ fontSize: 15, fontFamily: "Menlo", cursorStyle: "bar", scrollback: 5000, screenReader: true, cursorSteady: false });
  assert.deepEqual(termOptionsOf(p), {
    fontFamily: "Menlo, monospace", fontSize: 15, cursorStyle: "bar", cursorBlink: true, scrollback: 5000, screenReaderMode: true,
  });
  assert.equal(termOptionsOf(p, true).cursorBlink, false, "less motion stops the blinking");
  assert.equal(termOptionsOf({ ...p, cursorSteady: true }).cursorBlink, false);
  assert.equal(fontStack(""), DEFAULT_FONT);
  assert.equal(reducedMotion({ matchMedia: () => ({ matches: true }) }), true);
  assert.equal(reducedMotion({ matchMedia: () => { throw new Error("no"); } }), false);
  assert.equal(reducedMotion(null), false);
});

test("the palette and accent go on the document, and follow-system leaves no theme", () => {
  const dom = createDom();
  const win = { document: dom.document };
  applyLook(win, prefsOf({ theme: "light", accentColor: "blue" }));
  assert.equal(dom.document.documentElement.dataset.theme, "light");
  assert.equal(dom.document.documentElement.dataset.accent, "blue");
  applyLook(win, prefsOf({ theme: "system" }));
  assert.equal("theme" in dom.document.documentElement.dataset, false);
  assert.equal("accent" in dom.document.documentElement.dataset, false);
  applyLook(win, prefsOf({}));
  assert.equal(dom.document.documentElement.dataset.theme, "dark");
  applyLook(null, prefsOf({}));
  applyLook({ document: {} }, prefsOf({}));
});

test("the accents are the ones the stylesheet and the desktop know", () => {
  const css = readFileSync(join(here, "..", "assets", "app.css"), "utf8");
  for (const [value] of ACCENTS) if (value) assert.ok(css.includes('[data-accent="' + value + '"]'), value);
  assert.deepEqual(ACCENTS.map((a) => a[0]), ["", "blue", "purple", "green", "orange", "pink"]);
});

// ---------------------------------------------------------------- the page

const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }];
const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
  { id: "settings", keys: "Ctrl+,", label: "Settings", section: "The window" },
  { id: "fontUp", keys: "Ctrl+=", label: "Increase font size", section: "The window" },
  { id: "fontDown", keys: "Ctrl+-", label: "Decrease font size", section: "The window" },
  { id: "fontReset", keys: "Ctrl+0", label: "Reset font size", section: "The window" },
  { id: "newAgentTab", keys: "Ctrl+Shift+T", label: "New agent tab", section: "Tabs" },
];
const graph = () => ({ ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" });
const stateOf = (g) => ({
  type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {} }, activeTab: "", tabs: [],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured" }])),
});

async function mount(prefs = {}, liveProps = {}) {
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
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} ...${liveProps} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  const g = graph();
  const root = dom.document.documentElement;
  const m = {
    dom, container, ws, root,
    sent: (cmd) => ws().commands(cmd),
    clear: () => { ws().sent.length = 0; },
    push: async (msg) => { ws().frame(msg); await settle(); },
    key: async (spec) => {
      const parts = spec.split("+");
      dom.window.dispatch("keydown", { key: parts[parts.length - 1], ctrlKey: parts.includes("Ctrl"), shiftKey: parts.includes("Shift"), altKey: false });
      await settle();
    },
    click: async (el) => { assert.ok(el, "no element to click"); el.click(); await settle(); },
    byText: (sel, re) => container.findAll(sel).find((e) => re.test(e.textContent)),
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, prefs }));
      await settle();
      ws().frame(stateOf(g));
      await settle();
      ws().frame(g);
      await settle();
    },
    openSettings: async (section) => {
      await m.click(container.find(".rail-settings"));
      assert.ok(container.find(".settings-panel"), "the dialog did not open");
      if (section) await m.click(container.findAll(".settings-tab").find((b) => b.attributes["data-section"] === section));
    },
    done: () => { render(null, container); store.stop(); restore(); },
  };
  return m;
}

async function withPage(prefs, fn, liveProps = {}) {
  const m = await mount(prefs, liveProps);
  try {
    await fn(m);
  } finally {
    m.done();
  }
}

const activeTab = (m) => m.container.findAll(".settings-tab").find((t) => t.attributes.class.includes("is-active"));
const on = (el) => el.attributes["aria-checked"] === "true";
const disabled = (el) => !!el.disabled || el.attributes.disabled !== undefined;
const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const choice = (m, group, value) => m.container.find("#" + group).findAll("button").find((b) => b.attributes["data-value"] === value);
const type = async (m, sel, value) => {
  const el = m.container.find(sel);
  el.value = value;
  el.dispatch("input");
  await settle();
  return el;
};

test("the stored palette and accent are on the page, and a push from the desktop changes them", () =>
  withPage({ theme: "light", accentColor: "blue" }, async (m) => {
    await m.connect();
    assert.equal(m.root.dataset.theme, "light");
    assert.equal(m.root.dataset.accent, "blue");
    await m.push({ type: "prefs", prefs: { theme: "system" } });
    assert.equal("theme" in m.root.dataset, false);
    assert.equal("accent" in m.root.dataset, false);
    await m.push({ type: "prefs", prefs: { theme: "neon", accentColor: "red" } });
    assert.equal(m.root.dataset.theme, "dark", "a value that is not a theme is the default");
    assert.equal("accent" in m.root.dataset, false);
  }));

test("Settings opens from the rail and from its key, and Escape closes it and gives the keyboard back", () =>
  withPage({}, async (m) => {
    await m.connect();
    assert.equal(m.container.find(".settings-panel"), null);
    const button = m.container.find(".rail-settings");
    button.focus();
    await m.click(button);
    assert.ok(m.container.find(".settings-panel"));
    assert.equal(activeTab(m).attributes["data-section"], "general");
    m.container.find(".dialog").dispatch("keydown", { key: "Escape", ctrlKey: false, shiftKey: false, altKey: false });
    await settle();
    assert.equal(m.container.find(".settings-panel"), null);
    assert.equal(m.dom.document.activeElement, button);
    await m.key("Ctrl+,");
    assert.ok(m.container.find(".settings-panel"), "its key opens it");
  }));

test("no key reaches an action while Settings is open", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings();
    m.clear();
    await m.key("Ctrl+Shift+T");
    await m.key("Ctrl+=");
    assert.equal(m.ws().sent.length, 0);
  }));

test("the sections are tabs the arrow keys move between", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings();
    const tabs = () => m.container.findAll(".settings-tab");
    assert.deepEqual(tabs().map((t) => t.attributes.tabindex), ["0", "-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1"]);
    const ev = tabs()[0].keydown("ArrowDown");
    await settle();
    assert.equal(ev.defaultPrevented, true);
    assert.equal(activeTab(m).attributes["data-section"], "appearance");
    assert.equal(m.dom.document.activeElement, tabs()[1]);
    tabs()[1].keydown("ArrowUp");
    await settle();
    assert.equal(activeTab(m).attributes["data-section"], "general");
    tabs()[0].keydown("ArrowUp");
    await settle();
    assert.equal(activeTab(m).attributes["data-section"], "keyboard", "wraps round");
  }));

test("theme: choosing sends the command, shows at once, and the desktop's push confirms", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("appearance");
    assert.equal(on(choice(m, "set-theme-choice", "dark")), true);
    m.clear();
    await m.click(choice(m, "set-theme-choice", "light"));
    assert.deepEqual(m.sent("theme"), [{ cmd: "theme", text: "light" }]);
    assert.equal(m.root.dataset.theme, "light", "applied before the desktop answers");
    assert.equal(on(choice(m, "set-theme-choice", "light")), true);
    await m.push({ type: "prefs", prefs: { theme: "light" } });
    assert.equal(m.root.dataset.theme, "light");
    await m.click(choice(m, "set-theme-choice", "system"));
    assert.equal("theme" in m.root.dataset, false);
    await m.click(choice(m, "set-theme-choice", "dark"));
    assert.deepEqual(m.sent("theme").map((c) => c.text), ["light", "system", "dark"]);
  }));

test("another window's unrelated push does not undo a change that is still waiting", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("appearance");
    await m.click(choice(m, "set-theme-choice", "light"));
    await m.push({ type: "prefs", prefs: { fontSize: 15 } });
    assert.equal(m.root.dataset.theme, "light", "the unrelated push did not flick it back");
    await m.push({ type: "prefs", prefs: { fontSize: 15, theme: "light" } });
    assert.equal(m.root.dataset.theme, "light");
    await m.push({ type: "prefs", prefs: { fontSize: 15, theme: "system" } });
    assert.equal("theme" in m.root.dataset, false, "once the desktop agreed, its word is the one shown");
  }));

test("a different value pushed for the setting just changed is the one shown", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("appearance");
    await m.click(choice(m, "set-theme-choice", "light"));
    assert.equal(m.root.dataset.theme, "light");
    await m.push({ type: "prefs", prefs: { theme: "system" } });
    assert.equal("theme" in m.root.dataset, false, "the desktop's word won");
    assert.equal(on(choice(m, "set-theme-choice", "system")), true);
  }));

test("two changes made before the desktop answers are both shown", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("appearance");
    await m.click(choice(m, "set-theme-choice", "light"));
    await m.click(choice(m, "set-accent-choice", "pink"));
    assert.equal(m.root.dataset.theme, "light", "the first is still shown");
    assert.equal(m.root.dataset.accent, "pink");
    await m.click(m.byText(".settings-tab", /^Terminal$/));
    await m.click(m.container.find("#set-font-up"));
    assert.equal(m.container.find("#set-font-size-value").textContent, "14 px");
    assert.equal(m.root.dataset.theme, "light");
    assert.equal(m.sent("theme").length + m.sent("accentColor").length + m.sent("fontSize").length, 3);
  }));

test("stale answers to quick changes do not make the setting flick back", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    const size = () => m.container.find("#set-font-size-value").textContent;
    for (let i = 0; i < 3; i++) await m.click(m.container.find("#set-font-up"));
    assert.deepEqual(m.sent("fontSize").map((c) => c.size), [14, 15, 16]);
    assert.equal(size(), "16 px");
    await m.push({ type: "prefs", prefs: { fontSize: 14 } });
    assert.equal(size(), "16 px", "the answer to the first change");
    await m.push({ type: "prefs", prefs: { fontSize: 15 } });
    assert.equal(size(), "16 px", "the answer to the second");
    await m.push({ type: "prefs", prefs: { fontSize: 16 } });
    assert.equal(size(), "16 px", "the last");
    await m.push({ type: "prefs", prefs: { fontSize: 22 } });
    assert.equal(size(), "22 px", "another window's value is shown");
  }));

test("a setting another window changed stays changed through later pushes about something else", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    const size = () => m.container.find("#set-font-size-value").textContent;
    await m.click(m.container.find("#set-font-up"));
    await m.push({ type: "prefs", prefs: { fontSize: 20 } });
    assert.equal(size(), "20 px", "the other window's word");
    await m.click(m.container.find("#set-font-down"));
    assert.equal(size(), "19 px");
    // The next push is about the theme; the size in it is as it was.
    await m.push({ type: "prefs", prefs: { fontSize: 20, theme: "light" } });
    assert.equal(size(), "19 px", "this window's change is still waiting");
    await m.push({ type: "prefs", prefs: { fontSize: 19, theme: "light" } });
    assert.equal(size(), "19 px");
  }));

test("a notice that is not a refusal does not end the wait", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    const size = () => m.container.find("#set-font-size-value").textContent;
    await m.click(m.container.find("#set-font-up"));
    await m.click(m.container.find("#set-font-up"));
    assert.equal(size(), "15 px");
    await m.push({ type: "notice", text: "Agent finished", error: false });
    assert.equal(size(), "15 px", "an unrelated notice did not step it back");
    await m.push({ type: "prefs", prefs: { fontSize: 14 } });
    assert.equal(size(), "15 px");
    await m.push({ type: "notice", text: "This window can only look", error: true });
    assert.equal(size(), "14 px", "a refusal does, back to what the desktop keeps");
  }));

test("a change nothing answers is dropped after the wait", () =>
  withPage({ fontSize: 15 }, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    await m.click(m.container.find("#set-font-up"));
    assert.equal(m.container.find("#set-font-size-value").textContent, "16 px");
    await new Promise((r) => setTimeout(r, 150));
    await settle();
    assert.equal(m.container.find("#set-font-size-value").textContent, "15 px", "back to what the desktop keeps");
  }, { patchWaitMs: 40 }));

test("a change the desktop refuses is taken back when its notice arrives", () =>
  withPage({ theme: "light" }, async (m) => {
    await m.connect();
    await m.openSettings("appearance");
    await m.click(choice(m, "set-theme-choice", "dark"));
    assert.equal(m.root.dataset.theme, "dark");
    await m.push({ type: "notice", text: "This window can only look", error: true });
    assert.equal(m.root.dataset.theme, "light");
    assert.equal(on(choice(m, "set-theme-choice", "light")), true);
  }));

test("theme and accent: arrow keys choose as they move, and a swatch sends its name", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("appearance");
    m.clear();
    const ev = choice(m, "set-theme-choice", "dark").keydown("ArrowRight");
    await settle();
    assert.equal(ev.defaultPrevented, true);
    assert.deepEqual(m.sent("theme"), [{ cmd: "theme", text: "light" }]);
    assert.equal(m.dom.document.activeElement, choice(m, "set-theme-choice", "light"));
    assert.deepEqual(m.container.find("#set-theme-choice").findAll("button").map((b) => b.attributes.tabindex), ["-1", "0", "-1"]);
    await m.click(choice(m, "set-accent-choice", "orange"));
    assert.deepEqual(m.sent("accentColor"), [{ cmd: "accentColor", text: "orange" }]);
    assert.equal(m.root.dataset.accent, "orange");
    await m.click(choice(m, "set-accent-choice", ""));
    assert.equal("accent" in m.root.dataset, false);
    const names = m.container.find("#set-accent-choice").findAll("button").map((b) => b.attributes["aria-label"]);
    assert.equal(names[0], "Cyan (default)", "a swatch is named for a screen reader");
  }));

test("font size: the buttons step by one inside the range, and reset goes back", () =>
  withPage({ fontSize: 27 }, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    const size = () => m.container.find("#set-font-size-value").textContent;
    assert.equal(size(), "27 px");
    m.clear();
    await m.click(m.container.find("#set-font-up"));
    assert.equal(size(), "28 px");
    assert.equal(disabled(m.container.find("#set-font-up")), true, "no larger than 28");
    await m.click(m.container.find("#set-font-down"));
    await m.click(m.container.find("#set-font-reset"));
    assert.deepEqual(m.sent("fontSize").map((c) => c.size), [28, 27, 13]);
    assert.equal(disabled(m.container.find("#set-font-reset")), true);
  }));

test("font size: its keys change it from anywhere and say so, and stop at the ends", () =>
  withPage({ fontSize: 8 }, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+-");
    assert.equal(m.sent("fontSize").length, 0, "no smaller than 8");
    await m.key("Ctrl+=");
    assert.deepEqual(m.sent("fontSize"), [{ cmd: "fontSize", size: 9 }]);
    assert.match(m.container.find(".notices").textContent, /Font size 9px/);
    await m.key("Ctrl+0");
    assert.equal(m.sent("fontSize")[1].size, 13);
  }));

test("font family: Enter sends it trimmed, too long is refused with a reason, empty goes back to the default", () =>
  withPage({ fontFamily: "Menlo" }, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    const field = () => m.container.find("#set-font-family-field");
    assert.equal(valueIn(field()), "Menlo");
    m.clear();
    await type(m, "#set-font-family-field", "  Fira Code ");
    field().keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("fontFamily"), [{ cmd: "fontFamily", text: "Fira Code" }]);
    await type(m, "#set-font-family-field", "x".repeat(201));
    field().keydown("Enter");
    await settle();
    assert.equal(m.sent("fontFamily").length, 1, "nothing more was sent");
    assert.equal(m.container.find(".set-error").textContent, WHY.fontFamily);
    assert.equal(field().attributes["aria-invalid"], "true");
    await type(m, "#set-font-family-field", "");
    field().dispatch("blur");
    await settle();
    assert.deepEqual(m.sent("fontFamily")[1], { cmd: "fontFamily", text: "" });
    assert.equal(m.container.find(".set-error"), null);
  }));

test("font family: a name with markup is text, in the preview too", () =>
  withPage({ fontFamily: "<img src=x onerror=alert(1)>" }, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    assert.equal(m.container.findAll("img").length, 0);
    assert.equal(valueIn(m.container.find("#set-font-family-field")), "<img src=x onerror=alert(1)>");
  }));

test("scrollback: the list and the number box send lines inside the range only", () =>
  withPage({ scrollback: 7777 }, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    const list = m.container.find("#set-scrollback-list");
    assert.ok(list.findAll("option").some((o) => o.attributes.value === "7777"), "a kept custom value is in the list");
    m.clear();
    list.value = "50000";
    list.dispatch("change");
    await settle();
    assert.deepEqual(m.sent("scrollback"), [{ cmd: "scrollback", size: 50000 }]);
    await type(m, "#set-scrollback-custom", "999");
    m.container.find("#set-scrollback-custom").keydown("Enter");
    await settle();
    assert.equal(m.sent("scrollback").length, 1);
    assert.equal(m.container.find(".set-error").textContent, WHY.scrollback);
    await type(m, "#set-scrollback-custom", "");
    m.container.find("#set-scrollback-custom").dispatch("blur");
    await settle();
    assert.equal(m.sent("scrollback").length, 1, "empty is not a number of lines");
    await type(m, "#set-scrollback-custom", "30000");
    m.container.find("#set-scrollback-custom").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("scrollback")[1], { cmd: "scrollback", size: 30000 });
    assert.equal(m.container.find(".set-error"), null);
  }));

test("cursor shape and the switches send their commands", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("terminal");
    m.clear();
    await m.click(choice(m, "set-cursor-choice", "underline"));
    await m.click(choice(m, "set-cursor-choice", "block"));
    assert.deepEqual(m.sent("cursorStyle").map((c) => c.text), ["underline", "block"]);
    await m.click(m.container.find("#set-cursor-steady"));
    assert.deepEqual(m.sent("cursorBlink"), [{ cmd: "cursorBlink", kind: "off" }]);
    assert.equal(on(m.container.find("#set-cursor-steady")), true);
    await m.click(m.container.find("#set-cursor-steady"));
    assert.deepEqual(m.sent("cursorBlink")[1], { cmd: "cursorBlink", kind: "on" });
    await m.click(m.container.find("#set-screen-reader-switch"));
    assert.deepEqual(m.sent("screenReader"), [{ cmd: "screenReader", kind: "on" }]);
    assert.equal(m.container.find("#set-screen-reader-switch").attributes.role, "switch");
  }));

test("General: auto-approve for new panes, and the hints can be shown again", () =>
  withPage({ dismissedTips: ["a", "b"] }, async (m) => {
    await m.connect();
    await m.openSettings("general");
    m.clear();
    await m.click(m.container.find("#set-auto-review-default"));
    assert.deepEqual(m.sent("autoReviewDefault"), [{ cmd: "autoReviewDefault", kind: "on" }]);
    assert.match(m.container.find("#set-hints-about").textContent, /2 have been sent away/);
    await m.click(m.container.find("#set-tips"));
    assert.deepEqual(m.sent("resetTips"), [{ cmd: "resetTips" }]);
    assert.equal(disabled(m.container.find("#set-tips")), true);
    assert.match(m.container.find("#set-hints-about").textContent, /None has been sent away/);
  }));

test("an open terminal takes the settings at once, and a new one starts with them", () =>
  withPage({ fontSize: 17, scrollback: 5000, cursorStyle: "bar" }, async (m) => {
    await m.connect();
    await m.click(m.container.findAll("[data-pane]").find((e) => e.localName === "g" && e.getAttribute("data-pane") === CART && e.getAttribute("role") === "button"));
    const term = FakeTerm.last;
    assert.ok(term, "no terminal");
    assert.equal(term.options.fontSize, 17);
    assert.equal(term.options.scrollback, 5000);
    assert.equal(term.options.cursorStyle, "bar");
    assert.equal(term.options.fontFamily, DEFAULT_FONT);
    assert.equal(term.options.cursorBlink, true);
    await m.push({ type: "prefs", prefs: { fontSize: 20, scrollback: 5000, cursorStyle: "underline", fontFamily: "Menlo", cursorSteady: true, screenReader: true } });
    assert.equal(term.options.fontSize, 20);
    assert.equal(term.options.cursorStyle, "underline");
    assert.equal(term.options.fontFamily, "Menlo, monospace");
    assert.equal(term.options.cursorBlink, false);
    assert.equal(term.options.screenReaderMode, true);
    assert.equal(FakeTerm.instances.filter((t) => !t.disposed).length, 1, "the terminal was not remade");
  }));

test("General links to GitHub Sponsors in a new tab and says it buys nothing", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.openSettings("general");
    const a = m.container.find("#set-sponsor-link");
    assert.equal(a.attributes.href, "https://github.com/sponsors/jmwri");
    assert.equal(a.attributes.target, "_blank");
    assert.match(a.attributes.rel, /noopener/);
    assert.match(m.container.find("#set-sponsor").textContent, /buys nothing/);
  }));
