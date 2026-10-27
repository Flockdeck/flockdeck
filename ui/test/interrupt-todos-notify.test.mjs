// Esc and Ctrl+C stop a running turn, the agent's plan is drawn as a checklist, and a structured pane
// that needs the person, or finishes a turn while the window is behind, raises a notification.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { Live } from "../src/shell/Live.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { interruptKey, selectedText } from "../src/drawer/interruptModel.js";
import { todosOf, summaryText, TODO_MAX, TODO_TEXT_MAX } from "../src/drawer/todoModel.js";
import { waitsOf, noteOf, labelOf, LABEL_MAX } from "../src/lib/attention.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";
import { FakeWebSocket, harness, hello } from "./fakes.mjs";

// ---------------------------------------------------------------- the key model

const ok = { desk: true, working: true, draft: "", stopping: false, menuOpen: false, dialogOpen: false, selected: false };
const div = { localName: "div" };
const box = { localName: "textarea" };

test("Escape stops a running turn from the conversation or an empty box, and from nowhere that has its own use of it", () => {
  assert.equal(interruptKey({ key: "Escape", target: div }, ok), true);
  assert.equal(interruptKey({ key: "Escape", target: box }, ok), true);
  assert.equal(interruptKey({ key: "Escape", target: box }, { ...ok, draft: "x" }), false, "the box's text comes first");
  assert.equal(interruptKey({ key: "Escape", target: { localName: "select" } }, ok), false);
  assert.equal(interruptKey({ key: "Escape", target: { localName: "input" } }, ok), false);
  assert.equal(interruptKey({ key: "Escape", target: { localName: "div", isContentEditable: true } }, ok), false);
  assert.equal(interruptKey({ key: "Escape", target: div }, { ...ok, menuOpen: true }), false);
  assert.equal(interruptKey({ key: "Escape", target: div }, { ...ok, dialogOpen: true }), false);
  for (const bad of [{ isComposing: true }, { keyCode: 229 }, { defaultPrevented: true }, { shiftKey: true }, { altKey: true }, { metaKey: true }, { ctrlKey: true }]) {
    assert.equal(interruptKey({ key: "Escape", target: div, ...bad }, ok), false, JSON.stringify(bad));
  }
});

test("a turn that is not running, a window off the desk and a Stop already out are left alone", () => {
  for (const over of [{ working: false }, { desk: false }, { stopping: true }, { working: undefined }, { desk: "yes" }]) {
    assert.equal(interruptKey({ key: "Escape", target: div }, { ...ok, ...over }), false, JSON.stringify(over));
    assert.equal(interruptKey({ key: "c", ctrlKey: true, target: div }, { ...ok, ...over }), false, JSON.stringify(over));
  }
  assert.equal(interruptKey(null, ok), false);
  assert.equal(interruptKey({ key: "Escape" }, null), false);
});

test("Ctrl+C stops only with no text selected, and Copy keeps the key otherwise", () => {
  assert.equal(interruptKey({ key: "c", ctrlKey: true, target: div }, ok), true);
  assert.equal(interruptKey({ key: "C", ctrlKey: true, target: box }, ok), true);
  assert.equal(interruptKey({ key: "c", ctrlKey: true, target: div }, { ...ok, selected: true }), false);
  assert.equal(interruptKey({ key: "c", ctrlKey: true, target: div }, { ...ok, selected: undefined }), false, "unknown is a selection");
  assert.equal(interruptKey({ key: "c", target: div }, ok), false, "a plain c");
  assert.equal(interruptKey({ key: "c", metaKey: true, target: div }, ok), false, "Command+C is Copy");
  assert.equal(interruptKey({ key: "c", ctrlKey: true, shiftKey: true, target: div }, ok), false);
  assert.equal(interruptKey({ key: "c", ctrlKey: true, target: { localName: "input" } }, ok), false);
});

test("selectedText reads the page's selection and the box's, and calls an unknown page selected", () => {
  assert.equal(selectedText({ getSelection: () => "" }, null), false);
  assert.equal(selectedText({ getSelection: () => "some words" }, null), true);
  assert.equal(selectedText({ getSelection: () => "" }, { selectionStart: 1, selectionEnd: 3 }), true);
  assert.equal(selectedText({ getSelection: () => "" }, { selectionStart: 2, selectionEnd: 2 }), false);
  assert.equal(selectedText({}, null), true);
  assert.equal(selectedText(null, null), true);
  assert.equal(selectedText({ getSelection: () => { throw new Error("no"); } }, null), true);
});

// ---------------------------------------------------------------- the drawer

const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, status: "working", ...over });

async function mount(st = push(), { desk = true, selection = "" } = {}) {
  forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  dom.window.getSelection = () => selection;
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null });
  let cur = st;
  const draw = () => render(html`<${Structured} pane="p3" snap=${store.getState()} st=${cur} store=${store} desk=${desk} />`, container);
  draw();
  await settle();
  return {
    container, store, dom,
    box: () => container.find(".structured-input"),
    facts: () => container.find(".structured-facts"),
    show: async (over) => { cur = push(over); draw(); await settle(); },
    cancels: () => store.sent.filter((c) => c.cmd === "structuredCancel"),
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

test("Escape in an empty box and from the conversation sends Stop, and the hint is shown", async () => {
  const m = await mount();
  try {
    assert.ok(m.container.find("[data-interrupt-hint]"), "no visible hint while a turn runs");
    assert.match(m.container.find("[data-interrupt-hint]").textContent, /Esc stops the turn/);
    const ev = m.box().keydown("Escape");
    assert.equal(ev.defaultPrevented, true);
    assert.equal(m.cancels().length, 1);
    assert.equal(m.cancels()[0].id, "p3");
    assert.equal(m.cancels()[0].keepQueue, undefined, "no queue, so a plain Stop");
    await settle();
    // Stop is out: a second Escape does not send another.
    m.box().keydown("Escape");
    assert.equal(m.cancels().length, 1);
    assert.equal(m.container.find("[data-interrupt-hint]"), null);
  } finally {
    await m.done();
  }
  const n = await mount();
  try {
    n.facts().keydown("Escape");
    assert.equal(n.cancels().length, 1, "focus in the conversation");
  } finally {
    await n.done();
  }
});

test("Escape in a box with text, in a menu or when idle sends nothing", async () => {
  const m = await mount(push({ commands: [{ name: "review" }] }));
  try {
    m.box().enter("half a thought");
    await settle();
    assert.equal(m.box().keydown("Escape").defaultPrevented, false);
    assert.deepEqual(m.cancels(), []);
    assert.equal(m.box().value, "half a thought");
    // The slash menu takes its own Escape: it closes, and the turn goes on.
    m.box().enter("/re");
    await settle();
    assert.ok(m.container.find(".slash-menu"));
    m.box().keydown("Escape");
    await settle();
    assert.equal(m.container.find(".slash-menu"), null, "the menu closed");
    assert.deepEqual(m.cancels(), [], "the menu's Escape stopped the turn");
    // The same box, emptied, with the menu closed: now Escape stops the turn.
    m.box().enter("");
    await settle();
    m.box().keydown("Escape");
    assert.equal(m.cancels().length, 1);
  } finally {
    await m.done();
  }
  const idle = await mount(push({ status: "idle" }));
  try {
    assert.equal(idle.box().keydown("Escape").defaultPrevented, false);
    assert.equal(idle.facts().keydown("Escape").defaultPrevented, false);
    assert.equal(idle.container.find("[data-interrupt-hint]"), null);
    assert.deepEqual(idle.cancels(), []);
  } finally {
    await idle.done();
  }
});

test("a select keeps its Escape", async () => {
  const m = await mount(push({ modes: { current: "default", available: [{ id: "default", name: "Default" }, { id: "plan", name: "Plan" }] } }));
  try {
    const select = m.container.find("select");
    assert.ok(select, "the mode selector is drawn");
    select.keydown("Escape");
    assert.deepEqual(m.cancels(), []);
  } finally {
    await m.done();
  }
});

test("Escape and Ctrl+C keep the queue, and a window off the desk does neither", async () => {
  const q = [{ id: 1, pos: 1, kind: "user", state: "queued", text: "next" }];
  const m = await mount(push({ queue: q, queued: 1 }));
  try {
    m.box().keydown("Escape");
    assert.equal(m.cancels().length, 1);
    assert.equal(m.cancels()[0].keepQueue, true);
  } finally {
    await m.done();
  }
  const c = await mount(push({ queue: q, queued: 1 }));
  try {
    const ev = c.facts().keydown("c", { ctrlKey: true });
    assert.equal(ev.defaultPrevented, true);
    assert.equal(c.cancels().length, 1);
    assert.equal(c.cancels()[0].keepQueue, true);
  } finally {
    await c.done();
  }
  const off = await mount(push(), { desk: false });
  try {
    assert.equal(off.facts().keydown("Escape").defaultPrevented, false);
    assert.deepEqual(off.cancels(), []);
  } finally {
    await off.done();
  }
});

test("Ctrl+C over a selection is Copy and sends nothing; with none it stops, also in a box holding text", async () => {
  const copy = await mount(push(), { selection: "some selected words" });
  try {
    const ev = copy.facts().keydown("c", { ctrlKey: true });
    assert.equal(ev.defaultPrevented, false);
    assert.deepEqual(copy.cancels(), []);
  } finally {
    await copy.done();
  }
  const m = await mount();
  try {
    m.box().enter("typed words");
    await settle();
    m.box().selectionStart = 0;
    m.box().selectionEnd = 5;
    assert.equal(m.box().keydown("c", { ctrlKey: true }).defaultPrevented, false, "text selected in the box is Copy");
    assert.deepEqual(m.cancels(), []);
    m.box().selectionStart = 5;
    m.box().selectionEnd = 5;
    assert.equal(m.box().keydown("c", { ctrlKey: true }).defaultPrevented, true);
    assert.equal(m.cancels().length, 1);
  } finally {
    await m.done();
  }
});

test("a key of an input method's composition is not an interrupt", async () => {
  const m = await mount();
  try {
    m.box().keydown("Escape", { isComposing: true });
    m.box().keydown("Escape", { keyCode: 229 });
    assert.deepEqual(m.cancels(), []);
  } finally {
    await m.done();
  }
});

test("a turn waiting on a permission is stopped by Escape too", async () => {
  const m = await mount(push({ status: "waiting" }));
  try {
    m.box().keydown("Escape");
    assert.equal(m.cancels().length, 1);
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- the plan

test("todosOf keeps the status, one line, a bounded count and bounded text, and drops what is not a row", () => {
  const t = todosOf({ plan: [
    { content: "write the test", status: "completed", priority: "high" },
    { content: "  fix\nthe\tbug  ", status: "in_progress" },
    { content: "ship", status: "In-Progress" },
    { content: "odd", status: "who knows" },
    { content: "" }, { content: "   " }, null, "text", 4, { status: "pending" },
  ] });
  assert.deepEqual(t.rows.map((r) => [r.text, r.status]), [["write the test", "completed"], ["fix the bug", "in_progress"], ["ship", "in_progress"], ["odd", "pending"]]);
  assert.equal(t.done, 1);
  assert.equal(t.total, 4);
  assert.equal(summaryText(t), "1 of 4 done: fix the bug");
  const many = todosOf({ plan: Array.from({ length: TODO_MAX + 7 }, (_, i) => ({ content: "t" + i, status: "pending" })) });
  assert.equal(many.rows.length, TODO_MAX);
  assert.equal(many.more, 7);
  assert.equal(many.total, TODO_MAX + 7);
  const long = todosOf({ plan: [{ content: "x".repeat(TODO_TEXT_MAX * 5), status: "pending" }] });
  assert.equal(Array.from(long.rows[0].text).length, TODO_TEXT_MAX);
  assert.ok(long.rows[0].text.endsWith("…"));
  const hostile = todosOf({ plan: [{ content: "a‮b​c\u0007d\u001b[31m", status: "pending" }] });
  assert.doesNotMatch(hostile.rows[0].text, /[‮​\u0007\u001b]/u);
  for (const bad of [null, undefined, {}, { plan: "no" }, { plan: {} }]) assert.deepEqual(todosOf(bad).rows, []);
  assert.equal(summaryText(todosOf(null)), "");
});

test("the plan is a collapsible checklist that follows the push, draws the words as text, and is absent when empty", async () => {
  const plan = [
    { content: "read the code", status: "completed", priority: "medium" },
    { content: "<img src=x onerror=alert(1)> write it", status: "in_progress", priority: "high" },
    { content: "test it", status: "pending", priority: "low" },
  ];
  const m = await mount(push({ plan }));
  try {
    assert.ok(m.container.find(".structured-todos"));
    const rows = () => m.container.findAll(".structured-todo");
    assert.deepEqual(rows().map((r) => r.getAttribute("data-status")), ["completed", "in_progress", "pending"]);
    assert.match(m.container.find(".structured-todos-sum").textContent, /1 of 3 done: .*write it/);
    assert.equal(m.container.find(".structured-todos img"), null, "the agent's markup became an element");
    assert.match(rows()[1].textContent, /<img src=x onerror=alert\(1\)> write it/);
    assert.match(rows()[0].textContent, /Done: read the code/);
    // Collapses and opens on the head.
    const head = m.container.find(".structured-todos-head");
    assert.equal(head.getAttribute("aria-expanded"), "true");
    head.click();
    await settle();
    assert.equal(m.container.findAll(".structured-todo").length, 0);
    assert.equal(m.container.find(".structured-todos-head").getAttribute("aria-expanded"), "false");
    // A push that changes a status shows at once, and the person's choice stays.
    await m.show({ plan: plan.map((p) => ({ ...p, status: "completed" })) });
    assert.match(m.container.find(".structured-todos-sum").textContent, /3 of 3 done/);
    assert.equal(m.container.findAll(".structured-todo").length, 0, "still closed");
    m.container.find(".structured-todos-head").click();
    await settle();
    assert.deepEqual(m.container.findAll(".structured-todo").map((r) => r.getAttribute("data-status")), ["completed", "completed", "completed"]);
    await m.show({ plan: [] });
    assert.equal(m.container.find(".structured-todos"), null);
  } finally {
    await m.done();
  }
});

test("a finished plan starts closed, a plan with work left starts open, and rows past the bound are counted", async () => {
  const done = await mount(push({ plan: [{ content: "a", status: "completed" }] }));
  try {
    assert.equal(done.container.findAll(".structured-todo").length, 0);
  } finally {
    await done.done();
  }
  const big = await mount(push({ plan: Array.from({ length: TODO_MAX + 3 }, (_, i) => ({ content: "t" + i, status: "pending" })) }));
  try {
    assert.equal(big.container.findAll(".structured-todo").length, TODO_MAX + 1);
    assert.match(big.container.find(".structured-todo--more").textContent, /3 more not shown/);
  } finally {
    await big.done();
  }
});

// ---------------------------------------------------------------- notifications

test("labelOf cleans and cuts a pane label, so an agent-set name cannot carry a line break or hidden text", () => {
  assert.equal(labelOf("feat/x"), "feat/x");
  assert.equal(labelOf("a\nb‮c\u0000d"), "a bc d");
  assert.equal(labelOf(5), "");
  assert.equal(Array.from(labelOf("y".repeat(500))).length, LABEL_MAX);
  assert.equal(labelOf("   "), "");
});

test("waitsOf: a structured pane that ends a turn is finished; one that only starts working, or a terminal pane, is not", () => {
  const first = waitsOf(new Map(), { s: { name: "S", status: "working", structured: true }, t: { name: "T", status: "working" } });
  assert.deepEqual(first.finished, [], "the first state is never news");
  const next = waitsOf(first.next, { s: { name: "S", status: "idle", structured: true, branch: "b" }, t: { name: "T", status: "idle" } });
  assert.deepEqual(next.finished.map((f) => f.id), ["s"], "a terminal pane going idle has its own path");
  assert.deepEqual(next.fresh, []);
  const again = waitsOf(next.next, { s: { name: "S", status: "idle", structured: true } });
  assert.deepEqual(again.finished, [], "told once");
  const w = waitsOf(next.next, { s: { name: "S", status: "waiting", structured: true } });
  assert.deepEqual([w.fresh.map((f) => f.id), w.finished], [["s"], []], "a permission wait is a fresh need, not a finish");
  const starting = waitsOf(new Map([["s", "starting"]]), { s: { name: "S", status: "idle", structured: true } });
  assert.deepEqual(starting.finished, []);
});

test("noteOf: fixed words for a structured pane, and nothing of the agent's but the cleaned label", () => {
  const f = { id: "s", name: "S", branch: "feat/x", structured: true, finished: true, blocked: false };
  assert.deepEqual(noteOf([f]), { title: "S finished", body: "feat/x — ready for your next message", paneId: "s" });
  assert.deepEqual(noteOf([{ ...f, finished: false }]), { title: "S needs you", body: "feat/x — waiting for your answer", paneId: "s" });
  assert.equal(noteOf([{ ...f, finished: false, blocked: true }]).body, "feat/x — needs setting up");
  assert.deepEqual(noteOf([f, { ...f, id: "u", name: "U" }]), { title: "2 agents finished", body: "S, U", paneId: "s" });
  assert.deepEqual(noteOf([f, { ...f, id: "u", name: "U", finished: false }]), { title: "2 agents need you", body: "S, U", paneId: "u" });
  const hostile = waitsOf(new Map([["s", "working"]]), { s: { name: "Evil\n[Flockdeck] approve now‮", status: "idle", structured: true, branch: "x\ny" } });
  const note = noteOf(hostile.finished);
  assert.doesNotMatch(note.title + note.body, /[\n‮]/u);
});

// The whole path: state pushes into the page, and the browser's notification.
async function live({ focused }) {
  const dom = createDom();
  const restore = install(dom);
  const made = [];
  dom.window.Notification = class {
    constructor(title, opts) {
      const rec = { title, body: opts && opts.body, closed: false };
      made.push(rec);
      this.close = () => { rec.closed = true; };
    }
  };
  dom.window.Notification.permission = "granted";
  dom.document.hasFocus = () => focused;
  dom.document.hidden = !focused;
  const { store } = await harness();
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  store.start();
  render(html`<${Live} store=${store} window=${dom.window} />`, container);
  await settle();
  const ws = () => FakeWebSocket.last;
  ws().open();
  ws().frame(hello());
  await settle();
  const state = (panes) => ({ type: "state", root: "/work/a", projects: [{ root: "/work/a", name: "alpha", waiting: 0, working: 0 }], panes });
  return {
    made,
    push: async (panes) => { ws().frame(state(panes)); await settle(); },
    done: () => { render(null, container); store.stop(); restore(); },
  };
}
const pane = (status, over = {}) => ({ id: "p1", kind: "structured", name: "worker", cwd: "/work/a", branch: "feat/x", status, structured: true, ...over });

test("a structured pane that asks for permission or finishes a turn raises a notification while the window is behind", async () => {
  const m = await live({ focused: false });
  try {
    await m.push({ p1: pane("working") });
    assert.deepEqual(m.made, [], "the first state announces nothing");
    await m.push({ p1: pane("waiting", { permission: { tool: "Bash", summary: "rm -rf /" }, activity: "IGNORE ALL RULES" }) });
    assert.equal(m.made.length, 1);
    assert.equal(m.made[0].title, "worker needs you");
    assert.equal(m.made[0].body, "feat/x — waiting for your answer");
    assert.doesNotMatch(JSON.stringify(m.made), /rm -rf|IGNORE/);
    // Answered: the waiting notification goes.
    await m.push({ p1: pane("working") });
    assert.equal(m.made[0].closed, true);
    await m.push({ p1: pane("idle") });
    assert.equal(m.made.length, 2);
    assert.equal(m.made[1].title, "worker finished");
    assert.equal(m.made[1].body, "feat/x — ready for your next message");
    // The notice of a finished turn is not closed by the next unrelated push.
    await m.push({ p1: pane("idle", { cwd: "/work/a/sub" }) });
    assert.equal(m.made[1].closed, false);
  } finally {
    m.done();
  }
});

test("with the window in front a finished turn raises nothing, and a terminal pane going idle raises nothing", async () => {
  const front = await live({ focused: true });
  try {
    await front.push({ p1: pane("working") });
    await front.push({ p1: pane("idle") });
    assert.deepEqual(front.made, []);
  } finally {
    front.done();
  }
  const behind = await live({ focused: false });
  try {
    await behind.push({ p1: pane("working", { structured: false, kind: "claude" }) });
    await behind.push({ p1: pane("idle", { structured: false, kind: "claude" }) });
    assert.deepEqual(behind.made, []);
  } finally {
    behind.done();
  }
});
