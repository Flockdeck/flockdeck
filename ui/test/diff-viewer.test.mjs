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
import { showControls } from "../src/drawer/diff/diffModel.js";
import { createDom, install, settle } from "./dom.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
// The fixture is a diff of uncommitted changes (worktree true); most tests here are about a committed one.
const hunksFile = { ...fixture("04-diff-hunks"), worktree: false };
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
  const comments = [{ path: "a.go", state: "open", delivery: "read" }, { path: "a.go", state: "fixed" }, { path: "a.go", state: "open" }, { path: "a.go", state: "resolved" }];
  const out = renderToString(html`<${FileList} files=${files} selected="a.go" comments=${comments} />`);
  assert.match(out, /aria-label="Changed files, 4"/);
  assert.match(out, /Modified/);
  assert.match(out, /Renamed/);
  assert.match(out, /aria-current="true"/);
  assert.equal((out.match(/aria-current/g) || []).length, 1);
  assert.match(out, /2 open comments/, "a fixed or resolved one is not open");
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
  assert.match(out, /role="group" aria-label="Diff of internal\/limit\/limit\.go on feat\/limit"/);
  assert.match(out, /aria-expanded="true"/);
  assert.match(out, /Show 51 unchanged lines \(lines 1 to 51\)/);
  assert.match(out, /Show 31 unchanged lines \(lines 60 to 90\)/);
  assert.match(out, /Show the unchanged lines below from line 95/);
  assert.match(out, /removed: /);
  assert.match(out, /added: /);
  assert.match(out, /<mark>/);
  assert.match(out, /<p class="dv-live" role="status"><\/p>/, "the status line is in the page empty, and filled after the first draw");
  assert.doesNotMatch(out, /dv-plus/, "no + without onComment");
});

test("side by side and unified have their own tables, and a narrow container draws unified", () => {
  assert.equal(effectiveLayout("split", false), "split");
  assert.equal(effectiveLayout("split", true), "unified");
  assert.equal(effectiveLayout("unified", false), "unified");
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile, layout: "split" }), /dv-tbl--split/);
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile, layout: "split", narrow: true }), /dv-tbl--unified/);
});

test("the stylesheet keys the narrow layout on the viewer's own container", () => {
  const css = readFileSync(join(here, "..", "src", "drawer", "drawer.css"), "utf8");
  assert.match(css, /@container dv \(max-width: 680px\)\s*\{[^}]*\.dv-/);
  assert.doesNotMatch(css, /@container main[^{]*\{[^}]*\.dv-/, "the drawer is not inside the main column");
  assert.match(css, /\.dv\s*\{\s*container:\s*dv \/ inline-size/);
  assert.match(css, /\.dv-plus\s*\{[^}]*width:\s*24px/);
  assert.match(css, /\.dv-body\[hidden\]\s*\{\s*display:\s*none/);
});

test("fixture 05: a cut diff says so and draws no gap below the kept lines", () => {
  const out = view({ file: listing(truncated, { generated: false }), hunks: truncated });
  assert.match(out, /The desk cut this diff at 3 of 48,213 lines/);
  const big = { ...truncated, hunks: [{ oldStart: 0, oldLines: 0, newStart: 1, newLines: 900, header: "", lines: Array.from({ length: 900 }, (_, i) => ({ t: "add", text: "x" + i, newNo: i + 1 })) }] };
  assert.doesNotMatch(view({ file: listing(truncated), hunks: big, collapsed: false }), /The desk cut this diff/, "the cut note waits until the last page is drawn");
  assert.doesNotMatch(out, /unchanged lines below/, "the cut part was never sent, so there is nothing to ask for");
  assert.doesNotMatch(view({ file: listing(truncated), hunks: { ...truncated, truncated: true } }), /unchanged lines below/, "a bare true is a cut too");
  assert.match(view({ file: listing(hunksFile), hunks: hunksFile }), /unchanged lines below/, "an uncut diff has it");
});

test("a generated file starts shut, and the header opens it", async () => {
  const out = view({ file: listing(truncated), hunks: truncated });
  assert.match(out, /aria-expanded="false"/);
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${listing(truncated)} hunks=${truncated} />`, box);
    await settle();
    assert.ok(box.find(".dv-body").getAttribute("hidden") !== null);
    assert.match(box.textContent, /Generated file, collapsed|cut: 3 lines shown/, "the live line says what is shown");
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
    const comments = [{ path: hunksFile.path, side: "new", line: 55, state: "open" }, { path: hunksFile.path, side: "new", line: 56, state: "fixed" }];
    render(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} comments=${comments} onComment=${(c) => said.push(c)} onContext=${(id, ask) => asked.push([id, ask])} />`, box);
    await settle();
    const plusses = box.findAll(".dv-plus");
    const del = plusses.find((b) => /old line 55/.test(b.getAttribute("aria-label")));
    assert.ok(del, "a deleted line is commented on its old side");
    del.click();
    const add = plusses.find((b) => /^Comment on line 55 of internal\/limit\/limit\.go, 1 open comment$/.test(b.getAttribute("aria-label")));
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

const press = (el, key) => el.keydown(key);

test("the file list is one tab stop, and the arrow keys move between files", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    const files = ["a", "b", "c"].map((p) => ({ path: p + ".go", status: "M" }));
    render(html`<${FileList} files=${files} selected="b.go" />`, box);
    await settle();
    const btns = () => box.findAll(".dv-filebtn");
    assert.deepEqual(btns().map((b) => b.getAttribute("tabindex")), ["-1", "0", "-1"], "the picked file is the tab stop");
    btns()[1].focus();
    press(btns()[1], "ArrowDown");
    assert.equal(dom.document.activeElement, btns()[2]);
    press(btns()[2], "Home");
    assert.equal(dom.document.activeElement, btns()[0]);
    press(btns()[0], "End");
    assert.equal(dom.document.activeElement, btns()[2]);
    assert.match(box.find(".dv-live").textContent, /3 files\./, "the status is filled after the first draw");
  } finally {
    restore();
  }
});

test("the + buttons are one tab stop, the arrow keys move between them, and Skip goes past the diff", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} onComment=${() => {}} />`, box);
    await settle();
    const plusses = () => box.findAll(".dv-plus");
    assert.ok(plusses().length > 10);
    assert.equal(plusses().filter((b) => b.getAttribute("tabindex") === "0").length, 1, "one tab stop");
    plusses()[0].focus();
    press(plusses()[0], "ArrowDown");
    assert.equal(dom.document.activeElement, plusses()[1]);
    await settle();
    assert.equal(plusses().map((b) => b.getAttribute("tabindex")).indexOf("0"), 1, "the tab stop follows the focus");
    press(plusses()[1], "End");
    assert.equal(dom.document.activeElement, plusses()[plusses().length - 1]);
    box.find(".dv-skip").click();
    assert.equal(dom.document.activeElement, box.find(".dv-end"));
  } finally {
    restore();
  }
});

test("the + sits in its own 24 px column, and a read-only diff has none", () => {
  const withPlus = view({ file: listing(hunksFile), hunks: hunksFile, onComment: () => {} });
  assert.match(withPlus, /<col class="dv-col-p"/);
  assert.match(withPlus, /aria-describedby="dv\d+-h0:3"/, "the + is described by the line's code");
  const without = view({ file: listing(hunksFile), hunks: hunksFile });
  assert.doesNotMatch(without, /dv-col-p|dv-p"/);
});

test("two files whose paths differ only by punctuation get different ids", () => {
  const a = view({ file: { path: "src/a.js", status: "M" }, hunks: { path: "src/a.js", hunks: [], totalLines: 0 }, collapsed: false });
  const b = view({ file: { path: "src_a.js", status: "M" }, hunks: { path: "src_a.js", hunks: [], totalLines: 0 }, collapsed: false });
  const id = (out) => /aria-controls="([^"]+)"/.exec(out)[1];
  assert.notEqual(id(a), id(b));
  assert.doesNotMatch(a, /role="region"/, "a group, not a landmark per file");
});

test("split view shows a sign, and hostile characters in code and paths are made visible", () => {
  const split = view({ file: listing(hunksFile), hunks: hunksFile, layout: "split" });
  assert.match(split, /class="dv-sgn" aria-hidden="true">\+/);
  const evil = { path: "a\u202e.go", hunks: [{ oldStart: 1, oldLines: 0, newStart: 1, newLines: 1, header: "", lines: [{ t: "add", text: "x\u202e\u2066y", newNo: 1 }] }], totalLines: 1 };
  const out = view({ file: { path: "a\u202e.go", status: "A" }, hunks: evil });
  assert.ok(out.includes("U+202E"));
  assert.ok(!out.includes("\u202e"));
  assert.ok(!out.includes("\u2066"));
});

test("a very long line is cut with a button for the whole line, and a hunk with no numbers draws no range", async () => {
  const long = "y".repeat(5000);
  const reply = { path: "a", hunks: [{ header: "h", lines: [{ t: "add", text: long, newNo: 1 }] }], totalLines: 1 };
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${{ path: "a", status: "A" }} hunks=${reply} />`, box);
    await settle();
    assert.doesNotMatch(box.textContent, /undefined|NaN|@@ -/);
    assert.ok(box.textContent.length < 3000, "the line was cut");
    box.find(".dv-wholebtn").click();
    await settle();
    assert.ok(box.textContent.length > 5000);
  } finally {
    restore();
  }
});

test("more rows move the keyboard to the first new row", async () => {
  const lines = Array.from({ length: 900 }, (_, i) => ({ t: "add", text: "line " + i, newNo: i + 1 }));
  const reply = { path: "big.txt", hunks: [{ oldStart: 0, oldLines: 0, newStart: 1, newLines: 900, header: "", lines }], totalLines: 900 };
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${{ path: "big.txt", status: "A" }} hunks=${reply} />`, box);
    await settle();
    box.find(".drawer-btn").click();
    await settle();
    const rows = box.findAll(".dv-row");
    assert.equal(dom.document.activeElement, rows[399], "the first row of the new page");
    assert.match(box.find(".dv-live").textContent, /rows shown\./);
  } finally {
    restore();
  }
});

test("invisible and reordering characters are all shown, not just the bidi ones", () => {
  const out = showControls("a\u200b\u00ad\ufeff\u2060b\u200d");
  assert.equal(out, "a<U+200B><U+00AD><U+FEFF><U+2060>b<U+200D>");
});

test("a first line with no number does not leave every + out of the tab order", async () => {
  const reply = { path: "a", hunks: [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 2, header: "", lines: [{ t: "ctx", text: "a" }, { t: "add", text: "b", newNo: 2 }] }], totalLines: 2 };
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${{ path: "a", status: "M" }} hunks=${reply} onComment=${() => {}} />`, box);
    await settle();
    assert.deepEqual(box.findAll(".dv-plus").map((b) => b.getAttribute("tabindex")), ["0"]);
  } finally {
    restore();
  }
});

test("in split view Up and Down stay in the old or new column, and a failed read is said once", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} layout="split" narrow=${false} onComment=${() => {}} />`, box);
    await settle();
    const rid = (b) => b.getAttribute("data-rid");
    const right = box.findAll(".dv-plus").filter((b) => rid(b).endsWith(":r"));
    right[0].focus();
    press(right[0], "ArrowDown");
    assert.equal(dom.document.activeElement, right[1], "stayed in the new column");
    render(html`<${DiffViewer} file=${listing(failed)} hunks=${failed} />`, box);
    await settle();
    assert.equal(box.find(".dv-live").textContent, "", "the alert says it; the status line does not repeat it");
    assert.ok(box.find('[role="alert"]'));
  } finally {
    restore();
  }
});

test("a viewer that first mounts with no file draws once the file arrives, narrow as asked", async () => {
  const dom = createDom();
  const restore = install(dom);
  try {
    const box = dom.document.createElement("div");
    dom.body.appendChild(box);
    render(html`<${DiffViewer} file=${null} hunks=${null} />`, box);
    await settle();
    render(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} layout="split" narrow=${true} />`, box);
    await settle();
    assert.ok(box.find(".dv-tbl--unified"));
    assert.equal(box.find(".dv-tbl--split"), null, "narrow draws unified");
  } finally {
    restore();
  }
});

test("a gap whose lines could not be read says so on the gap", () => {
  const out = renderToString(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} onContext=${() => {}} contextErrors=${{ "h0:g": "gone" }} />`);
  assert.doesNotMatch(out, /dv-gap-err/, "no error for a gap that is not there");
  const ids = [...out.matchAll(/data-row="([^"]*g[^"]*)"/g)].map((m) => m[1]);
  assert.ok(ids.length, "there are gaps");
  const errs = Object.fromEntries(ids.map((id) => [id, "gone"]));
  assert.match(renderToString(html`<${DiffViewer} file=${listing(hunksFile)} hunks=${hunksFile} onContext=${() => {}} contextErrors=${errs} />`), /dv-gap-err" role="alert">gone/);
});

test("a diff that holds uncommitted changes has no + on its new side, and says why; the old side and the file stay open", () => {
  const props = { file: listing(hunksFile), onComment: () => {} };
  const plus = (out) => (out.match(/class="dv-plus"/g) || []).length;
  const plain = view({ ...props, hunks: { ...hunksFile, worktree: false } });
  const dirty = view({ ...props, hunks: { ...hunksFile, worktree: true } });
  assert.ok(plus(plain) > 0);
  assert.match(plain, /Comment on line \d+ of /);
  assert.doesNotMatch(plain, /uncommitted changes/);
  assert.doesNotMatch(dirty, /aria-label="Comment on line \d+ of /);
  assert.match(dirty, /aria-label="Comment on old line \d+ of /);
  assert.ok(plus(dirty) < plus(plain));
  assert.match(dirty, /uncommitted changes/);
  assert.match(dirty, /Comment on this file/);
  const split = view({ ...props, layout: "split", narrow: false, hunks: { ...hunksFile, worktree: true } });
  assert.doesNotMatch(split, /aria-label="Comment on line \d+ of /);
  assert.match(split, /aria-label="Comment on old line \d+ of /);
});
