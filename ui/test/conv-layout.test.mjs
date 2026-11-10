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

// --- structure: what scrolls, and what can clip a popover --------------------

/** rules reads the sheet's top-level style rules as [selector, {prop: value}], leaving @media blocks out. */
function rules(sheet) {
  const out = [];
  let text = sheet.replace(/\/\*[\s\S]*?\*\//g, "");
  let depth = 0, start = 0, head = "";
  for (let i = 0; i < text.length; i++) {
    if (text[i] === "{") {
      if (depth === 0) head = text.slice(start, i).trim();
      depth++;
      if (depth === 1) start = i + 1;
    } else if (text[i] === "}") {
      depth--;
      if (depth === 0) {
        if (!head.startsWith("@")) {
          const props = {};
          for (const d of text.slice(start, i).split(";")) {
            const at = d.indexOf(":");
            if (at > 0) props[d.slice(0, at).trim()] = d.slice(at + 1).trim();
          }
          out.push([head, props]);
        }
        start = i + 1;
      }
    }
  }
  return out;
}
const sheet = rules(css);
/** styleOf is the value of a property the sheet sets for a lone class, last rule winning. */
function styleOf(cls, prop) {
  let v = null;
  for (const [sel, props] of sheet) if (sel.split(",").map((x) => x.trim()).includes("." + cls) && prop in props) v = props[prop];
  return v;
}
const classesOf = (el) => (el.getAttribute("class") || "").split(/\s+/).filter(Boolean);
const clips = (el) => classesOf(el).some((c) => ["overflow", "overflow-x", "overflow-y"].some((p) => { const v = styleOf(c, p); return v && v !== "visible"; }));

test("the menu and the usage detail sit under the strip and no ancestor up to the pane clips them", async () => {
  const m = await mount();
  try {
    m.container.find(".conv-more-btn").click();
    m.container.find(".usage-meter-btn").click();
    await settle();
    for (const sel of [".conv-menu", ".usage-detail"]) {
      const el = m.container.find(sel);
      assert.ok(el, sel + " did not open");
      let n = el.parentNode;
      for (; n && !classesOf(n).includes("structured"); n = n.parentNode) {
        assert.equal(clips(n), false, sel + " is inside " + n.getAttribute("class") + ", which clips");
        assert.equal(classesOf(n).includes("conv"), false, sel + " is inside the scrolling log");
      }
      assert.ok(n, sel + " is not inside the pane");
      assert.equal(clips(n), false, "the pane itself clips " + sel);
    }
    // The detail hangs from the strip, which is positioned, so it stays inside the pane at any width.
    assert.equal(styleOf("conv-strip", "position"), "relative");
    assert.notEqual(styleOf("usage-meter", "position"), "relative");
    assert.ok(styleOf("usage-detail", "left") && styleOf("usage-detail", "right") && styleOf("usage-detail", "max-width"), "the detail is anchored on one side only");
  } finally {
    await m.done();
  }
});

test("the pane's parts: the strip and composer are fixed, the panels give way and scroll, and only the log scrolls on its own", async () => {
  const m = await mount();
  try {
    const pane = m.container.find(".structured");
    const kids = pane.childNodes.filter((n) => n.nodeType === 1).map((n) => classesOf(n));
    assert.ok(kids.some((c) => c.includes("conv-toolbar")), "no strip");
    assert.ok(kids.some((c) => c.includes("structured-log")), "no log");
    assert.ok(kids.some((c) => c.includes("structured-prompt")), "no composer");
    // Only the log is a scroller among the strip, the log and the composer.
    for (const c of kids.flat()) {
      const scrolls = ["overflow", "overflow-y"].some((p) => { const v = styleOf(c, p); return v === "auto" || v === "scroll"; });
      assert.equal(scrolls, c === "conv", c + (scrolls ? " scrolls" : " does not scroll"));
    }
  } finally {
    await m.done();
  }
  const panels = sheet.find(([sel]) => sel.startsWith(".structured > :not(.conv-toolbar):not(.structured-prompt):not(.structured-log)"));
  assert.ok(panels, "no rule for the panels between the strip and the composer");
  assert.match(panels[1]["flex"], /^0 1 auto$/, "the panels cannot shrink");
  assert.equal(panels[1]["overflow-y"], "auto");
  assert.match(panels[1]["max-height"], /vh$/);
  assert.match(panels[1]["min-height"], /em$/);
  const log = sheet.find(([sel]) => sel === ".structured > .structured-log")[1];
  assert.match(log["min-height"], /^[0-3](\.\d+)?em$/, "the log keeps more than three lines of its own");
  assert.equal(styleOf("structured", "overflow-y"), null);
  assert.equal(styleOf("structured", "overflow"), null);
});

test("the pickers are labelled in view as well as for assistive technology", async () => {
  const m = await mount();
  try {
    const names = m.container.findAll(".structured-setting-name");
    assert.deepEqual(names.map((n) => n.textContent), ["Mode", "Model"]);
    for (const n of names) assert.equal(classesOf(n).includes("sr-only"), false, n.textContent + " is hidden from sight");
  } finally {
    await m.done();
  }
});

test("at a narrow drawer the strip wraps instead of clipping: every part can shrink or drop to the next row", () => {
  assert.equal(styleOf("conv-strip", "flex-wrap"), "wrap");
  assert.equal(styleOf("conv-strip", "min-width"), "0");
  assert.equal(styleOf("structured-settings", "flex-wrap"), "wrap", "the two pickers cannot share a 320 px row with the rest");
  assert.equal(styleOf("structured-settings", "min-width"), "0");
  assert.equal(styleOf("structured-activity", "min-width"), "0");
  assert.match(styleOf("structured-select", "max-width"), /em$/);
  // The icon buttons and the pickers keep their size rather than shrinking to nothing.
  assert.equal(styleOf("conv-icon-btn", "flex"), "none");
  // The menu opens leftward from the More button, so it does not run past the right edge.
  assert.equal(styleOf("conv-menu", "right"), "0");
  assert.equal(styleOf("conv-menu", "left"), null);
});
