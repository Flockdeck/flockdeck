// The strip's More menu: a real menu's keys, where the keyboard goes when it opens and closes,
// and Escape left alone while it is closed. Toolbar is drawn on its own, the way the terminal
// pane's Conversation tab draws it (no lead), and with a lead and extra items, the way the
// structured pane does.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Toolbar } from "../src/drawer/conv/Toolbar.js";
import { createDom, install, settle } from "./dom.mjs";

const entries = [{ id: "a", kind: "prompt", text: "hi" }, { id: "b", kind: "reply", text: "hello" }];

async function mount(props = {}) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const calls = [];
  render(html`<${Toolbar} entries=${entries} onSearch=${() => true} onClear=${() => {}} onJump=${() => {}} canExport=${true} onExport=${() => calls.push("export")}
    copy=${() => Promise.resolve(true)} ...${props} />`, container);
  await settle();
  return { container, calls, doc: dom.document, done: async () => { render(null, container); await settle(); restore(); } };
}

const items = (m) => m.container.findAll('[role="menuitem"]');
const open = async (m) => { m.container.find(".conv-more-btn").click(); await settle(); };

test("opening the menu puts the keyboard on its first item", async () => {
  const m = await mount();
  try {
    await open(m);
    assert.equal(m.doc.activeElement, items(m)[0]);
  } finally { await m.done(); }
});

test("the menu follows its button in the tab order and its items are all tabbable", async () => {
  const m = await mount();
  try {
    await open(m);
    const more = m.container.find(".conv-more");
    const kids = more.childNodes.filter((n) => n.nodeType === 1);
    assert.equal(kids[0].getAttribute("class").includes("conv-more-btn"), true);
    assert.equal(kids[1].getAttribute("role"), "menu");
    for (const b of items(m)) assert.equal(b.getAttribute("tabindex"), null, "an item is taken out of the tab order");
    assert.equal(m.container.find(".conv-more-btn").getAttribute("tabindex"), null);
  } finally { await m.done(); }
});

test("arrow keys, Home and End move between items and wrap, skipping disabled ones", async () => {
  const m = await mount({ entries: [] });
  try {
    await open(m);
    // With nothing loaded Copy and Copy last reply are disabled; only Export can be reached.
    assert.deepEqual(items(m).filter((b) => !b.disabled).map((b) => b.textContent), ["Export"]);
    m.container.find(".conv-menu").keydown("ArrowDown");
    assert.equal(m.doc.activeElement.textContent, "Export");
  } finally { await m.done(); }
  const n = await mount({ items: [{ label: "Agent info", onClick: () => {} }] });
  try {
    await open(n);
    const [info, copy, last, exp] = items(n);
    assert.deepEqual(items(n).map((b) => b.textContent), ["Agent info", "Copy", "Copy last reply", "Export"]);
    assert.equal(n.doc.activeElement, info);
    const key = (k) => { const ev = n.doc.activeElement.keydown(k); assert.equal(ev.defaultPrevented, true, k + " was not handled"); return n.doc.activeElement; };
    assert.equal(key("ArrowDown"), copy);
    assert.equal(key("ArrowDown"), last);
    assert.equal(key("End"), exp);
    assert.equal(key("ArrowDown"), info, "ArrowDown at the end wraps");
    assert.equal(key("ArrowUp"), exp, "ArrowUp at the start wraps");
    assert.equal(key("Home"), info);
  } finally { await n.done(); }
});

test("ArrowDown on the closed button opens the menu", async () => {
  const m = await mount();
  try {
    const btn = m.container.find(".conv-more-btn");
    btn.focus();
    btn.keydown("ArrowDown");
    await settle();
    assert.ok(m.container.find(".conv-menu"));
    assert.equal(m.doc.activeElement, items(m)[0]);
  } finally { await m.done(); }
});

test("Escape with the menu closed is not swallowed, so it can still stop a turn", async () => {
  const m = await mount();
  try {
    const btn = m.container.find(".conv-more-btn");
    btn.focus();
    const ev = btn.keydown("Escape");
    assert.equal(ev.defaultPrevented, false);
    assert.equal(ev.stopped, false);
    assert.equal(m.container.find(".conv-menu"), null);
    // And the next mouse close does not pull the keyboard back to the button.
    const other = m.container.find(".conv-search-toggle");
    await open(m);
    other.focus();
    m.container.find(".conv-more").dispatch("focusout", { relatedTarget: other });
    await settle();
    assert.equal(m.container.find(".conv-menu"), null);
    assert.equal(m.doc.activeElement, other);
  } finally { await m.done(); }
});

test("Escape in the open menu closes it, is not passed on, and the keyboard goes back to the button", async () => {
  const m = await mount();
  try {
    await open(m);
    const ev = items(m)[0].keydown("Escape");
    await settle();
    assert.equal(m.container.find(".conv-menu"), null);
    assert.equal(ev.defaultPrevented, true);
    assert.equal(ev.stopped, true);
    assert.equal(m.doc.activeElement, m.container.find(".conv-more-btn"));
  } finally { await m.done(); }
});

test("choosing an item gives the keyboard back to the More button, not to the page", async () => {
  const picked = [];
  const m = await mount({ items: [{ label: "Agent info", onClick: () => picked.push("info") }] });
  try {
    for (const label of ["Agent info", "Copy", "Copy last reply", "Export"]) {
      await open(m);
      const item = items(m).find((b) => b.textContent === label);
      item.focus();
      item.click();
      await settle();
      assert.equal(m.container.find(".conv-menu"), null, label + " left the menu open");
      assert.equal(m.doc.activeElement, m.container.find(".conv-more-btn"), "after " + label + " the keyboard is not on the More button");
      m.doc._active = null; // the item is gone from the page, as in a browser
    }
    assert.deepEqual(picked, ["info"]);
    assert.deepEqual(m.calls, ["export"]);
  } finally { await m.done(); }
});

test("with no lead, as in the terminal pane's tab, the strip still has search and the menu", async () => {
  const m = await mount();
  try {
    assert.ok(m.container.find(".conv-search-toggle"));
    assert.ok(m.container.find(".conv-more-btn"));
    await open(m);
    assert.deepEqual(items(m).map((b) => b.textContent), ["Copy", "Copy last reply", "Export"]);
  } finally { await m.done(); }
});
