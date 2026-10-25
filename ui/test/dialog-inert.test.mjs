// A dialog makes the page behind it inert while it is open, except what must stay usable.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Dialog } from "../src/shell/Dialog.js";
import { ModalScrim } from "../src/shell/inert.js";
import { createDom, install, settle } from "./dom.mjs";

async function mount() {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  // a: the first dialog, b: a second one over it, ask: a ModalScrim question.
  const page = ({ a = false, b = false, ask = false } = {}) => html`<div id="app">
    <main id="page"><button id="opener">Behind</button></main>
    <p id="note" role="status"></p>
    <div id="approvals" class="approvals" data-keep-live role="region"><section role="alert"><button id="yes">Allow</button></section></div>
    ${a ? html`<${Dialog} id="da" title="A" onClose=${() => {}}><button id="ina">In A</button><//>` : null}
    ${b ? html`<${Dialog} id="db" title="B" onClose=${() => {}}><button id="inb">In B</button><//>` : null}
    ${ask ? html`<${ModalScrim}><button id="inask">Sure</button><//>` : null}
  </div>`;
  const draw = async (props) => { render(page(props), container); await settle(); };
  const inert = (id) => container.querySelector(id).getAttribute("inert");
  return { dom, restore, draw, inert, container };
}

test("the page behind a dialog is inert until it closes, and pending requests and status lines stay usable", async () => {
  const m = await mount();
  try {
    await m.draw({ a: true });
    assert.equal(m.inert("#page"), "", "the page is inert");
    assert.equal(m.inert("#note"), null, "a status line stays live");
    assert.equal(m.inert("#approvals"), null, "a request waiting for an answer can still be answered");
    await m.draw({});
    assert.equal(m.inert("#page"), null, "released when the dialog closes");
  } finally { m.restore(); }
});

test("a bare scrim question makes the page inert too", async () => {
  const m = await mount();
  try {
    await m.draw({ ask: true });
    assert.equal(m.inert("#page"), "");
    await m.draw({});
    assert.equal(m.inert("#page"), null);
  } finally { m.restore(); }
});

test("two dialogs at once: the page stays inert until the last one closes, whichever order they close in", async () => {
  for (const first of ["a", "b"]) {
    const m = await mount();
    try {
      await m.draw({ a: true });
      await m.draw({ a: true, b: true });
      assert.equal(m.inert("#page"), "");
      await m.draw(first === "a" ? { b: true } : { a: true });
      assert.equal(m.inert("#page"), "", "one dialog is still open (closed " + first + " first)");
      await m.draw({});
      assert.equal(m.inert("#page"), null, "released by the last one (closed " + first + " first)");
    } finally { m.restore(); }
  }
});

test("on close the page is live before the focus goes back to where it was", async () => {
  const m = await mount();
  try {
    await m.draw({});
    const opener = m.container.querySelector("#opener");
    opener.focus();
    await m.draw({ a: true });
    await m.draw({});
    assert.equal(m.inert("#page"), null);
    assert.equal(m.dom.document.activeElement, opener, "the focus is back on the opener");
  } finally { m.restore(); }
});
