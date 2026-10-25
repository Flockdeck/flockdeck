// The diff viewer's model: a `diffHunks` reply (docs/changes-api.md, section 4)
// turned into rows a view can draw, in unified or side-by-side form. It reads data
// and draws nothing, so it is tested on the built fixtures.
//
// - Rows are { type: "hunk" | "line" | "gap" } in unified form and
//   { type: "hunk" | "pair" | "gap" } in side-by-side form. A line row is
//   { id, t: "ctx" | "add" | "del", text, oldNo, newNo, marks, anchor }, where
//   marks are [start, end) pairs in code points (the server's `intra` when it sent
//   them, else worked out from the paired line) and anchor is { side, line }, the
//   place a review comment on the row points at (a deleted line is on the old
//   side, any other on the new).
// - A gap is the run of unchanged lines the diff left out: before the first hunk,
//   between two, or after the last. { id, side: "new", from, to, count, ask } says
//   what `diffContext` would be asked for. After the last hunk the length of the
//   file is not known, so to and count are null until the desk says `eof`.
// - Context already fetched is passed in as { [gap id]: { lines, eof } } and is
//   drawn as ctx rows at the start of the gap, which then shrinks.
// - The view keeps `fetched` itself: each `diffContext` reply for a gap is added to
//   that gap's entry ({ lines: earlier lines plus the new ones, eof }), and the
//   model reads it back; nothing here asks or stores.
// - Hunk numbers are taken as sent. A hunk with no new lines (a diff asked for
//   with no context) is read as sitting between the line it names and the next.

/** PAGE_ROWS is how many rows are drawn at a time for a long diff. */
export const PAGE_ROWS = 400;

/** CONTEXT_LINES is the most `diffContext` returns at once. */
export const CONTEXT_LINES = 200;

/** MARK_LIMIT is the longest line (in code points) a word mark is worked out for; a longer pair is shown unmarked. */
export const MARK_LIMIT = 2000;

const list = (v) => (Array.isArray(v) ? v : []);
const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const isNumber = (v) => typeof v === "number" && Number.isFinite(v);

/** pointsOf is a string as an array of code points, the unit `intra` counts in. */
const pointsOf = (s) => Array.from(typeof s === "string" ? s : "");

/** validMarks says whether marks is a sorted list of non-empty [start, end) pairs inside a line of len code points. */
function validMarks(marks, len) {
  if (!Array.isArray(marks)) return false;
  let at = 0;
  for (const m of marks) {
    if (!Array.isArray(m) || m.length !== 2 || !isNumber(m[0]) || !isNumber(m[1])) return false;
    if (m[0] < at || m[1] <= m[0] || m[1] > len) return false;
    at = m[1];
  }
  return true;
}

/**
 * pairMarks works out the changed span for a deleted line and the added line it
 * pairs with: what is left once the start and the end they share are taken off.
 * It answers { del, add }, each a list of [start, end) pairs. There are none when
 * the lines have nothing in common (marking the whole line says nothing) or are
 * equal.
 */
export function pairMarks(oldText, newText) {
  const a = pointsOf(oldText);
  const b = pointsOf(newText);
  if (a.length > MARK_LIMIT || b.length > MARK_LIMIT) return { del: [], add: [] };
  let p = 0;
  while (p < a.length && p < b.length && a[p] === b[p]) p++;
  let s = 0;
  while (s < a.length - p && s < b.length - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++;
  if (p + s === 0) return { del: [], add: [] };
  const span = (len) => (len - s > p ? [[p, len - s]] : []);
  return { del: span(a.length), add: span(b.length) };
}

/** segments cuts text at marks into { text, mark } runs, so a view can draw the marked words apart. */
export function segments(text, marks) {
  const pts = pointsOf(text);
  if (!validMarks(marks, pts.length) || !marks.length) return [{ text: pts.join(""), mark: false }];
  const out = [];
  let at = 0;
  for (const [s, e] of marks) {
    if (s > at) out.push({ text: pts.slice(at, s).join(""), mark: false });
    out.push({ text: pts.slice(s, e).join(""), mark: true });
    at = e;
  }
  if (at < pts.length) out.push({ text: pts.slice(at).join(""), mark: false });
  return out;
}

const firstNew = (h) => (h.newLines === 0 ? h.newStart + 1 : h.newStart);
const lastNew = (h) => (h.newLines === 0 ? h.newStart : h.newStart + h.newLines - 1);

/** lineRows makes the line rows of one hunk, with marks on the lines that pair across a change. */
function lineRows(h, hi) {
  const rows = list(h.lines).filter(isObject).map((l, i) => {
    const t = l.t === "add" || l.t === "del" ? l.t : "ctx";
    const oldNo = isNumber(l.oldNo) ? l.oldNo : null;
    const newNo = isNumber(l.newNo) ? l.newNo : null;
    const anchor = t === "del" ? { side: "old", line: oldNo } : { side: "new", line: newNo };
    return { type: "line", id: "h" + hi + ":" + i, t, text: typeof l.text === "string" ? l.text : "", oldNo, newNo, marks: [], anchor, intra: l.intra };
  });
  // A run of deletions followed by a run of additions: the n-th of one pairs with the n-th of the other.
  let i = 0;
  while (i < rows.length) {
    if (rows[i].t !== "del") { i++; continue; }
    let d = i;
    while (d < rows.length && rows[d].t === "del") d++;
    let a = d;
    while (a < rows.length && rows[a].t === "add") a++;
    for (let k = 0; k < Math.min(d - i, a - d); k++) {
      const del = rows[i + k];
      const add = rows[d + k];
      if (validMarks(del.intra, pointsOf(del.text).length) && validMarks(add.intra, pointsOf(add.text).length)) {
        del.marks = del.intra;
        add.marks = add.intra;
      } else {
        const m = pairMarks(del.text, add.text);
        del.marks = m.del;
        add.marks = m.add;
      }
    }
    i = a;
  }
  for (const r of rows) delete r.intra;
  return rows;
}

const ctxRow = (id, l) => {
  const newNo = isNumber(l.newNo) ? l.newNo : null;
  return { type: "line", id, t: "ctx", text: typeof l.text === "string" ? l.text : "", oldNo: isNumber(l.oldNo) ? l.oldNo : null, newNo, marks: [], anchor: { side: "new", line: newNo } };
};

/** gapRows makes the rows for the unchanged lines between prev and next (either may be null) on the new side. */
function gapRows(id, prev, next, filled) {
  const got = filled && Array.isArray(filled.lines) ? filled.lines.filter(isObject) : [];
  const rows = got.map((l, i) => ctxRow(id + ":c" + i, l));
  const from = (prev ? lastNew(prev) + 1 : 1) + got.length;
  const eof = !!(filled && filled.eof);
  let to = null;
  let count = null;
  if (next) {
    to = firstNew(next) - 1;
    count = Math.max(0, to - from + 1);
  } else if (eof) {
    count = 0;
  }
  if (count === null || count > 0) {
    const ask = { side: "new", from, to: Math.min(to === null ? Infinity : to, from + CONTEXT_LINES - 1) };
    rows.push({ type: "gap", id, side: "new", from, to, count, ask });
  }
  return rows;
}

/**
 * unifiedRows turns hunks into unified rows. fetched holds context already asked
 * for, by gap id ("g-top", "g0", "g1", ... and "g-end"): { lines, eof }. A gap
 * with nothing left to show is not drawn, and the one after the last hunk is
 * drawn until the file's end has been reached. It is not drawn at all when the
 * diff was cut (opts.cut: the hunks stop before the file does, and the view says
 * so there), after a deleted file, or when the one hunk of a new file holds it all.
 */
export function unifiedRows(hunks, fetched = {}, { cut = false } = {}) {
  const hs = list(hunks).filter(isObject);
  const fill = isObject(fetched) ? fetched : {};
  const rows = [];
  hs.forEach((h, hi) => {
    const id = hi === 0 ? "g-top" : "g" + (hi - 1);
    rows.push(...gapRows(id, hi === 0 ? null : hs[hi - 1], h, fill[id]));
    rows.push({ type: "hunk", id: "h" + hi, header: typeof h.header === "string" ? h.header : "", oldStart: h.oldStart, oldLines: h.oldLines, newStart: h.newStart, newLines: h.newLines });
    rows.push(...lineRows(h, hi));
  });
  const tail = hs.length ? hs[hs.length - 1] : null;
  const gone = tail && tail.newStart === 0 && tail.newLines === 0;
  const added = tail && hs.length === 1 && tail.oldStart === 0 && tail.oldLines === 0;
  if (tail && !cut && !gone && !added) rows.push(...gapRows("g-end", tail, null, fill["g-end"]));
  return rows;
}

/**
 * sideRows is the same rows as unified, but a run of deletions and the additions
 * that follow it share rows, old on the left and new on the right, with an empty
 * side where one has more lines than the other. A context line is on both sides.
 */
export function sideRows(hunks, fetched = {}, opts = {}) {
  const rows = unifiedRows(hunks, fetched, opts);
  const out = [];
  let i = 0;
  while (i < rows.length) {
    const r = rows[i];
    if (r.type !== "line") { out.push(r); i++; continue; }
    if (r.t === "ctx") { out.push({ type: "pair", id: r.id, left: r, right: r }); i++; continue; }
    const dels = [];
    const adds = [];
    while (i < rows.length && rows[i].type === "line" && rows[i].t === "del") dels.push(rows[i++]);
    while (i < rows.length && rows[i].type === "line" && rows[i].t === "add") adds.push(rows[i++]);
    for (let k = 0; k < Math.max(dels.length, adds.length); k++) {
      const left = dels[k] || null;
      const right = adds[k] || null;
      out.push({ type: "pair", id: (left || right).id, left, right });
    }
  }
  return out;
}

/** pageRows is the first `pages` pages of rows and how many are left. */
export function pageRows(rows, pages = 1) {
  const n = Math.max(1, Math.floor(Number.isFinite(pages) ? pages : 1)) * PAGE_ROWS;
  return { rows: rows.slice(0, n), hidden: Math.max(0, rows.length - n) };
}

// Paths a person does not review line by line: the rules of docs/changes-api.md, section 3, and no more.
const GENERATED = [
  /(^|\/)(go\.sum|package-lock\.json|yarn\.lock|pnpm-lock\.yaml|Cargo\.lock)$/,
  /(^|\/)(vendor|node_modules)\//,
  /\.min\.(js|css)$/,
  /\.pb\.go$/,
];

/** isGenerated says whether a path looks like one a generator wrote, for a file the listing did not mark. */
export function isGenerated(path) {
  return typeof path === "string" && GENERATED.some((re) => re.test(path));
}

/**
 * fileFlags reads a `diffHunks` reply for what the view says about the file:
 *   error: the reply's error text, or "";
 *   renamed, from: the file was renamed, and its old path;
 *   binary, sizeBefore, sizeAfter, modeChange;
 *   generated: the listing said so (listed.generated) or the path looks generated;
 *   truncated: { kept, bytes, total } when the desk cut the diff, else null. kept
 *     and bytes are what the desk said it kept (kept is counted from the hunks only
 *     when it sent no number, or a bare true), total is the whole diff's line count;
 *   eolChange: the line endings changed; eolOnly: that is all, so there are no hunks;
 *   unknown: no hunks but the diff has lines (totalLines above 0), so the reply
 *     is not to be read as "nothing changed";
 *   empty: nothing to draw and nothing to say.
 */
export function fileFlags(reply, listed = null) {
  const r = isObject(reply) ? reply : {};
  const hunks = list(r.hunks);
  const counted = hunks.reduce((n, h) => n + (isObject(h) ? list(h.lines).length : 0), 0);
  const binary = r.binary === true;
  const eolChange = r.eolChange === true;
  const eolOnly = eolChange && !hunks.length && !binary;
  const error = typeof r.error === "string" ? r.error : "";
  const cut = isObject(r.truncated) ? r.truncated : r.truncated === true ? {} : null;
  const renamed = typeof r.oldPath === "string" && r.oldPath !== "" && r.oldPath !== r.path;
  const modeChange = !!r.modeChange;
  const unknown = !error && !hunks.length && !binary && !eolOnly && isNumber(r.totalLines) && r.totalLines > 0;
  return {
    error,
    renamed,
    from: typeof r.oldPath === "string" ? r.oldPath : "",
    binary,
    sizeBefore: isNumber(r.sizeBefore) ? r.sizeBefore : null,
    sizeAfter: isNumber(r.sizeAfter) ? r.sizeAfter : null,
    modeChange,
    generated: !!(listed && listed.generated) || isGenerated(r.path),
    truncated: cut ? { kept: isNumber(cut.lines) ? cut.lines : counted, bytes: isNumber(cut.bytes) ? cut.bytes : null, total: isNumber(r.totalLines) ? r.totalLines : null } : null,
    eolChange,
    eolOnly,
    unknown,
    empty: !error && !hunks.length && !binary && !eolOnly && !renamed && !cut && !modeChange && !unknown,
  };
}
