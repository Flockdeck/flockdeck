// The slash menu's default row, composition, name collisions, selection and accessibility.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { cleanCommands, DESC_MAX } from "../src/drawer/slashModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const cmds = [
  { name: "compact", description: "Summarise the conversation" },
  { name: "review", description: "Review the diff", hint: "branch or PR" },
  { name: "init", description: "Write AGENTS.md" },
  { name: "security-review" },
];

async function mountWith(commands, { desk = true } = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {} });
  const draw = (list) => render(html`<${Structured} pane="p3" snap=${store.getState()} st=${{ type: "structured", id: "p3", kind: "structured", plan: [], usage: { used: 0, size: 0 }, commands: list }} store=${store} desk=${desk} />`, container);
  draw(commands);
  await settle();
  return { container, store, redraw: async (list) => { draw(list); await settle(); }, done: async () => { render(null, container); await settle(); restore(); } };
}
const items = (m) => m.container.findAll(".slash-item");
const named = (m, n) => items(m).filter((e) => e.getAttribute("data-slash") === n)[0];
const on = (m) => items(m).filter((e) => e.getAttribute("aria-selected") === "true").map((e) => e.getAttribute("data-slash"));

test("Enter on a partial match inserts the agent's command and never runs Stop", async () => {
  const m = await mountWith([{ name: "plan" }]);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/p");
    await settle();
    assert.deepEqual(on(m), ["plan"]);
    box().keydown("Enter");
    await settle();
    assert.equal(box().value, "/plan ");
    assert.deepEqual(m.store.sent, [], "Stop ran from a substring match");
    // Only Flockdeck's row matches: nothing is chosen and Enter and Tab are left alone.
    box().enter("/sto");
    await settle();
    assert.deepEqual(on(m), []);
    assert.equal(box().keydown("Enter").defaultPrevented, false);
    assert.equal(box().keydown("Tab").defaultPrevented, false);
    assert.deepEqual(m.store.sent, []);
    // An exact name chooses it.
    box().enter("/stop");
    await settle();
    assert.deepEqual(on(m), ["stop"]);
  } finally {
    await m.done();
  }
});

test("keys of an input method's composition are not taken", async () => {
  const m = await mountWith(cmds);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/re");
    await settle();
    for (const init of [{ isComposing: true }, { keyCode: 229 }]) {
      for (const key of ["Enter", "Tab", "ArrowDown", "ArrowUp", "Escape"]) {
        assert.equal(box().keydown(key, init).defaultPrevented, false, key + " " + JSON.stringify(init));
      }
    }
    assert.equal(box().value, "/re");
    assert.deepEqual(on(m), ["review"]);
  } finally {
    await m.done();
  }
});

test("an agent command named like one of Flockdeck's is not listed", async () => {
  const m = await mountWith([{ name: "stop", description: "agent stop" }, { name: "Stop" }, { name: "other" }]);
  try {
    m.container.find(".structured-input").enter("/");
    await settle();
    assert.deepEqual(items(m).map((e) => e.getAttribute("data-slash")), ["stop", "other"]);
    assert.equal(items(m)[0].textContent.includes("agent stop"), false);
  } finally {
    await m.done();
  }
});

test("the choice follows its command when the list changes, and the arrows wrap", async () => {
  const m = await mountWith(cmds);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/");
    await settle();
    assert.deepEqual(on(m), ["compact"]);
    box().keydown("ArrowDown");
    await settle();
    assert.deepEqual(on(m), ["review"]);
    await m.redraw([{ name: "new-first" }, ...cmds]);
    assert.deepEqual(on(m), ["review"], "the choice moved to another command");
    await m.redraw(cmds.filter((c) => c.name !== "review"));
    assert.deepEqual(on(m), ["new-first"].filter(() => false).concat(["compact"]), "with its row gone the default applies");
    box().keydown("ArrowUp");
    await settle();
    assert.deepEqual(on(m), ["stop"]);
    box().keydown("ArrowUp");
    await settle();
    assert.deepEqual(on(m), ["security-review"], "ArrowUp did not wrap to the last row");
    box().keydown("ArrowDown");
    await settle();
    assert.deepEqual(on(m), ["stop"], "ArrowDown did not wrap to the first row");
  } finally {
    await m.done();
  }
});

test("Tab chooses, a click chooses an agent row, hover moves the highlight, Escape does not propagate", async () => {
  const m = await mountWith(cmds);
  try {
    const box = () => m.container.find(".structured-input");
    box().enter("/i");
    await settle();
    assert.equal(box().keydown("Tab").defaultPrevented, true);
    await settle();
    assert.equal(box().value, "/init ");
    box().enter("/");
    await settle();
    named(m, "review").dispatch("mouseenter");
    await settle();
    assert.deepEqual(on(m), ["review"]);
    named(m, "review").click();
    await settle();
    assert.equal(box().value, "/review ");
    assert.deepEqual(m.store.sent, []);
    box().enter("/c");
    await settle();
    const esc = box().keydown("Escape");
    assert.equal(esc.defaultPrevented, true);
    assert.equal(esc.stopped, true, "Escape kept bubbling");
  } finally {
    await m.done();
  }
});

test("the menu has a count for screen readers, groups that are not options, and a combobox box", async () => {
  const m = await mountWith(cmds);
  try {
    const box = m.container.find(".structured-input");
    assert.equal(box.getAttribute("aria-autocomplete"), null);
    box.enter("/re");
    await settle();
    assert.equal(box.getAttribute("aria-autocomplete"), "list");
    assert.equal(box.getAttribute("role"), "combobox");
    assert.equal(m.container.find(".sr-only").textContent, "2 commands");
    assert.deepEqual(m.container.findAll("div").filter((e) => e.getAttribute("role") === "group").map((e) => e.getAttribute("aria-label")), ["From the agent"]);
    assert.equal(m.container.findAll("li").length, 0);
  } finally {
    await m.done();
  }
});

test("a relay window (no desk) has no menu and no box", async () => {
  const m = await mountWith(cmds, { desk: false });
  try {
    assert.equal(m.container.find(".structured-input"), null);
    assert.equal(m.container.find(".slash-menu"), null);
  } finally {
    await m.done();
  }
});

test("blank-looking and stacked-mark names are dropped, texts are cut on characters, and a huge list is scanned in part", () => {
  const bad = ["aㅤb", "ㅤ", "xᅟ", "xᅠ", "xﾠ", "⠀", "á̂̃", "zalgò́̂"].map((name) => ({ name }));
  assert.deepEqual(cleanCommands([...bad, { name: "é̂" }]).map((c) => c.name), ["é̂"]);
  const [c] = cleanCommands([{ name: "x", description: "😀".repeat(DESC_MAX) }]);
  assert.ok(c.description.length > 0 && Array.from(c.description).length <= DESC_MAX);
  assert.ok(!/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/.test(c.description), "a surrogate pair was split");
  const late = Array.from({ length: 1000 }, () => ({ name: "bad name" }));
  late.push({ name: "too-late" });
  assert.deepEqual(cleanCommands(late), []);
});
