// One file's diff, drawn from a `diffHunks` reply (docs/changes-api.md, section 4)
// through the model of diffModel.js. It draws and reports; it asks for nothing and
// keeps nothing the page should keep, except which file is folded and how many
// pages are drawn: the caller must key a DiffViewer by the file's path, so those do
// not carry over to another file.
//
// Props:
//   branch: the branch name, for labels.
//   file: the listing entry { path, oldPath, status, added, removed, generated }.
//   hunks: the diffHunks reply for the file, or null while it is being read. An
//     `error` in it is drawn in place of the diff.
//   layout: "unified" or "split". narrow says the container is too narrow for split
//     and the diff is drawn unified then; left null, it is worked out from the
//     viewer's own width (NARROW_PX), which the stylesheet's `@container dv` rules
//     use too.
//   comments: [{ path, side, line, state }]; a line with open ones is marked.
//   onComment({ file, side, line }): called by the "+" of a line. With none there
//     is no "+" and no column for it: the diff is read-only.
//   context: the unchanged lines fetched so far, { [gap id]: { lines, eof } }, kept
//     by the caller, which adds each `diffContext` reply to it.
//   contextErrors: { [gap id]: text } says on that gap that its lines could not be read.
//   onContext(gapId, ask): called by a "Show N unchanged lines" button, with the
//     diffContext request to make ({ side, from, to }).
//   flow: the state of review comments (commentFlow.js's useCommentFlow). With it
//     the diff draws each place's comments under its line, the composer where one is
//     being written (the keyboard returns to the "+" when it closes), and a "+" that
//     opens it; comments and onComment are then taken from it. headSha marks a
//     comment written at another head as outdated (it is the branch's head now, not
//     the head the diff was read at: that is the flow's drawnSha), and nameOf(pane)
//     gives the name drawn for a pane. Comments on lines the diff does not show (a
//     page not drawn yet, a gap, a line the new head dropped) are drawn in a block
//     above the diff, with their place; a renamed file's comments may carry its old path.
//   readOnly: removes every way to write or close a comment ("+", "Comment on this
//     file", Resolve, Reopen); the comments are still drawn. The caller passes it for
//     a viewer (lib/role.js isViewer), a relay window, and a desk whose caps lack
//     changesDiff.
//   collapsed, onToggle: the file's open state when the caller keeps it; without
//     them the file keeps its own, shut at first for a generated file.
//
// Keyboard and screen reader:
// - The "+" buttons are one tab stop for the whole diff: the arrow keys, Home and
//   End move between them, and "Skip this diff" (shown on focus) goes past them.
// - One status line stays in the page, empty first and filled after, so a reader
//   announces what is happening ("Reading the diff", the cut, rows shown). When
//   more rows or unchanged lines are drawn the keyboard goes to the first new row.
// - The diff is a group named by the file's path. Signs are text, not only colour,
//   in both layouts. Characters that reorder text are shown as <U+202E>.
// Everything in the diff is source text, so it is drawn as text and never as markup.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";
import { statusWord, isOpenComment, pathText } from "./FileList.js";
import { unifiedRows, sideRows, pageRows, segments, fileFlags, showControls, PAGE_ROWS } from "./diffModel.js";
import { Composer, Thread } from "./Comments.js";
import { commentsAt, lineKey } from "./commentModel.js";

/** NARROW_PX is the width under which a diff is drawn unified and tightly. */
export const NARROW_PX = 680;

/** LINE_CAP is the most characters of one line drawn before "Show the whole line". */
export const LINE_CAP = 2000;

/** effectiveLayout is the layout to draw: split only when asked for and there is room. */
export function effectiveLayout(layout, narrow) {
  return layout === "split" && !narrow ? "split" : "unified";
}

const NONE = [];
const NO_CONTEXT = {};
const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const plural = (n, one, many) => n.toLocaleString("en-US") + " " + (n === 1 ? one : many);
const SIGN = { add: "+", del: "−", ctx: " " };
const SIGN_WORD = { add: "added: ", del: "removed: ", ctx: "" };
let counter = 0;

/** sizeWords is a byte count as words. */
export function sizeWords(n) {
  if (typeof n !== "number" || !Number.isFinite(n)) return "unknown size";
  if (n < 1024) return n + " bytes";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
  return (n / (1024 * 1024)).toFixed(1) + " MiB";
}

/** useWidth follows the width of an element, 0 until it is known or where nothing can measure it. It measures before the first paint, and again when the element first exists (present). */
function useWidth(ref, present) {
  const [w, setW] = useState(0);
  useLayoutEffect(() => {
    if (ref.current && ref.current.clientWidth) setW(ref.current.clientWidth);
  }, [present]);
  useEffect(() => {
    const el = ref.current;
    if (!el) return undefined;
    if (typeof ResizeObserver === "undefined") return undefined;
    const ro = new ResizeObserver((entries) => {
      const e = entries && entries[0];
      if (e && e.contentRect) setW(Math.round(e.contentRect.width));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [present]);
  return w;
}

function Code({ line, id }) {
  const [whole, setWhole] = useState(false);
  const t = line.text;
  if (t.length > LINE_CAP && !whole) {
    return html`<span id=${id}>${showControls(Array.from(t).slice(0, LINE_CAP).join(""))}…</span> <button type="button" class="dv-wholebtn" onClick=${() => setWhole(true)}>${"Show the whole line (" + Array.from(t).length.toLocaleString("en-US") + " characters)"}</button>`;
  }
  return html`<span id=${id}>${segments(t, line.marks).map((s, i) => (s.mark ? html`<mark key=${i}>${s.text}</mark>` : s.text))}</span>`;
}

function Plus({ file, line, side, notes, onComment, rid, active, onActive, describedBy }) {
  if (!onComment) return null;
  if (typeof line !== "number") return html`<td class="dv-p"></td>`;
  const n = notes.get(lineKey(side, line)) || 0;
  const where = (side === "old" ? "old line " : "line ") + line + " of " + showControls(file);
  return html`<td class="dv-p"><button type="button" class="dv-plus" data-rid=${rid} tabindex=${active === rid ? 0 : -1} aria-describedby=${describedBy}
    aria-label=${"Comment on " + where + (n ? ", " + plural(n, "open comment", "open comments") : "")}
    onFocus=${() => onActive(rid)} onClick=${() => onComment({ file, side, line })}>+</button></td>`;
}

function Gap({ row, cols, onContext, error = "" }) {
  const where = row.to === null ? " from line " + row.from : " (lines " + row.from + " to " + row.to + ")";
  const label = row.count === null ? "Show the unchanged lines below" + where : "Show " + plural(row.count, "unchanged line", "unchanged lines") + where;
  return html`<tr class="dv-gap" data-row=${row.id}><td colspan=${cols}>
    <button type="button" class="dv-gapbtn" disabled=${!onContext} onClick=${() => onContext && onContext(row.id, row.ask)}>${label}</button>
    ${error ? html` <span class="dv-gap-err" role="alert">${showControls(clip(text(error), 200))}</span>` : null}
  </td></tr>`;
}

function Hunk({ row, cols }) {
  const n = (v) => typeof v === "number" && Number.isFinite(v);
  const range = n(row.oldStart) && n(row.oldLines) && n(row.newStart) && n(row.newLines) ? "@@ -" + row.oldStart + "," + row.oldLines + " +" + row.newStart + "," + row.newLines + " @@" : "";
  return html`<tr class="dv-hunk" data-row=${row.id}><td colspan=${cols}>${range ? html`<span class="dv-range">${range}</span> ` : null}${showControls(clip(text(row.header), 300))}</td></tr>`;
}

function UnifiedRow({ row, uid, file, notes, onComment, active, onActive }) {
  const mark = notes.get(lineKey(row.anchor.side, row.anchor.line)) || 0;
  const cid = uid + "-" + row.id;
  return html`<tr class=${"dv-row dv-row--" + row.t + (mark ? " has-comment" : "")} data-row=${row.id}>
    <${Plus} file=${file} line=${row.anchor.line} side=${row.anchor.side} notes=${notes} onComment=${onComment} rid=${row.id} active=${active} onActive=${onActive} describedBy=${cid} />
    <td class="dv-n">${row.oldNo == null ? "" : row.oldNo}</td>
    <td class="dv-n">${row.newNo == null ? "" : row.newNo}</td>
    <td class="dv-sg"><span aria-hidden="true">${SIGN[row.t]}</span><span class="dv-sr">${SIGN_WORD[row.t]}</span></td>
    <td class="dv-c"><${Code} line=${row} id=${cid} /></td>
  </tr>`;
}

function Side({ line, which, uid, file, notes, onComment, active, onActive, withPlus, rid }) {
  const tds = [];
  if (withPlus) {
    tds.push(line && withPlus === "yes"
      ? html`<${Plus} key="p" file=${file} line=${line.anchor.line} side=${line.anchor.side} notes=${notes} onComment=${onComment} rid=${rid} active=${active} onActive=${onActive} describedBy=${uid + "-" + line.id + which} />`
      : html`<td key="p" class="dv-p"></td>`);
  }
  if (!line) return html`${tds}<td class="dv-n dv-e"></td><td class="dv-c dv-e"></td>`;
  const no = which === "l" ? line.oldNo : line.newNo;
  return html`${tds}<td class=${"dv-n dv-" + line.t}>${no == null ? "" : no}</td>
    <td class=${"dv-c dv-" + line.t}><span class="dv-sgn" aria-hidden="true">${SIGN[line.t]}</span><span class="dv-sr">${SIGN_WORD[line.t]}</span><${Code} line=${line} id=${uid + "-" + line.id + which} /></td>`;
}

function SplitRow({ row, uid, file, notes, onComment, active, onActive }) {
  const same = row.left === row.right;
  const commentable = !!onComment;
  return html`<tr class="dv-row" data-row=${row.id}>
    <${Side} line=${row.left} which="l" uid=${uid} file=${file} notes=${notes} onComment=${onComment} active=${active} onActive=${onActive} withPlus=${commentable ? (same ? "empty" : "yes") : ""} rid=${row.id + ":l"} />
    <${Side} line=${row.right} which="r" uid=${uid} file=${file} notes=${notes} onComment=${onComment} active=${active} onActive=${onActive} withPlus=${commentable ? "yes" : ""} rid=${row.id + ":r"} />
  </tr>`;
}

/** plusIds are the keys of the "+" buttons of the rows drawn, in order. */
function plusIds(rows, split) {
  const out = [];
  const ok = (l) => !!l && typeof l.anchor.line === "number";
  for (const r of rows) {
    if (r.type === "line") {
      if (ok(r)) out.push(r.id);
    } else if (r.type === "pair" && split) {
      if (r.left !== r.right && ok(r.left)) out.push(r.id + ":l");
      if (ok(r.right)) out.push(r.id + ":r");
    }
  }
  return out;
}

// What goes under a line: its comments, and the composer when one is open there. In the table it is a row; above it (wrap) a block.
function Inline({ keys, path, at, flow, headSha, nameOf, cols, wrap = false, readOnly = false }) {
  const here = (flow && flow.composing && flow.composing.file === path) ? lineKey(flow.composing.side, flow.composing.line) : "";
  const shown = keys.filter((k) => at.has(k) || k === here);
  if (!shown.length) return null;
  const body = shown.map((k) => html`<div key=${k}>
    <${Thread} comments=${at.get(k) || NONE} headSha=${headSha} errors=${flow ? flow.errors : NO_CONTEXT} closing=${flow ? flow.closing : NO_CONTEXT} nameOf=${nameOf} onResolve=${flow && !readOnly ? flow.close : null} onReopen=${flow && !readOnly ? flow.reopen : null} />
    ${k === here ? html`<${Composer} target=${flow.composing} draft=${flow.draft} error=${flow.error} busy=${flow.busy} onDraft=${flow.setDraft} onSend=${flow.submit} onCancel=${flow.cancel} />` : null}
  </div>`);
  return wrap ? html`<div class="dv-cm">${body}</div>` : html`<tr class="dv-cm"><td colspan=${cols}>${body}</td></tr>`;
}

/** DiffViewer draws one file: its header, which folds it, and its diff. */
export function DiffViewer({ branch = "", file = null, hunks = null, layout = "unified", narrow = null, comments: given = NONE, onComment: onCommentGiven = null, flow = null, readOnly = false, headSha = "", nameOf = null, context = NO_CONTEXT, contextErrors = NO_CONTEXT, onContext = null, collapsed = null, onToggle = null }) {
  const comments = flow ? flow.comments : given;
  const path = file ? text(file.path) : "";
  const uidRef = useRef(null);
  if (!uidRef.current) uidRef.current = "dv" + ++counter;
  const uid = uidRef.current;
  const wrap = useRef(null);
  const endRef = useRef(null);
  const width = useWidth(wrap, !!file);
  const isNarrow = narrow !== null ? !!narrow : width > 0 && width < NARROW_PX;
  const flags = useMemo(() => (hunks ? fileFlags(hunks, file) : null), [hunks, file]);
  const [shutOwn, setShutOwn] = useState(null);
  const [pages, setPages] = useState(1);
  const [active, setActive] = useState("");
  const [said, setSaid] = useState("");
  const focusRow = useRef("");
  const lay = effectiveLayout(layout, isNarrow);
  const returnTo = useRef("");
  // The composer opens from a "+" and gives the keyboard back to it.
  const onComment = flow
    ? readOnly ? null : (t) => {
        const el = wrap.current && wrap.current.ownerDocument ? wrap.current.ownerDocument.activeElement : null;
        returnTo.current = el && el.getAttribute ? el.getAttribute("data-rid") || "" : "";
        flow.open(t);
      }
    : readOnly ? null : onCommentGiven;
  const composingHere = !!(flow && flow.composing);
  useEffect(() => {
    if (composingHere || !returnTo.current || !wrap.current) return;
    const rid = returnTo.current;
    returnTo.current = "";
    const b = Array.from(wrap.current.querySelectorAll(".dv-plus")).find((x) => x.getAttribute("data-rid") === rid);
    if (b) b.focus();
  }, [composingHere]);
  const generated = !!(flags ? flags.generated : file && file.generated);
  const shut = collapsed !== null ? collapsed : shutOwn !== null ? shutOwn : generated;
  const toggle = () => (onToggle ? onToggle(!shut) : setShutOwn(!shut));
  const rows = useMemo(() => {
    if (!hunks || !Array.isArray(hunks.hunks) || !hunks.hunks.length) return NONE;
    const cut = !!(flags && flags.truncated);
    return lay === "split" ? sideRows(hunks.hunks, context, { cut }) : unifiedRows(hunks.hunks, context, { cut });
  }, [hunks, context, lay, flags]);
  const oldPath = file ? text(file.oldPath) : "";
  const at = useMemo(() => commentsAt(comments, path, oldPath), [comments, path, oldPath]);
  const notes = useMemo(() => {
    const m = new Map();
    for (const c of Array.isArray(comments) ? comments : NONE) {
      if (!isOpenComment(c) || !(c.path === path || (oldPath && c.path === oldPath))) continue;
      const k = lineKey(c.side, c.line);
      m.set(k, (m.get(k) || 0) + 1);
    }
    return m;
  }, [comments, path, oldPath]);
  const page = useMemo(() => pageRows(rows, pages), [rows, pages]);
  const ids = useMemo(() => plusIds(page.rows, lay === "split"), [page, lay]);
  const stop = ids.includes(active) ? active : ids[0] || "";
  // Comments whose line is not drawn: they are not lost, they go above the diff.
  const unplaced = useMemo(() => {
    if (!hunks) return NONE;
    const drawn = new Set();
    for (const r of page.rows) {
      if (r.type === "line") drawn.add(lineKey(r.anchor.side, r.anchor.line));
      else if (r.type === "pair") for (const l of [r.left, r.right]) if (l) drawn.add(lineKey(l.anchor.side, l.anchor.line));
    }
    const out = [];
    for (const [k, v] of at) if (k !== "file" && !drawn.has(k)) out.push(...v);
    return out;
  }, [page, at, hunks]);

  // What the status line says; it is filled after the first draw so a reader announces it.
  const added = file && typeof file.added === "number" ? file.added : 0;
  const removed = file && typeof file.removed === "number" ? file.removed : 0;
  let statusText = "";
  if (!hunks) statusText = "Reading the diff of " + showControls(path) + ".";
  else if (flags.binary) statusText = "Binary file changed. There is no text diff.";
  else if (page.hidden > 0) statusText = page.rows.length.toLocaleString("en-US") + " of " + rows.length.toLocaleString("en-US") + " rows shown.";
  else if (flags.truncated) statusText = "The diff is cut: " + flags.truncated.kept.toLocaleString("en-US") + " lines shown.";
  else if (shut && generated) statusText = "Generated file, collapsed.";
  useEffect(() => { setSaid(statusText); }, [statusText]);
  // After more rows or unchanged lines are drawn, the keyboard goes to the first new row.
  useEffect(() => {
    if (!focusRow.current || !wrap.current) return;
    const tr = wrap.current.querySelector('[data-row="' + focusRow.current + '"]');
    if (tr) {
      focusRow.current = "";
      tr.setAttribute("tabindex", "-1");
      tr.focus();
    }
  }, [page, rows]);

  if (!file) return null;
  const cols = lay === "split" ? (onComment ? 6 : 4) : (onComment ? 5 : 4);
  const renamed = flags ? flags.renamed : !!(file.oldPath && file.oldPath !== file.path);
  const from = flags && flags.from ? flags.from : text(file.oldPath);
  const bodyId = uid + "-body";
  const nameId = uid + "-name";

  let note = null;
  if (!hunks) note = html`<p class="dv-note">Reading the diff…</p>`;
  else if (flags.error) note = html`<p class="dv-note dv-note--err" role="alert">${"The diff could not be read: " + showControls(clip(flags.error, 300))}</p>`;
  else if (flags.binary) {
    note = html`<p class="dv-note dv-binary">${"Binary file changed (" + sizeWords(flags.sizeBefore) + " to " + sizeWords(flags.sizeAfter) + "). There is no text diff. A comment can still be left on the file."}</p>`;
  } else if (flags.eolOnly) note = html`<p class="dv-note">Only the line endings changed.</p>`;
  else if (flags.unknown) note = html`<p class="dv-note">The diff has lines, but none came back. Try again.</p>`;
  else if (flags.empty) note = html`<p class="dv-note">Nothing changed in this file.</p>`;
  else if (flags.renamed && !rows.length) note = html`<p class="dv-note">Renamed with no change to the lines.</p>`;
  else if (flags.modeChange && !rows.length) note = html`<p class="dv-note">Only the file mode changed.</p>`;

  const onKey = (e) => {
    const k = e.key;
    if (!["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight", "Home", "End"].includes(k) || !wrap.current) return;
    let btns = Array.from(wrap.current.querySelectorAll(".dv-plus"));
    if (btns.indexOf(e.target) < 0) return;
    // Up and Down stay in the column (old or new) of a split diff; Left and Right step through every "+".
    if (lay === "split" && (k === "ArrowDown" || k === "ArrowUp")) {
      const col = (e.target.getAttribute("data-rid") || "").slice(-2);
      btns = btns.filter((b) => (b.getAttribute("data-rid") || "").slice(-2) === col);
    }
    const at = btns.indexOf(e.target);
    const forward = k === "ArrowDown" || k === "ArrowRight";
    const to = k === "Home" ? 0 : k === "End" ? btns.length - 1 : Math.max(0, Math.min(btns.length - 1, at + (forward ? 1 : -1)));
    e.preventDefault();
    btns[to].focus();
    setActive(btns[to].getAttribute("data-rid") || "");
  };
  const more = () => {
    const next = rows[page.rows.length];
    if (next) focusRow.current = next.id;
    setPages((n) => n + 1);
  };
  const askContext = (id, ask) => {
    const have = context && context[id] && Array.isArray(context[id].lines) ? context[id].lines.length : 0;
    focusRow.current = id + ":c" + have;
    if (onContext) onContext(id, ask);
  };

  return html`<div class="dv" ref=${wrap}><section class=${"dv-file" + (shut ? " is-shut" : "")} data-path=${showControls(path)}>
    <p class="dv-live" role="status">${said}</p>
    <button type="button" class="dv-fh" aria-expanded=${shut ? "false" : "true"} aria-controls=${bodyId} onClick=${toggle}>
      <span class="dv-chev" aria-hidden="true">${shut ? "▸" : "▾"}</span>
      <span class="dv-status" data-status=${text(file.status)}>${statusWord(file.status)}</span>
      <span class="dv-path" id=${nameId} title=${showControls(path)}><bdi>${renamed ? html`<s>${pathText(from)}</s> ${"→ "}` : null}${pathText(path)}</bdi></span>
      ${generated ? html`<span class="dv-chip">generated</span>` : null}
      ${added || removed ? html`<span class="dv-counts"><span class="dv-added">${"+" + added.toLocaleString("en-US")}<span class="dv-sr"> added</span></span> <span class="dv-removed">${"−" + removed.toLocaleString("en-US")}<span class="dv-sr"> removed</span></span></span>` : null}
    </button>
    <div class="dv-body" id=${bodyId} role="group" aria-label=${"Diff of " + showControls(path) + (branch ? " on " + showControls(branch) : "")} hidden=${shut}>
      ${onComment || at.has("file")
        ? html`<div class="dv-filecm">
            ${onComment ? html`<button type="button" class="drawer-btn dv-filecm-btn" onClick=${() => onComment({ file: path, side: "new", line: 0 })}>Comment on this file</button>` : null}
            <${Inline} keys=${["file"]} path=${path} at=${at} flow=${flow} headSha=${headSha} nameOf=${nameOf} cols=${1} wrap=${true} readOnly=${readOnly} />
          </div>`
        : null}
      ${unplaced.length
        ? html`<div class="dv-cm dv-cm--unplaced">
            <p class="dv-note">${plural(unplaced.length, "comment is", "comments are") + " on lines not shown here."}</p>
            <${Thread} comments=${unplaced} headSha=${headSha} errors=${flow ? flow.errors : NO_CONTEXT} closing=${flow ? flow.closing : NO_CONTEXT} nameOf=${nameOf} showWhere=${true} label="Comments on lines not shown"
              onResolve=${flow && !readOnly ? flow.close : null} onReopen=${flow && !readOnly ? flow.reopen : null} />
          </div>`
        : null}
      ${renamed ? html`<p class="dv-note">Renamed from <span class="dv-mono">${pathText(from)}</span>. Only the changed lines are shown.</p>` : null}
      ${note}
      ${rows.length
        ? html`${onComment ? html`<button type="button" class="dv-skip drawer-btn" onClick=${() => endRef.current && endRef.current.focus()}>Skip this diff</button>` : null}
          <div class="dv-scroll"><table class=${"dv-tbl dv-tbl--" + lay} aria-label=${"Changed lines of " + showControls(path)} onKeyDown=${onKey}>
            ${lay === "split"
              ? html`<colgroup>${onComment ? html`<col class="dv-col-p" />` : null}<col class="dv-col-n" /><col />${onComment ? html`<col class="dv-col-p" />` : null}<col class="dv-col-n" /><col /></colgroup>`
              : html`<colgroup>${onComment ? html`<col class="dv-col-p" />` : null}<col class="dv-col-n" /><col class="dv-col-n" /><col class="dv-col-s" /><col /></colgroup>`}
            <tbody>${page.rows.map((r) => (r.type === "hunk"
              ? html`<${Hunk} key=${r.id} row=${r} cols=${cols} />`
              : r.type === "gap"
                ? html`<${Gap} key=${r.id} row=${r} cols=${cols} onContext=${onContext ? askContext : null} error=${contextErrors[r.id] || ""} />`
                : lay === "split"
                  ? [html`<${SplitRow} key=${r.id} row=${r} uid=${uid} file=${path} notes=${notes} onComment=${onComment} active=${stop} onActive=${setActive} />`,
                     html`<${Inline} key=${r.id + ":cm"} keys=${[r.left && r.left !== r.right ? lineKey(r.left.anchor.side, r.left.anchor.line) : "", r.right ? lineKey(r.right.anchor.side, r.right.anchor.line) : ""].filter(Boolean)} path=${path} at=${at} flow=${flow} headSha=${headSha} nameOf=${nameOf} cols=${cols} readOnly=${readOnly} />`]
                  : [html`<${UnifiedRow} key=${r.id} row=${r} uid=${uid} file=${path} notes=${notes} onComment=${onComment} active=${stop} onActive=${setActive} />`,
                     html`<${Inline} key=${r.id + ":cm"} keys=${[lineKey(r.anchor.side, r.anchor.line)]} path=${path} at=${at} flow=${flow} headSha=${headSha} nameOf=${nameOf} cols=${cols} readOnly=${readOnly} />`]))}</tbody>
          </table></div>
          <p ref=${endRef} tabindex="-1" class="dv-end">${"End of the diff of " + showControls(path) + "."}</p>`
        : null}
      ${page.hidden > 0
        ? html`<p class="dv-note">${"Long diff: " + page.rows.length.toLocaleString("en-US") + " rows shown. "}<button type="button" class="drawer-btn" onClick=${more}>${"Show " + Math.min(PAGE_ROWS, page.hidden).toLocaleString("en-US") + " more rows"}</button></p>`
        : null}
      ${flags && flags.truncated && page.hidden === 0
        ? html`<p class="dv-note dv-cut">${"The desk cut this diff"
          + (flags.truncated.total != null ? " at " + flags.truncated.kept.toLocaleString("en-US") + " of " + flags.truncated.total.toLocaleString("en-US") + " lines" : "")
          + ". The rest is not shown."}</p>`
        : null}
    </div>
  </section></div>`;
}
