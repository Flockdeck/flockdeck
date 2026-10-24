// A maximised drawer fills the main area. Every grid area the shell places a child in
// must be in the maximised template too: one left out (the manager bar's composer was)
// is put in an implicit column, which squeezes the drawer to a strip.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (f) => readFileSync(new URL("../src/" + f, import.meta.url), "utf8");

test("the maximised template names every area the shell uses", () => {
  const app = read("app.css");
  const drawer = read("drawer/drawer.css");
  const areas = new Set([...app.matchAll(/grid-area:\s*([a-z]+)\s*;/g)].map((m) => m[1]));
  const shell = app.match(/\.shell \{[^}]*grid-template-areas:([^;]*);/);
  assert.ok(shell, "the shell's template");
  const max = drawer.match(/\.shell--max \{[^}]*grid-template-areas:([^;]*);/);
  assert.ok(max, "the maximised template");
  const named = (s) => new Set(s.match(/[a-z]+/g));
  const inShell = named(shell[1]);
  const inMax = named(max[1]);
  for (const a of inShell) if (a !== "main") assert.ok(inMax.has(a), a + " is in the shell's template but not the maximised one");
  assert.ok(areas.has("composer") && inMax.has("composer"));
});

test("the stacked template names the same areas, so maximising it from either orientation loses none", () => {
  const app = read("app.css");
  const drawer = read("drawer/drawer.css");
  const stack = app.match(/\.shell--stack:not\(\.shell--max\) \{[^}]*grid-template-areas:([^;]*);/);
  const maxRule = drawer.match(/\.shell--max \{[^}]*\}/)[0];
  const max = maxRule.match(/grid-template-areas:([^;]*);/);
  assert.ok(stack && max);
  const named = (s) => new Set(s.match(/[a-z]+/g));
  for (const a of named(stack[1])) if (a !== "main") assert.ok(named(max[1]).has(a), a + " is in the stacked template but not the maximised one");
  // The maximised template has as many rows as it names, so none is left to be made implicitly.
  const areaRows = max[1].match(/"[^"]*"/g).length;
  const rowSizes = maxRule.match(/grid-template-rows:([^;]*);/)[1].match(/minmax\([^)]*\)|auto/g).length;
  assert.equal(rowSizes, areaRows);
});

test("the drawer's title row can wrap, so the name keeps room and the buttons stay whole", () => {
  const css = read("drawer/drawer.css");
  assert.match(css, /\.drawer-title \{[^}]*flex-wrap: wrap/);
  assert.match(css, /\.drawer-actions \{[^}]*flex-wrap: nowrap/);
});
