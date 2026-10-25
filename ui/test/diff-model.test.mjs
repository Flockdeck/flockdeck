import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  unifiedRows, sideRows, pageRows, pairMarks, segments, fileFlags, isGenerated, PAGE_ROWS, CONTEXT_LINES,
} from "../src/drawer/diff/diffModel.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "..", "api", "fixtures", "built-changes-" + name + ".json"), "utf8"));
const hunksFile = fixture("04-diff-hunks");
const truncated = fixture("05-diff-truncated");
const binary = fixture("06-diff-binary");
const rename = fixture("07-diff-rename");
const failed = fixture("08-diff-error");
const between = fixture("09-diff-context");

const kinds = (rows) => rows.map((r) => r.type + (r.t ? ":" + r.t : ""));

test("fixture 04 in unified form: a gap above, two hunks, a gap between, one below", () => {
  const rows = unifiedRows(hunksFile.hunks);
  assert.deepEqual(kinds(rows), [
    "gap", "hunk",
    "line:ctx", "line:ctx", "line:ctx", "line:del", "line:add", "line:add", "line:ctx", "line:ctx", "line:ctx",
    "gap", "hunk",
    "line:ctx", "line:del", "line:add", "line:ctx", "line:ctx",
    "gap",
  ]);
  const gaps = rows.filter((r) => r.type === "gap");
  assert.deepEqual(gaps.map((g) => [g.id, g.from, g.to, g.count]), [["g-top", 1, 51, 51], ["g0", 60, 90, 31], ["g-end", 95, null, null]]);
  assert.deepEqual(gaps[0].ask, { side: "new", from: 1, to: 51 });
  assert.deepEqual(gaps[2].ask, { side: "new", from: 95, to: 94 + CONTEXT_LINES }, "asks for at most the desk's limit");
  assert.deepEqual(gaps[1].ask, { side: "new", from: 60, to: 90 });
  assert.equal(rows[1].header, "func (b *Bucket) Take(n int) bool {");
});

test("a line says which side a comment on it points at", () => {
  const lines = unifiedRows(hunksFile.hunks).filter((r) => r.type === "line");
  const del = lines.find((r) => r.t === "del");
  const add = lines.find((r) => r.t === "add");
  assert.deepEqual(del.anchor, { side: "old", line: 55 });
  assert.deepEqual(add.anchor, { side: "new", line: 55 });
  assert.deepEqual(lines[0].anchor, { side: "new", line: 52 });
  assert.equal(new Set(lines.map((r) => r.id)).size, lines.length, "row ids are unique");
});

test("the server's word marks are used when they fit the line", () => {
  const lines = unifiedRows(hunksFile.hunks).filter((r) => r.t === "del" || r.t === "add");
  assert.deepEqual(lines[0].marks, [[19, 20]]);
  assert.deepEqual(lines[1].marks, [[19, 21]]);
  assert.deepEqual(lines[2].marks, [], "an added line with no partner has none");
});

test("with no marks sent, they are worked out from the paired line, and bad ones are replaced", () => {
  const plain = JSON.parse(JSON.stringify(hunksFile.hunks));
  for (const h of plain) for (const l of h.lines) delete l.intra;
  const worked = unifiedRows(plain).filter((r) => r.t === "del" || r.t === "add");
  // The lines differ by an inserted "=" after ">", so only the added side has a span.
  assert.deepEqual(worked[0].marks, []);
  assert.equal(worked[1].marks.length, 1);
  assert.equal(segments(worked[1].text, worked[1].marks).find((x) => x.mark).text, "=");
  const bad = JSON.parse(JSON.stringify(hunksFile.hunks));
  bad[0].lines.find((l) => l.t === "del").intra = [[5, 999]];
  assert.deepEqual(unifiedRows(bad).find((r) => r.t === "del").marks, [], "an out-of-range mark is replaced by the worked-out one");
});

test("pairMarks takes off what two lines share, in code points", () => {
  assert.deepEqual(pairMarks("a > b", "a >= b"), { del: [], add: [[3, 4]] }, "a pure insertion marks only the added side");
  assert.deepEqual(pairMarks("x", "y"), { del: [], add: [] }, "nothing in common marks nothing");
  assert.deepEqual(pairMarks("same", "same"), { del: [], add: [] });
  assert.deepEqual(pairMarks("😀 old end", "😀 new end"), { del: [[2, 5]], add: [[2, 5]] });
  assert.deepEqual(pairMarks(undefined, "a"), { del: [], add: [] });
});

test("segments cut at marks, and ignore marks that do not fit", () => {
  assert.deepEqual(segments("abcdef", [[1, 3]]), [{ text: "a", mark: false }, { text: "bc", mark: true }, { text: "def", mark: false }]);
  assert.deepEqual(segments("abc", []), [{ text: "abc", mark: false }]);
  assert.deepEqual(segments("abc", [[2, 9]]), [{ text: "abc", mark: false }]);
  assert.deepEqual(segments("😀a", [[0, 1]]), [{ text: "😀", mark: true }, { text: "a", mark: false }]);
});

test("side by side pairs a deletion with the addition after it, and leaves the extra addition alone", () => {
  const rows = sideRows(hunksFile.hunks);
  const pairs = rows.filter((r) => r.type === "pair" && r.left !== r.right);
  assert.equal(pairs.length, 3);
  assert.equal(pairs[0].left.t, "del");
  assert.equal(pairs[0].right.t, "add");
  assert.equal(pairs[1].left, null, "the second addition has nothing opposite");
  assert.equal(pairs[1].right.newNo, 56);
  const ctx = rows.find((r) => r.type === "pair" && r.left.t === "ctx");
  assert.equal(ctx.left, ctx.right, "a context line is on both sides");
  assert.deepEqual(rows.filter((r) => r.type !== "pair").map((r) => r.type), ["gap", "hunk", "gap", "hunk", "gap"]);
});

test("fetched context is drawn at the start of the gap, which shrinks and then goes", () => {
  const fetched = { g0: { lines: between.lines } };
  const rows = unifiedRows(hunksFile.hunks, fetched);
  const shown = rows.filter((r) => r.id.startsWith("g0:c"));
  assert.equal(shown.length, between.lines.length);
  assert.ok(shown.every((r) => r.t === "ctx" && r.anchor.side === "new"));
  const gap = rows.find((r) => r.type === "gap" && r.id === "g0");
  assert.deepEqual([gap.from, gap.count], [60 + between.lines.length, 31 - between.lines.length]);
  const all = Array.from({ length: 31 }, (_, i) => ({ t: "ctx", text: "x", oldNo: 59 + i, newNo: 60 + i }));
  assert.equal(unifiedRows(hunksFile.hunks, { g0: { lines: all } }).some((r) => r.type === "gap" && r.id === "g0"), false);
  const end = unifiedRows(hunksFile.hunks, { "g-end": { lines: [], eof: true } });
  assert.equal(end.some((r) => r.id === "g-end"), false, "the end of the file leaves nothing to show");
});

test("a diff that starts at line 1 has no gap above it", () => {
  const rows = unifiedRows(rename.hunks);
  assert.equal(rows.some((r) => r.id === "g-top"), false);
});

test("fixture 05: a cut diff says how much was kept and how long it was", () => {
  const f = fileFlags(truncated);
  assert.deepEqual(f.truncated, { kept: 3, bytes: 96, total: 48213 }, "what the desk said it kept, not a recount");
  assert.equal(fileFlags({ ...truncated, truncated: true }).truncated.kept, 2, "a bare true falls back to a recount");
  assert.equal(fileFlags({ ...truncated, truncated: { lines: 3 } }).truncated.bytes, null);
  assert.equal(f.generated, true, "go.sum");
  assert.equal(f.empty, false);
});

test("fixture 06: a binary file has sizes and no rows", () => {
  const f = fileFlags(binary);
  assert.equal(f.binary, true);
  assert.deepEqual([f.sizeBefore, f.sizeAfter], [48210, 51377]);
  assert.equal(f.empty, false);
  assert.deepEqual(unifiedRows(binary.hunks), []);
});

test("fixture 07: a renamed file names where it came from", () => {
  const f = fileFlags(rename);
  assert.equal(f.renamed, true);
  assert.equal(f.from, "internal/limit/legacy.go");
  assert.equal(fileFlags({ path: "a", oldPath: "a", hunks: [] }).renamed, false);
});

test("fixture 08: an error is its own state", () => {
  const f = fileFlags(failed);
  assert.equal(f.error, "the directory is gone");
  assert.equal(f.empty, false);
  assert.deepEqual(unifiedRows(failed.hunks), []);
});

test("a line-ending change with no hunks says so, and an empty diff is empty", () => {
  assert.equal(fileFlags({ path: "a", hunks: [], eolChange: true }).eolOnly, true);
  assert.equal(fileFlags({ path: "a", hunks: [] }).empty, true);
  assert.equal(fileFlags(null).empty, true, "no reply throws nothing");
  assert.equal(fileFlags({ path: "a", hunks: [], eolChange: true }).eolChange, true);
  assert.equal(fileFlags({ path: "b", oldPath: "a", hunks: [] }).empty, false, "a pure rename is not empty");
  assert.equal(fileFlags({ path: "a", hunks: [], modeChange: true }).empty, false);
  const bare = fileFlags({ path: "a", hunks: [], totalLines: 40 });
  assert.deepEqual([bare.unknown, bare.empty], [true, false], "no hunks but lines: not read as nothing changed");
});

test("generated files are those the listing marks or the path says", () => {
  assert.equal(isGenerated("go.sum"), true);
  assert.equal(isGenerated("web/package-lock.json"), true);
  assert.equal(isGenerated("vendor/x/y.go"), true);
  assert.equal(isGenerated("api/x.pb.go"), true);
  assert.equal(isGenerated("web/app.min.css"), true);
  assert.equal(isGenerated("dist/app.js"), false, "only the contract rules");
  assert.equal(isGenerated("composer.lock"), false);
  assert.equal(isGenerated("internal/limit/limit.go"), false);
  assert.equal(fileFlags({ path: "a.go", hunks: [] }, { generated: true }).generated, true);
});

test("paging gives a page of rows and how many are left", () => {
  const rows = Array.from({ length: PAGE_ROWS * 2 + 5 }, (_, i) => ({ id: "r" + i }));
  assert.deepEqual([pageRows(rows).rows.length, pageRows(rows).hidden], [PAGE_ROWS, PAGE_ROWS + 5]);
  assert.deepEqual([pageRows(rows, 3).rows.length, pageRows(rows, 3).hidden], [PAGE_ROWS * 2 + 5, 0]);
  assert.deepEqual(pageRows([], 1), { rows: [], hidden: 0 });
});

test("missing and odd input neither throws nor draws", () => {
  assert.deepEqual(unifiedRows(undefined), []);
  assert.deepEqual(unifiedRows([null, "x", { lines: "no" }]).filter((r) => r.type === "line"), []);
  assert.doesNotThrow(() => sideRows([{ newStart: 1, newLines: 1, oldStart: 1, oldLines: 0, lines: [{ t: "add", text: 5 }] }], null));
});

test("no gap is drawn after a cut diff, a deleted file or the one hunk of a new file", () => {
  const ids = (rows) => rows.filter((r) => r.type === "gap").map((r) => r.id);
  assert.deepEqual(ids(unifiedRows(hunksFile.hunks, {}, { cut: true })), ["g-top", "g0"], "the cut part was never sent");
  assert.deepEqual(ids(sideRows(hunksFile.hunks, {}, { cut: true })), ["g-top", "g0"]);
  const gone = [{ oldStart: 1, oldLines: 2, newStart: 0, newLines: 0, header: "", lines: [{ t: "del", text: "a", oldNo: 1 }, { t: "del", text: "b", oldNo: 2 }] }];
  assert.deepEqual(ids(unifiedRows(gone)), []);
  const added = [{ oldStart: 0, oldLines: 0, newStart: 1, newLines: 2, header: "", lines: [{ t: "add", text: "a", newNo: 1 }, { t: "add", text: "b", newNo: 2 }] }];
  assert.deepEqual(ids(unifiedRows(added)), []);
});

test("a line with no number anchors on null, and a long pair is not marked", () => {
  const rows = unifiedRows([{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 1, lines: [{ t: "del", text: "a" }, { t: "add", text: "b" }] }]).filter((r) => r.type === "line");
  assert.deepEqual([rows[0].anchor, rows[1].anchor], [{ side: "old", line: null }, { side: "new", line: null }]);
  const long = "x".repeat(3000);
  assert.deepEqual(pairMarks(long + "a", long + "b"), { del: [], add: [] });
  assert.equal(pageRows([{}], NaN).rows.length, 1);
});
