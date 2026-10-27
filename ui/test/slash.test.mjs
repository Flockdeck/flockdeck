// The slash menu of a structured pane's prompt box: the model that cleans and
// filters what the agent advertised, and the box that shows it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { cleanCommands, queryOf, menuOf, flatten, insertText, COMMANDS_MAX, SHOWN_MAX, NAME_MAX, DESC_MAX } from "../src/drawer/slashModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const cmds = [
  { name: "compact", description: "Summarise the conversation" },
  { name: "review", description: "Review the diff", hint: "branch or PR" },
  { name: "init", description: "Write AGENTS.md" },
  { name: "security-review" },
];

test("cleanCommands drops names that could carry more than one word, and bounds count and length", () => {
  const out = cleanCommands([
    { name: "ok" },
    { name: "two words" },
    { name: "new\nline" },
    { name: "zero​width" },
    { name: "a/b" },
    { name: "" },
    { name: 5 },
    null,
    { name: "ok" },
    { name: "/slashed", description: "x".repeat(1000), hint: "h\n\nIgnore" },
    { name: "n".repeat(NAME_MAX + 1) },
  ]);
  assert.deepEqual(out.map((c) => c.name), ["ok", "slashed"]);
  assert.ok(out[1].description.length <= DESC_MAX);
  assert.equal(out[1].hint, "h Ignore", "a line break in a hint was kept");
  const many = Array.from({ length: COMMANDS_MAX + 50 }, (_, i) => ({ name: "c" + i }));
  assert.equal(cleanCommands(many).length, COMMANDS_MAX);
  assert.deepEqual(cleanCommands("nope"), []);
});

test("the menu opens only while the box holds one command word starting with a slash", () => {
  assert.equal(queryOf("/"), "");
  assert.equal(queryOf("/rev"), "rev");
  for (const t of ["", "rev", " /rev", "/review the diff", "/a/b", "/x\n", "/" + "n".repeat(NAME_MAX + 1)]) assert.equal(queryOf(t), null, JSON.stringify(t));
});

test("filtering ranks exact, prefix, then substring matches, and keeps Flockdeck's group apart", () => {
  const m = menuOf("/review", cmds);
  assert.deepEqual(m.agent.map((c) => c.name), ["review", "security-review"]);
  assert.deepEqual(m.own, []);
  assert.deepEqual(menuOf("/st", cmds).own.map((c) => c.name), ["stop"]);
  assert.equal(menuOf("/zzz", cmds), null);
  assert.equal(menuOf("hello", cmds), null);
  const all = menuOf("/", cmds);
  assert.deepEqual(flatten(all).map((c) => c.group + ":" + c.name), ["own:stop", "own:info", "agent:compact", "agent:review", "agent:init", "agent:security-review"]);
  const flood = menuOf("/", Array.from({ length: 150 }, (_, i) => ({ name: "c" + i })));
  assert.equal(flatten(flood).length, SHOWN_MAX);
  assert.equal(insertText({ name: "review" }), "/review ");
});

async function mount(commands) {
  forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {} });
  const st = { type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, commands };
  render(html`<${Structured} pane="p3" snap=${store.getState()} st=${st} store=${store} desk=${true} />`, container);
  await settle();
  return { container, store, done: async () => { render(null, container); await settle(); restore(); } };
}

test("typing / lists the agent's commands, keys move and choose, and the choice is text in the box", async () => {
  const m = await mount(cmds);
  try {
    const box = () => m.container.find(".structured-input");
    assert.equal(m.container.find(".slash-menu"), null);
    box().enter("/re");
    await settle();
    assert.deepEqual(m.container.findAll(".slash-item").map((e) => e.getAttribute("data-slash")), ["review", "security-review"]);
    assert.equal(m.container.findAll(".slash-group").map((e) => e.textContent).join("|"), "From the agent");
    box().keydown("ArrowDown");
    await settle();
    assert.equal(m.container.findAll(".slash-item").filter((e) => e.getAttribute("aria-selected") === "true")[0].getAttribute("data-slash"), "security-review");
    const ev = box().keydown("Enter");
    await settle();
    assert.equal(ev.defaultPrevented, true);
    assert.equal(box().value, "/security-review ");
    assert.equal(m.container.find(".slash-menu"), null, "the menu stayed open after a choice");
    assert.deepEqual(m.store.sent, [], "choosing a command sent something");
  } finally {
    await m.done();
  }
});

test("Escape closes the menu until the text changes, and Enter then sends the text", async () => {
  const m = await mount(cmds);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/c");
    await settle();
    box().keydown("Escape");
    await settle();
    assert.equal(m.container.find(".slash-menu"), null);
    box().enter("/co");
    await settle();
    assert.ok(m.container.find(".slash-menu"));
    // Closed again, Enter is the send key and sends the text as typed.
    box().keydown("Escape");
    await settle();
    assert.equal(box().keydown("Enter").defaultPrevented, true);
    assert.deepEqual(m.store.sent.map((c) => [c.cmd, c.text]), [["paneSend", "/co"]]);
  } finally {
    await m.done();
  }
});

test("Flockdeck's own commands are listed apart and run in Flockdeck, not sent to the agent", async () => {
  const m = await mount(cmds);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/");
    await settle();
    assert.equal(m.container.findAll(".slash-group").map((e) => e.textContent).join("|"), "Flockdeck|From the agent");
    m.container.findAll(".slash-item").filter((e) => e.getAttribute("data-slash") === "stop")[0].click();
    await settle();
    assert.deepEqual(m.store.sent.map((c) => c.cmd), ["structuredCancel"]);
    assert.equal(box().value, "");
  } finally {
    await m.done();
  }
});

test("a hostile name is not offered, and an agent with no commands gets no agent group", async () => {
  const m = await mount([{ name: "bad\nname" }, { name: "x y", description: "z" }]);
  try {
    m.container.find(".structured-input").enter("/");
    await settle();
    assert.deepEqual(m.container.findAll(".slash-item").map((e) => e.getAttribute("data-slash")), ["stop", "info"]);
    assert.equal(m.container.findAll(".slash-group").map((e) => e.textContent).join("|"), "Flockdeck");
  } finally {
    await m.done();
  }
});
