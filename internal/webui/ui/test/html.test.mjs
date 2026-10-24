// The pure modules are tested directly, with no rendering.
import { test } from "node:test";
import assert from "node:assert/strict";
import { html } from "../assets/html.js";
import { h, Fragment } from "../assets/vendor/preact.mjs";

test("html builds the same tree as h", () => {
  const a = html`<p class="x">hi</p>`;
  const b = h("p", { class: "x" }, "hi");
  assert.equal(a.type, b.type);
  assert.deepEqual(a.props, b.props);
});

test("html with two roots gives a list", () => {
  const out = html`<p>a</p><p>b</p>`;
  assert.ok(Array.isArray(out) && out.length === 2);
});

test("a component is called with its props", () => {
  const C = (p) => h(Fragment, null, p.n);
  const v = html`<${C} n=${3} />`;
  assert.equal(v.type, C);
  assert.equal(v.props.n, 3);
});
