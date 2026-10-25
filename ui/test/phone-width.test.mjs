// At phone width nothing makes the page itself scroll sideways: the rail and the
// goal tabs scroll inside their own box without a bar, and the view switch wraps.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (f) => readFileSync(new URL("../src/" + f, import.meta.url), "utf8");
const phone = (css) => css.match(/@media \(max-width: 640px\) \{[\s\S]*$/)[0];

test("the view switch is a three-column grid on a phone, and the tab strip and rail hide their scroll bars", () => {
  const start = phone(read("shell/start.css"));
  assert.match(start, /\.viewbar \{[^}]*grid-template-columns: repeat\(3, 1fr\)/);
  assert.match(start, /\.goaltabs-list \{[^}]*scrollbar-width: none/);
  assert.match(read("app.css"), /\.rail \{ scrollbar-width: none; \}/);
});

test("the phone grid has one shrinkable column, so wide content cannot widen the page", () => {
  assert.match(read("app.css"), /@media \(max-width: 640px\) \{\s*\.shell \{\s*grid-template-columns: minmax\(0, 1fr\);/);
});

test("the drawer's layout button is gone on a phone, and the goal tab's close and plus have a 24 px target", () => {
  assert.match(phone(read("drawer/drawer.css")), /\.drawer-layout \{ display: none; \}/);
  const css = read("shell/start.css");
  assert.match(css, /\.tab-close \{[^}]*min-width: 24px; min-height: 24px/);
  assert.match(css, /\.tabs-plus \{[^}]*min-width: 26px[^}]*border: 1px solid var\(--line\)/);
});
