// The list keeps following the bottom when its own height changes (the composer grows, a panel opens),
// and leaves a reader who scrolled up where they were.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Log } from "../src/drawer/conv/Log.js";
import { createDom, install, settle } from "./dom.mjs";

const entries = Array.from({ length: 5 }, (_, i) => ({ id: "e" + i, kind: "prompt", text: "say " + i }));

async function mount() {
  const dom = createDom();
  const restore = install(dom);
  const seen = [];
  const before = globalThis.ResizeObserver;
  globalThis.ResizeObserver = class {
    constructor(fn) { this.fn = fn; seen.push(this); }
    observe(el) { this.el = el; }
    disconnect() { this.gone = true; }
  };
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  render(html`<${Log} entries=${entries} atStart=${true} />`, container);
  await settle();
  return {
    seen, list: container.find(".conv"),
    done: async () => { render(null, container); await settle(); globalThis.ResizeObserver = before; restore(); },
  };
}

test("a reader at the bottom is taken to the bottom again when the list is resized", async () => {
  const m = await mount();
  try {
    assert.equal(m.seen.length, 1);
    assert.ok(m.seen[0].el === m.list, "the list is not the element watched");
    // The composer grew: the list is shorter, its content the same, and it was at the bottom (nothing scrolled).
    Object.assign(m.list, { scrollHeight: 1000, clientHeight: 300, scrollTop: 0 });
    m.seen[0].fn([]);
    assert.equal(m.list.scrollTop, 1000);
  } finally { await m.done(); }
});

test("a reader who scrolled up is left where they are", async () => {
  const m = await mount();
  try {
    Object.assign(m.list, { scrollHeight: 1000, clientHeight: 300, scrollTop: 100 });
    m.list.dispatch("scroll");
    m.seen[0].fn([]);
    assert.equal(m.list.scrollTop, 100);
  } finally { await m.done(); }
});

test("the watcher is dropped with the list", async () => {
  const m = await mount();
  const w = m.seen[0];
  await m.done();
  assert.equal(w.gone, true);
});
