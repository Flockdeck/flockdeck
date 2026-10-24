// The activity feed's drawing: entries newest first with how long ago, the empty
// sentence, and a click that names the pane to focus.
import { test } from "node:test";
import assert from "node:assert/strict";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { EMPTY_WORDS, Feed } from "../src/graph/Feed.js";
import { pushEntries } from "../src/graph/FeedDiff.js";

const NOW = 10_000_000;
const feedOf = () => pushEntries(null, [
  { kind: "info", pane: "m", text: "mgr started rev", at: NOW - 3_600_000 },
  { kind: "need", pane: "a", text: "api asks to run go test", at: NOW - 120_000 },
  { kind: "down", pane: "l", text: "lint exited", at: NOW - 10_000 },
]);

test("an empty feed says so in one sentence and has no list", () => {
  const out = renderToString(html`<${Feed} feed=${null} now=${NOW} />`);
  assert.match(out, /aria-label="Activity"/);
  assert.ok(out.includes(EMPTY_WORDS));
  assert.ok(!out.includes("feed-list"));
});

test("entries are newest first, each with its time and a mark for its kind", () => {
  const out = renderToString(html`<${Feed} feed=${feedOf()} now=${NOW} />`);
  const at = (s) => out.indexOf(s);
  assert.ok(at("lint exited") < at("api asks to run go test") && at("api asks to run go test") < at("mgr started rev"));
  assert.match(out, /feed-time">now</);
  assert.match(out, /feed-time">2m</);
  assert.match(out, /feed-time">1h</);
  assert.match(out, /feed-mark feed-mark--need/);
  assert.match(out, /feed-mark feed-mark--down/);
  assert.match(out, /Needs you: /, "a screen reader hears the kind");
  assert.ok(!out.includes(EMPTY_WORDS));
});

test("a text with markup is drawn as text", () => {
  const f = pushEntries(null, [{ kind: "info", pane: "a", text: '<img src=x onerror="alert(1)">', at: NOW }]);
  const out = renderToString(html`<${Feed} feed=${f} now=${NOW} />`);
  assert.ok(!out.includes("<img"));
  assert.ok(out.includes("&lt;img"));
});
