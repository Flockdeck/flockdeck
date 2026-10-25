// The small fixes of T172 (nits from the Team, Projects, Start, Settings, Remote and Help
// read), each mounted on its own on the fake DOM.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Team } from "../src/shell/Team.js";
import { Settings, SECTIONS, VIEWER_HIDDEN } from "../src/shell/Settings.js";
import { Start } from "../src/shell/Start.js";
import { Projects } from "../src/shell/Projects.js";
import { Remote } from "../src/shell/Remote.js";
import { Help } from "../src/shell/Help.js";
import { cleanPath, isAbsolute } from "../src/lib/projects.js";
import { prefsOf, refusesChange, REFUSAL_MS } from "../src/lib/prefs.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeKeys } from "./util.mjs";

async function mount(view) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const draw = (v) => render(v, container);
  draw(view);
  await settle();
  return { dom, container, draw, done: () => { render(null, container); restore(); } };
}

// ---------------------------------------------------------------- Team

const NOW = Date.now();
const iso = (ms) => new Date(ms).toISOString();
const teamMsg = (over = {}) => ({
  type: "team", seq: 1, root: "/work/a", project: "alpha", hasManager: true,
  roster: [{ pane: "p1", name: "boss", role: "manager", status: "idle" }],
  counts: { panes: 1, live: 1, tasks: {}, open: 0, messages: 1, unread: 0, unseen: 0 },
  tasks: [],
  messages: [{ id: 7, from: { name: "api", role: "worker", pane: "p2" }, to: "p1", toName: "boss", toRole: "manager", time: iso(NOW - 1000), text: "Begin", cut: true }],
  ...over,
});
const teamView = (sent, over = {}) => html`<${Team} ...${{ team: teamMsg(), tasks: null, messages: null, result: null, gone: "", root: "/work/a", send: (c) => { sent.push(c); return true; }, onClose: () => {}, ...over }} />`;
const activeTab = (m) => m.container.findAll(".team-tab").find((t) => t.attributes.class.includes("is-active")).attributes.id;

test("Team: the tabs take arrow keys, Home and End, with one tab stop and a tabpanel named by the tab", async () => {
  const m = await mount(teamView([]));
  try {
    const tabs = m.container.findAll(".team-tab");
    assert.deepEqual(tabs.map((t) => t.getAttribute("tabindex")), ["0", "-1", "-1"]);
    assert.ok(tabs.every((t) => t.getAttribute("aria-controls") === "team-panel"));
    assert.equal(m.container.find("#team-panel").getAttribute("aria-labelledby"), "team-tab-tasks");
    tabs[0].keydown("ArrowRight");
    await settle();
    assert.equal(activeTab(m), "team-tab-messages");
    assert.equal(m.container.find("#team-panel").getAttribute("aria-labelledby"), "team-tab-messages");
    m.container.find("#team-tab-messages").keydown("End");
    await settle();
    assert.equal(activeTab(m), "team-tab-agents");
    m.container.find("#team-tab-agents").keydown("ArrowRight");
    await settle();
    assert.equal(activeTab(m), "team-tab-tasks", "the arrows wrap");
    m.container.find("#team-tab-tasks").keydown("ArrowLeft");
    await settle();
    assert.equal(activeTab(m), "team-tab-agents");
    m.container.find("#team-tab-agents").keydown("Home");
    await settle();
    assert.equal(activeTab(m), "team-tab-tasks");
  } finally {
    m.done();
  }
});

test("Team: Show all is judged by the message it asked for, and a cut or empty answer is said", async () => {
  const sent = [];
  const m = await mount(teamView(sent));
  try {
    m.container.find("#team-tab-messages").click();
    await settle();
    m.container.find(".team-whole").click();
    await settle();
    assert.deepEqual(sent.filter((c) => c.cmd === "msgList").map((c) => c.msgId), [7]);
    // A push that does not carry the message is not its answer.
    m.draw(teamView(sent, { messages: { type: "messages", seq: 2, root: "/work/a", messages: [{ id: 9, from: { name: "x" }, to: "p1", time: iso(NOW), text: "Other" }] } }));
    await settle();
    assert.equal(m.container.find("#team-problem"), null);
    assert.ok(m.container.find(".team-whole"), "still waiting for its answer");
    // The message again, still cut: said.
    m.draw(teamView(sent, { messages: { type: "messages", seq: 3, root: "/work/a", messages: [{ id: 7, from: { name: "api" }, to: "p1", time: iso(NOW), text: "Begin", cut: true }] } }));
    await settle();
    assert.match(m.container.find("#team-problem").textContent, /whole message could not be shown/);
    // Asked again, answered in full.
    m.container.find(".team-whole").click();
    await settle();
    m.draw(teamView(sent, { messages: { type: "messages", seq: 4, root: "/work/a", messages: [{ id: 7, from: { name: "api" }, to: "p1", time: iso(NOW), text: "Begin and the rest" }] } }));
    await settle();
    assert.ok(m.container.find("#team-messages").textContent.includes("Begin and the rest"));
    assert.equal(m.container.find(".team-whole"), null);
  } finally {
    m.done();
  }
});

// ---------------------------------------------------------------- Settings

test("Settings: Home and End go to the first and last section", async () => {
  const m = await mount(html`<${Settings} prefs=${prefsOf({})} change=${() => true} onClose=${() => {}} ctx=${{}} />`);
  try {
    const active = () => m.container.findAll(".settings-tab").find((t) => t.attributes.class.includes("is-active")).getAttribute("data-section");
    assert.equal(active(), SECTIONS[0][0]);
    m.container.find("#settings-tab-" + SECTIONS[0][0]).keydown("End");
    await settle();
    assert.equal(active(), SECTIONS[SECTIONS.length - 1][0]);
    m.container.find("#settings-tab-" + SECTIONS[SECTIONS.length - 1][0]).keydown("Home");
    await settle();
    assert.equal(active(), SECTIONS[0][0]);
  } finally {
    m.done();
  }
});

test("Settings: a font typed once is sent once however many of Enter, change and blur fire, and again after a refusal", async () => {
  const calls = [];
  let accept = true;
  const change = (name, value) => { calls.push([name, value]); return accept; };
  const m = await mount(html`<${Settings} prefs=${prefsOf({})} change=${change} onClose=${() => {}} section="terminal" ctx=${{}} />`);
  try {
    const field = () => m.container.find("#set-font-family-field");
    field().enter("Fira Code");
    await settle();
    field().keydown("Enter");
    field().dispatch("change");
    field().dispatch("blur");
    await settle();
    assert.deepEqual(calls.filter((c) => c[0] === "fontFamily"), [["fontFamily", "Fira Code"]]);
    accept = false;
    calls.length = 0;
    field().enter("Nope Mono");
    await settle();
    field().keydown("Enter");
    await settle();
    field().keydown("Enter");
    await settle();
    assert.equal(calls.filter((c) => c[0] === "fontFamily").length, 2, "a refused edit is tried again, not held back");
  } finally {
    m.done();
  }
});

test("Settings: for a viewer End goes to the last section it has, not one that is hidden", async () => {
  const visible = SECTIONS.filter(([id]) => !VIEWER_HIDDEN.includes(id));
  const m = await mount(html`<${Settings} prefs=${prefsOf({})} change=${() => true} onClose=${() => {}} ctx=${{ viewer: true }} />`);
  try {
    const active = () => m.container.findAll(".settings-tab").find((t) => t.attributes.class.includes("is-active")).getAttribute("data-section");
    m.container.find("#settings-tab-" + visible[0][0]).keydown("End");
    await settle();
    assert.equal(active(), visible[visible.length - 1][0]);
    assert.ok(!VIEWER_HIDDEN.includes(active()));
  } finally {
    m.done();
  }
});

test("the message box loads: Message.js has no syntax error and exports the box", async () => {
  const mod = await import("../src/drawer/Message.js");
  assert.ok(Object.keys(mod).length > 0, "it exports something");
});

// ---------------------------------------------------------------- Projects

test("cleanPath takes the quotes and spaces off a pasted path", () => {
  assert.equal(cleanPath('  "C:\\Users\\me\\my project"  '), "C:\\Users\\me\\my project");
  assert.equal(cleanPath("/home/me/x"), "/home/me/x");
  assert.equal(cleanPath(null), "");
  assert.equal(isAbsolute(' "C:\\x" '), true);
  assert.equal(isAbsolute("relative/path"), false);
});

test("Projects: a quoted, padded path is opened as the cleaned path", async () => {
  const sent = [];
  const m = await mount(html`<${Projects} open=${[]} recents=${null} browse=${null} root="C:\\work" send=${(c) => { sent.push(c); return true; }} onClose=${() => {}} />`);
  try {
    m.container.find("#browse-path").enter('  "C:\\Users\\me\\my project" ');
    await settle();
    m.container.find("#browse-open").click();
    await settle();
    const open = sent.filter((c) => c.cmd === "openProject");
    assert.deepEqual(open.map((c) => c.path), ["C:\\Users\\me\\my project"]);
  } finally {
    m.done();
  }
});

// ---------------------------------------------------------------- Start

const CATALOG = { items: [{ id: "claude", name: "Claude Code" }], roles: [], presets: [] };

test("Start: a click outside closes an empty dialog and leaves one with a goal in it", async () => {
  let closed = 0;
  const m = await mount(html`<${Start} mode="agent" catalog=${CATALOG} project=${null} send=${() => true} onClose=${() => { closed++; }} />`);
  try {
    m.container.find(".dialog-scrim").dispatch("mousedown");
    await settle();
    assert.equal(closed, 1);
    m.container.find("#start-task").enter("Fix the parser");
    await settle();
    m.container.find(".dialog-scrim").dispatch("mousedown");
    await settle();
    assert.equal(closed, 1, "the goal is not thrown away by a stray click");
  } finally {
    m.done();
  }
});

// ---------------------------------------------------------------- Remote

const off = { type: "remoteDevices", enabled: false, devices: [], hosts: [], seq: 1 };
const remoteView = (sent, over = {}) => html`<${Remote} ...${{ status: null, roster: off, pair: null, outcome: null, remote: false, send: (c) => { sent.push(c); return true; }, copy: () => {}, ...over }} />`;

test("Remote: the codes sent come back into the form when the relay answers with an error, and only then", async () => {
  const sent = [];
  const m = await mount(remoteView(sent));
  try {
    const more = m.container.find("#remote-more");
    more.click();
    await settle();
    m.container.find("#remote-relay").enter("https://relay.example.com");
    await settle();
    m.container.find("#remote-join").enter("JOIN-1");
    await settle();
    m.container.find("#remote-invite").enter("INV-1");
    await settle();
    await settle();
    m.container.find("#remote-enable").click();
    await settle();
    assert.equal(sent.filter((c) => c.cmd === "remoteEnable").length, 1);
    assert.equal(m.container.find("#remote-join").value, "", "not kept once sent");
    m.draw(remoteView(sent, { outcome: { type: "remoteOutcome", action: "enable", error: "the relay refused", seq: 1 } }));
    await settle();
    assert.equal(m.container.find("#remote-join").value, "JOIN-1");
    assert.equal(m.container.find("#remote-invite").value, "INV-1");
  } finally {
    m.done();
  }
  const sent2 = [];
  const n = await mount(remoteView(sent2));
  try {
    const more = n.container.find("#remote-more");
    more.click();
    await settle();
    n.container.find("#remote-relay").enter("https://relay.example.com");
    await settle();
    n.container.find("#remote-join").enter("JOIN-2");
    await settle();
    await settle();
    n.container.find("#remote-enable").click();
    await settle();
    n.draw(remoteView(sent2, { outcome: { type: "remoteOutcome", action: "enable", seq: 1 } }));
    await settle();
    assert.equal(n.container.find("#remote-join").value, "", "a success does not bring the code back");
  } finally {
    n.done();
  }
});

// ---------------------------------------------------------------- Help

const PAGES = [
  { slug: "panes", title: "Panes", summary: "About panes", text: "A pane is one agent.", tree: [{ t: "p", c: [{ x: "See " }, { t: "a", h: "#nowhere", c: [{ x: "a missing page" }] }] }] },
];
const helpView = (onClose) => html`<${Help} keys=${fakeKeys} implemented=${[]} load=${() => Promise.resolve({ pages: PAGES })} slug="panes" onClose=${onClose} />`;

test("Help: Escape empties a search before it closes, the content is a region, and a link to a missing page says so until the person moves on", async () => {
  let closed = 0;
  const m = await mount(helpView(() => { closed++; }));
  try {
    await settle();
    const content = m.container.find(".help-content");
    assert.equal(content.getAttribute("role"), "region");
    m.container.find("a").click();
    await settle();
    assert.match(m.container.find(".help-content").textContent, /There is no help page called "nowhere"/);
    m.container.find(".help-search").enter("pane");
    await settle();
    assert.doesNotMatch(m.container.find(".help-content").textContent, /There is no help page/);
    m.container.find(".help-search").keydown("Escape");
    await settle();
    assert.equal(closed, 0);
    assert.equal(m.container.find(".help-search").value, "");
    m.container.find(".help-search").keydown("Escape");
    await settle();
    assert.equal(closed, 1);
  } finally {
    m.done();
  }
});

// ---------------------------------------------------------------- Live

test("an error notice ends the pending settings only when it comes soon after the change", () => {
  const err = { error: true, text: "no" };
  assert.equal(refusesChange(err, 1000, 1000 + REFUSAL_MS - 1), true);
  assert.equal(refusesChange(err, 1000, 1000 + REFUSAL_MS), false);
  assert.equal(refusesChange({ text: "Agent finished" }, 1000, 1001), false);
  assert.equal(refusesChange(null, 1000, 1001), false);
});
