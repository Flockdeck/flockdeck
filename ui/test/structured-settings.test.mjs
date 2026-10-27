// The mode, model and settings selectors of a structured pane, and /mode and /model.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { controlsOf, controlFor, setMessage, offered, parseSetting, ownCommandsFor, CONTROLS_MAX, CHOICES_MAX, LABEL_MAX } from "../src/drawer/settingsModel.js";
import { OWN_COMMANDS } from "../src/drawer/slashModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const geminiLike = {
  modes: { current: "default", available: [{ id: "default", name: "Default" }, { id: "plan", name: "Plan", description: "Read only" }] },
  models: { current: "pro", available: [{ id: "pro", name: "Pro" }, { id: "flash" }] },
};
const codexLike = {
  configOptions: [
    { id: "model", name: "Model", category: "model", type: "select", current: "big", values: [{ value: "big", name: "Big" }, { value: "small", name: "Small" }] },
    { id: "effort", name: "Effort", category: "thought_level", type: "select", current: "low", values: [{ value: "low", name: "Low", group: "Fast" }, { value: "high", name: "High" }] },
    { id: "fast", name: "Fast mode", type: "boolean", current: "false" },
    { id: "odd", name: "Odd", type: "text", current: "x" },
    { id: "empty", name: "Empty", type: "select", current: "", values: [] },
  ],
};

test("controls come from the modes and models blocks and from select and boolean config options", () => {
  const g = controlsOf(geminiLike);
  assert.deepEqual(g.map((c) => [c.kind, c.label, c.current, c.choices.map((o) => o.value)]), [["mode", "Mode", "default", ["default", "plan"]], ["model", "Model", "pro", ["pro", "flash"]]]);
  assert.equal(g[1].choices[1].label, "flash", "a choice with no name shows its id");
  const c = controlsOf(codexLike);
  assert.deepEqual(c.map((x) => x.key), ["config:model", "config:effort", "config:fast"], "a text option or an empty select was drawn");
  assert.deepEqual(c[2].choices.map((o) => o.value), ["true", "false"]);
  assert.equal(c[1].choices[0].group, "Fast");
  assert.deepEqual(controlsOf(null), []);
  assert.deepEqual(controlsOf({ modes: { available: "x" }, configOptions: "y" }), []);
});

test("a hostile list is bounded, and a value that would need cleaning is not offered", () => {
  const many = { configOptions: Array.from({ length: CONTROLS_MAX + 20 }, (_, i) => ({ id: "o" + i, type: "select", current: "a", values: [{ value: "a" }] })) };
  assert.equal(controlsOf(many).length, CONTROLS_MAX);
  const vals = { models: { current: "m0", available: Array.from({ length: CHOICES_MAX + 20 }, (_, i) => ({ id: "m" + i })) } };
  assert.equal(controlsOf(vals)[0].choices.length, CHOICES_MAX);
  const bad = controlsOf({ modes: { current: "a", available: [{ id: "a", name: "x".repeat(1000) }, { id: "b\nc" }, { id: "" }, { id: "a" }, { id: "d".repeat(300) }, { id: "zero​width" }] } });
  assert.deepEqual(bad[0].choices.map((o) => o.value), ["a"]);
  assert.ok(bad[0].choices[0].label.length <= LABEL_MAX);
});

test("only a value from the list becomes a command", () => {
  const [mode] = controlsOf(geminiLike);
  assert.deepEqual(setMessage("p3", mode, "plan", "s1"), { cmd: "structuredSet", id: "p3", kind: "mode", setting: "", value: "plan", req: "s1" });
  for (const v of ["bypass", "", "PLAN", undefined, null, 5]) assert.equal(setMessage("p3", mode, v, "s1"), null, String(v));
  assert.equal(offered(null, "plan"), false);
  const [model] = controlsOf(codexLike);
  assert.equal(setMessage("p3", model, "small", "s2").setting, "model");
  assert.equal(setMessage("p3", model, "small", "s2").kind, "config");
});

test("/mode and /model are offered only for what the agent has, and found by block or category", () => {
  assert.deepEqual(ownCommandsFor(OWN_COMMANDS, []).map((c) => c.name), ["stop"]);
  assert.deepEqual(ownCommandsFor(OWN_COMMANDS, controlsOf(geminiLike)).map((c) => c.name), ["stop", "mode", "model"]);
  assert.deepEqual(ownCommandsFor(OWN_COMMANDS, controlsOf(codexLike)).map((c) => c.name), ["stop", "model"]);
  assert.equal(controlFor(controlsOf(codexLike), "model").setting, "model");
  assert.equal(controlFor(controlsOf(codexLike), "mode"), null);
});

test("'/mode plan' names a choice by value or label; a word that is none says what is on offer", () => {
  const controls = controlsOf(geminiLike);
  assert.equal(parseSetting("/mode plan", controls).value, "plan");
  assert.equal(parseSetting("/mode  PLAN ", controls).value, "plan");
  assert.equal(parseSetting("/model Pro", controls).value, "pro");
  const miss = parseSetting("/mode yolo", controls);
  assert.equal(miss.value, undefined);
  assert.match(miss.error, /Choices: Default, Plan/);
  for (const t of ["/mode", "/mode ", "hello", "/modes plan", "/mode plan\nmore", "/stop now", null, 4]) assert.equal(parseSetting(t, controls), null, String(t));
  assert.equal(parseSetting("/mode plan", controlsOf(codexLike)), null, "the agent has no mode, so the text goes to the agent");
});

async function mount(extra = {}, props = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {} });
  const st = { type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, commands: [{ name: "model", description: "the agent's own" }], ...extra };
  render(html`<${Structured} pane="p3" snap=${store.getState()} st=${st} store=${store} desk=${true} ...${props} />`, container);
  await settle();
  return { container, store, done: async () => { render(null, container); await settle(); restore(); } };
}

test("a selector per control shows the agent's current value, and choosing one sends structuredSet", async () => {
  const m = await mount(geminiLike);
  try {
    const sel = m.container.findAll(".structured-select");
    assert.deepEqual(sel.map((s) => s.getAttribute("data-setting")), ["mode", "model"]);
    assert.deepEqual(sel[0].findAll("option").map((o) => o.getAttribute("value")), ["default", "plan"]);
    sel[0].value = "plan";
    sel[0].dispatch("change");
    await settle();
    assert.equal(m.store.sent.length, 1);
    const { cmd, id, kind, setting, value } = m.store.sent[0];
    assert.deepEqual({ cmd, id, kind, setting, value }, { cmd: "structuredSet", id: "p3", kind: "mode", setting: "", value: "plan" });
    assert.match(m.store.sent[0].req, /^s\d+$/);
  } finally {
    await m.done();
  }
});

test("a value the list does not have is not sent", async () => {
  const m = await mount(geminiLike);
  try {
    const sel = m.container.findAll(".structured-select")[0];
    sel.value = "bypass";
    sel.dispatch("change");
    await settle();
    assert.deepEqual(m.store.sent, []);
    assert.match(m.container.find(".structured-note").textContent, /not on the agent's list/);
  } finally {
    await m.done();
  }
});

test("an agent with nothing to choose has no selectors, and a window through the relay has none", async () => {
  let m = await mount({});
  try {
    assert.equal(m.container.find(".structured-settings"), null);
  } finally {
    await m.done();
  }
  m = await mount(geminiLike, { desk: false });
  try {
    assert.equal(m.container.find(".structured-settings"), null);
  } finally {
    await m.done();
  }
});

test("the desk's refusal of a change is shown under the box, for that pane only", async () => {
  const m = await mount(geminiLike, { settingError: { id: "p3", error: "That setting is not on offer any more." } });
  try {
    assert.match(m.container.find(".structured-note").textContent, /not on offer any more/);
  } finally {
    await m.done();
  }
  const other = await mount(geminiLike, { settingError: { id: "p9", error: "elsewhere" } });
  try {
    assert.equal(other.container.find(".structured-note"), null);
  } finally {
    await other.done();
  }
});

test("/mode and /model join Flockdeck's group and take the place of an agent command of the same name", async () => {
  const m = await mount(geminiLike);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/mo");
    await settle();
    const items = m.container.findAll(".slash-item");
    assert.deepEqual(items.map((e) => e.getAttribute("data-slash")), ["mode", "model"]);
    assert.equal(m.container.findAll(".slash-group").map((e) => e.textContent).join("|"), "Flockdeck", "the agent's /model was listed too");
    items[0].click();
    await settle();
    assert.deepEqual(m.store.sent, [], "choosing /mode sent something");
    assert.equal(box().value, "");
    assert.equal(m.container.find('[data-setting="mode"]').focusCalls, 1, "the mode selector did not get the focus");
  } finally {
    await m.done();
  }
});

test("'/mode plan' in the box sets the mode instead of going to the agent", async () => {
  const m = await mount(geminiLike);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/mode plan");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.deepEqual(m.store.sent.map((c) => [c.cmd, c.kind, c.value]), [["structuredSet", "mode", "plan"]]);
    assert.equal(box().value, "");
    box().enter("/mode yolo");
    await settle();
    m.container.find(".structured-prompt").dispatch("submit");
    await settle();
    assert.equal(m.store.sent.length, 1, "an unknown mode was sent");
    assert.equal(box().value, "/mode yolo", "the draft was lost");
    assert.match(m.container.find(".structured-note").textContent, /Choices: Default, Plan/);
  } finally {
    await m.done();
  }
});
