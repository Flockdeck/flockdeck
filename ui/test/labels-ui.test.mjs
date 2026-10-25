// Pane labels in the UI: the graph model carries them, rows read "role · label", the
// drawer header shows the label and the Doing now line apart, and the rename box sends paneName.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { buildGraphModel, labelOf } from "../src/graph/model.js";
import { Header } from "../src/drawer/Header.js";
import { Rename, renameCommand } from "../src/drawer/Rename.js";

const graph = (nodes) => ({ type: "graph", ok: true, nodes, teams: [], rev: 1 });

test("a labelled pane reads as its role and label, and a shared one still gets told apart", () => {
  const m = buildGraphModel(graph([
    { pane: "aaaa1111", name: "flock", label: "rate limiter PR", role: "worker" },
    { pane: "bbbb2222", name: "flock", role: "worker" },
    { pane: "cccc3333", name: "docs", label: "release notes", role: "worker" },
    { pane: "dddd4444", name: "docs", label: "release notes", role: "worker" },
  ]));
  assert.equal(m.byPane.get("aaaa1111").label, "rate limiter PR");
  assert.equal(labelOf(m, "aaaa1111"), "worker · rate limiter PR");
  assert.equal(labelOf(m, "bbbb2222"), "flock");
  assert.equal(labelOf(m, "cccc3333"), "worker · release notes · cccc");
  assert.equal(labelOf(m, "dddd4444"), "worker · release notes · dddd");
});

test("the drawer header shows the label as the title, the name beside it, and Doing now as its own line", () => {
  const node = { pane: "p1", name: "flock", label: "rate limiter PR", role: "worker", agent: "claude", status: "working", live: "Editing limiter.go" };
  const out = renderToString(html`<${Header} node=${node} onClose=${() => {}} onToggleMax=${() => {}} />`);
  assert.ok(out.includes('<h2 class="drawer-name">rate limiter PR</h2>'));
  assert.ok(out.includes("<dt>Name</dt><dd>flock</dd>"));
  assert.ok(out.includes("<dt>Doing now</dt><dd>Editing limiter.go</dd>"));
  const plain = renderToString(html`<${Header} node=${{ ...node, label: "" }} onClose=${() => {}} onToggleMax=${() => {}} />`);
  assert.ok(plain.includes('<h2 class="drawer-name">flock</h2>'));
  assert.ok(!plain.includes("<dt>Name</dt>"));
});

test("the rename box sends a cleaned, cut label and clears with empty text", () => {
  assert.deepEqual(renameCommand("p1", "  rate   limiter\nPR "), { cmd: "paneName", id: "p1", text: "rate limiter PR" });
  assert.equal(renameCommand("p1", "x".repeat(80)).text.length, 40);
  assert.equal(renameCommand("p1", "   ").text, "");
  const out = renderToString(html`<${Rename} node=${{ pane: "p1", name: "flock", label: "old" }} send=${() => true} />`);
  assert.ok(out.includes('value="old"'));
  assert.ok(out.includes('maxLength="40"') || out.includes('maxlength="40"'));
});
