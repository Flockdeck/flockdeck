// Rough edges found against the prototype: the drawer's message box and the tree's
// chips use the design tokens, not browser defaults or a pill shape.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (f) => readFileSync(new URL("../src/" + f, import.meta.url), "utf8");

test("the drawer's message box is drawn as a field", () => {
  assert.match(read("drawer/drawer.css"), /\.structured-input \{[^}]*background: var\(--bg-inset\)[^}]*border: 1px solid var\(--line-strong\)/);
});

test("tree chips are 4 px boxes, tinted for asks and down, quiet for waits", () => {
  const css = read("graph/tree.css");
  assert.match(css, /\.tree-chip \{[^}]*border-radius: var\(--radius-sm\)/);
  assert.match(css, /\.tree-chip--ask[^{]*\{[^}]*background: var\(--waiting-subtle\)/);
  assert.match(css, /\.tree-chip--down \{[^}]*background: var\(--exited-subtle\)/);
  assert.match(css, /\.tree-chip--wait \{[^}]*color: var\(--fg-dim\)/);
});
