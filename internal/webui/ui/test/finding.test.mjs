// History, find in terminal, find pane, pane info, and recording and exporting a
// transcript (L4), through the real page on a fake control socket. The pure parts
// come first, then the terminal's search, then the page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Live, NO_TERMINAL_WORDS, OPEN_AN_AGENT_WORDS, SHELL_RECORD_WORDS } from "../assets/shell/Live.js";
import {
  HISTORY_REMOTE_WORDS, STRUCTURED_RESUME_WORDS, conversationsCommand, filterConversations, holdsAll, listingOf, resumeCommand, statsOf, wordsOf,
} from "../assets/lib/history.js";
import {
  DESK_FILES_WORDS, EXPORT_WORDS, NOT_SET, RECORD_WORDS, REVEAL_NONE_WORDS, STRUCTURED_RECORD_WORDS, acknowledged, askFor, canTranscribe, infoOf, infoText,
  matchesOf, revealCommand, transcriptCommand, whereOf, whyOf,
} from "../assets/lib/paneinfo.js";
import { countText } from "../assets/drawer/FindBar.js";
import { SCRIPTS, SEARCH_SCRIPT } from "../assets/lib/load-script.js";
import { createTerminal } from "../assets/lib/terminal.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";
import { PtySocket, FakeTerm, FakeFit, FakeObserver, FakeHost, fakeStore, paneState, waitFor } from "./termfakes.mjs";

// The pane's actions are behind the drawer header's More button: open it when the
// button asked for is not there yet.
const pickMore = async (m, sel) => {
  if (!m.container.find(sel) && m.container.find(".drawer-more")) await m.click(m.container.find(".drawer-more"));
  return m.container.find(sel);
};

const here = dirname(fileURLToPath(import.meta.url));
const fixtureFile = join(here, "..", "..", "..", "server", "testdata", "graph-fixtures", "01-six-helpers-one-permission.json");

// ---------------------------------------------------------------- pure parts

const ROWS = [
  { id: "aaaa1111-0000", agent: "", summary: "Fix the login form", ago: "2 hours ago", messages: 12, kb: 34, open: false },
  { id: "bbbb2222-0000", agent: "opencode", summary: "Write the release notes", ago: "3 days ago", messages: 1, kb: 0, open: true },
  { id: "cccc3333-0000", agent: "", summary: "Fix the CSS build", ago: "just now", messages: 0, kb: 2, open: false },
];

test("a listing is read defensively: rows without an id are dropped and wrong types read as empty", () => {
  const l = listingOf({
    cwd: "/work/a", items: [null, 3, { id: "" }, { summary: "no id" }, { id: "x", summary: 4, agent: null, ago: [], messages: "9", kb: -1, open: "yes" }, ROWS[0]],
    error: 7,
  });
  assert.equal(l.cwd, "/work/a");
  assert.equal(l.error, "");
  assert.equal(l.items.length, 2);
  assert.deepEqual(l.items[0], { id: "x", agent: "", summary: "", ago: "", messages: 0, kb: 0, open: false });
  assert.deepEqual(listingOf(null), { cwd: "", items: [], error: "" });
  assert.deepEqual(listingOf({ items: "no" }).items, []);
});

test("the list narrows to the conversations that hold every word, in the summary or the id", () => {
  assert.deepEqual(wordsOf("  Fix   THE "), ["fix", "the"]);
  assert.deepEqual(wordsOf(""), []);
  assert.deepEqual(wordsOf(null), []);
  assert.equal(filterConversations(ROWS, "").length, 3);
  assert.deepEqual(filterConversations(ROWS, "fix").map((c) => c.id), ["aaaa1111-0000", "cccc3333-0000"]);
  assert.deepEqual(filterConversations(ROWS, "fix css").map((c) => c.id), ["cccc3333-0000"]);
  assert.deepEqual(filterConversations(ROWS, "bbbb2").map((c) => c.id), ["bbbb2222-0000"], "the start of an id finds it");
  assert.deepEqual(filterConversations(ROWS, "fix zebra"), []);
  assert.equal(holdsAll("Hello World", ["world"]), true);
  assert.equal(holdsAll("Hello", ["world"]), false);
});

test("a row's small print says when, how many, how big and which one", () => {
  assert.deepEqual(statsOf(ROWS[0]), ["2 hours ago", "12 entries", "34 KB", "aaaa1111"]);
  assert.deepEqual(statsOf(ROWS[1]), ["3 days ago", "1 entry", "bbbb2222", "already open"]);
  assert.deepEqual(statsOf({ ...ROWS[2], ago: "" }), ["0 entries", "2 KB", "cccc3333"]);
});

test("resuming sends the folder the listing was for and the agent that kept the conversation, and never for an open one", () => {
  const listing = { cwd: "/work/a" };
  assert.deepEqual(resumeCommand(listing, ROWS[0]), { cmd: "resumeConversation", id: "aaaa1111-0000", path: "/work/a", text: "Fix the login form" });
  assert.deepEqual(resumeCommand(listing, { ...ROWS[0], agent: "opencode" }), {
    cmd: "resumeConversation", id: "aaaa1111-0000", path: "/work/a", text: "Fix the login form", agent: "opencode",
  });
  assert.equal(resumeCommand(listing, ROWS[1]), null);
  assert.equal(resumeCommand(null, ROWS[0]), null);
  assert.deepEqual(conversationsCommand("/work/a"), { cmd: "conversations", path: "/work/a" });
  assert.deepEqual(conversationsCommand(""), { cmd: "conversations" });
});

test("pane info is read defensively and copied as one line a value", () => {
  const i = infoOf({ id: "p1", name: "cart", fields: [null, { label: "" }, { label: "Pane id", value: "p1", note: "n" }, { label: "Branch", value: 4 }, { value: "x" }], transcriptOff: "no" });
  assert.deepEqual(i.fields, [{ label: "Pane id", value: "p1", note: "n" }, { label: "Branch", value: "", note: "" }]);
  assert.equal(i.transcriptOff, "no");
  assert.deepEqual(infoOf(undefined), { id: "", name: "", fields: [], transcriptOff: "" });
  assert.equal(infoText(i.fields), "Pane id: p1\nBranch: " + NOT_SET);
});

test("found panes are read defensively and chosen with revealPane", () => {
  const m = matchesOf({
    query: "x", items: [null, { name: "no id" }, { paneId: "p1", tabId: "t1", root: "/r", name: "cart", project: "alpha", tab: "First", matched: [{ label: "Branch", value: "main" }, null, { value: "no label" }] }],
  });
  assert.equal(m.query, "x");
  assert.equal(m.items.length, 1);
  assert.deepEqual(m.items[0].matched, [{ label: "Branch", value: "main" }]);
  assert.equal(whereOf(m.items[0]), "alpha · First");
  assert.equal(whereOf({ project: "", tab: "First" }), "First");
  assert.equal(whyOf(m.items[0]), "Matched branch main");
  assert.equal(whyOf({ matched: [] }), "");
  assert.deepEqual(revealCommand(m.items[0]), { cmd: "revealPane", root: "/r", node: "t1", id: "p1" });
  assert.deepEqual(matchesOf(null), { query: "", items: [] });
});

test("recording asks once, exporting asks every time, and stopping asks never", () => {
  assert.equal(askFor("record", true), null);
  assert.equal(askFor("record", false).label, "Start recording");
  assert.equal(askFor("record", false).words, RECORD_WORDS);
  assert.equal(askFor("export", true).words, EXPORT_WORDS);
  assert.equal(askFor("export", false).label, "Export transcript");
  assert.equal(acknowledged({ recordingAcknowledged: true }), true);
  assert.equal(acknowledged({ recordingAcknowledged: "yes" }), false);
  assert.equal(acknowledged(null), false);
  assert.deepEqual(transcriptCommand("record", "p", { on: true }), { cmd: "recordPane", id: "p", recording: true });
  assert.deepEqual(transcriptCommand("record", "p", { on: true, confirmed: true }), { cmd: "recordPane", id: "p", recording: true, confirmed: true });
  assert.deepEqual(transcriptCommand("record", "p", { on: false }), { cmd: "recordPane", id: "p", recording: false });
  assert.deepEqual(transcriptCommand("export", "p", {}), { cmd: "exportTranscript", id: "p", confirmed: true });
  for (const w of [...RECORD_WORDS, ...EXPORT_WORDS]) assert.ok(w.length > 20);
});

test("only an agent with a terminal can be recorded or exported", () => {
  assert.equal(canTranscribe({ pane: "p", kind: "terminal" }, null), true);
  assert.equal(canTranscribe({ pane: "p", shell: true }, null), false);
  assert.equal(canTranscribe({ pane: "p" }, { shell: true }), false);
  assert.equal(canTranscribe({ pane: "p", structured: true }, null), false);
  assert.equal(canTranscribe({ pane: "p", kind: "structured" }, null), false);
  assert.equal(canTranscribe({ pane: "p" }, { structured: true }), false);
  assert.equal(canTranscribe(null, null), false);
});

test("the count says which match is showing, none, or too many to count", () => {
  assert.equal(countText("a", { index: 1, count: 5 }), "2 of 5");
  assert.equal(countText("a", { index: 0, count: 0 }), "No matches");
  assert.equal(countText("a", { index: -1, count: -1 }), "Many matches");
  assert.equal(countText("", { index: 1, count: 5 }), "");
  assert.equal(countText("a", null), "");
});

// ---------------------------------------------------------------- the store

test("the store keeps the history, pane info and found panes, numbered, and a remote window keeps none", async () => {
  const { store } = await harness();
  store.start();
  const ws = FakeWebSocket.last;
  ws.open();
  ws.frame(hello());
  assert.equal(store.getState().history, null);
  ws.frame({ type: "conversations", cwd: "/r", items: [] });
  ws.frame({ type: "conversations", cwd: "/s", items: [] });
  assert.equal(store.getState().history.cwd, "/s");
  assert.equal(store.getState().history.seq, 2);
  ws.frame({ type: "paneInfo", id: "p", fields: [] });
  assert.equal(store.getState().paneInfo.seq, 1);
  ws.frame({ type: "paneMatches", query: "q", items: [] });
  assert.equal(store.getState().paneMatches.query, "q");
  store.stop();

  const r = await harness();
  r.store.start();
  const rws = FakeWebSocket.last;
  rws.open();
  rws.frame(hello({ remote: true }));
  rws.frame({ type: "conversations", cwd: "/r", items: [] });
  rws.frame({ type: "paneInfo", id: "p", fields: [] });
  rws.frame({ type: "paneMatches", query: "q", items: [] });
  const s = r.store.getState();
  assert.equal(s.history, null);
  assert.equal(s.paneInfo, null);
  assert.equal(s.paneMatches, null);
  for (const cmd of ["conversations", "resumeConversation", "paneInfo", "findPane", "revealPane", "exportTranscript", "revealTranscript", "openRecordings"]) {
    assert.equal(r.store.send({ cmd }), false, cmd + " is not sent from a remote window");
  }
  assert.equal(r.store.send({ cmd: "recordPane", id: "p", recording: false }), true, "recording is not held back");
  r.store.stop();
});

// ---------------------------------------------------------------- the terminal's search

class FakeSearch {
  static instances = [];
  static reset() { FakeSearch.instances = []; }
  static get last() { return FakeSearch.instances[FakeSearch.instances.length - 1]; }
  constructor() { this.forward = []; this.back = []; this.cleared = 0; this.hit = true; FakeSearch.instances.push(this); }
  findNext(q, o) { this.forward.push([q, o]); return this.hit; }
  findPrevious(q, o) { this.back.push([q, o]); return this.hit; }
  clearDecorations() { this.cleared++; }
  onDidChangeResults(fn) { this.report = fn; }
}

function build(over = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeSearch.reset();
  FakeObserver.instances.length = 0;
  const store = fakeStore({ hello: { type: "hello" }, state: paneState({ p1: { status: "idle" } }) });
  const matches = [];
  const ctl = createTerminal({
    host: new FakeHost(), paneId: "p1", store, location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" },
    Terminal: FakeTerm, FitAddon: FakeFit, ResizeObserver: FakeObserver, WebSocket: PtySocket, isFocused: () => false,
    onMatches: (m) => matches.push(m), ...over,
  });
  return { ctl, matches };
}

test("the search addon is added the first time it is asked for, and finds, steps back and clears", async () => {
  const { ctl, matches } = build({ SearchAddon: FakeSearch });
  assert.equal(await ctl.searchReady(), false, "no terminal yet");
  ctl.start();
  assert.equal(ctl.find("x"), null, "nothing to search with before the addon is ready");
  assert.equal(await ctl.searchReady(), true);
  assert.equal(await ctl.searchReady(), true);
  assert.equal(FakeSearch.instances.length, 1, "added once");
  const s = FakeSearch.last;
  assert.equal(ctl.find("needle"), true);
  assert.equal(s.forward[0][0], "needle");
  assert.equal(s.forward[0][1].incremental, false);
  assert.equal(ctl.find("needle", { typing: true }), true);
  assert.equal(s.forward[1][1].incremental, true);
  assert.equal(ctl.find("needle", { back: true }), true);
  assert.equal(s.back.length, 1);
  s.hit = false;
  assert.equal(ctl.find("zebra"), false);
  assert.equal(ctl.find(""), null, "nothing typed searches nothing");
  assert.equal(s.cleared, 1, "and clears the marks");
  s.report({ resultIndex: 1, resultCount: 4 });
  assert.deepEqual(matches.at(-1), { index: 1, count: 4 });
  ctl.clearFind();
  assert.equal(matches.at(-1), null);
  ctl.dispose();
});

test("the search addon is loaded when the page does not have it, and a failed load is tried again", async () => {
  let calls = 0;
  const { ctl } = build({
    loadSearch: async () => {
      calls++;
      if (calls === 1) throw new Error("offline");
      globalThis.SearchAddon = { SearchAddon: FakeSearch };
    },
  });
  ctl.start();
  try {
    assert.equal(await ctl.searchReady(), false, "the load failed");
    assert.equal(await ctl.searchReady(), true, "asked again, it loads");
    assert.equal(calls, 2);
    assert.equal(ctl.find("x"), true);
  } finally {
    delete globalThis.SearchAddon;
    ctl.dispose();
  }
});

test("the search addon is a same-origin file of its own, not one of the terminal's libraries", () => {
  assert.equal(SEARCH_SCRIPT.src, "assets/vendor/addon-search.js");
  assert.equal(SEARCH_SCRIPT.global, "SearchAddon");
  assert.ok(!Object.values(SCRIPTS).some((d) => d.src === SEARCH_SCRIPT.src), "a terminal does not wait for it");
  const file = join(here, "..", "assets", "vendor", "addon-search.js");
  assert.ok(readFileSync(file, "utf8").includes("SearchAddon"), "the old window's vendored file is the one served");
});

test("an addon that will not be added leaves the terminal as it was", async () => {
  class Broken { constructor() { throw new Error("no"); } }
  const { ctl } = build({ SearchAddon: Broken });
  ctl.start();
  assert.equal(await ctl.searchReady(), false);
  assert.equal(ctl.find("x"), null);
  ctl.clearFind();
  ctl.dispose();
});

// ---------------------------------------------------------------- the page

const projects = [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }, { root: "/work/b", name: "beta", waiting: 0, working: 0 }];
const CART = "6fb40971";
const TAX = "366c9143";
const DOCS = "ec776492";
const KEYS = [
  { id: "palette", keys: "Ctrl+Shift+K", label: "Command palette", section: "Window" },
  { id: "history", keys: "Ctrl+Shift+R", label: "Resume", section: "Finding your way" },
  { id: "findPane", keys: "Ctrl+Shift+L", label: "Find pane", section: "Finding your way" },
  { id: "paneInfo", keys: "Ctrl+Shift+Y", label: "Pane info", section: "Panes" },
  { id: "findInTerminal", keys: "Ctrl+Shift+F", label: "Find in terminal", section: "Finding your way" },
  { id: "recordPane", keys: "Ctrl+Alt+1", label: "Start recording", section: "Panes" },
  { id: "exportTranscript", keys: "Ctrl+Alt+2", label: "Export transcript", section: "Panes" },
  { id: "revealTranscript", keys: "Ctrl+Alt+3", label: "Reveal transcript", section: "Panes" },
  { id: "openRecordings", keys: "Ctrl+Alt+4", label: "Open recordings folder", section: "The window" },
];
const graph = (change = {}) => {
  const g = { ...JSON.parse(readFileSync(fixtureFile, "utf8")), root: "/work/a" };
  g.nodes = g.nodes.map((n) => ({ ...n, ...(change[n.pane] || {}) }));
  return g;
};
const stateOf = (g, extra = {}, over = {}) => ({
  type: "state", root: "/work/a", projects, agents: { items: [], roles: [], default: {} }, activeTab: "t1", tabs: [{ id: "t1", title: "First" }],
  panes: Object.fromEntries(g.nodes.map((n) => [n.pane, { id: n.pane, name: n.name, structured: n.kind === "structured", ...(extra[n.pane] || {}) }])),
  ...over,
});

async function mount({ g = graph(), extra = {}, prefs = {}, helloOver = {} } = {}) {
  PtySocket.reset();
  FakeTerm.reset();
  FakeSearch.reset();
  FakeObserver.instances.length = 0;
  const dom = createDom();
  const restore = install(dom);
  const copied = [];
  dom.window.navigator = { clipboard: { writeText: async (t) => { copied.push(t); } } };
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  const terminalOptions = {
    Terminal: FakeTerm, FitAddon: FakeFit, SearchAddon: FakeSearch, ResizeObserver: FakeObserver, WebSocket: PtySocket,
    location: { href: "http://127.0.0.1:5000/", pathname: "/", protocol: "http:" }, isFocused: () => false,
  };
  render(html`<${Live} store=${store} window=${dom.window} terminalOptions=${terminalOptions} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  const m = {
    dom, store, container, ws, copied,
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
    notice: () => (container.find(".notice") ? container.find(".notice").textContent : ""),
    connect: async () => {
      ws().open();
      ws().frame(hello({ keys: KEYS, prefs, ...helloOver }));
      await settle();
      ws().frame(stateOf(g, extra));
      await settle();
      if (!helloOver.remote) {
        ws().frame(g);
        await settle();
      }
    },
    open: async (pane) => {
      await m.click(m.box(pane));
      assert.ok(m.container.find(".drawer-body"), "no drawer");
    },
    type: async (sel, value) => {
      const el = container.find(sel);
      assert.ok(el, "no field " + sel);
      el.value = value;
      el.dispatch("input");
      await settle();
      return el;
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

const dialog = (m) => m.container.find(".dialog");
const valueIn = (el) => (el.value !== undefined ? el.value : el.attributes.value);
const escape = async (m, el) => { el.keydown("Escape"); await settle(); };
const CONVERSATIONS = { type: "conversations", cwd: "/work/a", items: ROWS };

// ---- history

test("Resume asks for the project's conversations, says it is reading, and lists what comes back", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+R");
    assert.ok(dialog(m), "its key opens it");
    assert.deepEqual(m.sent("conversations"), [{ cmd: "conversations", path: "/work/a" }]);
    assert.match(dialog(m).textContent, /Reading conversations/);
    assert.equal(m.container.find("#history-filter"), null, "no field before there is a list");
    assert.ok(dialog(m).textContent.includes(STRUCTURED_RESUME_WORDS), "it says a structured agent has nothing to resume");
    await m.push(CONVERSATIONS);
    assert.doesNotMatch(dialog(m).textContent, /Reading conversations/);
    const rows = m.container.findAll(".hist-row");
    assert.equal(rows.length, 3);
    assert.match(rows[0].textContent, /Fix the login form/);
    assert.match(rows[0].textContent, /2 hours ago · 12 entries · 34 KB · aaaa1111/);
    assert.match(rows[1].textContent, /already open · opencode/);
    assert.equal(!!rows[1].disabled, true, "a conversation already open cannot be resumed");
    assert.equal(!!rows[0].disabled, false);
  }));

test("Resume is also a button on the tab strip", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.click(m.byText(".tabs-btn", /^Resume…$/));
    assert.ok(dialog(m));
    assert.equal(m.sent("conversations").length, 1);
  }));

test("a listing read before the dialog opened is not drawn in it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.push({ ...CONVERSATIONS, cwd: "/work/b", items: [{ ...ROWS[0], summary: "Another project's" }] });
    await m.key("Ctrl+Shift+R");
    assert.doesNotMatch(dialog(m).textContent, /Another project/);
    assert.match(dialog(m).textContent, /Reading conversations/);
    await m.push(CONVERSATIONS);
    assert.match(dialog(m).textContent, /Fix the login form/);
  }));

test("the field narrows the list to the words typed, Escape empties it first and then closes, and the keyboard goes back", () =>
  withPage({}, async (m) => {
    await m.connect();
    const opener = m.container.find(".tabs-history");
    opener.focus();
    await m.click(opener);
    await m.push(CONVERSATIONS);
    const field = m.container.find("#history-filter");
    assert.equal(m.dom.document.activeElement, field, "the keyboard starts in the field");
    await m.type("#history-filter", "fix css");
    assert.deepEqual(m.container.findAll(".hist-row").map((r) => r.attributes["data-conversation"]), ["cccc3333-0000"]);
    await m.type("#history-filter", "zebra");
    assert.equal(m.container.findAll(".hist-row").length, 0);
    assert.match(m.container.find("#history-none").textContent, /No conversation matches that/);
    await escape(m, field);
    assert.ok(dialog(m), "the first Escape only empties the field");
    assert.equal(valueIn(m.container.find("#history-filter")), "");
    assert.equal(m.container.findAll(".hist-row").length, 3);
    await escape(m, m.container.find("#history-filter"));
    assert.equal(dialog(m), null);
    assert.equal(m.dom.document.activeElement, opener, "the keyboard is back where it was");
  }));

test("the arrow keys walk the rows that can be chosen, and Up from the first goes back to the field", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+R");
    await m.push(CONVERSATIONS);
    const field = m.container.find("#history-filter");
    field.keydown("ArrowDown");
    const first = m.container.findAll(".hist-row")[0];
    assert.equal(m.dom.document.activeElement, first);
    first.keydown("ArrowDown");
    assert.equal(m.dom.document.activeElement, m.container.findAll(".hist-row")[2], "the open conversation is skipped");
    m.container.findAll(".hist-row")[2].keydown("ArrowUp");
    assert.equal(m.dom.document.activeElement, first);
    first.keydown("ArrowUp");
    assert.equal(m.dom.document.activeElement, field);
  }));

test("choosing a row resumes it with the folder and agent, and closes the dialog", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+R");
    await m.push({ ...CONVERSATIONS, items: [{ ...ROWS[0], agent: "opencode" }, ROWS[1]] });
    m.clear();
    await m.click(m.container.findAll(".hist-row")[0]);
    assert.deepEqual(m.sent("resumeConversation"), [{
      cmd: "resumeConversation", id: "aaaa1111-0000", path: "/work/a", text: "Fix the login form", agent: "opencode",
    }]);
    assert.equal(dialog(m), null);
  }));

test("Refresh asks again and waits for the new answer", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+R");
    await m.push(CONVERSATIONS);
    m.clear();
    await m.click(m.byText(".dialog-btn", /^Refresh$/));
    assert.equal(m.sent("conversations").length, 1);
    assert.match(dialog(m).textContent, /Reading conversations/);
    await m.push({ ...CONVERSATIONS, items: [{ ...ROWS[0], summary: "Newer" }] });
    assert.match(dialog(m).textContent, /Newer/);
  }));

test("a listing that failed says why in place of the list, and an empty one says so", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+R");
    await m.push({ type: "conversations", cwd: "/work/a", items: [], error: "cannot read the store" });
    assert.match(m.container.find('[role="alert"]').textContent, /cannot read the store/);
    await m.click(m.byText(".dialog-btn", /^Refresh$/));
    await m.push({ type: "conversations", cwd: "/work/a", items: [] });
    assert.match(dialog(m).textContent, /No stored conversations for this project yet/);
    assert.equal(m.container.find("#history-filter"), null);
  }));

test("what is in a summary is drawn as text", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+R");
    await m.push({ ...CONVERSATIONS, items: [{ ...ROWS[0], summary: "<img src=x onerror=alert(1)><b>bold</b>" }] });
    const row = m.container.findAll(".hist-row")[0];
    assert.ok(row.textContent.includes("<img src=x onerror=alert(1)><b>bold</b>"));
    assert.equal(m.container.find("img"), null);
    assert.equal(m.container.find("b"), null);
  }));

test("no key reaches an action while a dialog of L4 is open", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+R");
    m.clear();
    await m.key("Ctrl+Shift+L");
    await m.key("Ctrl+Alt+4");
    assert.equal(m.sent("findPane").length, 0);
    assert.equal(m.sent("openRecordings").length, 0);
    assert.equal(m.container.findAll(".dialog").length, 1);
  }));

// ---- pane info

const INFO = {
  type: "paneInfo", id: CART, name: "cart",
  fields: [
    { label: "Pane id", value: CART, note: "What flockdeck commands take" },
    { label: "Branch", value: "" },
    { label: "Transcript file", value: "", note: "The file being recorded to" },
  ],
  transcriptOff: "",
};

test("Pane info needs an agent open, asks the desktop for it, and lists the values with a Copy each", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+Y");
    assert.ok(m.notice().includes(OPEN_AN_AGENT_WORDS));
    assert.equal(m.sent("paneInfo").length, 0);
    await m.open(CART);
    await m.key("Ctrl+Shift+Y");
    assert.deepEqual(m.sent("paneInfo"), [{ cmd: "paneInfo", id: CART }]);
    assert.match(dialog(m).textContent, /Loading/);
    await m.push({ ...INFO, id: TAX, name: "an answer for another pane" });
    assert.match(dialog(m).textContent, /Loading/, "an answer for another pane is not this one's");
    await m.push(INFO);
    assert.match(dialog(m).textContent, /What names “cart” in files, logs and commands/);
    const rows = m.container.findAll(".pi-row");
    assert.deepEqual(rows.map((r) => r.attributes["data-key"]), ["Pane id", "Branch", "Transcript file"]);
    assert.match(rows[1].textContent, /not set/);
    assert.equal(rows[1].findAll("button").length, 0, "nothing to copy where nothing is set");
    await m.click(rows[0].findAll("button").find((b) => b.textContent === "Copy"));
    assert.deepEqual(m.copied, [CART]);
    assert.match(m.notice(), /Copied pane id/);
    await m.click(m.container.find("#pane-info-copy-all"));
    assert.equal(m.copied[1], "Pane id: " + CART + "\nBranch: not set\nTranscript file: not set");
  }));

test("Pane info is a button in the drawer too", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    m.clear();
    await m.click((await pickMore(m, ".tools-info")));
    assert.equal(m.sent("paneInfo").length, 1);
  }));

test("the transcript file can be shown in the folder when there is one, and says why not when there is none", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+Y");
    await m.push(INFO);
    const show = () => m.byText("button", /^Show in folder$/);
    assert.equal(!!show().disabled, true);
    assert.ok(m.container.find("#pane-info-reveal-why").textContent.includes(REVEAL_NONE_WORDS));
    assert.equal(show().attributes["aria-describedby"], "pane-info-reveal-why");
    await m.push({ ...INFO, transcriptOff: "This agent stores no conversation Flockdeck can read." });
    assert.match(m.container.find("#pane-info-reveal-why").textContent, /stores no conversation/);
    await m.push({ ...INFO, fields: [{ label: "Transcript file", value: "/rec/a.jsonl" }] });
    assert.equal(!!show().disabled, false);
    assert.equal(m.container.find("#pane-info-reveal-why"), null);
    m.clear();
    await m.click(show());
    assert.deepEqual(m.sent("revealTranscript"), [{ cmd: "revealTranscript", id: CART }]);
  }));

test("a refusal that comes in place of the answer takes the place of Loading", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+Y");
    await m.push({ type: "notice", text: "That pane is no longer open", error: true });
    assert.match(m.container.find('[role="alert"]').textContent, /no longer open/);
    assert.doesNotMatch(dialog(m).textContent, /Loading/);
  }));

test("a pane info value from an agent is drawn as text", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+Y");
    await m.push({ ...INFO, name: "<i>x</i>", fields: [{ label: "Peer name", value: "<script>boom()</script>" }] });
    assert.ok(dialog(m).textContent.includes("<script>boom()</script>"));
    assert.equal(m.container.find("script"), null);
    assert.equal(m.container.find("i"), null);
  }));

// ---- find pane

const FOUND = (query, items) => ({
  type: "paneMatches", query,
  items: items || [
    { paneId: CART, tabId: "t1", root: "/work/a", name: "cart", project: "alpha", tab: "First", matched: [{ label: "Branch", value: "feature/cart" }] },
    { paneId: TAX, tabId: "t1", root: "/work/a", name: "tax", project: "alpha", tab: "First" },
  ],
});

test("Find pane asks as the words change, shows only the answer to what is in the field, and Enter chooses the first", () =>
  withPage({}, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+L");
    assert.deepEqual(m.sent("findPane"), [{ cmd: "findPane", text: "" }]);
    assert.equal(m.dom.document.activeElement, m.container.find("#find-pane-input"));
    await m.push(FOUND(""));
    assert.equal(m.container.findAll(".pf-row").length, 2);
    assert.match(m.container.findAll(".pf-row")[0].textContent, /cart/);
    assert.match(m.container.findAll(".pf-row")[0].textContent, /alpha · First/);
    assert.match(m.container.findAll(".pf-row")[0].textContent, /Matched branch feature\/cart/);
    await m.type("#find-pane-input", "ca");
    assert.deepEqual(m.sent("findPane").at(-1), { cmd: "findPane", text: "ca" });
    // The answer to what was typed a moment ago is not this one's.
    await m.push(FOUND("c"));
    assert.equal(m.container.findAll(".pf-row").length, 0, "the old answer is not drawn");
    m.clear();
    m.container.find("#find-pane-input").keydown("Enter");
    await settle();
    assert.equal(m.sent("revealPane").length, 0, "Enter does not act on results for other words");
    await m.push(FOUND("ca", [FOUND("").items[0]]));
    m.container.find("#find-pane-input").keydown("Enter");
    await settle();
    assert.deepEqual(m.sent("revealPane"), [{ cmd: "revealPane", root: "/work/a", node: "t1", id: CART }]);
    assert.equal(dialog(m), null);
  }));

test("choosing a found pane reveals it on the desktop and opens it in the drawer", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.click(m.byText(".tabs-btn", /^Find pane…$/));
    await m.push(FOUND(""));
    m.clear();
    await m.click(m.container.findAll(".pf-row")[1]);
    assert.deepEqual(m.sent("revealPane"), [{ cmd: "revealPane", root: "/work/a", node: "t1", id: TAX }]);
    assert.equal(m.container.find(".drawer-body").attributes["data-drawer-pane"], TAX);
  }));

test("a pane in another project opens once that project's graph arrives", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+L");
    await m.push(FOUND("", [{ paneId: "far0far0", tabId: "t9", root: "/work/b", name: "far", project: "beta", tab: "Other" }]));
    await m.click(m.container.findAll(".pf-row")[0]);
    assert.equal(m.container.find(".drawer-body"), null, "the pane is not in the graph on screen yet");
    const other = graph();
    other.root = "/work/b";
    other.nodes = [{ ...other.nodes[0], pane: "far0far0", name: "far" }];
    other.edges = [];
    other.teams = [];
    other.solo = [];
    other.soloNeeds = [];
    await m.push(stateOf(other, {}, { root: "/work/b" }));
    await m.push(other);
    assert.equal(m.container.find(".drawer-body").attributes["data-drawer-pane"], "far0far0");
  }));

test("no pane matching says so, and Escape empties the field before it closes the dialog", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+L");
    await m.type("#find-pane-input", "zebra");
    await m.push(FOUND("zebra", []));
    assert.match(m.container.find("#find-pane-results").textContent, /No pane matches “zebra”/);
    await escape(m, m.container.find("#find-pane-input"));
    assert.ok(dialog(m));
    assert.equal(valueIn(m.container.find("#find-pane-input")), "");
    assert.deepEqual(m.sent("findPane").at(-1), { cmd: "findPane", text: "" });
    await escape(m, m.container.find("#find-pane-input"));
    assert.equal(dialog(m), null);
  }));

test("the arrow keys walk the found panes", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+L");
    await m.push(FOUND(""));
    const field = m.container.find("#find-pane-input");
    field.keydown("ArrowDown");
    const rows = m.container.findAll(".pf-row");
    assert.equal(m.dom.document.activeElement, rows[0]);
    rows[0].keydown("ArrowDown");
    assert.equal(m.dom.document.activeElement, rows[1]);
    rows[1].keydown("ArrowUp");
    rows[0].keydown("ArrowUp");
    assert.equal(m.dom.document.activeElement, field);
  }));

// ---- find in terminal

const finder = (m) => m.container.find(".find-bar");
const typed = async (m, value) => {
  const el = m.container.find("#find-input");
  el.value = value;
  el.dispatch("input");
  await settle();
};

test("Find in terminal needs an agent open, opens a bar on its terminal and searches as the words are typed", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.key("Ctrl+Shift+F");
    assert.ok(m.notice().includes(OPEN_AN_AGENT_WORDS));
    assert.equal(finder(m), null);
    await m.open(CART);
    await m.key("Ctrl+Shift+F");
    await waitFor(() => FakeSearch.instances.length === 1, "the search addon");
    await settle();
    assert.ok(finder(m));
    assert.equal(m.dom.document.activeElement, m.container.find("#find-input"));
    assert.match(finder(m).attributes["aria-label"], /Find in cart/);
    await typed(m, "needle");
    const s = FakeSearch.last;
    assert.equal(s.forward.at(-1)[0], "needle");
    assert.equal(s.forward.at(-1)[1].incremental, true, "as the words are typed");
    s.report({ resultIndex: 0, resultCount: 3 });
    await settle();
    assert.equal(m.container.find("#find-count").textContent, "1 of 3");
    m.container.find("#find-input").keydown("Enter");
    assert.equal(s.forward.at(-1)[1].incremental, false, "Enter goes to the next one");
    m.container.find("#find-input").keydown("Enter", { shiftKey: true });
    assert.equal(s.back.at(-1)[0], "needle", "Shift+Enter goes to the one before");
    m.container.find("#find-input").keydown("F3");
    assert.equal(s.forward.length >= 3, true, "F3 steps on");
  }));

test("a word that is not there marks the field and says so", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+F");
    await waitFor(() => FakeSearch.instances.length === 1, "the search addon");
    await settle();
    FakeSearch.last.hit = false;
    await typed(m, "zebra");
    assert.equal(m.container.find("#find-input").attributes["aria-invalid"], "true");
    assert.match(m.container.find("#find-input").attributes.class, /is-miss/);
    FakeSearch.last.report({ resultIndex: -1, resultCount: 0 });
    await settle();
    assert.equal(m.container.find("#find-count").textContent, "No matches");
    FakeSearch.last.hit = true;
    await typed(m, "cart");
    assert.equal(m.container.find("#find-input").attributes["aria-invalid"], "false");
  }));

test("Escape closes the bar, clears the marks, gives the keyboard to the terminal and leaves the drawer open; the words come back", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+F");
    await waitFor(() => FakeSearch.instances.length === 1, "the search addon");
    await settle();
    await typed(m, "needle");
    const before = FakeSearch.last.cleared;
    FakeTerm.last.focused = false;
    m.container.find("#find-input").keydown("Escape");
    await settle();
    assert.equal(finder(m), null);
    assert.ok(m.container.find(".drawer-body"), "the drawer stays");
    assert.ok(FakeSearch.last.cleared > before, "the marks are cleared");
    assert.equal(FakeTerm.last.focused, true, "the keyboard is in the terminal");
    await m.key("Ctrl+Shift+F");
    assert.equal(valueIn(m.container.find("#find-input")), "needle", "the last search comes back");
  }));

test("asking again while the bar is open takes the keyboard back to it", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+F");
    await waitFor(() => FakeSearch.instances.length === 1, "the search addon");
    await settle();
    m.container.find(".drawer-body").focus();
    await m.key("Ctrl+Shift+F");
    assert.equal(m.dom.document.activeElement, m.container.find("#find-input"));
    assert.equal(m.container.findAll(".find-bar").length, 1);
  }));

test("the bar closes when the drawer moves to another pane, and an old ask does not open it on a later pane", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.key("Ctrl+Shift+F");
    await settle();
    assert.ok(finder(m));
    await m.click(m.box(TAX));
    assert.equal(finder(m), null);
    await m.click(m.container.find(".drawer-close"));
    await m.click(m.box(DOCS));
    assert.equal(finder(m), null, "the earlier ask is not acted on again");
  }));

test("Find in terminal says a structured pane has no terminal to search", () =>
  withPage({ g: graph({ [TAX]: { kind: "structured", structured: true } }) }, async (m) => {
    await m.connect();
    await m.open(TAX);
    assert.equal((await pickMore(m, ".tools-find")), null, "no button for it");
    await m.key("Ctrl+Shift+F");
    assert.ok(m.notice().includes(NO_TERMINAL_WORDS));
    assert.equal(finder(m), null);
  }));

// ---- recording and exporting

test("the first recording ever is put to the person, and starts with confirmed once they agree", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    m.clear();
    await m.click((await pickMore(m, ".tools-record")));
    assert.equal(m.sent("recordPane").length, 0, "nothing is sent before the answer");
    const ask = m.container.find(".confirm");
    assert.match(ask.textContent, /Record this pane's agent interaction/);
    for (const w of RECORD_WORDS) assert.ok(ask.textContent.includes(w));
    await m.click(m.byText(".confirm .dialog-btn", /^Cancel$/));
    assert.equal(m.sent("recordPane").length, 0);
    assert.equal(m.container.find(".confirm"), null);
    await m.click((await pickMore(m, ".tools-record")));
    await m.click(m.byText(".confirm .dialog-btn", /^Start recording$/));
    assert.deepEqual(m.sent("recordPane"), [{ cmd: "recordPane", id: CART, recording: true, confirmed: true }]);
  }));

test("once told, recording starts at once, and stopping never asks", () =>
  withPage({ prefs: { recordingAcknowledged: true } }, async (m) => {
    await m.connect();
    await m.open(CART);
    m.clear();
    await m.click((await pickMore(m, ".tools-record")));
    assert.deepEqual(m.sent("recordPane"), [{ cmd: "recordPane", id: CART, recording: true }]);
    assert.equal(m.container.find(".confirm"), null);
    await m.push(stateOf(graph(), { [CART]: { recording: true } }));
    const stop = (await pickMore(m, ".tools-record"));
    assert.equal(stop.textContent.trim(), "Stop recording");
    assert.equal(stop.attributes["aria-pressed"], "true");
    m.clear();
    await m.click(stop);
    assert.deepEqual(m.sent("recordPane"), [{ cmd: "recordPane", id: CART, recording: false }]);
  }));

test("a first recording stopped never asks, and an acknowledgement that arrives later is used", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.push(stateOf(graph(), { [CART]: { recording: true } }));
    await m.open(CART);
    m.clear();
    await m.click((await pickMore(m, ".tools-record")));
    assert.deepEqual(m.sent("recordPane"), [{ cmd: "recordPane", id: CART, recording: false }]);
    await m.push(stateOf(graph(), { [CART]: { recording: false } }));
    await m.push({ type: "prefs", prefs: { recordingAcknowledged: true } });
    m.clear();
    await m.click((await pickMore(m, ".tools-record")));
    assert.deepEqual(m.sent("recordPane"), [{ cmd: "recordPane", id: CART, recording: true }]);
  }));

test("an export asks every time, even after recording was agreed to, and sends confirmed", () =>
  withPage({ prefs: { recordingAcknowledged: true } }, async (m) => {
    await m.connect();
    await m.open(CART);
    m.clear();
    await m.click((await pickMore(m, ".tools-export")));
    const ask = m.container.find(".confirm");
    assert.match(ask.textContent, /Export this pane's transcript/);
    for (const w of EXPORT_WORDS) assert.ok(ask.textContent.includes(w));
    assert.equal(m.sent("exportTranscript").length, 0);
    await m.click(m.byText(".confirm .dialog-btn", /^Export transcript$/));
    assert.deepEqual(m.sent("exportTranscript"), [{ cmd: "exportTranscript", id: CART, confirmed: true }]);
    m.clear();
    await m.click((await pickMore(m, ".tools-export")));
    assert.ok(m.container.find(".confirm"), "asked again");
    await m.click(m.byText(".confirm .dialog-btn", /^Cancel$/));
    assert.equal(m.sent("exportTranscript").length, 0);
  }));

test("a question belongs to the pane it was asked for", () =>
  withPage({}, async (m) => {
    await m.connect();
    await m.open(CART);
    await m.click((await pickMore(m, ".tools-export")));
    assert.ok(m.container.find(".confirm"));
    await m.click(m.box(TAX));
    assert.equal(m.container.find(".confirm"), null);
    await m.click(m.box(CART));
    assert.equal(m.container.find(".confirm"), null, "and is dropped when the pane changes");
  }));

test("a shell has nothing to record or export, and a structured pane says why", () =>
  withPage({ g: graph({ [TAX]: { kind: "structured", structured: true } }), extra: { [DOCS]: { shell: true } } }, async (m) => {
    await m.connect();
    await m.open(DOCS);
    assert.equal((await pickMore(m, ".tools-record")), null);
    assert.equal((await pickMore(m, ".tools-export")), null);
    assert.ok((await pickMore(m, ".tools-info")), "a shell has pane info and a search");
    assert.ok((await pickMore(m, ".tools-find")));
    m.clear();
    await m.key("Ctrl+Alt+1");
    assert.ok(m.notice().includes(SHELL_RECORD_WORDS));
    assert.equal(m.sent("recordPane").length, 0);
    await m.open(TAX);
    assert.equal((await pickMore(m, ".tools-record")), null);
    assert.ok((await pickMore(m, ".tools")).textContent.includes(STRUCTURED_RECORD_WORDS));
    await m.key("Ctrl+Alt+2");
    assert.ok(m.notice().includes(STRUCTURED_RECORD_WORDS));
    assert.equal(m.sent("exportTranscript").length, 0);
  }));

test("the keys for record, export, reveal and the recordings folder act on the open pane", () =>
  withPage({ prefs: { recordingAcknowledged: true } }, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Alt+1");
    await m.key("Ctrl+Alt+3");
    assert.equal(m.sent("recordPane").length, 0, "no agent open");
    assert.ok(m.notice().includes(OPEN_AN_AGENT_WORDS));
    await m.key("Ctrl+Alt+4");
    assert.deepEqual(m.sent("openRecordings"), [{ cmd: "openRecordings" }], "the folder needs no pane");
    await m.open(CART);
    m.clear();
    await m.key("Ctrl+Alt+1");
    assert.deepEqual(m.sent("recordPane"), [{ cmd: "recordPane", id: CART, recording: true }]);
    await m.key("Ctrl+Alt+3");
    assert.deepEqual(m.sent("revealTranscript"), [{ cmd: "revealTranscript", id: CART }]);
    await m.key("Ctrl+Alt+2");
    assert.ok(m.container.find(".confirm"), "export asks");
    assert.equal(m.sent("exportTranscript").length, 0);
  }));

// ---- a window reached through the relay

test("a window reached through the relay says these are on the desktop and sends nothing", () =>
  withPage({ helloOver: { remote: true } }, async (m) => {
    await m.connect();
    m.clear();
    await m.key("Ctrl+Shift+R");
    assert.ok(m.notice().includes(HISTORY_REMOTE_WORDS));
    assert.equal(dialog(m), null);
    await m.key("Ctrl+Shift+L");
    assert.ok(m.notice().includes(DESK_FILES_WORDS));
    assert.equal(dialog(m), null);
    await m.key("Ctrl+Alt+4");
    assert.equal(m.sent("conversations").length + m.sent("findPane").length + m.sent("openRecordings").length, 0);
    assert.equal(m.container.find(".tabs-history"), null, "no button for what it cannot do");
    assert.equal(m.container.find(".tabs-find"), null);
  }));
