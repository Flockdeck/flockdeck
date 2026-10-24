// At phone width nothing makes the page itself scroll sideways: the rail and the
// goal tabs scroll inside their own box without a bar, and the view switch wraps.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (f) => readFileSync(new URL("../src/" + f, import.meta.url), "utf8");
const phone = (css) => css.match(/@media \(max-width: 640px\) \{[\s\S]*$/)[0];

test("the view switch wraps on a phone, and the tab strip and rail hide their scroll bars", () => {
  const start = phone(read("shell/start.css"));
  assert.match(start, /\.viewbar \{[^}]*flex-wrap: wrap/);
  assert.match(start, /\.goaltabs-list \{[^}]*scrollbar-width: none/);
  assert.match(read("app.css"), /\.rail \{ scrollbar-width: none; \}/);
});

test("the phone grid has one shrinkable column, so wide content cannot widen the page", () => {
  assert.match(read("app.css"), /@media \(max-width: 640px\) \{\s*\.shell \{\s*grid-template-columns: minmax\(0, 1fr\);/);
});
