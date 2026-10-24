// The task detail panel in the drawer (C5): what it draws for a task with no
// owner, with 200 notes and with HTML in a note, what a remote window is shown,
// and how the panel asks the store for the task.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { render } from "../assets/vendor/preact.mjs";
import { html } from "../assets/html.js";
import { Task, NOTES_FIRST, NOTES_MORE, ON_DESKTOP_WORDS, NO_OWNER_WORDS } from "../assets/drawer/Task.js";
import { Header } from "../assets/drawer/Header.js";
import { createDom, install, settle } from "./dom.mjs";

const node = (over = {}) => ({ pane: "p2", kind: "terminal", name: "w1", role: "worker", agent: "claude", status: "working", task: { id: "T9", title: "Basket", state: "in-progress" }, ...over });
const view = (over = {}) => ({ id: "T9", title: "Basket totals", state: "in-progress", owner: "shop-w1", ownerPane: "p3", ownerOpen: true, ownerRole: "worker", branch: "basket", creator: { name: "lead" }, updated: "2026-10-20", notes: 0, noteList: [], body: "Add up the basket.", ...over });
const ok = (task) => ({ taskDetail: { id: "T9", req: "t1", state: "ok", task, error: "" } });
const fakeTaskStore = () => {
  const calls = [];
  return { calls, requestTask(id) { calls.push(["request", id]); return true; }, clearTask() { calls.push(["clear"]); } };
};
const draw = (snap) => renderToString(html`<${Task} node=${node()} snap=${snap} store=${fakeTaskStore()} onClose=${() => {}} />`);
const notes = (n) => Array.from({ length: n }, (_, i) => ({ author: { name: "a" + i }, time: "t" + i, text: "note " + i }));

async function mount(fn) {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    await fn(box);
  } finally {
    restore();
  }
}

test("a task with no owner says nobody has it", () => {
  const out = draw(ok(view({ owner: "", ownerPane: "", ownerOpen: false })));
  assert.ok(out.includes(NO_OWNER_WORDS));
  assert.ok(!out.includes("Go to pane"));
  assert.ok(out.includes("Add up the basket."));
});

test("a task with 200 notes draws the first batch and a step for more", () => {
  const out = draw(ok(view({ notes: 200, noteList: notes(200) })));
  assert.equal(out.split('class="task-note"').length - 1, NOTES_FIRST);
  assert.ok(out.includes("note 0<") && !out.includes("note " + NOTES_FIRST + "<"));
  assert.ok(out.includes("Notes (200)"));
  assert.ok(out.includes("Show " + NOTES_MORE + " more notes (" + (200 - NOTES_FIRST) + " not drawn yet)"));
});

test("showing more draws more notes", () => mount(async (box) => {
  render(html`<${Task} node=${node()} snap=${ok(view({ notes: 200, noteList: notes(200) }))} store=${fakeTaskStore()} onClose=${() => {}} />`, box);
  await settle();
  const count = () => box.querySelectorAll(".task-note").length;
  assert.equal(count(), NOTES_FIRST);
  box.querySelector(".task-more").click();
  await settle();
  assert.equal(count(), NOTES_FIRST + NOTES_MORE);
}));

test("HTML in a note, the body and the title is drawn as text", () => {
  const evil = "<img src=x onerror=alert(1)><script>boom()</script>";
  const out = draw(ok(view({ title: evil, body: evil, notes: 1, noteList: [{ author: { name: evil }, time: "t", text: evil }] })));
  assert.ok(!out.includes("<img") && !out.includes("<script"));
  assert.ok(out.includes("&lt;img"), out.slice(0, 600));
});

test("loading and error states say so", () => {
  const loading = draw({ taskDetail: { id: "T9", req: "t1", state: "loading", task: null, error: "" } });
  assert.ok(loading.includes("Loading the task"));
  const bad = draw({ taskDetail: { id: "T9", req: "t1", state: "error", task: null, error: "no such task" } });
  assert.ok(bad.includes("no such task") && bad.includes('role="alert"'));
});

test("a remote window shows the chip only and asks for nothing", () => mount(async (box) => {
  const st = fakeTaskStore();
  render(html`<${Task} node=${node()} snap=${{ remote: true, taskDetail: null }} store=${st} onClose=${() => {}} />`, box);
  await settle();
  assert.deepEqual(st.calls, []);
  const out = draw({ remote: true, ...ok(view({ body: "secret text" })) });
  assert.ok(out.includes(ON_DESKTOP_WORDS) && out.includes("Basket"));
  assert.ok(!out.includes("secret text"), "task text was drawn in a remote window");
}));

test("the desk asks for the task on opening and lets go on closing", () => mount(async (box) => {
  const st = fakeTaskStore();
  render(html`<${Task} node=${node()} snap=${{ taskDetail: null }} store=${st} onClose=${() => {}} />`, box);
  await settle();
  assert.deepEqual(st.calls, [["request", "T9"]]);
  render(null, box);
  await settle();
  assert.deepEqual(st.calls, [["request", "T9"], ["clear"]]);
}));

test("go to pane opens the owner's pane, and is not offered for this pane or a closed one", async () => {
  await mount(async (box) => {
    const opened = [];
    render(html`<${Task} node=${node()} snap=${ok(view())} store=${fakeTaskStore()} onOpenPane=${(p) => opened.push(p)} onClose=${() => {}} />`, box);
    await settle();
    box.querySelector(".task-goto").click();
    assert.deepEqual(opened, ["p3"]);
  });
  assert.ok(!draw(ok(view({ ownerPane: "p2" }))).includes("Go to pane"));
  assert.ok(!draw(ok(view({ ownerOpen: false }))).includes("Go to pane"));
});

test("an answer for another task is not drawn", () => {
  const out = draw({ taskDetail: { id: "T1", req: "t1", state: "ok", task: view({ id: "T1", body: "other" }), error: "" } });
  assert.ok(!out.includes("other"));
});

test("the header has a task chip only when the pane has a task and a handler", () => {
  const withChip = renderToString(html`<${Header} node=${node()} onClose=${() => {}} onToggleMax=${() => {}} onTask=${() => {}} />`);
  assert.ok(withChip.includes("drawer-task-chip") && withChip.includes("T9 in-progress"));
  const none = renderToString(html`<${Header} node=${node({ task: null })} onClose=${() => {}} onToggleMax=${() => {}} onTask=${() => {}} />`);
  assert.ok(!none.includes("drawer-task-chip"));
});
