// The popover's effects: which anchor and which onClose it acts on, as the owner
// redraws it with new ones.
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { Popover } from "../src/shell/Popover.js";
import { createDom, install, settle } from "./dom.mjs";

async function mount() {
  const dom = createDom();
  const restore = install(dom);
  // The fake document has no listeners of its own: keep the popover's and let a test press anywhere.
  const down = new Set();
  dom.document.addEventListener = (t, f) => { if (t === "mousedown") down.add(f); };
  dom.document.removeEventListener = (t, f) => { if (t === "mousedown") down.delete(f); };
  const press = (target) => [...down].forEach((f) => f({ type: "mousedown", target }));
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const a = dom.document.createElement("button");
  const b = dom.document.createElement("button");
  const outside = dom.document.createElement("button");
  for (const n of [a, b, outside]) dom.body.appendChild(n);
  const draw = async (anchor, onClose) => {
    render(html`<${Popover} anchor=${anchor} label="List" onClose=${onClose}><button type="button" class="r1">one</button><button type="button" class="r2">two</button><//>`, container);
    await settle();
  };
  return { dom, container, a, b, outside, draw, press, done: () => { render(null, container); restore(); } };
}

test("a new onClose from a redraw is the one a press outside calls, and a redraw does not move the keyboard", async () => {
  const m = await mount();
  try {
    const first = [], second = [];
    await m.draw(m.a, (x) => first.push(x));
    const r2 = m.container.find(".r2");
    r2.focus();
    await m.draw(m.a, (x) => second.push(x));
    assert.equal(m.dom.document.activeElement, r2, "a redraw leaves the keyboard on the row");
    m.press(m.outside);
    assert.deepEqual(first, [], "the first onClose is not kept");
    assert.deepEqual(second, [false]);
  } finally { m.done(); }
});

test("a different anchor takes the keyboard again and is the button a press on does not close it", async () => {
  const m = await mount();
  try {
    const closed = [];
    await m.draw(m.a, (x) => closed.push(x));
    m.container.find(".r2").focus();
    await m.draw(m.b, (x) => closed.push(x));
    assert.equal(m.dom.document.activeElement, m.container.find(".r1"), "the list opened from another badge takes the keyboard");
    m.press(m.b);
    assert.deepEqual(closed, [], "a press on its own anchor is not outside");
    m.press(m.a);
    assert.deepEqual(closed, [false], "the old anchor is outside now");
  } finally { m.done(); }
});
