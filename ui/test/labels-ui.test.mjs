// Pane labels in the UI: the graph model carries them, rows read "role · label", the
// drawer header shows the label and the Doing now line apart, and the rename box sends paneName.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel, labelOf } from "../src/graph/model.js";
import { Header } from "../src/drawer/Header.js";
import { Rename, canRename, renameCommand } from "../src/drawer/Rename.js";

const graph = (nodes) => ({ type: "graph", ok: true, nodes, teams: [], rev: 1 });

test("a labelled pane reads as its role and label, and a shared one still gets told apart", () => {
  const m = buildGraphModel(graph([
    { pane: "aaaa1111", name: "flock", label: "rate limiter PR", role: "worker" },
    { pane: "bbbb2222", name: "flock", role: "worker" },
    { pane: "cccc3333", name: "docs", label: "release notes", role: "worker" },
    { pane: "dddd4444", name: "docs", label: "release notes", role: "worker" },
  ]));
  assert.equal(m.byPane.get("aaaa1111").label, "rate limiter PR");
  assert.equal(labelOf(m, "aaaa1111"), "rate limiter PR");
  assert.equal(labelOf(m, "bbbb2222"), "flock");
  assert.equal(labelOf(m, "cccc3333"), "worker · release notes · cccc");
  assert.equal(labelOf(m, "dddd4444"), "worker · release notes · dddd");
});

test("the drawer header shows the label as the title, the name beside it, and Doing now as its own line", () => {
  const node = { pane: "p1", name: "flock", label: "rate limiter PR", role: "worker", agent: "claude", status: "working", live: "Editing limiter.go" };
  const out = renderToString(html`<${Header} node=${node} onClose=${() => {}} onToggleMax=${() => {}} />`);
  assert.ok(out.includes('<h2 class="drawer-name" title="rate limiter PR">rate limiter PR</h2>'));
  assert.ok(out.includes("<dt>Name</dt><dd>flock</dd>"));
  assert.ok(out.includes('<dt>Doing now</dt><dd title="Editing limiter.go">Editing limiter.go</dd>'));
  const plain = renderToString(html`<${Header} node=${{ ...node, label: "" }} onClose=${() => {}} onToggleMax=${() => {}} />`);
  assert.ok(plain.includes('<h2 class="drawer-name" title="flock">flock</h2>'));
  assert.ok(!plain.includes("<dt>Name</dt>"));
});

test("the rename box sends a cleaned, cut label and clears with empty text", () => {
  assert.deepEqual(renameCommand("p1", "  rate   limiter\nPR "), { cmd: "paneName", id: "p1", text: "rate limiter PR" });
  assert.equal(renameCommand("p1", "x".repeat(80)).text.length, 40);
  assert.equal(renameCommand("p1", "   ").text, "");
  const out = renderToString(html`<${Rename} node=${{ pane: "p1", name: "flock", label: "old" }} send=${() => true} />`);
  assert.ok(out.includes('value="old"'));
});

// The rename box on a page of its own.
import { render } from "../src/vendor/preact.mjs";
import { createDom, install, settle } from "./dom.mjs";
import { labelText } from "../src/drawer/Rename.js";

const val = (el) => (el.value !== undefined ? el.value : el.getAttribute("value"));

async function box(node, send) {
  const dom = createDom();
  const restore = install(dom);
  const container = dom.document.createElement("div");
  dom.body.appendChild(container);
  const draw = (n) => render(html`<${Rename} key=${n.pane} node=${n} send=${send} />`, container);
  draw(node);
  await settle();
  return { dom, container, draw, restore, input: () => container.querySelector("input"), status: () => container.querySelector(".rename-status") };
}

test("a label is cut by characters, not by UTF-16 units", () => {
  const edge = "a".repeat(39) + "\u{1F600}" + "tail";
  assert.equal(labelText(edge), "a".repeat(39) + "\u{1F600}");
  assert.equal(Array.from(labelText("\u{1F600}".repeat(60))).length, 40);
  assert.equal(renameCommand("p", "\u{1F600}".repeat(60)).text, "\u{1F600}".repeat(40));
});

test("an automatic label is the placeholder, and Clear waits for a chosen one", async () => {
  const sent = [];
  const b = await box({ pane: "p1", name: "flock", label: "Fix the tabs", labelAuto: true }, (c) => { sent.push(c); return true; });
  try {
    assert.equal(val(b.input()), "");
    assert.equal(b.input().getAttribute("placeholder"), "Fix the tabs");
    assert.ok(b.container.querySelector(".rename-clear").getAttribute("disabled") != null);
  } finally { b.restore(); }
});

test("a refused send shows the alert, a good one says saved, and a label change keeps the draft", async () => {
  let ok = false;
  const b = await box({ pane: "p1", name: "flock", label: "old" }, () => ok);
  try {
    assert.equal(val(b.input()), "old");
    b.input().enter("my draft");
    await settle();
    const before = b.input();
    b.draw({ pane: "p1", name: "flock", label: "set by the manager" });
    await settle();
    assert.equal(b.input(), before, "the same input stays, so the keyboard stays in it");
    assert.equal(val(b.input()), "my draft", "a rename under the box keeps what was typed");
    b.container.querySelector("form").dispatch("submit");
    await settle();
    assert.equal(b.status().getAttribute("role"), "status");
    assert.ok(String(b.status().getAttribute("class")).includes("rename-status--error"));
    assert.match(b.status().textContent, /could not be sent/);
    ok = true;
    b.container.querySelector("form").dispatch("submit");
    await settle();
    assert.equal(b.status().textContent, "Label sent");
    b.input().enter("more");
    await settle();
    assert.equal(b.status().textContent, "", "the status goes with the next edit");
  } finally { b.restore(); }
});

test("a label the server already marked as an agent's is drawn as it came", () => {
  const m = buildGraphModel(graph([{ pane: "aaaa1111", name: "api", label: "(agent) manager", role: "worker" }]));
  assert.equal(labelOf(m, "aaaa1111"), "(agent) manager");
});

test("only a full desk window offers the rename box", () => {
  assert.equal(canRename({ remote: false, hello: { role: "owner" } }), true);
  assert.equal(canRename({ remote: true }), false);
  assert.equal(canRename({ remote: false, hello: { role: "viewer" } }), false);
});

test("a 41st character typed at the limit is not left in the box", async () => {
  const b = await box({ pane: "p1", name: "flock" }, () => true);
  try {
    b.input().enter("a".repeat(40));
    await settle();
    b.input().enter("a".repeat(41));
    await settle();
    assert.equal(val(b.input()), "a".repeat(40));
  } finally { b.restore(); }
});
