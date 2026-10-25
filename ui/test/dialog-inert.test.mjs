// A dialog makes the page behind it inert while it is open, except what announces changes.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Dialog } from "../src/shell/Dialog.js";
import { createDom, install, settle } from "./dom.mjs";

test("the page behind a dialog is inert until it closes, and a status line is left live", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const container = dom.document.createElement("div");
    dom.body.appendChild(container);
    const page = (open) => html`<div id="app"><main id="page"><button>Behind</button></main><p id="note" role="status"></p>
      ${open ? html`<${Dialog} id="d" title="Title" onClose=${() => {}}><button>In</button><//>` : null}</div>`;
    render(page(true), container);
    await settle();
    assert.equal(container.querySelector("#page").getAttribute("inert"), "", "the page is inert");
    assert.equal(container.querySelector("#note").getAttribute("inert"), null, "a status line stays live");
    render(page(false), container);
    await settle();
    assert.equal(container.querySelector("#page").getAttribute("inert"), null, "released when the dialog closes");
  } finally { restore(); }
});
