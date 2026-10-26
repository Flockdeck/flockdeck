// showControls (drawer/diff/diffModel.js) is shared by every view that draws text a person or an agent wrote:
// a name, a path, a line of a diff, a command. It must make visible what hides or reorders text, and leave
// ordinary text alone.
import { test } from "node:test";
import assert from "node:assert/strict";
import { showControls } from "../src/drawer/diff/diffModel.js";
import { checksListOf } from "../src/drawer/diff/gateModel.js";

const cp = (n) => String.fromCodePoint(n);
const mark = (n) => "<U+" + n.toString(16).toUpperCase().padStart(4, "0") + ">";

test("line and paragraph separators, the fillers and the braille blank are shown", () => {
  for (const n of [0x2028, 0x2029, 0x3164, 0x115f, 0x1160, 0x2800, 0x034f, 0xffa0]) {
    assert.equal(showControls("a" + cp(n) + "b"), "a" + mark(n) + "b", mark(n));
  }
});

test("the tag characters are shown one by one, as code points and not as halves of a pair", () => {
  const hidden = cp(0xe0041) + cp(0xe0042) + cp(0xe007f) + cp(0xe0000);
  assert.equal(showControls("x" + hidden + "y"), "x<U+E0041><U+E0042><U+E007F><U+E0000>y");
  assert.equal(showControls(cp(0xe0080)), cp(0xe0080), "past the block is not tag text");
  assert.equal(showControls(cp(0xdfff)), cp(0xdfff), "a lone half of a pair is left as it is");
});

test("the ones that were shown before still are, and the bidi controls stay together", () => {
  for (const n of [0x00ad, 0x200b, 0x200c, 0x200d, 0x200e, 0x200f, 0x061c, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2060, 0x2066, 0x2067, 0x2068, 0x2069, 0xfeff]) {
    assert.equal(showControls(cp(n)), mark(n), mark(n));
  }
});

test("ordinary text is left alone: letters of other scripts, emoji with a variation selector, spaces and no-break spaces", () => {
  for (const t of ["plain", "Grüße", "日本語 テキスト", "한국어 ᄀ", "עברית", "عربي", "👍🏽 ❤️ 1️⃣", "a b", "1" + cp(0xa0) + "000", "a" + cp(0x3000) + "b", "a" + cp(0x202f) + "b", "tab\tand\nnewline", "한" + cp(0x1100) + "x"]) {
    assert.equal(showControls(t), t, JSON.stringify(t));
  }
  assert.equal(showControls(null), "");
  assert.equal(showControls(undefined), "");
  assert.equal(showControls(42), "42");
});

test("a command is cleaned with the same list, and its odd spaces and line breaks are marked besides", () => {
  const cmd = "echo a" + cp(0x2028) + "b" + cp(0x3164) + "c" + cp(0xe0041) + "d" + cp(0xa0) + "e" + cp(0x3000) + "f" + String.fromCharCode(10) + "g";
  const found = checksListOf({ type: "changesChecks", commands: [{ name: "n" + cp(0x2800), cmd }], hash: "h" });
  assert.equal(found.commands[0].name, "n<U+2800>");
  assert.equal(found.commands[0].cmd, "echo a<U+2028>b<U+3164>c<U+E0041>d<U+00A0>e<U+3000>f <LF> g");
});
