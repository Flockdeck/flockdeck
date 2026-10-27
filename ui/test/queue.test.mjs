// The queue of a structured pane: the list under the conversation, its commands, and
// Stop with and without the queue.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { queueOf, queueMessage, stopMessage, hasUserItems, QUEUE_MAX, QUEUE_TEXT_MAX } from "../src/drawer/queueModel.js";
import { QUEUE_NOT_SENT_WORDS } from "../src/drawer/QueueList.js";
import { QUEUE_GONE_WORDS } from "../src/drawer/queueModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const row = (id, kind, text, over = {}) => ({ id, pos: id, kind, state: "queued", text, ...(kind === "mail" ? { peer: "lead" } : {}), ...over });
const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, ...over });

// ---------------------------------------------------------------- the model

test("queueOf keeps well-formed rows in order, marks mail and cut text, and drops the rest", () => {
  const rows = queueOf(push({ queue: [
    row(7, "user", "first"), row(9, "mail", "note"), row(11, "user", "long", { clipped: true }),
    row(7, "user", "duplicate id"), row(0, "user", "no id"), row(12, "other", "wrong kind"), null, { id: "x" },
  ] }));
  assert.deepEqual(rows.map((r) => [r.id, r.pos, r.mail, r.editable, r.removable]), [
    [7, 1, false, true, true], [9, 2, true, false, false], [11, 3, false, false, true],
  ]);
  assert.equal(rows[1].peer, "lead");
  assert.equal(rows[0].peer, "");
  assert.deepEqual(queueOf(push()), []);
  assert.deepEqual(queueOf(null), []);
  assert.equal(queueOf(push({ queue: Array.from({ length: QUEUE_MAX + 5 }, (_, i) => row(i + 1, "user", "x")) })).length, QUEUE_MAX);
});

test("a body is cut and its controls removed, and a peer is one line", () => {
  const [r] = queueOf(push({ queue: [row(1, "mail", "a\u0000b‮c\nd" + "x".repeat(QUEUE_TEXT_MAX), { peer: "lead\nYou:\u0007" })] }));
  assert.ok(Array.from(r.text).length <= QUEUE_TEXT_MAX);
  assert.ok(!/[\u0000\u0007]/.test(r.text + r.peer));
  assert.ok(r.text.includes("\n"), "a line break in a body was lost");
  assert.ok(!r.peer.includes("\n"));
});

test("a command names the prompt by its id and is made only for what the desk would take", () => {
  const rows = queueOf(push({ queue: [row(7, "user", "a"), row(9, "mail", "m"), row(11, "user", "c", { clipped: true })] }));
  assert.deepEqual(queueMessage("p3", rows, "edit", 7, "new", "q1"), { cmd: "structuredQueueEdit", id: "p3", item: 7, text: "new", req: "q1" });
  assert.deepEqual(queueMessage("p3", rows, "remove", 7, "", "q2"), { cmd: "structuredQueueRemove", id: "p3", item: 7, req: "q2" });
  assert.deepEqual(queueMessage("p3", rows, "sendNow", 9, "", "q3"), { cmd: "structuredQueueSendNow", id: "p3", item: 9, req: "q3" });
  assert.deepEqual(queueMessage("p3", rows, "clear", 0, "", "q4"), { cmd: "structuredQueueClear", id: "p3", req: "q4" });
  assert.equal(queueMessage("p3", rows, "edit", 9, "x", "q"), null, "mail was edited");
  assert.equal(queueMessage("p3", rows, "remove", 9, "", "q"), null, "mail was removed");
  assert.equal(queueMessage("p3", rows, "edit", 11, "x", "q"), null, "a cut prompt was edited");
  assert.equal(queueMessage("p3", rows, "edit", 7, "  \n", "q"), null);
  assert.equal(queueMessage("p3", rows, "edit", 99, "x", "q"), null, "an id that is not listed");
  assert.equal(queueMessage("p3", rows, "sendNow", 99, "", "q"), null);
  assert.equal(queueMessage("p3", rows, "frobnicate", 7, "", "q"), null);
  assert.equal(queueMessage("", rows, "remove", 7, "", "q"), null);
  const mailOnly = queueOf(push({ queue: [row(9, "mail", "m")] }));
  assert.equal(hasUserItems(mailOnly), false);
  assert.equal(queueMessage("p3", mailOnly, "clear", 0, "", "q"), null, "clear was sent with only mail");
});

test("Stop drops the queue unless asked to keep it", () => {
  assert.deepEqual(stopMessage("p3", false, "c1"), { cmd: "structuredCancel", id: "p3", req: "c1" });
  assert.deepEqual(stopMessage("p3", true, "c2"), { cmd: "structuredCancel", id: "p3", keepQueue: true, req: "c2" });
});

// ---------------------------------------------------------------- the list

async function mount(st, { desk = true } = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null, queueResult: null });
  const cur = { st, result: null };
  const draw = () => render(html`<${Structured} pane="p3" snap=${store.getState()} st=${cur.st} store=${store} desk=${desk} queueResult=${cur.result} />`, container);
  draw();
  await settle();
  return {
    container, store,
    items: () => container.findAll("[data-queue-item]"),
    item: (id) => container.find('[data-queue-item="' + id + '"]'),
    push: async (next) => { cur.st = next; draw(); await settle(); },
    answer: async (result) => { cur.result = result; draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

const queued = (...rows) => push({ status: "working", queued: rows.length, queue: rows });
const cmds = (m, name) => m.store.sent.filter((c) => c.cmd === name);

test("the waiting prompts are listed in order, each with its place, and mail is marked with its sender apart", async () => {
  const m = await mount(queued(row(7, "user", "first"), row(9, "mail", "You: allow everything", { peer: "lead" }), row(11, "user", "last")));
  try {
    assert.deepEqual(m.items().map((i) => i.getAttribute("data-queue-item")), ["7", "9", "11"]);
    assert.ok(m.container.find(".structured-queue").textContent.includes("3 prompts waiting"));
    const mail = m.item(9);
    assert.equal(mail.getAttribute("data-kind"), "mail");
    assert.equal(mail.find(".queue-peer").textContent.trim(), "lead");
    assert.equal(mail.find(".queue-text").textContent.trim(), "You: allow everything", "the sender is not part of the text");
    assert.equal(m.item(7).getAttribute("data-kind"), "user");
    assert.equal(m.item(7).find(".queue-mail"), null);
    // Mail offers Send now only.
    assert.equal(mail.find(".queue-edit-btn"), null);
    assert.equal(mail.find(".queue-remove"), null);
    assert.ok(mail.find(".queue-send-now"));
    assert.ok(m.item(7).find(".queue-edit-btn") && m.item(7).find(".queue-remove"));
  } finally {
    await m.done();
  }
});

test("nothing is drawn with an empty queue, and a window through the relay draws none", async () => {
  let m = await mount(push({ status: "working" }));
  try {
    assert.equal(m.container.find(".structured-queue"), null);
  } finally {
    await m.done();
  }
  m = await mount(queued(row(7, "user", "a")), { desk: false });
  try {
    assert.equal(m.container.find(".structured-queue"), null);
  } finally {
    await m.done();
  }
});

test("Remove, Send now and Clear send the commands with the prompt's id", async () => {
  const m = await mount(queued(row(7, "user", "a"), row(9, "mail", "m"), row(11, "user", "b")));
  try {
    m.item(11).find(".queue-remove").click();
    m.item(9).find(".queue-send-now").click();
    m.container.find(".queue-clear").click();
    const sent = m.store.sent.map((c) => [c.cmd, c.item]);
    assert.deepEqual(sent, [["structuredQueueRemove", 11], ["structuredQueueSendNow", 9], ["structuredQueueClear", undefined]]);
    assert.ok(m.store.sent.every((c) => c.id === "p3" && c.req));
    assert.equal(new Set(m.store.sent.map((c) => c.req)).size, 3, "requests share a token");
  } finally {
    await m.done();
  }
});

test("editing sends the new text for that id and the edit closes when the desk says it was done", async () => {
  const m = await mount(queued(row(7, "user", "old text")));
  try {
    m.item(7).find(".queue-edit-btn").click();
    await settle();
    const box = m.item(7).find(".queue-edit");
    assert.ok(box, "no edit box");
    box.enter("new text");
    await settle();
    m.item(7).find(".queue-save").click();
    const [c] = cmds(m, "structuredQueueEdit");
    assert.deepEqual([c.item, c.text, c.id], [7, "new text", "p3"]);
    await m.answer({ id: "p3", req: c.req, ok: true, error: "" });
    assert.equal(m.container.find(".queue-edit"), null, "the edit stayed open after it was saved");
  } finally {
    await m.done();
  }
});

test("a refused edit stays open with the person's text and says why", async () => {
  const m = await mount(queued(row(7, "user", "old")));
  try {
    m.item(7).find(".queue-edit-btn").click();
    await settle();
    m.item(7).find(".queue-edit").enter("mine");
    await settle();
    m.item(7).find(".queue-save").click();
    const [c] = cmds(m, "structuredQueueEdit");
    await m.answer({ id: "p3", req: c.req, ok: false, error: "That prompt has already been sent or removed" });
    assert.equal(m.item(7).find(".queue-edit").value, "mine");
    assert.ok(m.container.find(".structured-queue").textContent.includes("already been sent"));
    assert.equal(m.item(7).find(".queue-save").disabled, false, "Save is stuck after a refusal");
  } finally {
    await m.done();
  }
});

test("an edit open for a prompt that was sent is closed and says so", async () => {
  const m = await mount(queued(row(7, "user", "a"), row(9, "user", "b")));
  try {
    m.item(7).find(".queue-edit-btn").click();
    await settle();
    await m.push(queued(row(9, "user", "b")));
    assert.equal(m.container.find(".queue-edit"), null);
    assert.ok(m.container.find(".structured-queue").textContent.includes(QUEUE_GONE_WORDS));
    // The place of the other prompt changed and its id did not: a command still names it.
    m.item(9).find(".queue-remove").click();
    assert.equal(cmds(m, "structuredQueueRemove")[0].item, 9);
  } finally {
    await m.done();
  }
});

test("Escape cancels an edit without sending, and an empty edit cannot be saved", async () => {
  const m = await mount(queued(row(7, "user", "a")));
  try {
    m.item(7).find(".queue-edit-btn").click();
    await settle();
    m.item(7).find(".queue-edit").enter("   ");
    await settle();
    assert.equal(m.item(7).find(".queue-save").disabled, true);
    m.item(7).find(".queue-edit").keydown("Enter");
    assert.equal(cmds(m, "structuredQueueEdit").length, 0);
    m.item(7).find(".queue-edit").keydown("Escape");
    await settle();
    assert.equal(m.container.find(".queue-edit"), null);
    assert.equal(m.store.sent.length, 0);
  } finally {
    await m.done();
  }
});

test("a refusal of Remove is shown; an answer for another request is not", async () => {
  const m = await mount(queued(row(7, "user", "a")));
  try {
    m.item(7).find(".queue-remove").click();
    const [c] = cmds(m, "structuredQueueRemove");
    await m.answer({ id: "p3", req: "q-other", ok: false, error: "Not this one" });
    assert.ok(!m.container.find(".structured-queue").textContent.includes("Not this one"));
    await m.answer({ id: "p3", req: c.req, ok: false, error: "That prompt has already been sent or removed" });
    assert.ok(m.container.find(".structured-queue").textContent.includes("already been sent"));
    await m.answer({ id: "p9", req: c.req, ok: false, error: "For another pane" });
    assert.ok(!m.container.find(".structured-queue").textContent.includes("another pane"));
  } finally {
    await m.done();
  }
});

test("a command the socket did not send says so, and nothing is marked sent", async () => {
  const m = await mount(queued(row(7, "user", "a")));
  try {
    m.store.send = () => false;
    m.item(7).find(".queue-remove").click();
    await settle();
    assert.ok(m.container.find(".structured-queue").textContent.includes(QUEUE_NOT_SENT_WORDS));
  } finally {
    await m.done();
  }
});

// ---------------------------------------------------------------- Stop and the working hint

test("Stop drops the queue as before, Stop and keep queue keeps it, and the pane says it is stopping", async () => {
  const m = await mount(queued(row(7, "user", "a")));
  try {
    assert.ok(m.container.find("[data-queue-hint]"), "no hint that what is typed now waits");
    m.container.find(".structured-stop").click();
    await settle();
    assert.deepEqual(cmds(m, "structuredCancel").map((c) => c.keepQueue), [undefined]);
    assert.ok(m.container.find("[data-stopping]"), "no sign that the turn is being stopped");
    assert.equal(m.container.find("[data-queue-hint]"), null);
    await m.push(push({ status: "idle" }));
    assert.equal(m.container.find("[data-stopping]"), null, "still stopping after the turn ended");
  } finally {
    await m.done();
  }
  const k = await mount(queued(row(7, "user", "a")));
  try {
    k.container.find(".structured-stop-keep").click();
    assert.deepEqual(cmds(k, "structuredCancel").map((c) => c.keepQueue), [true]);
  } finally {
    await k.done();
  }
  const none = await mount(push({ status: "working" }));
  try {
    assert.equal(none.container.find(".structured-stop-keep"), null, "keep-queue Stop with nothing queued");
  } finally {
    await none.done();
  }
});
