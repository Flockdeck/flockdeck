import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "../src/vendor/preact.mjs";
import { renderToString } from "./vendor/render-to-string.mjs";
import { html } from "../src/html.js";
import { FileList, openByPath, statusWord } from "../src/drawer/diff/FileList.js";
import { DiffViewer, effectiveLayout, sizeWords } from "../src/drawer/diff/DiffViewer.js";
import { createDom, install, settle } from "./dom.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
const hunksFile = fixture("04-diff-hunks");
const truncated = fixture("05-diff-truncated");
const binary = fixture("06-diff-binary");
const rename = fixture("07-diff-rename");
const failed = fixture("08-diff-error");

const listing = (r, over = {}) => ({ path: r.path, oldPath: r.oldPath, status: r.oldPath ? "R" : "M", added: 2, removed: 1, ...over });
const view = (props) => renderToString(html`<${DiffViewer} ...${props} />`);

test("the file list names each status, marks the picked row and says how many comments are open", () => {
  const files = [
    { path: "a.go", status: "M", added: 3, removed: 1 },
    { path: "b.go", oldPath: "old.go", status: "R" },
    { path: "c.png", status: "A", binary: true },
    { path: "go.sum", status: "M", generated: true },
  ];
  const comments = [{ path: "a.go", status: "delivered" }, { path: "a.go", status: "fixed" }, { path: "a.go", status: "sending" }];
  const out = renderToString(html`<${FileList} files=${files} selected="a.go" comments=${comments} />`);
  assert.match(out, /aria-label="Changed files, 4"/);
  assert.match(out, /Modified/);
  assert.match(out, /Renamed/);
  assert.match(out, /aria-current="true"/);
  assert.equal((out.match(/aria-current/g) || []).length, 1);
  assert.match(out, /2 open comments/, "a fixed one is not open");
  assert.match(out, /binary/);
  assert.match(out, /generated/);
  assert.match(out, /old\.go/);
  assert.deepEqual([...openByPath(comments)], [["a.go", 2]]);
  assert.equal(statusWord("Z"), "Changed");
  assert.match(renderToString(html`<${FileList} files=${[]} />`), /No files changed/);
});

test("the file list reports a pick by path, and draws a hostile path as text", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const picked = [];
    render(html`<${FileList} files=${[{ path: "<img src=x onerror=1>.go", status: "M" }]} onPick=${(p) => picked.push(p)} />`, box);
    await settle();
    assert.equal(box.find("img"), null);
    box.find(".dv-filebtn").click();
    assert.deepEqual(picked, ["<img src=x onerror=1>.go"]);
  } finally {
    restore();
  }
});

test("a diff is a named region with named buttons, and its lines carry a sign as well as a colour", () => {
  const out = view({ branch: "feat/limit", file: listing(hunksFile), hunks: hunksFile });
  assert.match(out, /role="region" aria-label="Diff of internal\/limit\/limit\.go on feat\/limit"/);
  assert.match(out, /aria-expanded="true"/);
  assert.match(out, /Show 51 unchanged lines/);
  assert.match(out, /Show 31 unchanged lines/);
  assert.match(out, /Show the unchanged lines below/);
  assert.match(out, /removed: /);
  assert.match(out, /added: /);
  assert.match(out, /<mark>/);
  assert.match(out, /role="status"/, "the status line stays in the page");
  assert.doesNotMatch(out, /dv-plus/, "no + without onComment");
});

test("side by side and unified have their own tables, and a narrow container draws unified", () => {
  assert.equal(effectiveLayout("split", false), "split");
  assert.equal(effectiveLayout("split", true), "unified");
  assert.equal(effectiveLayout("unified", false), "unified");
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile, layout: "split" }), /dv-tbl--split/);
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile, layout: "split", narrow: true }), /dv-tbl--unified/);
});

test("the stylesheet keys the narrow layout on the main container", () => {
  const css = readFileSync(join(here, "..", "src", "drawer", "drawer.css"), "utf8");
  assert.match(css, /@container main \(max-width: 680px\)\s*\{[^}]*\.dv-/);
  assert.match(css, /\.dv-body\[hidden\]\s*\{\s*display:\s*none/);
});

test("fixture 05: a cut diff says so and draws no gap below the kept lines", () => {
  const out = view({ file: listing(truncated, { generated: false }), hunks: truncated });
  assert.match(out, /The desk cut this diff at 3 of 48,213 lines/);
  assert.doesNotMatch(out, /unchanged lines below/, "the cut part was never sent, so there is nothing to ask for");
  assert.doesNotMatch(view({ file: listing(truncated), hunks: { ...truncated, truncated: true } }), /unchanged lines below/, "a bare true is a cut too");
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile }), /unchanged lines below/, "an uncut diff has it");
});

test("a generated file starts shut, and the header opens it", async () => {
  const out = view({ file: listing(truncated), hunks: truncated });
  assert.match(out, /aria-expanded="false"/);
  assert.match(out, /Generated file, collapsed\./);
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${listing(truncated)} hunks=${truncated} />`, box);
    await settle();
    assert.ok(box.find(".dv-body").getAttribute("hidden") !== null);
    box.find(".dv-fh").click();
    await settle();
    assert.equal(box.find(".dv-body").getAttribute("hidden"), null);
    assert.equal(box.find(".dv-fh").getAttribute("aria-expanded"), "true");
  } finally {
    restore();
  }
});

test("fixtures 06 to 08: binary, renamed and failed files each say what they are", () => {
  const bin = view({ file: listing(binary, { status: "M" }), hunks: binary });
  assert.match(bin, /Binary file changed \(47\.1 KiB to 50\.2 KiB\)/);
  assert.doesNotMatch(bin, /<table/);
  const ren = view({ file: listing(rename), hunks: rename });
  assert.match(ren, /Renamed from <span class="dv-mono">internal\/limit\/legacy\.go<\/span>/);
  assert.match(ren, /<table/);
  const bad = view({ file: listing(failed), hunks: failed });
  assert.match(bad, /role="alert"/);
  assert.match(bad, /the directory is gone/);
  assert.match(view({ file: listing(failed), hunks: null }), /Reading the diff/);
  assert.equal(sizeWords(512), "512 bytes");
  assert.equal(sizeWords(null), "unknown size");
});

test("a line's + reports its side and number, a gap button its request, and open comments are marked", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const said = [];
    const asked = [];
    const comments = [{ path: hunksFile.path, side: "new", line: 55, status: "delivered" }, { path: hunksFile.path, side: "new", line: 56, status: "fixed" }];
    render(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} comments=${comments} onComment=${(c) => said.push(c)} onContext=${(id, ask) => asked.push([id, ask])} />`, box);
    await settle();
    const plusses = box.findAll(".dv-plus");
    const del = plusses.find((b) => /old line 55/.test(b.getAttribute("aria-label")));
    assert.ok(del, "a deleted line is commented on its old side");
    del.click();
    const add = plusses.find((b) => /^Comment on line 55, 1 open comment$/.test(b.getAttribute("aria-label")));
    assert.ok(add, "an open comment is counted in the label");
    add.click();
    assert.deepEqual(said, [{ file: hunksFile.path, side: "old", line: 55 }, { file: hunksFile.path, side: "new", line: 55 }]);
    assert.equal(box.findAll(".has-comment").length, 1, "a fixed comment is not marked");
    const gaps = box.findAll(".dv-gapbtn");
    gaps[1].click();
    assert.deepEqual(asked, [["g0", { side: "new", from: 60, to: 90 }]]);
  } finally {
    restore();
  }
});

test("without a handler a gap button is disabled, and a long diff is paged", async () => {
  const lines = Array.from({ length: 900 }, (_, i) => ({ t: "add", text: "line " + i, newNo: i + 1 }));
  const reply = { path: "big.txt", hunks: [{ oldStart: 0, oldLines: 0, newStart: 1, newLines: 900, header: "", lines }], totalLines: 900 };
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${{ path: "big.txt", status: "A" }} hunks=${reply} />`, box);
    await settle();
    assert.equal(box.findAll(".dv-row").length, 399, "one page of rows, less the hunk row");
    const more = box.find(".drawer-btn");
    assert.match(more.textContent, /Show 400 more rows/);
    more.click();
    await settle();
    assert.equal(box.findAll(".dv-row").length, 799);
  } finally {
    restore();
  }
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile }), /class="dv-gapbtn" disabled/);
});

test("missing input draws nothing or a quiet note, and never throws", () => {
  assert.equal(view({ file: null }), "");
  assert.doesNotThrow(() => view({ file: { path: "a" }, hunks: {} }));
  assert.doesNotThrow(() => view({ file: { path: "a" }, hunks: { hunks: [null, { lines: [null] }] }, comments: [null, 1] }));
  assert.match(view({ file: { path: "a" }, hunks: { path: "a", hunks: [], totalLines: 0 } }), /Nothing changed in this file/);
});
