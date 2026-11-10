// The composer's layout: the images slot, the control row, Send turning into Stop, Stop and keep queue in the
// queue's header, and the one status line.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const push = (over = {}) => ({ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, ...over });
const item = (id, text) => ({ id, pos: id, kind: "user", state: "queued", text });

async function mount(st, extra = {}) {
  forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {}, files: null, attached: null, queueResult: null });
  const cur = { st, extra };
  const draw = () => render(html`<${Structured} pane="p3" snap=${store.getState()} st=${cur.st} store=${store} desk=${true} ...${cur.extra} />`, container);
  draw();
  await settle();
  return {
    container, store,
    push: async (next, more = {}) => { cur.st = next; cur.extra = { ...cur.extra, ...more }; draw(); await settle(); },
    done: async () => { render(null, container); await settle(); restore(); },
  };
}

test("the composer is the images slot, the text, one control row and one status line, in that order", async () => {
  const m = await mount(push({ capabilities: { image: true } }));
  try {
    const box = m.container.find(".composer-box");
    const kids = box.childNodes.filter((n) => n.nodeType === 1).map((c) => c.attributes.class.split(" ")[0]);
    assert.deepEqual(kids, ["composer-images", "structured-input", "composer-tools"]);
    assert.ok(m.container.find('[data-composer-slot="images"]'), "no slot for the images strip");
    const tools = m.container.find(".composer-tools").findAll("button").map((b) => b.getAttribute("aria-label") || b.textContent);
    assert.deepEqual(tools, ["Attach image", "Mention a file", "Commands", "Send"]);
  } finally {
    await m.done();
  }
});

test("the attach button is only there when the agent takes images", async () => {
  const m = await mount(push());
  try {
    assert.equal(m.container.find(".structured-attach"), null);
    assert.deepEqual(m.container.find(".composer-tools").findAll("button").map((b) => b.textContent), ["@", "/", "Send"]);
  } finally {
    await m.done();
  }
});

test("Send becomes Stop while the agent works and nothing is typed, and Send again with text, which queues", async () => {
  const m = await mount(push({ status: "idle" }));
  try {
    assert.ok(m.container.find(".structured-send"));
    assert.equal(m.container.find(".structured-stop"), null, "Stop on an idle pane");
    await m.push(push({ status: "working" }));
    assert.equal(m.container.find(".structured-send"), null, "Send stayed while the box was empty and the agent worked");
    assert.equal(m.container.find(".composer-send").textContent, "Stop");
    assert.equal(m.container.find(".composer-send").getAttribute("type"), "button");
    m.container.find(".structured-input").enter("one more thing");
    await settle();
    const send = m.container.find(".composer-send");
    assert.equal(send.textContent, "Send");
    assert.equal(send.getAttribute("type"), "submit");
    assert.equal(m.container.find(".structured-stop"), null);
    m.container.find(".structured-input").enter("");
    await settle();
    m.container.find(".structured-stop").click();
    await settle();
    assert.equal(m.store.sent.filter((c) => c.cmd === "structuredCancel").length, 1);
    await m.push(push({ status: "waiting" }));
    assert.equal(m.container.find(".composer-send").textContent, "Stop", "Stop while a request waits for an answer");
    await m.push(push({ status: "idle" }));
    assert.equal(m.container.find(".composer-send").textContent, "Send");
  } finally {
    await m.done();
  }
});

test("Stop, keep queue is in the queue's header and not in the composer", async () => {
  const m = await mount(push({ status: "working", queued: 1, queue: [item(7, "a")] }));
  try {
    assert.equal(m.container.find(".composer").find(".structured-stop-keep"), null, "still in the composer");
    const keep = m.container.find(".queue-head").find(".structured-stop-keep");
    assert.ok(keep, "not in the queue header");
    keep.click();
    await settle();
    assert.deepEqual(m.store.sent.filter((c) => c.cmd === "structuredCancel").map((c) => c.keepQueue), [true]);
  } finally {
    await m.done();
  }
});

test("the at and slash buttons put the character in the box, the at after a space, and the slash only in an empty box", async () => {
  const m = await mount(push());
  try {
    const box = () => m.container.find(".structured-input");
    // The fake textarea has a value property only once something has typed into it.
    const val = () => (box().value !== undefined ? box().value : box().getAttribute("value"));
    m.container.find(".composer-mention").click();
    await settle();
    assert.equal(val(), "@");
    box().enter("look at");
    await settle();
    m.container.find(".composer-mention").click();
    await settle();
    assert.equal(val(), "look at @");
    assert.equal(m.container.find(".composer-command").disabled, true, "slash with text in the box");
    box().enter("");
    await settle();
    m.container.find(".composer-command").click();
    await settle();
    assert.equal(val(), "/");
    assert.ok(m.container.find(".slash-menu"), "the command list did not open");
    box().enter("!ls");
    await settle();
    assert.ok(m.container.find(".composer-mention").disabled === true, "the at button works in a shell command");
  } finally {
    await m.done();
  }
});

test("there is one status line, and the error wins over the hint under it", async () => {
  const m = await mount(push({ status: "working" }));
  try {
    const lines = () => m.container.findAll(".composer-status");
    assert.equal(lines().length, 1);
    assert.equal(lines()[0].getAttribute("data-note"), "working");
    assert.ok(lines()[0].attributes.class.includes("composer-status--hint"));
    await m.push(push({ status: "working" }), { cancelError: { id: "p3", error: "That pane has nothing running to stop" } });
    assert.equal(lines().length, 1, "two lines under the composer");
    assert.equal(lines()[0].textContent, "That pane has nothing running to stop");
    assert.ok(lines()[0].attributes.class.includes("composer-status--error"));
    assert.equal(m.container.find(".composer").findAll(".structured-note").length, 1);
  } finally {
    await m.done();
  }
});
