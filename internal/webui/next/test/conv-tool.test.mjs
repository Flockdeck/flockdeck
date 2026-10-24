// Tool detail in the conversation: the size cuts and what they say, diffs, the
// states of a detail being asked for, a helper's steps one level down, the
// store's detail and permission bookkeeping, and the permission card drawn in the
// stream as well as in the strip.
// Elements are compared with assert.ok(a === b), never equal or deepEqual: a
// failing deepEqual on the fake DOM prints the whole tree.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../assets/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../assets/html.js";
import { capBytes, cutWords } from "../assets/drawer/conv/cap.js";
import { Diff, diffLines, DIFF_CAP, DIFF_CHUNK } from "../assets/drawer/conv/Diff.js";
import { ToolBody, Output, OUTPUT_CAP, GONE_WORDS, LOADING_DETAIL_WORDS, NOT_ASKED_WORDS, pendingAfter } from "../assets/drawer/conv/Tool.js";
import { Row, MAX_NESTED } from "../assets/drawer/conv/rows.js";
import { Log } from "../assets/drawer/conv/Log.js";
import { PermissionCard, StreamPermission } from "../assets/drawer/Prompt.js";
import { MAX_DETAILS } from "../assets/lib/store.js";
import { useStore } from "../assets/lib/use-store.js";
import { createDom, install, settle } from "./dom.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

const draw = (v) => renderToString(v);

// --- Cuts --------------------------------------------------------------------

test("capBytes cuts at a byte count, never inside a character, and counts what is left out", () => {
  assert.deepEqual(capBytes("abc", 10), { text: "abc", more: 0 });
  assert.deepEqual(capBytes("abcdef", 4), { text: "abcd", more: 2 });
  assert.deepEqual(capBytes("abcd", 4), { text: "abcd", more: 0 });
  // é is two bytes: a cut after one of them leaves the whole character out.
  assert.deepEqual(capBytes("aé", 2), { text: "a", more: 2 });
  assert.deepEqual(capBytes("aé", 3), { text: "aé", more: 0 });
  // Three-byte and four-byte characters.
  assert.deepEqual(capBytes("€€€", 7), { text: "€€", more: 3 });
  assert.deepEqual(capBytes("😀😀", 5), { text: "😀", more: 4 });
  assert.deepEqual(capBytes(undefined, 5), { text: "", more: 0 });
});

test("the words for a cut say what was cut and how many bytes", () => {
  assert.equal(cutWords("Output", 5904), "Output truncated: 5,904 bytes more are not shown.");
  assert.equal(cutWords("Diff", 1), "Diff truncated: 1 byte more is not shown.");
});

test("output over the cap is drawn to the cap and says how much more there is", () => {
  const out = draw(html`<${Output} text=${"x".repeat(OUTPUT_CAP + 1500)} />`);
  assert.ok(out.includes("x".repeat(OUTPUT_CAP)) && !out.includes("x".repeat(OUTPUT_CAP + 1)));
  assert.ok(out.includes("Output truncated: 1,500 bytes more are not shown."), out.slice(-200));
  const whole = draw(html`<${Output} text=${"y".repeat(OUTPUT_CAP)} />`);
  assert.ok(!whole.includes("truncated"), "a cut was said for output that fit");
});

test("output is drawn as text", () => {
  const out = draw(html`<${Output} text=${"<img src=x onerror=alert(1)> <b>x</b>"} />`);
  assert.ok(!out.includes("<img") && !out.includes("<b>"), out);
});

// --- Diffs -------------------------------------------------------------------

test("a diff's lines are told apart by their first character", () => {
  const rows = diffLines(["--- a/f.go", "+++ b/f.go", "@@ -1,3 +1,3 @@", " keep", "-old", "+new", "--x", "++y"].join("\n") + "\n");
  assert.deepEqual(rows.map((r) => r.kind), ["head", "head", "hunk", "ctx", "del", "add", "del", "add"]);
  assert.equal(rows[4].text, "-old", "the first character was dropped");
  assert.deepEqual(diffLines(""), []);
});

const bigDiff = (n) => "@@ -1 +1 @@\n" + Array.from({ length: n - 1 }, (_, i) => (i % 3 === 0 ? "+added " + i : i % 3 === 1 ? "-removed " + i : " same " + i)).join("\n");

test("a diff of 5000 lines draws added and removed lines with their marks as text as well as their colour, a chunk at a time", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const container = dom.document.createElement("div");
    dom.body.appendChild(container);
    render(html`<${Diff} text=${bigDiff(5000)} />`, container);
    await settle();
    const lines = () => container.findAll("[data-diff-kind]");
    assert.equal(lines().length, DIFF_CHUNK, "the first chunk");
    const add = container.find(".conv-diff-add");
    const del = container.find(".conv-diff-del");
    assert.ok(add && add.textContent.startsWith("+added"), "an added line lost its +");
    assert.ok(del && del.textContent.startsWith("-removed"), "a removed line lost its -");
    assert.equal(lines()[0].getAttribute("data-diff-kind"), "hunk");
    const more = container.find(".conv-diff-more");
    assert.match(more.textContent, /Show 400 more lines \(4,600 not drawn yet\)/);
    more.click();
    await settle();
    assert.equal(lines().length, 2 * DIFF_CHUNK);
    for (let i = 0; i < 12; i++) {
      const b = container.find(".conv-diff-more");
      if (!b) break;
      b.click();
      await settle();
    }
    assert.equal(lines().length, 5000, "the whole diff is reachable");
    assert.ok(container.find(".conv-diff-more") === null);
  } finally {
    restore();
  }
});

test("a diff over its cap says how many bytes more there are, and draws no markup", () => {
  const out = draw(html`<${Diff} text=${"@@ -1 +1 @@\n+" + "z".repeat(DIFF_CAP) + "\n+<img src=x onerror=alert(1)>"} />`);
  assert.match(out, /Diff truncated: [\d,]+ bytes more are not shown\./);
  assert.ok(!out.includes("<img"), out.slice(0, 300));
  const small = draw(html`<${Diff} text=${"@@ -1 +1 @@\n+<img src=x onerror=alert(1)>"} />`);
  assert.ok(small.includes("&lt;img") && !small.includes("<img"), small);
});

// --- The states of a tool row's body -----------------------------------------

const tool = (over = {}) => ({ id: "t1", kind: "tool", name: "Bash", label: "Bash go test", status: "ok", summary: "go test ./...", hasDetail: true, ...over });

test("a body says it is loading, that it is no longer available, or that it was not asked for", () => {
  assert.ok(draw(html`<${ToolBody} entry=${tool()} detail=${{ state: "loading" }} />`).includes(LOADING_DETAIL_WORDS));
  const gone = draw(html`<${ToolBody} entry=${tool()} detail=${{ state: "gone" }} />`);
  assert.ok(gone.includes(GONE_WORDS) && gone.includes('data-detail="gone"'), gone);
  assert.ok(draw(html`<${ToolBody} entry=${tool()} />`).includes(NOT_ASKED_WORDS));
  assert.equal(draw(html`<${ToolBody} entry=${tool({ hasDetail: false })} />`), "");
});

test("a body draws the output and the diff the desktop sent, and an inline diff without asking", () => {
  const out = draw(html`<${ToolBody} entry=${tool()} detail=${{ state: "ok", detail: { text: "PASS\nok  pkg 0.1s", diff: "@@ -1 +1 @@\n-a\n+b" } }} />`);
  assert.ok(out.includes("PASS") && out.includes("conv-diff-add") && out.includes("conv-diff-del"), out);
  const inline = draw(html`<${ToolBody} entry=${tool({ hasDetail: false, diff: "@@ -1 +1 @@\n-a\n+b" })} />`);
  assert.ok(inline.includes("conv-diff-add"), inline);
});

// --- A tool row in the list --------------------------------------------------

async function mount(props, wrap = null) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  render(wrap ? wrap(props) : html`<${Log} ...${props} />`, container);
  await settle();
  return { dom, container, done: () => { render(null, container); restore(); } };
}

test("opening a tool row asks for its detail once, and a row with nothing held asks for nothing", async () => {
  const asked = [];
  const entries = [{ id: "p1", kind: "prompt", text: "hi" }, tool({ id: "t1" }), tool({ id: "t2", hasDetail: false })];
  const m = await mount({ entries, atStart: true, onDetail: (id) => asked.push(id), details: {} });
  try {
    const head = (id) => m.container.findAll(".conv-tool").find((r) => r.getAttribute("data-entry") === id).find(".conv-tool-head");
    head("t2").click();
    await settle();
    assert.deepEqual(asked, [], "asked for a row with no detail");
    head("t1").click();
    await settle();
    assert.deepEqual(asked, ["t1"]);
    head("t1").click(); // closes
    await settle();
    assert.deepEqual(asked, ["t1"], "asked on closing");
  } finally {
    m.done();
  }
});

test("Enter on a tool row opens it and asks for its detail", async () => {
  const asked = [];
  const m = await mount({ entries: [tool({ id: "t1" })], atStart: true, onDetail: (id) => asked.push(id), details: {} });
  try {
    m.container.find(".conv-tool").keydown("Enter");
    await settle();
    assert.deepEqual(asked, ["t1"]);
    assert.ok(m.container.find(".conv-tool-summary"), "Enter did not open the row");
  } finally {
    m.done();
  }
});

test("the answer for an opened row is drawn in it: loading, then the output, or gone", async () => {
  const entries = [tool({ id: "t1" })];
  let details = {};
  const props = () => ({ entries, atStart: true, onDetail() {}, details });
  const m = await mount(props());
  try {
    m.container.find(".conv-tool-head").click();
    await settle();
    assert.ok(m.container.find("[data-detail]") === null, "a body for a row nothing was asked for");
    details = { t1: { state: "loading" } };
    render(html`<${Log} ...${props()} />`, m.container);
    await settle();
    assert.equal(m.container.find("[data-detail]").getAttribute("data-detail"), "loading");
    details = { t1: { state: "ok", detail: { text: "all <good>" } } };
    render(html`<${Log} ...${props()} />`, m.container);
    await settle();
    assert.equal(m.container.find(".conv-output-text").textContent, "all <good>");
    assert.equal(m.container.findAll("good").length, 0);
    details = { t1: { state: "gone" } };
    render(html`<${Log} ...${props()} />`, m.container);
    await settle();
    assert.equal(m.container.find("[data-detail]").textContent, GONE_WORDS);
  } finally {
    m.done();
  }
});

test("a helper agent's entries are drawn one level down, and a helper inside one is a line", async () => {
  const inner = [
    { id: "n1", kind: "tool", name: "Read", label: "Read a.go", status: "ok", summary: "a.go", diff: "@@ -1 +1 @@\n-a\n+b" },
    { id: "n2", kind: "subagent", description: "deeper", status: "ok", hasDetail: true },
    { id: "n3", kind: "reply", markdown: "done" },
  ];
  const sub = { id: "s1", kind: "subagent", description: "check the tax code", status: "ok", report: "found two", hasDetail: true };
  const m = await mount({ entries: [sub], atStart: true, onDetail() {}, details: { s1: { state: "ok", detail: { entries: inner } } } });
  try {
    m.container.find(".conv-tool-head").click();
    await settle();
    const nested = m.container.find(".conv-nested");
    assert.ok(nested, "no nested list");
    assert.equal(nested.findAll(".conv-row").length, 3);
    assert.equal(m.container.findAll("[data-entry]").length, 1, "a nested row is a row of the list");
    assert.ok(m.container.find(".conv-tool-report").textContent.includes("found two"));
    // The inner helper opens to its summary and no list of its own.
    const deeper = nested.findAll(".conv-tool").find((r) => r.getAttribute("data-kind") === "subagent");
    deeper.find(".conv-tool-head").click();
    await settle();
    assert.equal(m.container.findAll(".conv-nested").length, 1, "a second level was drawn");
    assert.ok(!nested.find(".conv-diff-add"), "a diff was drawn before its row was opened");
    nested.findAll(".conv-tool").find((r) => r.getAttribute("data-kind") === "tool").find(".conv-tool-head").click();
    await settle();
    assert.ok(nested.find(".conv-diff-add"), "the inner tool's diff was not drawn");
  } finally {
    m.done();
  }
});

test("a helper with more than MAX_NESTED steps draws that many and says how many are left", async () => {
  const inner = Array.from({ length: MAX_NESTED + 7 }, (_, i) => ({ id: "n" + i, kind: "prompt", text: "step " + i }));
  const m = await mount({ entries: [{ id: "s1", kind: "subagent", description: "d", status: "ok", hasDetail: true }], atStart: true, details: { s1: { state: "ok", detail: { entries: inner } } } });
  try {
    m.container.find(".conv-tool-head").click();
    await settle();
    assert.equal(m.container.find(".conv-nested").findAll(".conv-prompt").length, MAX_NESTED);
    assert.ok(m.container.find(".conv-nested").textContent.includes("7 more steps are not shown."));
  } finally {
    m.done();
  }
});

// --- A permission drawn where it was asked -----------------------------------

test("a card is drawn after the running tool it was asked for, else the latest running tool, else at the end", () => {
  const entries = [
    tool({ id: "a", status: "running", name: "Edit" }),
    tool({ id: "b", status: "running", name: "Bash" }),
    tool({ id: "c", status: "ok", name: "Bash" }),
    { id: "d", kind: "reply", markdown: "x" },
  ];
  assert.equal(pendingAfter(entries, { tool: "Edit" }), "a");
  assert.equal(pendingAfter(entries, { tool: "bash" }), "b");
  assert.equal(pendingAfter(entries, { tool: "Other" }), "b", "the latest running tool");
  assert.equal(pendingAfter(entries.slice(2), { tool: "Bash" }), "");
  assert.equal(pendingAfter([], null), "");
});

test("the log draws the pending card right after its entry, and at the end when that entry is not drawn", async () => {
  const entries = [tool({ id: "a", status: "running" }), { id: "b", kind: "reply", markdown: "later" }];
  const node = html`<p class="here">card</p>`;
  const m = await mount({ entries, atStart: true, pending: { card: { tool: "Bash" }, node } });
  try {
    const kids = m.container.find(".conv-list").childNodes.filter((c) => c.localName === "li");
    assert.deepEqual(kids.map((k) => k.getAttribute("data-entry") || (k.getAttribute("data-pending") !== null ? "pending" : "?")), ["a", "pending", "b"]);
  } finally {
    m.done();
  }
  const m2 = await mount({ entries: [{ id: "b", kind: "reply", markdown: "x" }], atStart: true, pending: { card: { tool: "Bash" }, node } });
  try {
    const kids = m2.container.find(".conv-list").childNodes.filter((c) => c.localName === "li");
    assert.equal(kids[kids.length - 1].getAttribute("data-pending"), "");
  } finally {
    m2.done();
  }
  const empty = draw(html`<${Log} entries=${[]} atStart=${true} pending=${{ card: { tool: "Bash" }, node }}><p>none</p><//>`);
  assert.ok(empty.includes("card") && empty.includes("none"), "the card was lost with no entries");
});

// --- The store: detail and one answer for each card --------------------------

async function up() {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello());
  return { ...h, ws: FakeWebSocket.last };
}

test("requestDetail asks once for an entry, keeps the answer, and draws gone for an entry the desktop dropped", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  assert.equal(store.requestDetail("t1"), true);
  assert.equal(store.requestDetail("t1"), true);
  assert.deepEqual(ws.commands("conversationDetail"), [{ cmd: "conversationDetail", id: "p1", entry: "t1" }]);
  assert.equal(store.getState().details.t1.state, "loading");
  ws.frame({ type: "conversationDetail", id: "p1", entry: "t1", detail: { text: "out" } });
  assert.deepEqual(store.getState().details.t1.detail, { text: "out" });
  assert.equal(store.requestDetail("t1"), true);
  assert.equal(ws.commands("conversationDetail").length, 1, "asked again for an answer it holds");
  // An answer for an entry nobody asked for, or for another pane, is not kept.
  ws.frame({ type: "conversationDetail", id: "p1", entry: "zz", detail: { text: "x" } });
  ws.frame({ type: "conversationDetail", id: "other", entry: "t1", detail: { text: "x" } });
  assert.equal(store.getState().details.zz, undefined);
  assert.equal(store.getState().details.t1.detail.text, "out");
  // Gone is kept, and asking again is allowed.
  assert.equal(store.requestDetail("t2"), true);
  ws.frame({ type: "conversationDetail", id: "p1", entry: "t2", gone: true });
  assert.equal(store.getState().details.t2.state, "gone");
  assert.equal(store.requestDetail("t2"), true);
  assert.equal(ws.commands("conversationDetail").length, 3);
});

test("the store holds at most MAX_DETAILS answers, dropping the oldest asked for", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  for (let i = 0; i < MAX_DETAILS + 5; i++) store.requestDetail(String(i));
  const keys = Object.keys(store.getState().details);
  assert.equal(keys.length, MAX_DETAILS);
  assert.ok(!keys.includes("0") && keys.includes(String(MAX_DETAILS + 4)), keys.join(","));
  assert.equal(ws.commands("conversationDetail").length, MAX_DETAILS + 5);
});

test("details go when the conversation is closed, changed, or started over", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  store.requestDetail("t1");
  ws.frame({ type: "conversationPage", id: "p1", supported: true, entries: [{ id: "x", kind: "prompt", text: "a" }], atStart: true, reset: true });
  assert.deepEqual(Object.keys(store.getState().details), []);
  store.requestDetail("t1");
  store.watchConversation("p2");
  assert.deepEqual(Object.keys(store.getState().details), []);
  store.requestDetail("t1");
  store.unwatchConversation();
  assert.deepEqual(Object.keys(store.getState().details), []);
  assert.equal(store.requestDetail("t1"), false, "asked with no conversation open");
});

test("a remote window asks for no detail", async () => {
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello({ remote: true }));
  h.store.watchConversation("p1");
  assert.equal(h.store.requestDetail("t1"), false);
  assert.equal(FakeWebSocket.last.commands("conversationDetail").length, 0);
});

const isDisabled = (b) => b.disabled === true || b.getAttribute("disabled") !== null;
const answer = (over = {}) => ({ cmd: "permissionAnswer", id: "p1", cardSeq: 7, requestId: "req-1", optionId: "once", ...over });

test("the store sends one answer for a card however many times it is asked, and again after a refusal", async () => {
  const { store, ws } = await up();
  assert.equal(store.send(answer()), true);
  assert.equal(store.send(answer({ optionId: "always" })), true, "a second ask is not a failure");
  assert.deepEqual(ws.commands("permissionAnswer").map((c) => c.optionId), ["once"]);
  assert.deepEqual({ ...store.getState().asking, after: 0 }, { id: "p1", cardSeq: 7, requestId: "req-1", after: 0 });
  // The next card is its own.
  assert.equal(store.send(answer({ cardSeq: 8, requestId: "req-2" })), true);
  assert.equal(ws.commands("permissionAnswer").length, 2);
  // A refusal frees only its card.
  ws.frame({ type: "permissionAnswer", ok: false, error: "ended", id: "p1", cardSeq: 7, requestId: "req-1" });
  assert.equal(store.getState().asking, null);
  assert.equal(store.send(answer({ optionId: "always" })), true);
  assert.deepEqual(ws.commands("permissionAnswer").map((c) => c.optionId), ["once", "once", "always"]);
  // Not sent (a remote window): not remembered as sent.
  const h = await harness();
  h.store.start();
  FakeWebSocket.last.open();
  FakeWebSocket.last.frame(hello({ remote: true }));
  assert.equal(h.store.send(answer()), false);
  assert.equal(h.store.getState().asking, null);
});

// --- Two copies of one card --------------------------------------------------

test("the card in the strip and the card in the stream give one answer, once, whichever is pressed and however often", async () => {
  const { store, ws } = await up();
  const dom = createDom();
  const restore = install(dom);
  try {
    const container = dom.document.createElement("div");
    dom.body.appendChild(container);
    const node = { pane: "p1", name: "cart", structured: true };
    const card = { seq: 7, requestId: "req-1", tool: "Bash", command: "ls", options: [{ id: "once", name: "Allow once", kind: "allow_once" }, { id: "no", name: "No", kind: "reject_once" }] };
    function App() {
      const snap = useStore(store);
      return html`<div>
        <section class="strip"><${PermissionCard} node=${node} card=${card} snap=${snap} store=${store} /></section>
        <section class="stream"><${StreamPermission} node=${node} card=${card} snap=${snap} store=${store} /></section>
      </div>`;
    }
    render(html`<${App} />`, container);
    await settle();
    const button = (where, id) => container.find(where).findAll(".prompt-choice").find((b) => b.getAttribute("data-option") === id);
    assert.equal(container.findAll(".prompt-choice").length, 4);
    assert.ok(container.find(".stream").find(".prompt-title").textContent.includes("Bash"));
    button(".stream", "once").click();
    button(".stream", "once").click(); // a double click
    button(".strip", "no").click(); // and the other copy
    await settle();
    assert.deepEqual(ws.commands("permissionAnswer").map((c) => c.optionId), ["once"]);
    for (const b of container.findAll(".prompt-choice")) assert.ok(isDisabled(b), "a copy of the card can still be pressed");
    ws.frame({ type: "permissionAnswer", ok: true, id: "p1", cardSeq: 7, requestId: "req-1" });
    await settle();
    assert.deepEqual(container.findAll(".prompt-status").map((s) => s.textContent), ["Answered.", "Answered."]);
    // A refusal shows in both and frees both.
    ws.frame({ type: "permissionAnswer", ok: false, error: "That request has ended", id: "p1", cardSeq: 7, requestId: "req-1" });
    await settle();
    assert.deepEqual(container.findAll(".prompt-status").map((s) => s.textContent), ["That request has ended", "That request has ended"]);
    for (const b of container.findAll(".prompt-choice")) assert.ok(!isDisabled(b), "a copy stayed shut after a refusal");
    button(".strip", "no").click();
    await settle();
    assert.deepEqual(ws.commands("permissionAnswer").map((c) => c.optionId), ["once", "no"]);
  } finally {
    restore();
  }
});

test("a card made for the relay draws no details in the stream either", () => {
  const out = draw(html`<${StreamPermission} node=${{ pane: "p1" }} card=${{ seq: 1, requestId: "r", tool: "Bash", redacted: true, command: "SECRET-COMMAND", options: [] }} snap=${{ remote: false, answer: null, asking: null }} store=${{ send() { return false; } }} />`);
  assert.ok(!out.includes("SECRET-COMMAND"), out);
});

test("an entry id of __proto__, toString or constructor is an ordinary key of the details", async () => {
  const { store, ws } = await up();
  store.watchConversation("p1");
  for (const id of ["__proto__", "toString", "constructor"]) {
    assert.equal(store.getState().details[id], undefined, id + " was there before it was asked for");
    assert.equal(store.requestDetail(id), true);
    assert.equal(store.getState().details[id].state, "loading", id);
    ws.frame({ type: "conversationDetail", id: "p1", entry: id, detail: { text: "out " + id } });
    assert.equal(store.getState().details[id].detail.text, "out " + id);
  }
  assert.equal(Object.getPrototypeOf(store.getState().details), null);
  assert.equal(ws.commands("conversationDetail").length, 3);
});
