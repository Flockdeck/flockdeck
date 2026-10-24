// Helper names in a reply become chips that open that agent; code, links and
// short names are left alone, and a name is never read as markup or a pattern.
import { test } from "node:test";
import assert from "node:assert/strict";
import { h } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { renderMarkdown } from "../src/drawer/conv/markdown.js";
import { chipify, patternOf, MIN_NAME } from "../src/drawer/conv/chips.js";

const names = [{ pane: "p1", name: "api" }, { pane: "p2", name: "docs" }, { pane: "p3", name: "a" }, { pane: "p4", name: "api-gateway" }, { pane: "p5", name: "c++ (old)" }];
const draw = (text, list = names, open = () => {}) => renderToString(h("div", null, ...chipify(renderMarkdown(text), { names: list, open })));

test("a whole-word helper name in a reply is a chip, and the longer name wins", () => {
  const out = draw("api owns T4, docs waits on api-gateway.");
  assert.equal((out.match(/class="conv-chip"/g) || []).length, 3);
  assert.match(out, /title="Open api-gateway"[^>]*>api-gateway</);
  assert.doesNotMatch(out, />api-gateway<\/button>-gateway/);
});

test("a name inside another word, or a name that is too short, is not a chip", () => {
  const out = draw("The apis are a mess; docsify is not docs-ish, and a is not a chip.");
  assert.doesNotMatch(out, /conv-chip/);
  assert.ok(MIN_NAME >= 3);
});

test("code, code blocks and links are left as written", () => {
  const out = draw("Run `api serve` then see [docs](https://example.com/docs) and\n\n```\napi docs\n```\n");
  assert.doesNotMatch(out, /conv-chip/);
});

test("chips inside bold, lists and tables work, and the text around them stays", () => {
  const out = draw("- **api** is done\n- ask docs\n\n| who | state |\n| --- | --- |\n| api | ok |\n");
  assert.equal((out.match(/class="conv-chip"/g) || []).length, 3);
  assert.match(out, /is done/);
  assert.match(out, /ask /);
});

test("a name with pattern characters or markup is matched as text and drawn as text", () => {
  const out = draw("c++ (old) is gone", [{ pane: "p5", name: "c++ (old)" }]);
  assert.match(out, /class="conv-chip"[^>]*>c\+\+ \(old\)</);
  const evil = draw("see <img> now", [{ pane: "p9", name: "<img>" }]);
  assert.match(evil, />&lt;img></);
  assert.doesNotMatch(evil, /<img>/);
});

test("clicking a chip opens that pane", () => {
  const opened = [];
  const nodes = chipify(renderMarkdown("ask docs"), { names, open: (p) => opened.push(p) });
  const chip = nodes[0].props.children.find((c) => c && c.type === "button");
  chip.props.onClick();
  assert.deepEqual(opened, ["p2"]);
});

test("no names, or none that can be chips, leaves the nodes as they were", () => {
  const nodes = renderMarkdown("api docs");
  assert.equal(chipify(nodes, { names: [], open: () => {} }), nodes);
  assert.equal(chipify(nodes, null), nodes);
  assert.equal(patternOf([{ pane: "x", name: "ab" }, { pane: "", name: "long" }]), null);
});
