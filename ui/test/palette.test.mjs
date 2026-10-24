import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { Palette, filterActions, nameOf, NOT_YET } from "../src/shell/palette.js";
import { IMPLEMENTED } from "../src/shell/app.js";
import { fakeKeys, realKeys } from "./util.mjs";

const names = (rows) => rows.map((r) => r.name);

test("an empty query lists the table's palette actions in table order", () => {
  const rows = filterActions(fakeKeys, "", IMPLEMENTED);
  assert.deepEqual(names(rows), ["Expand or collapse the rail", "Projects", "Worktrees", "Changes", "Agents", "Quit"]);
});

test("actions meant for the keyboard alone are left out of the palette", () => {
  const rows = filterActions(fakeKeys, "", IMPLEMENTED);
  assert.ok(!rows.some((r) => r.key.id === "palette" || r.key.id === "nextRegion"));
});

test("a query finds an action by name, short name or label", () => {
  assert.deepEqual(names(filterActions(fakeKeys, "work", IMPLEMENTED)), ["Worktrees"]);
  assert.deepEqual(names(filterActions(fakeKeys, "agents", IMPLEMENTED))[0], "Agents");
  // "All agents across projects" is the label; "Agents" is its short name.
  assert.ok(names(filterActions(fakeKeys, "across", IMPLEMENTED)).includes("Agents"));
  assert.ok(names(filterActions(fakeKeys, "COMMIT", IMPLEMENTED)).includes("Changes"));
});

test("the best match comes first", () => {
  const rows = filterActions(fakeKeys, "pro", IMPLEMENTED);
  assert.equal(rows[0].name, "Projects");
});

test("letters in order find a name", () => {
  assert.deepEqual(names(filterActions(fakeKeys, "wtr", IMPLEMENTED)), ["Worktrees"]);
});

test("nothing matches nonsense", () => {
  assert.deepEqual(filterActions(fakeKeys, "zzzq", IMPLEMENTED), []);
});

test("an action this window does not run is listed and marked, never hidden", () => {
  const rows = filterActions(fakeKeys, "", IMPLEMENTED);
  const byId = Object.fromEntries(rows.map((r) => [r.key.id, r.implemented]));
  assert.equal(byId.toggleRail, true);
  assert.equal(byId.projects, true);
  assert.equal(byId.worktrees, false);
  assert.equal(byId.quit, false);
  assert.equal(rows.length, fakeKeys.filter((k) => !k.noPalette).length);
});

test("a row for an action not here says so, and one that is here does not", () => {
  const out = renderToString(html`<${Palette} keys=${fakeKeys} implemented=${IMPLEMENTED} />`);
  const row = (id) => new RegExp(`<li[^>]*data-action="${id}"[^>]*>.*?</li>`).exec(out)[0];
  assert.ok(row("worktrees").includes(NOT_YET), row("worktrees"));
  assert.ok(row("worktrees").includes('aria-disabled="true"'));
  assert.ok(!row("toggleRail").includes(NOT_YET));
  assert.ok(row("toggleRail").includes("Ctrl+B"));
  assert.match(out, /role="dialog"/);
});

test("a label that carries a gloss is named by the part before the dash", () => {
  assert.equal(nameOf({ id: "quit", label: "Quit — stop every agent in every project" }), "Quit");
  assert.equal(nameOf({ id: "x", short: "Short", label: "A long label" }), "Short");
});

test("every action this window runs is in the server's table", { skip: realKeys() ? false : "NEXT_KEYS_JSON is not set" }, () => {
  const ids = new Set(realKeys().map((k) => k.id));
  for (const id of IMPLEMENTED) assert.ok(ids.has(id), `${id} is not an action of the key table`);
});

test("the server's own table lists in the palette, with the rest marked", { skip: realKeys() ? false : "NEXT_KEYS_JSON is not set" }, () => {
  const table = realKeys();
  const rows = filterActions(table, "", IMPLEMENTED);
  assert.equal(rows.length, table.filter((k) => !k.noPalette).length);
  assert.ok(rows.some((r) => !r.implemented));
  assert.deepEqual(names(filterActions(table, "command palette", IMPLEMENTED)), []);
});
