// The fake DOM must not make a failing assertion on two elements expensive: node
// prints both in the failure message, and that once walked a whole page through
// the parent and child links until it ran out of memory.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createDom } from "./dom.mjs";

test("a failing equal on two elements of a big tree fails fast, with a short message", () => {
  const dom = createDom();
  const root = dom.document.createElement("div");
  dom.body.appendChild(root);
  let last = root;
  for (let i = 0; i < 3000; i++) {
    const el = dom.document.createElement("div");
    el.setAttribute("data-i", String(i));
    last.appendChild(el);
    if (i % 3) last = el;
  }
  const a = root.childNodes[0];
  const b = last;
  const t0 = Date.now();
  let message = "";
  try {
    assert.equal(a, b);
  } catch (e) {
    message = String(e.message);
  }
  assert.ok(message.length > 0, "the assertion did not fail");
  assert.ok(message.length < 20000, "the failure message is " + message.length + " characters");
  assert.ok(Date.now() - t0 < 1000, "the failure took " + (Date.now() - t0) + " ms");
});

test("the links between nodes still work and are not listed", () => {
  const dom = createDom();
  const p = dom.document.createElement("div");
  const c = dom.document.createElement("span");
  p.appendChild(c);
  assert.equal(c.parentNode, p);
  assert.equal(p.childNodes[0], c);
  assert.equal(c.ownerDocument, dom.document);
  for (const k of ["parentNode", "childNodes", "ownerDocument", "listeners"]) assert.ok(!Object.keys(c).includes(k), k + " is listed");
});
