// One file's diff, drawn from a `diffHunks` reply (docs/changes-api.md, section 4)
// through the model of diffModel.js. It draws and reports; it asks for nothing and
// keeps nothing the page should keep.
//
// Props:
//   branch: the branch name, for labels.
//   file: the listing entry { path, oldPath, status, added, removed, generated }.
//   hunks: the diffHunks reply for the file, or null while it is being read. An
//     `error` in it is drawn in place of the diff.
//   layout: "unified" or "split"; narrow says the container is too narrow for
//     split, and the diff is drawn unified then (effectiveLayout).
//   comments: [{ path, side, line, state }]; a line with open ones is marked.
//   onComment({ file, side, line }): called by the "+" of a line. With none there
//     is no "+": the diff is read-only.
//   context: the unchanged lines fetched so far, { [gap id]: { lines, eof } }, kept
//     by the caller, which adds each `diffContext` reply to it.
//   onContext(gapId, ask): called by a "Show N unchanged lines" button, with the
//     diffContext request to make ({ side, from, to }).
//   collapsed, onToggle: the file's open state when the caller keeps it; without
//     them the file keeps its own, shut at first for a generated file.
//
// Everything in the diff is source text, so it is drawn as text and never as markup.
// The diff is a region with a name; the buttons have names; the "+" is reachable
// by keyboard (it shows on focus, as on hover); the status line is one live region
// that stays in the page, so a change of what it says is announced.
import { useMemo, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";
import { statusWord, isOpenComment } from "./FileList.js";
import { unifiedRows, sideRows, pageRows, segments, fileFlags, PAGE_ROWS } from "./diffModel.js";

/** effectiveLayout is the layout to draw: split only when asked for and there is room. */
export function effectiveLayout(layout, narrow) {
  return layout === "split" && !narrow ? "split" : "unified";
}

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const plural = (n, one, many) => n.toLocaleString("en-US") + " " + (n === 1 ? one : many);
const SIGN = { add: "+", del: "−", ctx: " " };
const SIGN_WORD = { add: "added: ", del: "removed: ", ctx: "" };

/** sizeWords is a byte count as words. */
export function sizeWords(n) {
  if (typeof n !== "number" || !Number.isFinite(n)) return "unknown size";
  if (n < 1024) return n + " bytes";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
  return (n / (1024 * 1024)).toFixed(1) + " MiB";
}

function Code({ line }) {
  return html`${segments(line.text, line.marks).map((s, i) => (s.mark ? html`<mark key=${i}>${s.text}</mark>` : s.text))}`;
}

function Plus({ file, line, side, notes, onComment }) {
  if (!onComment || typeof line !== "number") return null;
  const n = notes.get(side + ":" + line) || 0;
  const where = (side === "old" ? "old line " : "line ") + line;
  return html`<button type="button" class="dv-plus" aria-label=${"Comment on " + where + (n ? ", " + plural(n, "open comment", "open comments") : "")} onClick=${() => onComment({ file, side, line })}>+</button>`;
}

function Gap({ row, cols, onContext }) {
  const label = row.count === null ? "Show the unchanged lines below" : "Show " + plural(row.count, "unchanged line", "unchanged lines");
  return html`<tr class="dv-gap"><td colspan=${cols}>
    <button type="button" class="dv-gapbtn" disabled=${!onContext} onClick=${() => onContext && onContext(row.id, row.ask)}>${label}</button>
  </td></tr>`;
}

function Hunk({ row, cols }) {
  const range = "@@ -" + row.oldStart + "," + row.oldLines + " +" + row.newStart + "," + row.newLines + " @@";
  return html`<tr class="dv-hunk"><td colspan=${cols}><span class="dv-range">${range}</span> ${clip(text(row.header), 300)}</td></tr>`;
}

function UnifiedRow({ row, file, notes, onComment }) {
  const mark = notes.get(row.anchor.side + ":" + row.anchor.line) || 0;
  return html`<tr class=${"dv-row dv-row--" + row.t + (mark ? " has-comment" : "")}>
    <td class="dv-n">${row.oldNo == null ? "" : row.oldNo}</td>
    <td class="dv-n">${row.newNo == null ? "" : row.newNo}<${Plus} file=${file} line=${row.anchor.line} side=${row.anchor.side} notes=${notes} onComment=${onComment} /></td>
    <td class="dv-sg"><span aria-hidden="true">${SIGN[row.t]}</span><span class="dv-sr">${SIGN_WORD[row.t]}</span></td>
    <td class="dv-c"><${Code} line=${row} /></td>
  </tr>`;
}

function Side({ line, which, file, notes, onComment, showPlus }) {
  if (!line) return html`<td class="dv-n dv-e"></td><td class="dv-c dv-e"></td>`;
  const no = which === "left" ? line.oldNo : line.newNo;
  return html`<td class=${"dv-n dv-" + line.t}>${no == null ? "" : no}${showPlus ? html`<${Plus} file=${file} line=${line.anchor.line} side=${line.anchor.side} notes=${notes} onComment=${onComment} />` : null}</td>
    <td class=${"dv-c dv-" + line.t}><span class="dv-sr">${SIGN_WORD[line.t]}</span><${Code} line=${line} /></td>`;
}

function SplitRow({ row, file, notes, onComment }) {
  const same = row.left === row.right;
  return html`<tr class="dv-row">
    <${Side} line=${row.left} which="left" file=${file} notes=${notes} onComment=${onComment} showPlus=${!same} />
    <${Side} line=${row.right} which="right" file=${file} notes=${notes} onComment=${onComment} showPlus=${true} />
  </tr>`;
}

/** DiffViewer draws one file: its header, which folds it, and its diff. */
export function DiffViewer({ branch = "", file = null, hunks = null, layout = "unified", narrow = false, comments = [], onComment = null, context = {}, onContext = null, collapsed = null, onToggle = null }) {
  const path = file ? text(file.path) : "";
  const flags = useMemo(() => (hunks ? fileFlags(hunks, file) : null), [hunks, file]);
  const [shutOwn, setShutOwn] = useState(null);
  const [pages, setPages] = useState(1);
  const lay = effectiveLayout(layout, narrow);
  const generated = !!(flags ? flags.generated : file && file.generated);
  const shut = collapsed !== null ? collapsed : shutOwn !== null ? shutOwn : generated;
  const toggle = () => (onToggle ? onToggle(!shut) : setShutOwn(!shut));
  const rows = useMemo(() => {
    if (!hunks || !Array.isArray(hunks.hunks) || !hunks.hunks.length) return [];
    const cut = !!(flags && flags.truncated);
    return lay === "split" ? sideRows(hunks.hunks, context, { cut }) : unifiedRows(hunks.hunks, context, { cut });
  }, [hunks, context, lay, flags]);
  const notes = useMemo(() => {
    const m = new Map();
    for (const c of Array.isArray(comments) ? comments : []) {
      if (!isOpenComment(c) || c.path !== path) continue;
      const k = c.side + ":" + c.line;
      m.set(k, (m.get(k) || 0) + 1);
    }
    return m;
  }, [comments, path]);
  if (!file) return null;
  const id = "dv-" + path.replace(/[^A-Za-z0-9_-]/g, "_");
  const page = pageRows(rows, pages);
  const cols = lay === "split" ? 4 : 4;
  const added = typeof file.added === "number" ? file.added : 0;
  const removed = typeof file.removed === "number" ? file.removed : 0;
  const renamed = flags ? flags.renamed : !!(file.oldPath && file.oldPath !== file.path);
  const from = flags && flags.from ? flags.from : text(file.oldPath);

  // What stands in place of, or beside, the table.
  let note = null;
  if (!hunks) note = html`<p class="dv-note">Reading the diff…</p>`;
  else if (flags.error) note = html`<p class="dv-note dv-note--err" role="alert">${"The diff could not be read: " + clip(flags.error, 300)}</p>`;
  else if (flags.binary) {
    note = html`<p class="dv-note dv-binary">${"Binary file changed (" + sizeWords(flags.sizeBefore) + " to " + sizeWords(flags.sizeAfter) + "). There is no text diff. A comment can still be left on the file."}</p>`;
  } else if (flags.eolOnly) note = html`<p class="dv-note">Only the line endings changed.</p>`;
  else if (flags.unknown) note = html`<p class="dv-note">The diff has lines, but none came back. Try again.</p>`;
  else if (flags.empty) note = html`<p class="dv-note">Nothing changed in this file.</p>`;
  else if (flags.renamed && !rows.length) note = html`<p class="dv-note">Renamed with no change to the lines.</p>`;
  else if (flags.modeChange && !rows.length) note = html`<p class="dv-note">Only the file mode changed.</p>`;

  return html`<section class=${"dv-file" + (shut ? " is-shut" : "")} data-path=${path}>
    <button type="button" class="dv-fh" aria-expanded=${shut ? "false" : "true"} aria-controls=${id} onClick=${toggle}>
      <span class="dv-chev" aria-hidden="true">${shut ? "▸" : "▾"}</span>
      <span class="dv-status" data-status=${text(file.status)}>${statusWord(file.status)}</span>
      <span class="dv-path" title=${path}>${renamed ? html`<s>${clip(from, 300)}</s> ${"→ "}` : null}${clip(path, 300)}</span>
      ${generated ? html`<span class="dv-chip">generated</span>` : null}
      ${added || removed ? html`<span class="dv-counts"><span class="dv-added">${"+" + added.toLocaleString("en-US")}<span class="dv-sr"> added</span></span> <span class="dv-removed">${"−" + removed.toLocaleString("en-US")}<span class="dv-sr"> removed</span></span></span>` : null}
    </button>
    <div class="dv-body" id=${id} role="region" aria-label=${"Diff of " + path + (branch ? " on " + branch : "")} hidden=${shut}>
      ${renamed ? html`<p class="dv-note">Renamed from <span class="dv-mono">${clip(from, 300)}</span>. Only the changed lines are shown.</p>` : null}
      ${note}
      ${rows.length
        ? html`<div class="dv-scroll"><table class=${"dv-tbl dv-tbl--" + lay} aria-label=${"Changed lines of " + path}>
            ${lay === "split"
              ? html`<colgroup><col class="dv-col-n" /><col /><col class="dv-col-n" /><col /></colgroup>`
              : html`<colgroup><col class="dv-col-n" /><col class="dv-col-n" /><col class="dv-col-s" /><col /></colgroup>`}
            <tbody>${page.rows.map((r) => (r.type === "hunk"
              ? html`<${Hunk} key=${r.id} row=${r} cols=${cols} />`
              : r.type === "gap"
                ? html`<${Gap} key=${r.id} row=${r} cols=${cols} onContext=${onContext} />`
                : lay === "split"
                  ? html`<${SplitRow} key=${r.id} row=${r} file=${path} notes=${notes} onComment=${onComment} />`
                  : html`<${UnifiedRow} key=${r.id} row=${r} file=${path} notes=${notes} onComment=${onComment} />`))}</tbody>
          </table></div>`
        : null}
      ${page.hidden > 0
        ? html`<p class="dv-note">${"Long diff: " + page.rows.length.toLocaleString("en-US") + " rows shown. "}<button type="button" class="drawer-btn" onClick=${() => setPages((n) => n + 1)}>${"Show " + Math.min(PAGE_ROWS, page.hidden).toLocaleString("en-US") + " more rows"}</button></p>`
        : null}
      ${flags && flags.truncated
        ? html`<p class="dv-note dv-cut">${"The desk cut this diff"
          + (flags.truncated.total != null ? " at " + flags.truncated.kept.toLocaleString("en-US") + " of " + flags.truncated.total.toLocaleString("en-US") + " lines" : "")
          + ". The rest is not shown."}</p>`
        : null}
    </div>
    <p class="dv-live" role="status">${shut ? (generated ? "Generated file, collapsed." : "") : ""}</p>
  </section>`;
}
