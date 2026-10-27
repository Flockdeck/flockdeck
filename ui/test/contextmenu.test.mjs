// The right-click menu: the pure parts (which items, where it goes, where the
// keyboard moves) and the component on the fake page (what opens it, what leaves
// the window's own menu alone, the keyboard, and where the focus goes back to).
import { test } from "node:test";
import assert from "node:assert/strict";
import { render } from "../src/vendor/preact.mjs";
import { html } from "../src/html.js";
import { ContextMenu } from "../src/shell/ContextMenu.js";
import { MENUS, MARGIN, build, edgeItem, hasSelection, hintFor, nextItem, place, pointOf, resolve, typeAhead } from "../src/shell/menus.js";
import { menuMove } from "../src/shell/keys.js";
import { Shell } from "../src/shell/app.js";
import { fakeKeys, fakeState } from "./util.mjs";
import { createDom, install, settle } from "./dom.mjs";

// These tests are of the component and the pure parts, not of the pane menus (see
// panemenus.test.mjs), so the agent menu here is a small one of their own.
MENUS.agent = (el, ctx) => {
  const pane = el.getAttribute("data-pane");
  return [
    { id: "open", label: "Open", run: () => ctx.drawer && ctx.drawer.open(pane), hidden: !pane || !ctx.drawer },
    { id: "copy-id", label: "Copy pane id", run: () => ctx.copy && ctx.copy(pane, "the pane id"), hidden: !pane || !ctx.copy },
  ];
};

const KEYS = [
  { id: "paneInfo", keys: "Ctrl+Shift+Y", label: "Pane info — what it is running" },
  { id: "closePane", keys: "Ctrl+Shift+W", label: "Close the pane", short: "Close" },
  { id: "nokey", keys: "", label: "Nothing bound" },
];

test("an action item takes its label and key hint from the table and runs through act", () => {
  const ran = [];
  const items = resolve([{ action: "paneInfo" }, { action: "closePane", danger: true }, { action: "nokey" }], { keys: KEYS, implemented: ["paneInfo", "closePane", "nokey"], act: (id) => ran.push(id) });
  assert.deepEqual(items.map((i) => [i.label, i.hint, i.danger]), [["Pane info", "Ctrl+Shift+Y", false], ["Close", "Ctrl+Shift+W", true], ["Nothing bound", "", false]]);
  items[0].run();
  assert.deepEqual(ran, ["paneInfo"]);
});

test("an action this window does not run, and a hidden item, are left out", () => {
  const items = resolve([{ action: "paneInfo" }, { id: "x", label: "X", run() {}, hidden: true }, { id: "y", label: "Y", run() {} }], { keys: KEYS, implemented: [] });
  assert.deepEqual(items.map((i) => i.label), ["Y"]);
});

test("rules never lead, trail or double", () => {
  const it = (id) => ({ id, label: id, run() {} });
  const out = resolve([{ separator: true }, it("a"), { separator: true }, { separator: true }, it("b"), { separator: true }], {});
  assert.deepEqual(out.map((i) => (i.separator ? "-" : i.id)), ["a", "-", "b"]);
  const gap = resolve([it("a"), { separator: true }, { id: "h", label: "h", hidden: true }, { separator: true }, it("b")], {});
  assert.deepEqual(gap.map((i) => (i.separator ? "-" : i.id)), ["a", "-", "b"]);
});

test("a check item says whether it is checked", () => {
  const [a, b] = resolve([{ id: "a", label: "A", checked: true, run() {} }, { id: "b", label: "B", run() {} }], {});
  assert.equal(a.checked, true);
  assert.equal(b.checked, undefined);
});

test("hintFor reads the table and draws the key as the palette does", () => {
  assert.equal(hintFor(KEYS, "paneInfo"), "Ctrl+Shift+Y");
  assert.equal(hintFor(KEYS, "unknown"), "");
});

test("a kind with no menu builds nothing, so the window's menu is left alone", () => {
  assert.deepEqual(build("nope", null, {}), []);
});

test("the agent menu opens the drawer on the row's pane and copies its id", () => {
  const calls = [];
  const el = { getAttribute: (k) => (k === "data-pane" ? "p-7" : null) };
  const ctx = { drawer: { open: (p) => calls.push(["open", p]) }, copy: (t, w) => calls.push(["copy", t, w]) };
  const items = resolve(MENUS.agent(el, ctx), ctx);
  assert.deepEqual(items.map((i) => i.label), ["Open", "Copy pane id"]);
  items.forEach((i) => i.run());
  assert.deepEqual(calls, [["open", "p-7"], ["copy", "p-7", "the pane id"]]);
});

test("place keeps a menu at the pointer, flips it at the edges and clamps it", () => {
  const size = { vw: 800, vh: 600, w: 200, h: 100 };
  assert.deepEqual(place({ ...size, x: 100, y: 100 }), { left: 100, top: 100, maxHeight: 584 });
  const flipped = place({ ...size, x: 700, y: 550 });
  assert.equal(flipped.left, 500);
  assert.equal(flipped.top, 450);
  // Flipping would still run off the left or the top of a small window: clamp.
  const small = place({ x: 100, y: 100, w: 400, h: 500, vw: 300, vh: 300 });
  assert.equal(small.left, MARGIN);
  assert.equal(small.top, MARGIN);
  assert.equal(small.maxHeight, 300 - 2 * MARGIN);
});

test("pointOf is the pointer for the mouse and the element's lower left for the keyboard", () => {
  const el = { getBoundingClientRect: () => ({ left: 5, bottom: 50 }) };
  assert.deepEqual(pointOf({ clientX: 12, clientY: 34 }, el), { x: 12, y: 34 });
  assert.deepEqual(pointOf({ clientX: 0, clientY: 0 }, el), { x: 5, y: 50 });
});

test("moving skips rules and disabled items and wraps", () => {
  const items = [{ label: "One" }, { separator: true }, { label: "Two", disabled: true }, { label: "Three" }];
  assert.equal(nextItem(items, 0, 1), 3);
  assert.equal(nextItem(items, 3, 1), 0);
  assert.equal(nextItem(items, 0, -1), 3);
  assert.equal(edgeItem(items, 0), 0);
  assert.equal(edgeItem(items, 3), 3);
  assert.equal(nextItem([{ label: "x", disabled: true }], 0, 1), -1);
});

test("type-ahead finds the next label that starts with the letter", () => {
  const items = [{ label: "Copy id" }, { label: "Close" }, { separator: true }, { label: "Open" }];
  assert.equal(typeAhead(items, 0, "c"), 1);
  assert.equal(typeAhead(items, 1, "C"), 0);
  assert.equal(typeAhead(items, 0, "o"), 3);
  assert.equal(typeAhead(items, 0, "z"), -1);
});

test("menuMove reads the keys of a menu", () => {
  const k = (key, more = {}) => menuMove({ key, ...more }, 4);
  assert.deepEqual(k("ArrowDown"), { step: 1 });
  assert.deepEqual(k("ArrowUp"), { step: -1 });
  assert.deepEqual(k("Home"), { to: 0 });
  assert.deepEqual(k("End"), { to: 3 });
  assert.equal(k("Enter"), "accept");
  assert.equal(k(" "), "accept");
  assert.equal(k("Escape"), "cancel");
  assert.equal(k("Tab"), "tab");
  assert.deepEqual(k("c"), { type: "c" });
  assert.equal(k("c", { ctrlKey: true }), "");
  assert.equal(k("c", { isComposing: true }), "");
  assert.equal(k("F5"), "");
});

// The component on the fake page. Every element answers a size of 200 by 100,
// which is what the flip is measured with.
async function mount({ implemented = ["paneInfo"] } = {}) {
  const dom = createDom();
  const restore = install(dom);
  dom.window.innerWidth = 800;
  dom.window.innerHeight = 600;
  // Preact makes its elements with createElementNS.
  const make = dom.document.createElementNS;
  dom.document.createElementNS = (ns, n) => {
    const el = make(ns, n);
    el.getBoundingClientRect = () => ({ left: 0, top: 0, right: 200, bottom: 100, width: 200, height: 100 });
    return el;
  };
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const calls = [];
  const ctx = { keys: KEYS, implemented, act: (id) => calls.push(["act", id]), drawer: { open: (p) => calls.push(["open", p]) }, copy: (t) => calls.push(["copy", t]) };
  render(
    html`<div id="page">
      <div id="row" data-ctx="agent" data-pane="p1" tabindex="0"><span id="name">Agent one</span></div>
      <div id="plain"><span id="plain-in">Nothing here</span></div>
      <div id="shell" data-ctx="agent" data-pane="p2" tabindex="0"><textarea id="box"></textarea><div id="edit" contenteditable="true">x</div></div>
      <div id="unknown" data-ctx="nope"></div>
      <button id="after">After</button>
      <${ContextMenu} win=${dom.window} ctx=${ctx} />
    </div>`,
    container
  );
  await settle();
  const $ = (sel) => container.querySelector(sel);
  const menu = () => $('[role="menu"]');
  const items = () => container.findAll('[role="menuitem"]');
  const right = async (el, init = { clientX: 40, clientY: 30 }) => {
    const ev = el.dispatch("contextmenu", init);
    await settle();
    return ev;
  };
  const press = async (key, init) => {
    const ev = dom.document.activeElement.keydown(key, init);
    await settle();
    return ev;
  };
  return { dom, container, $, menu, items, right, press, calls, done: () => { render(null, container); restore(); } };
}

test("a right-click on a part with a menu opens it, stops the window's menu and takes the keyboard", async () => {
  const m = await mount();
  try {
    const ev = await m.right(m.$("#name"));
    assert.equal(ev.defaultPrevented, true);
    assert.ok(m.menu(), "the menu is open");
    assert.equal(m.menu().getAttribute("role"), "menu");
    assert.deepEqual(m.items().map((i) => i.textContent.trim()), ["Open", "Copy pane id"]);
    assert.equal(m.dom.document.activeElement, m.items()[0], "the first item has the keyboard");
  } finally {
    m.done();
  }
});

test("a right-click on a part with no data-ctx, or an unknown kind, is left to the window", async () => {
  const m = await mount();
  try {
    for (const id of ["#plain-in", "#unknown"]) {
      const ev = await m.right(m.$(id));
      assert.equal(ev.defaultPrevented, false, id);
      assert.equal(m.menu(), null, id);
    }
  } finally {
    m.done();
  }
});

test("a text field inside a part with a menu keeps the window's menu", async () => {
  const m = await mount();
  try {
    for (const id of ["#box", "#edit"]) {
      const ev = await m.right(m.$(id));
      assert.equal(ev.defaultPrevented, false, id);
      assert.equal(m.menu(), null, id);
    }
  } finally {
    m.done();
  }
});

test("selected text inside the part keeps the window's menu, a selection elsewhere does not", async () => {
  const m = await mount();
  try {
    const row = m.$("#row");
    m.dom.window.getSelection = () => ({ anchorNode: m.$("#name"), focusNode: m.$("#name"), toString: () => "Agent" });
    let ev = await m.right(m.$("#name"));
    assert.equal(ev.defaultPrevented, false);
    assert.equal(m.menu(), null);
    m.dom.window.getSelection = () => ({ anchorNode: m.$("#plain-in"), focusNode: m.$("#plain-in"), toString: () => "Nothing" });
    ev = await m.right(row);
    assert.equal(ev.defaultPrevented, true);
    assert.ok(m.menu());
    m.dom.window.getSelection = () => ({ anchorNode: row, focusNode: row, toString: () => "  " });
    assert.equal(hasSelection(m.dom.window, row), false, "white space is not a selection");
  } finally {
    m.done();
  }
});

test("the arrow keys, Home, End and a letter move; Enter chooses and closes", async () => {
  const m = await mount();
  try {
    await m.right(m.$("#row"));
    const [open, copy] = m.items();
    await m.press("ArrowDown");
    assert.equal(m.dom.document.activeElement, copy);
    await m.press("ArrowDown");
    assert.equal(m.dom.document.activeElement, open, "wraps");
    await m.press("End");
    assert.equal(m.dom.document.activeElement, copy);
    await m.press("Home");
    assert.equal(m.dom.document.activeElement, open);
    await m.press("c");
    assert.equal(m.dom.document.activeElement, copy, "type-ahead");
    await m.press("Enter");
    assert.equal(m.menu(), null, "closed");
    assert.deepEqual(m.calls, [["copy", "p1"]]);
  } finally {
    m.done();
  }
});

test("Space chooses the item and a click on an item chooses it", async () => {
  const m = await mount();
  try {
    await m.right(m.$("#row"));
    await m.press(" ");
    assert.deepEqual(m.calls, [["open", "p1"]]);
    await m.right(m.$("#row"));
    m.items()[1].click();
    await settle();
    assert.deepEqual(m.calls, [["open", "p1"], ["copy", "p1"]]);
    assert.equal(m.menu(), null);
  } finally {
    m.done();
  }
});

test("Escape closes the menu and puts the keyboard back where it was", async () => {
  const m = await mount();
  try {
    m.$("#row").focus();
    await m.right(m.$("#row"));
    assert.notEqual(m.dom.document.activeElement, m.$("#row"));
    const ev = await m.press("Escape");
    assert.equal(ev.defaultPrevented, true);
    assert.equal(m.menu(), null);
    assert.equal(m.dom.document.activeElement, m.$("#row"));
    assert.deepEqual(m.calls, []);
  } finally {
    m.done();
  }
});

test("Tab closes the menu and returns the keyboard too", async () => {
  const m = await mount();
  try {
    m.$("#after").focus();
    await m.right(m.$("#row"));
    await m.press("Tab");
    assert.equal(m.menu(), null);
    assert.equal(m.dom.document.activeElement, m.$("#after"));
  } finally {
    m.done();
  }
});

test("a press outside the menu closes it and a press inside does not", async () => {
  const m = await mount();
  try {
    await m.right(m.$("#row"));
    m.items()[0].dispatch("mousedown");
    await settle();
    assert.ok(m.menu(), "inside");
    m.$("#plain-in").dispatch("mousedown");
    await settle();
    assert.equal(m.menu(), null, "outside");
  } finally {
    m.done();
  }
});

test("a right-click elsewhere closes the menu, and on another row opens that row's", async () => {
  const m = await mount();
  try {
    await m.right(m.$("#row"));
    await m.right(m.$("#plain-in"));
    assert.equal(m.menu(), null);
    await m.right(m.$("#row"));
    await m.right(m.$("#shell"));
    m.items()[0].click();
    await settle();
    assert.deepEqual(m.calls, [["open", "p2"]]);
  } finally {
    m.done();
  }
});

test("the menu key opens it below the element the keyboard is on", async () => {
  const m = await mount();
  try {
    const row = m.$("#row");
    row.getBoundingClientRect = () => ({ left: 20, bottom: 90, top: 60, right: 200 });
    row.focus();
    const ev = await m.right(row, { clientX: 0, clientY: 0 });
    assert.equal(ev.defaultPrevented, true);
    assert.match(m.menu().style.cssText, /left:20px;top:90px/);
  } finally {
    m.done();
  }
});

test("a menu opened near the corner is flipped back inside the window", async () => {
  const m = await mount();
  try {
    await m.right(m.$("#row"), { clientX: 790, clientY: 590 });
    assert.match(m.menu().style.cssText, /left:590px;top:490px/);
  } finally {
    m.done();
  }
});

test("the agent menu is built from the bag the page gives it", async () => {
  const m = await mount({ implemented: [] });
  try {
    assert.deepEqual(build("agent", m.$("#row"), { drawer: {}, copy() {}, keys: KEYS, implemented: [] }).map((i) => i.id), ["open", "copy-id"]);
    assert.deepEqual(build("agent", m.$("#row"), { keys: KEYS, implemented: [] }), [], "with no drawer and no copy there is nothing to offer");
  } finally {
    m.done();
  }
});

test("a click is ignored until the opening press is over or a frame has passed", async () => {
  const m = await mount();
  try {
    const frames = [];
    m.dom.window.requestAnimationFrame = (fn) => frames.push(fn);
    await m.right(m.$("#row"));
    m.items()[0].click();
    await settle();
    assert.ok(m.menu(), "still open");
    assert.deepEqual(m.calls, []);
    frames.shift()();
    m.items()[0].click();
    await settle();
    assert.deepEqual(m.calls, [["open", "p1"]]);
    // The press's own pointerup arms it as well.
    await m.right(m.$("#row"));
    m.dom.window.dispatch("pointerup");
    m.items()[1].click();
    await settle();
    assert.deepEqual(m.calls, [["open", "p1"], ["copy", "p1"]]);
  } finally {
    m.done();
  }
});

test("a right-click the menu does not take forgets the old opener", async () => {
  const m = await mount();
  try {
    m.$("#row").focus();
    await m.right(m.$("#row"));
    await m.right(m.$("#plain-in"));
    m.$("#after").focus();
    await m.right(m.$("#row"));
    await m.press("Escape");
    assert.equal(m.dom.document.activeElement, m.$("#after"));
  } finally {
    m.done();
  }
});

test("an item with an action id runs through the Shell's act path", async () => {
  const dom = createDom();
  const restore = install(dom);
  dom.window.innerWidth = 800;
  dom.window.innerHeight = 600;
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  MENUS.probe = () => [{ action: "worktrees" }];
  const ran = [];
  try {
    render(html`<${Shell} hello=${{ keys: fakeKeys }} state=${fakeState} window=${dom.window} actions=${{ worktrees: () => ran.push("worktrees") }}><div id="probe" data-ctx="probe">x</div><//>`, container);
    await settle();
    container.querySelector("#probe").dispatch("contextmenu", { clientX: 10, clientY: 10 });
    await settle();
    const item = container.findAll('[role="menuitem"]')[0];
    assert.equal(item.textContent.includes("Worktrees"), true);
    assert.equal(item.textContent.includes("Ctrl+Shift+G"), true, "the hint comes from the table");
    dom.window.dispatch("pointerup");
    item.click();
    await settle();
    assert.deepEqual(ran, ["worktrees"]);
  } finally {
    delete MENUS.probe;
    render(null, container);
    restore();
  }
});
