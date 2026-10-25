// Rough edges found against the prototype: the drawer's message box and the tree's
// chips use the design tokens, not browser defaults or a pill shape.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";

const read = (f) => readFileSync(new URL("../src/" + f, import.meta.url), "utf8");

test("the drawer's message box is drawn as a field", () => {
  assert.match(read("drawer/drawer.css"), /\.structured-input \{[^}]*background: var\(--bg-inset\)[^}]*border: 1px solid var\(--line-strong\)/);
});

test("tree chips are pills (the C brief), tinted for asks and down, and a wait chip is --fg on inset", () => {
  const css = read("graph/tree.css");
  assert.match(css, /\.tree-chip \{[^}]*border-radius: 999px/);
  assert.match(css, /\.tree-chip--ask[^{]*\{[^}]*background: var\(--waiting-subtle\)/);
  assert.match(css, /\.tree-chip--down \{[^}]*background: var\(--exited-subtle\)/);
  assert.match(css, /\.tree-chip--wait \{[^}]*color: var\(--fg\)/);
});

const cssFiles = () => {
  const out = [];
  const walk = (dir) => {
    for (const e of readdirSync(new URL("../src/" + dir, import.meta.url), { withFileTypes: true })) {
      if (e.isDirectory()) walk(dir + e.name + "/");
      else if (e.name.endsWith(".css")) out.push(dir + e.name);
    }
  };
  walk("");
  return out;
};

test("--fg-faint is for glyphs and rules, never for text", () => {
  for (const f of cssFiles()) {
    assert.doesNotMatch(read(f), /(^|[\s{;])(color|fill):\s*var\(--fg-faint\)/m, f + " draws text in --fg-faint");
  }
});

test("every var(--token) a stylesheet reads is defined in app.css or by the rule that reads it", () => {
  const all = cssFiles().map(read).join("\n");
  const defined = new Set([...all.matchAll(/(--[a-z0-9-]+)\s*:/gi)].map((m) => m[1]));
  const missing = new Set();
  for (const m of all.matchAll(/var\((--[a-z0-9-]+)\s*([,)])/gi)) if (!defined.has(m[1]) && m[2] === ")") missing.add(m[1]);
  assert.deepEqual([...missing], [], "read with no fallback and never defined");
});
