// The conversation's steps, images, search, Copy and Export, and the structured
// pane having no second list of its own.
// Elements are compared with assert.ok(a === b), never equal or deepEqual: a
// failing deepEqual on the fake DOM prints the whole tree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { groupEntries, defaultOpen, stepsWords, lastSaid } from "../assets/drawer/conv/steps.js";
import { imageSrc, decodedBytes, imageWords, sizeWords, ImageBody, MAX_IMAGE_BYTES, NOT_DRAWN_WORDS, TOO_BIG_WORDS } from "../assets/drawer/conv/Image.js";
import { Toolbar, snippetParts, NO_MATCH_WORDS, NOT_LOADED_WORDS, MORE_MATCHES_WORDS, EXPORT_UNAVAILABLE_WORDS, NOT_SENT_WORDS } from "../assets/drawer/conv/Toolbar.js";
import { entryText, conversationText } from "../assets/drawer/conv/text.js";
import { Log } from "../assets/drawer/conv/Log.js";
import { Conversation } from "../assets/drawer/Conversation.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const draw = (v) => renderToString(v);
const tool = (id, over = {}) => ({ id, kind: "tool", name: "Bash", label: "Bash " + id, status: "ok", summary: "s " + id, ...over });
const say = (id, kind = "reply") => (kind === "prompt" ? { id, kind, text: "words " + id } : { id, kind, markdown: "words " + id });

async function mount(props) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  render(html`<${Log} ...${props} />`, container);
  await settle();
  return {
    dom, container,
    redraw: async (p) => { render(html`<${Log} ...${p} />`, container); await settle(); },
    ids: () => container.findAll("[data-entry]").map((r) => r.getAttribute("data-entry")),
    done: () => { render(null, container); restore(); },
  };
}

// --- Steps -------------------------------------------------------------------

test("runs of two or more tool rows become a group, a lone tool row does not, and order and count are kept", () => {
  const entries = [say("a", "prompt"), tool("t1"), tool("t2"), { id: "h1", kind: "subagent", status: "ok" }, say("b"), tool("t3"), say("c"), tool("t4"), tool("t5")];
  const items = groupEntries(entries);
  assert.deepEqual(items.map((i) => (i.steps ? "steps:" + i.steps.map((s) => s.id).join("+") : i.entry.id)), ["a", "steps:t1+t2+h1", "b", "t3", "c", "steps:t4+t5"]);
  const flat = items.flatMap((i) => (i.steps ? i.steps : [i.entry]));
  assert.deepEqual(flat.map((e) => e.id), entries.map((e) => e.id));
  assert.equal(items[1].id, "steps:t1");
  assert.deepEqual(groupEntries([]), []);
});

test("a group is open after the last prompt or reply and closed before it", () => {
  const entries = [say("a", "prompt"), tool("t1"), tool("t2"), say("b"), tool("t3"), tool("t4")];
  const [, g1, , g2] = groupEntries(entries);
  assert.equal(lastSaid(entries), 3);
  assert.equal(defaultOpen(g1, entries), false);
  assert.equal(defaultOpen(g2, entries), true);
  const none = [tool("t1"), tool("t2")];
  assert.equal(defaultOpen(groupEntries(none)[0], none), true, "nothing said yet, so nothing finished");
});

test("the header counts the steps and notes the ones that failed or are running", () => {
  assert.equal(stepsWords([tool("a"), tool("b"), tool("c")]), "3 steps");
  assert.equal(stepsWords([tool("a"), tool("b", { status: "error" }), tool("c", { status: "running" }), tool("d", { status: "error" })]), "4 steps, 2 failed, 1 running");
});

test("steps before the last reply are drawn as a closed header; Enter and a click open them, and the choice stays as entries come in", async () => {
  const entries = [say("a", "prompt"), tool("t1"), tool("t2"), tool("t3"), say("b"), tool("t4"), tool("t5")];
  const m = await mount({ entries, atStart: true });
  try {
    assert.deepEqual(m.ids(), ["a", "steps:t1", "b", "steps:t4", "t4", "t5"]);
    const head = m.container.findAll(".conv-steps")[0];
    assert.equal(head.find(".conv-tool-title").textContent, "3 steps");
    assert.equal(head.find(".conv-tool-head").getAttribute("aria-expanded"), "false");
    head.keydown("Enter");
    await settle();
    assert.deepEqual(m.ids(), ["a", "steps:t1", "t1", "t2", "t3", "b", "steps:t4", "t4", "t5"]);
    // A click closes the open one after the last reply, and new entries leave both choices alone.
    m.container.findAll(".conv-steps")[1].find(".conv-tool-head").click();
    await settle();
    assert.deepEqual(m.ids(), ["a", "steps:t1", "t1", "t2", "t3", "b", "steps:t4"]);
    await m.redraw({ entries: [...entries, say("c", "prompt"), tool("t6"), tool("t7")], atStart: true });
    assert.deepEqual(m.ids(), ["a", "steps:t1", "t1", "t2", "t3", "b", "steps:t4", "c", "steps:t6", "t6", "t7"]);
  } finally {
    m.done();
  }
});

test("j and k step over a steps header like any row", async () => {
  const m = await mount({ entries: [say("a", "prompt"), tool("t1"), tool("t2"), say("b")], atStart: true });
  try {
    const list = m.container.find(".conv");
    list.focus();
    const rows = m.container.findAll("[data-entry]");
    list.keydown("j");
    assert.ok(m.dom.document.activeElement === rows[0]);
    rows[0].keydown("j");
    assert.equal(m.dom.document.activeElement.getAttribute("data-entry"), "steps:t1");
  } finally {
    m.done();
  }
});

test("a jump to a step inside a closed group opens the group and takes the keyboard to the row", async () => {
  const entries = [say("a", "prompt"), tool("t1"), tool("t2"), say("b")];
  const m = await mount({ entries, atStart: true });
  try {
    assert.deepEqual(m.ids(), ["a", "steps:t1", "b"]);
    await m.redraw({ entries, atStart: true, jump: { id: "t2", seq: 1 } });
    await settle();
    assert.deepEqual(m.ids(), ["a", "steps:t1", "t1", "t2", "b"]);
    assert.equal(m.dom.document.activeElement.getAttribute("data-entry"), "t2");
    // The same jump again does nothing; a new one does.
    await m.redraw({ entries, atStart: true, jump: { id: "a", seq: 2 } });
    await settle();
    assert.equal(m.dom.document.activeElement.getAttribute("data-entry"), "a");
  } finally {
    m.done();
  }
});

test("a permission card for a step in a closed group is drawn under the group's header", async () => {
  const entries = [say("a", "prompt"), tool("t1"), tool("t2", { status: "running" }), say("b")];
  const m = await mount({ entries, atStart: true, pending: { card: { tool: "Bash" }, node: html`<p class="here">card</p>` } });
  try {
    assert.ok(m.container.find(".conv-pending"), "the card was lost with its group closed");
    const kids = m.container.find(".conv-list").childNodes.filter((c) => c.localName === "li");
    assert.deepEqual(kids.map((k) => k.getAttribute("data-entry") || "pending"), ["a", "steps:t1", "pending", "b"]);
  } finally {
    m.done();
  }
});

// --- Images ------------------------------------------------------------------

const PNG = "iVBORw0KGgo=";

test("only the formats a browser draws without running anything are drawn, and only from base64", () => {
  assert.equal(imageSrc("image/png", PNG), "data:image/png;base64," + PNG);
  assert.equal(imageSrc("IMAGE/JPEG", "/9j/4AAQ"), "data:image/jpeg;base64,/9j/4AAQ");
  for (const type of ["image/svg+xml", "text/html", "", "image/png;base64,x", "javascript:alert(1)", "image/png\" onerror=\"x"]) {
    assert.equal(imageSrc(type, PNG), "", type);
  }
  for (const data of ["", "not base64!", PNG + "\"onerror=\"x", "data:image/png;base64,AA==", "AA==AA", undefined, 5]) {
    assert.equal(imageSrc("image/png", data), "", String(data));
  }
});

test("an image is described by its type, size in pixels and size in bytes", () => {
  assert.equal(imageWords({ mediaType: "image/png", width: 800, height: 600, bytes: 12595 }), "Image (image/png, 800 by 600, 12.3 KB)");
  assert.equal(imageWords({}), "Image (unknown type)");
  assert.equal(sizeWords(5), "5 B");
  assert.equal(sizeWords(3 * 1024 * 1024), "3.0 MB");
  assert.equal(sizeWords(-1), "");
  assert.equal(decodedBytes("AA=="), 1);
  assert.equal(decodedBytes("AAA="), 2);
  assert.equal(decodedBytes("AAAA"), 3);
});

test("an image row asks for its picture when opened, draws it from a data address, and the picture toggles its size", async () => {
  const asked = [];
  const entries = [{ id: "i1", kind: "image", mediaType: "image/png", width: 2, height: 2, bytes: 8, hasDetail: true }];
  const props = (details) => ({ entries, atStart: true, details, onDetail: (id) => asked.push(id) });
  const m = await mount(props({}));
  try {
    assert.equal(m.container.findAll("img").length, 0, "drawn before it was asked for");
    m.container.find(".conv-tool-head").click();
    await settle();
    assert.deepEqual(asked, ["i1"]);
    await m.redraw(props({ i1: { state: "loading" } }));
    assert.equal(m.container.find("[data-detail]").getAttribute("data-detail"), "loading");
    await m.redraw(props({ i1: { state: "ok", detail: { data: PNG } } }));
    const imgs = m.container.findAll("img");
    assert.equal(imgs.length, 1);
    assert.equal(imgs[0].getAttribute("src"), "data:image/png;base64," + PNG);
    const btn = m.container.find(".conv-image");
    assert.equal(btn.getAttribute("aria-pressed"), "false");
    btn.click();
    await settle();
    assert.equal(m.container.find(".conv-image").getAttribute("aria-pressed"), "true");
    assert.ok(m.container.find(".conv-image").getAttribute("class").includes("is-big"));
    await m.redraw(props({ i1: { state: "gone" } }));
    assert.equal(m.container.findAll("img").length, 0);
    assert.equal(m.container.find("[data-detail]").getAttribute("data-detail"), "gone");
  } finally {
    m.done();
  }
});

test("an image that is not a drawable type, is not base64, or is too large is not drawn, and says so", () => {
  const e = { id: "i", kind: "image", mediaType: "image/png" };
  const bad = (type, data) => draw(html`<${ImageBody} entry=${{ ...e, mediaType: type }} detail=${{ state: "ok", detail: { data } }} />`);
  assert.ok(bad("image/svg+xml", PNG).includes(NOT_DRAWN_WORDS));
  assert.ok(bad("image/png", "<script>").includes(NOT_DRAWN_WORDS));
  assert.ok(!bad("image/png", "<script>").includes("<img"));
  assert.ok(bad("image/png", "A".repeat(Math.ceil((MAX_IMAGE_BYTES * 4) / 3) + 8)).includes(TOO_BIG_WORDS));
  assert.equal(draw(html`<${ImageBody} entry=${e} detail=${null} />`), "");
});

test("an image entry with nothing held is a line, not a button", async () => {
  const m = await mount({ entries: [{ id: "i1", kind: "image", mediaType: "image/png" }], atStart: true });
  try {
    assert.equal(m.container.findAll("button").length, 0);
    assert.ok(m.container.find(".conv-image-row").textContent.includes("Image (image/png)"));
  } finally {
    m.done();
  }
});

// --- Copy as text ------------------------------------------------------------

test("entries copy as plain text in order, with thinking left out", () => {
  const text = conversationText([
    say("a", "prompt"), say("b"), { id: "x", kind: "thinking", chars: 9 }, tool("t", { summary: "ls" }),
    { id: "h", kind: "subagent", description: "check", status: "ok", report: "found it" },
    { id: "c", kind: "compaction" }, { id: "n", kind: "notice", text: "restarted" },
    { id: "i", kind: "image", mediaType: "image/png", width: 1, height: 1, bytes: 3 }, { id: "u", kind: "mystery" },
  ]);
  assert.equal(text, ["You: words a", "Agent: words b", "Tool: Bash t - ls [ok]", "Helper: check [ok]\nfound it", "(conversation compacted)", "(restarted)", "(Image (image/png, 1 by 1, 3 B))"].join("\n\n"));
  assert.equal(entryText({ kind: "thinking" }), "");
  assert.equal(conversationText([]), "");
});

// --- The toolbar -------------------------------------------------------------

async function mountToolbar(props) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const draw2 = (p) => render(html`<${Toolbar} ...${p} />`, container);
  draw2(props);
  await settle();
  return { dom, container, draw: async (p) => { draw2(p); await settle(); }, done: () => { render(null, container); restore(); } };
}

test("a hit is marked by its offset and the length of the query, counted in characters", () => {
  assert.deepEqual(snippetParts("find the needle here", 9, 6), ["find the ", "needle", " here"]);
  assert.deepEqual(snippetParts("😀 hit", 2, 3), ["😀 ", "hit", ""]);
  assert.deepEqual(snippetParts("abc", 9, 3), ["abc", "", ""]);
  assert.deepEqual(snippetParts("", 0, 0), ["", "", ""]);
});

test("the search form sends the query when submitted, lists matches, takes a loaded one to its entry and says an earlier one is not loaded", async () => {
  const sent = [];
  const jumps = [];
  const entries = [say("e1"), say("e2")];
  const search = { q: "needle", state: "done", more: true, matches: [
    { entryId: "e2", kind: "reply", snippet: "a needle b", offset: 2 },
    { entryId: "old", kind: "prompt", snippet: "needle", offset: 0 },
  ] };
  const base = { entries, onSearch: (q) => (sent.push(q), true), onClear() {}, onJump: (id) => jumps.push(id) };
  const m = await mountToolbar({ ...base, search: null });
  try {
    m.container.find(".conv-search-input").enter("needle");
    await settle();
    m.container.find(".conv-search").dispatch("submit");
    await settle();
    assert.deepEqual(sent, ["needle"]);
    await m.draw({ ...base, search: { q: "needle", state: "loading", matches: [], more: false } });
    assert.ok(m.container.find(".conv-results-note").textContent.includes("Searching"));
    await m.draw({ ...base, search });
    const results = m.container.findAll(".conv-result");
    assert.equal(results.length, 2);
    assert.equal(results[0].find("mark").textContent, "needle");
    assert.ok(!results[0].textContent.includes(NOT_LOADED_WORDS));
    assert.ok(results[1].textContent.includes(NOT_LOADED_WORDS));
    results[0].click();
    results[1].click();
    await settle();
    assert.deepEqual(jumps, ["e2"], "a match that is not loaded was jumped to");
    assert.ok(m.container.textContent.includes(MORE_MATCHES_WORDS));
    await m.draw({ ...base, search: { q: "zzz", state: "done", matches: [], more: false } });
    assert.ok(m.container.find(".conv-results-note").textContent.includes(NO_MATCH_WORDS));
  } finally {
    m.done();
  }
});

test("a match is drawn as text", async () => {
  const m = await mountToolbar({ entries: [say("e1")], search: { q: "x", state: "done", more: false, matches: [{ entryId: "e1", kind: "reply", snippet: "<img src=x onerror=alert(1)>", offset: 0 }] }, onSearch() { return true; }, onClear() {}, onJump() {} });
  try {
    assert.equal(m.container.findAll("img").length, 0);
    assert.ok(m.container.find(".conv-result-text").textContent.includes("<img src=x onerror=alert(1)>"));
  } finally {
    m.done();
  }
});

test("a search the store did not send says so, an empty one clears, and Clear drops the search", async () => {
  const cleared = [];
  const m = await mountToolbar({ entries: [], search: null, onSearch: () => false, onClear: () => cleared.push(1), onJump() {} });
  try {
    m.container.find(".conv-search-input").enter("abc");
    await settle();
    m.container.find(".conv-search").dispatch("submit");
    await settle();
    assert.equal(m.container.find(".conv-search-note").textContent, NOT_SENT_WORDS);
    m.container.find(".conv-search-input").enter("   ");
    await settle();
    m.container.find(".conv-search").dispatch("submit");
    await settle();
    assert.equal(cleared.length, 1);
    assert.equal(m.container.find(".conv-search-note"), null);
    m.container.find(".conv-search-clear") && m.container.find(".conv-search-clear").click();
  } finally {
    m.done();
  }
});

test("Copy puts the loaded entries on the clipboard and says how many; Export is a button only for a pane that has an exporter", async () => {
  const copied = [];
  const exported = [];
  const entries = [say("a", "prompt"), say("b")];
  const m = await mountToolbar({ entries, onSearch() { return true; }, onClear() {}, onJump() {}, copy: async (t) => (copied.push(t), true) });
  try {
    m.container.find(".conv-copy").click();
    await settle();
    assert.deepEqual(copied, ["You: words a\n\nAgent: words b"]);
    assert.equal(m.container.find(".conv-copied").textContent, "Copied 2 entries that are loaded.");
    assert.equal(m.container.find(".conv-export"), null);
    assert.equal(m.container.find(".conv-export-note").textContent, EXPORT_UNAVAILABLE_WORDS);
    await m.draw({ entries, onSearch() { return true; }, onClear() {}, onJump() {}, canExport: true, onExport: () => exported.push(1), copy: async () => false });
    m.container.find(".conv-export").click();
    assert.equal(exported.length, 1);
    m.container.find(".conv-copy").click();
    await settle();
    assert.equal(m.container.find(".conv-copied").textContent, "Couldn't copy.");
    await m.draw({ entries: [], onSearch() { return true; }, onClear() {}, onJump() {} });
    assert.ok(m.container.find(".conv-copy").disabled === true || m.container.find(".conv-copy").getAttribute("disabled") !== null, "Copy on nothing");
  } finally {
    m.done();
  }
});

test("a jump from a search result lands on the entry in the conversation panel", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const container = dom.document.createElement("div");
    dom.body.appendChild(container);
    const conv = { id: "p", supported: true, entries: [say("e1", "prompt"), say("e2"), say("e3", "prompt")], atStart: true, older: "idle", refused: "" };
    const search = { q: "words", state: "done", more: false, matches: [{ entryId: "e2", kind: "reply", snippet: "words e2", offset: 0 }] };
    render(html`<${Conversation} conv=${conv} search=${search} onSearch=${() => true} onClearSearch=${() => {}} />`, container);
    await settle();
    container.find(".conv-result").click();
    await settle();
    await settle();
    assert.equal(dom.document.activeElement.getAttribute("data-entry"), "e2");
    // A conversation not yet there, or refused, has no toolbar.
    render(html`<${Conversation} conv=${null} />`, container);
    assert.equal(container.find(".conv-toolbar"), null);
  } finally {
    restore();
  }
});

// --- The store's search ------------------------------------------------------

async function up() {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  return { ...h, ws: FakeWebSocket.last };
}

test("searchConversation sends the query for the open conversation, keeps the matching answer, and ignores the others", async () => {
  const { store, ws } = await up();
  assert.equal(store.searchConversation("needle"), false, "asked with no conversation open");
  store.watchConversation("p1");
  assert.equal(store.searchConversation("  needle "), true);
  assert.deepEqual(ws.commands("conversationSearch"), [{ cmd: "conversationSearch", id: "p1", q: "  needle " }]);
  assert.equal(store.getState().search.state, "loading");
  // The desktop trims the query, and answers with it so.
  ws.frame({ type: "conversationSearch", id: "other", q: "needle", matches: [{ entryId: "x", kind: "reply", snippet: "s", offset: 0 }], more: false });
  ws.frame({ type: "conversationSearch", id: "p1", q: "another", matches: [{ entryId: "y", kind: "reply", snippet: "s", offset: 0 }], more: false });
  assert.equal(store.getState().search.state, "loading", "an answer for something else was kept");
  ws.frame({ type: "conversationSearch", id: "p1", q: "needle", matches: [{ entryId: "e2", kind: "reply", snippet: "a needle", offset: 2.5 }, { entryId: "", kind: "x" }, null, { entryId: "e3", kind: "tool", snippet: "t", offset: -1 }], more: true });
  const s = store.getState().search;
  assert.equal(s.state, "done");
  assert.equal(s.more, true);
  assert.deepEqual(s.matches.map((m) => [m.entryId, m.offset]), [["e2", 0], ["e3", 0]]);
});

test("a query over 200 characters is matched to the desktop's cut of it", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  const long = "é".repeat(250);
  store.searchConversation(long);
  ws.frame({ type: "conversationSearch", id: "p1", q: "é".repeat(200), matches: [], more: false });
  assert.equal(store.getState().search.state, "done");
});

test("an empty query clears the search, and a search goes when the conversation changes or closes", async () => {
  const { store } = await up();
  store.watchConversation("p1");
  store.searchConversation("a");
  assert.equal(store.searchConversation("   "), false);
  assert.equal(store.getState().search, null);
  store.searchConversation("a");
  store.clearSearch();
  assert.equal(store.getState().search, null);
  store.searchConversation("a");
  store.watchConversation("p2");
  assert.equal(store.getState().search, null);
  store.searchConversation("a");
  store.unwatchConversation();
  assert.equal(store.getState().search, null);
});

test("a remote window sends no search", async () => {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello({ remote: true }));
  h.store.watchConversation("p1");
  assert.equal(h.store.searchConversation("a"), false);
  assert.equal(FakeWebSocket.last.commands("conversationSearch").length, 0);
  assert.equal(h.store.getState().search, null);
});
