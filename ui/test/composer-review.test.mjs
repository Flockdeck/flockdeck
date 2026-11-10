// Review fixes for the composer: stale words expire, Stop does not catch a double click, the status regions keep
// their roles, and a send in flight when the box goes away is not brought back as unsent text.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured, SET_NOT_SENT_WORDS } from "../src/drawer/Structured.js";
import { STOP_GUARD_MS, IMAGE_UNSUPPORTED_WORDS } from "../src/drawer/Composer.js";
import { forgetComposerMemory, loadComposer } from "../src/drawer/composerModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, ...over });
const MODES = { modes: { current: "default", available: [{ id: "default", name: "Default" }, { id: "plan", name: "Plan" }] } };

async function mount(st, extra = {}, snap = {}) {
  forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null, queueResult: null, ...snap });
  const cur = { st, extra };
  const draw = () => render(html`<${Structured} pane="p3" snap=${store.getState()} st=${cur.st} store=${store} desk=${true} ...${cur.extra} />`, container);
  draw();
  await settle();
  return {
    container, store, dom,
    box: () => container.find(".structured-input"),
    note: () => container.find(".composer-status"),
    push: async (next, more = {}) => { if (next !== undefined) cur.st = next; cur.extra = { ...cur.extra, ...more }; draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

test("a Stop refusal goes when the draft changes, the person stops again, or the turn ends; a new one shows", async () => {
  const err1 = { id: "p3", error: "nothing to stop" };
  const m = await mount(push({ status: "working" }), { cancelError: err1 });
  try {
    assert.equal(m.note().textContent, "nothing to stop");
    m.box().enter("a");
    await settle();
    assert.equal(m.note().getAttribute("data-note"), "working", "the old refusal hid the hint");
    // The same object is not shown again; a new refusal is.
    await m.push(undefined, { cancelError: err1 });
    assert.equal(m.note().getAttribute("data-note"), "working");
    await m.push(undefined, { cancelError: { id: "p3", error: "again" } });
    assert.equal(m.note().textContent, "again");
    // Stopping again expires it.
    m.box().enter("");
    await settle();
    await m.push(undefined, { cancelError: { id: "p3", error: "third" } });
    assert.equal(m.note().textContent, "third");
    m.container.find(".structured-stop").click();
    await settle();
    assert.notEqual(m.note() && m.note().getAttribute("data-note"), "cancel-error");
    // The turn ends.
    await m.push(push({ status: "working" }), { cancelError: { id: "p3", error: "fourth" } });
    assert.equal(m.note().textContent, "fourth");
    await m.push(push({ status: "idle" }));
    assert.equal(m.note(), null, "a Stop refusal outlived the turn");
  } finally {
    await m.done();
  }
});

test("a refused setting goes when the draft changes or the person acts again", async () => {
  const e1 = { id: "p3", error: "mode refused" };
  const m = await mount(push({ status: "idle", ...MODES }), { settingError: e1 });
  try {
    assert.equal(m.note().textContent, "mode refused");
    m.box().enter("x");
    await settle();
    assert.equal(m.note(), null, "the setting refusal stayed after the draft changed");
    m.box().enter("");
    await settle();
    await m.push(undefined, { settingError: { id: "p3", error: "second" } });
    assert.equal(m.note().textContent, "second");
    const sel = m.container.find('[data-setting="mode"]');
    sel.value = "plan";
    sel.dispatch("change");
    await settle();
    assert.notEqual(m.note() && m.note().textContent, "second", "the old refusal stayed on a new change");
  } finally {
    await m.done();
  }
});

test("the words about a setting that was not sent go when the draft changes", async () => {
  const m = await mount(push({ status: "idle", ...MODES }));
  try {
    m.store.send = () => false;
    const sel = m.container.find('[data-setting="mode"]');
    sel.value = "plan";
    sel.dispatch("change");
    await settle();
    assert.equal(m.note().textContent, SET_NOT_SENT_WORDS);
    m.box().enter("hi");
    await settle();
    assert.equal(m.note(), null);
  } finally {
    await m.done();
  }
});

test("a picture warning goes when the draft changes, and when the turn ends", async () => {
  const m = await mount(push({ status: "working", capabilities: { image: false } }));
  try {
    m.container.find(".structured-attach").click();
    await settle();
    assert.equal(m.note().textContent, IMAGE_UNSUPPORTED_WORDS);
    m.box().enter("x");
    await settle();
    assert.equal(m.note().getAttribute("data-note"), "working");
    m.container.find(".structured-attach").click();
    await settle();
    assert.equal(m.note().textContent, IMAGE_UNSUPPORTED_WORDS);
    await m.push(push({ status: "idle", capabilities: { image: false } }));
    assert.equal(m.note(), null, "the picture note outlived the turn");
  } finally {
    await m.done();
  }
});

test("the shell hint shows over a Stop refusal while the draft starts with a bang", async () => {
  const m = await mount(push({ status: "working" }), { cancelError: { id: "p3", error: "nothing to stop" } });
  try {
    assert.equal(m.note().textContent, "nothing to stop");
    m.box().enter("!ls");
    await settle();
    assert.equal(m.note().getAttribute("data-note"), "shell-hint");
  } finally {
    await m.done();
  }
});

test("Send becomes Stop in the same place: a second press within the guard sends no Stop, and the box keeps the focus", async () => {
  const m = await mount(push({ status: "working" }));
  try {
    m.box().enter("go on");
    await settle();
    m.box().keydown("Enter");
    await settle();
    const sent = m.store.sent.filter((c) => c.cmd === "paneSend");
    assert.equal(sent.length, 1);
    assert.equal(m.dom.document.activeElement, m.box(), "the focus is not back in the box");
    // The desk takes it: the box is empty and the button is Stop.
    m.store.set({ sent: { seq: 1, id: "p3", req: sent[0].req } });
    await m.push();
    const stop = m.container.find(".structured-stop");
    assert.ok(stop, "no Stop after the send was taken");
    stop.click();
    await settle();
    assert.equal(m.store.sent.filter((c) => c.cmd === "structuredCancel").length, 0, "a double click stopped the turn");
    assert.equal(m.dom.document.activeElement, m.box());
    assert.ok(STOP_GUARD_MS >= 300 && STOP_GUARD_MS <= 1000);
  } finally {
    await m.done();
  }
});

test("Escape in the emptied box stops the turn after a send, as the focus is back in the box", async () => {
  const m = await mount(push({ status: "working" }));
  try {
    m.box().enter("go on");
    await settle();
    m.box().keydown("Enter");
    await settle();
    const sent = m.store.sent.filter((c) => c.cmd === "paneSend");
    m.store.set({ sent: { seq: 1, id: "p3", req: sent[0].req } });
    await m.push();
    m.dom.document.activeElement.keydown("Escape");
    await settle();
    assert.equal(m.store.sent.filter((c) => c.cmd === "structuredCancel").length, 1);
  } finally {
    await m.done();
  }
});

test("a refused send puts the text back in the box and the focus in it", async () => {
  const m = await mount(push({ status: "working" }));
  try {
    m.box().enter("hello");
    await settle();
    m.box().keydown("Enter");
    await settle();
    const req = m.store.sent.find((c) => c.cmd === "paneSend").req;
    m.store.set({ notice: { seq: 1, error: true, kind: "send", text: "gone", req } });
    await m.push();
    assert.equal(m.box().value, "hello");
    assert.equal(m.dom.document.activeElement, m.box());
    assert.equal(m.note().getAttribute("role"), "alert");
  } finally {
    await m.done();
  }
});

test("the two live regions keep their roles while the text moves between them", async () => {
  const m = await mount(push({ status: "working" }));
  try {
    const status = () => m.container.findAll('[role="status"]').pop();
    const alert = () => m.container.find('[role="alert"]');
    assert.equal(m.container.findAll('[role="alert"]').length, 1);
    assert.equal(status().getAttribute("data-note"), "working");
    assert.equal(alert().textContent, "");
    const s0 = status();
    const a0 = alert();
    m.box().enter("hi");
    await settle();
    m.box().keydown("Enter");
    await settle();
    const req = m.store.sent.find((c) => c.cmd === "paneSend").req;
    m.store.set({ notice: { seq: 1, error: true, kind: "send", text: "gone", req } });
    await m.push();
    assert.equal(alert().textContent, "gone");
    assert.equal(alert(), a0, "the alert element was replaced");
    assert.equal(status(), s0, "the status element was replaced");
    assert.equal(status().textContent, "");
  } finally {
    await m.done();
  }
});

test("a send in flight when the composer goes away is kept in the history, not as unsent text", async () => {
  const m = await mount(push({ status: "idle" }));
  try {
    m.box().enter("do the thing");
    await settle();
    m.box().keydown("Enter");
    await settle();
    assert.equal(m.store.sent.filter((c) => c.cmd === "paneSend").length, 1);
    // The pane's push goes null: the composer unmounts with the answer still out.
    await m.push(null);
    const kept = loadComposer("p3");
    assert.equal(kept.draft, "", "the sent text came back as a draft");
    assert.deepEqual(kept.history, ["do the thing"]);
  } finally {
    await m.done();
  }
});

test("a draft that was not sent is kept when the composer goes away", async () => {
  const m = await mount(push({ status: "idle" }));
  try {
    m.box().enter("half written");
    await settle();
    await m.push(null);
    assert.equal(loadComposer("p3").draft, "half written");
  } finally {
    await m.done();
  }
});
