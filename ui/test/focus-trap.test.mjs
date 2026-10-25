// Tab inside a dialog reaches what the browser's Tab reaches: links and tab
// stops, not the nodes a roving group keeps at tabindex -1.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createDom, install } from "./dom.mjs";
import { focusables, trapTab } from "../src/shell/focus.js";
import { trapTab as fromStart } from "../src/shell/Start.js";
import { trapTab as fromUpdates } from "../src/shell/Updates.js";

function dialog() {
  const dom = createDom();
  const restore = install(dom);
  const doc = dom.document;
  const root = doc.createElement("div");
  dom.body.appendChild(root);
  const make = (tag, attrs = {}) => {
    const n = doc.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    root.appendChild(n);
    return n;
  };
  return { root, make, doc, restore };
}

test("links and tab stops are reachable, and roving members at tabindex -1, hidden inputs, disabled controls and bare anchors are not", () => {
  const d = dialog();
  try {
    const first = d.make("button");
    const link = d.make("a", { href: "https://example.test" });
    d.make("a"); // no href: not a tab stop
    const roving = d.make("button", { tabindex: "0" });
    d.make("button", { tabindex: "-1" });
    d.make("button", { tabindex: "-1" });
    d.make("input", { type: "hidden" });
    const off = d.make("button");
    off.disabled = true;
    const last = d.make("button");
    assert.deepEqual(focusables(d.root), [first, link, roving, last]);
  } finally {
    d.restore();
  }
});

test("Tab wraps over the reachable controls only, the link included", () => {
  const d = dialog();
  try {
    const a = d.make("button");
    const link = d.make("a", { href: "https://example.test" });
    d.make("button", { tabindex: "-1" });
    const z = d.make("button");
    const press = (shiftKey) => { const e = { shiftKey, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; } }; trapTab(e, d.root); return e; };
    a.focus();
    assert.equal(press(false).defaultPrevented, true);
    assert.equal(d.doc.activeElement, link, "Tab from the first control skipped the link");
    press(false);
    assert.equal(d.doc.activeElement, z, "Tab stopped on a tabindex -1 node");
    press(false);
    assert.equal(d.doc.activeElement, a, "Tab did not wrap");
    press(true);
    assert.equal(d.doc.activeElement, z, "Shift+Tab did not wrap");
  } finally {
    d.restore();
  }
});

test("the dialogs use the one trap", () => {
  assert.equal(fromStart, trapTab);
  assert.equal(fromUpdates, trapTab);
});
