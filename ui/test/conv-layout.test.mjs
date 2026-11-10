// The structured pane's layout: one header strip, the log filling the drawer, one border and one type scale.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Structured } from "../src/drawer/Structured.js";
import { forgetComposerMemory } from "../src/drawer/composerModel.js";
import { createDom, install, settle } from "./dom.mjs";
import { fakeStore } from "./termfakes.mjs";

const css = readFileSync(new URL("../src/drawer/drawer.css", import.meta.url), "utf8");

async function mount(extra = {}) {
  forgetComposerMemory();
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const store = fakeStore({ connection: "open", hello: { type: "hello" }, state: {} });
  const st = {
    type: "structured", id: "p3", kind: "structured", plan: [], status: "working", activity: "Run go test", usage: { used: 38, size: 100 },
    modes: { current: "default", available: [{ id: "default", name: "Default" }, { id: "plan", name: "Plan" }] },
    models: { current: "pro", available: [{ id: "pro", name: "Pro" }] }, ...extra,
  };
  const conv = { supported: true, entries: [], atStart: true };
  render(html`<${Structured} pane="p3" snap=${store.getState()} st=${st} store=${store} desk=${true} conv=${conv} onSearch=${() => true} />`, container);
  await settle();
  return { container, store, done: async () => { render(null, container); await settle(); restore(); } };
}

test("one strip holds the activity, meter, pickers, search and More, and the old rows are gone", async () => {
  const m = await mount();
  try {
    const strip = m.container.find(".conv-strip");
    assert.ok(strip, "no strip");
    for (const sel of [".structured-dot", ".structured-activity", ".usage-meter", '[data-setting="mode"]', '[data-setting="model"]', ".conv-search-toggle", ".conv-more-btn"]) {
      assert.ok(strip.find(sel), sel + " is not in the strip");
    }
    for (const gone of [".structured-facts", ".structured-usage", ".structured-info-btn", ".conv-copy", ".conv-export"]) {
      assert.equal(m.container.find(gone), null, gone + " is still on the page");
    }
    assert.equal(m.container.findAll(".conv-strip").length, 1);
    // The pickers keep a name for assistive technology.
    assert.equal(strip.find(".structured-setting-name").textContent, "Mode");
  } finally {
    await m.done();
  }
});

test("the search field opens from its button and closes again", async () => {
  const m = await mount();
  try {
    assert.equal(m.container.find(".conv-search-input"), null);
    m.container.find(".conv-search-toggle").click();
    await settle();
    assert.ok(m.container.find(".conv-search-input"));
    assert.equal(m.container.find(".conv-search-toggle").getAttribute("aria-expanded"), "true");
    m.container.find(".conv-search-clear").click();
    await settle();
    assert.equal(m.container.find(".conv-search-input"), null);
  } finally {
    await m.done();
  }
});

test("Escape in the More menu closes it and does not stop the running turn", async () => {
  const m = await mount();
  try {
    m.container.find(".conv-more-btn").click();
    await settle();
    assert.ok(m.container.find(".conv-menu"));
    assert.equal(m.container.find(".conv-more-btn").getAttribute("aria-expanded"), "true");
    const ev = m.container.find(".conv-menu").keydown("Escape");
    await settle();
    assert.equal(m.container.find(".conv-menu"), null);
    assert.equal(ev.defaultPrevented, true);
    assert.equal(m.store.sent.filter((c) => c.cmd === "structuredCancel").length, 0, "Escape in the menu stopped the turn");
  } finally {
    await m.done();
  }
});

test("the log fills the drawer: no height cap, one flex chain, and no second scroller around it", () => {
  assert.doesNotMatch(css, /\.structured-log[^}]*50vh/);
  assert.doesNotMatch(css, /\.structured-log\s*\{[^}]*max-height/);
  const pane = css.match(/\.structured \{([^}]*)\}/)[1];
  assert.match(pane, /flex: 1 1 auto/);
  assert.match(pane, /min-height: 0/);
  const log = css.match(/\.structured > \.structured-log \{([^}]*)\}/)[1];
  assert.match(log, /flex: 1 1 auto/);
  assert.doesNotMatch(log, /overflow/, "the log's scroll is the .conv rule's, set once");
});

test("the drawer uses one panel border and a three-step type scale", () => {
  assert.doesNotMatch(css, /var\(--border/, "the undefined --border token is still used");
  assert.match(css, /--panel-line: var\(--line\)/);
  for (const [name, px] of [["sm", 11], ["md", 12], ["lg", 13]]) assert.match(css, new RegExp("--fs-" + name + ": " + px + "px"));
  // Outside the token definitions no 11, 12 or 13 px size is written out.
  const body = css.replace(/--fs-(sm|md|lg): \d+px;/g, "");
  assert.doesNotMatch(body, /font(-size)?:[^;]*\b1[123](\.5)?px/);
  assert.equal((css.match(/^\.conv-who \{/gm) || []).length, 1, "a second .conv-who rule cancels the first");
  assert.doesNotMatch(css, /\.conv-who \{[^}]*text-transform: uppercase/);
});
