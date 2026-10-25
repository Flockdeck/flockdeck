// The list of a branch's changed files, drawn from the listing the desk sends with
// `changes` (docs/changes-api.md, section 3). It draws and reports a pick; it asks
// for nothing.
//
// - files: [{ path, oldPath, status ("A" "M" "D" "R" "C" "U"), added, removed,
//   binary, generated, conflictWith }], in the order to draw them.
// - selected: the path picked, or "". onPick(path) is called with the path.
// - comments: the review comments, [{ path, state }] (state "open", "fixed",
//   "resolved" or "dismissed", as the desk sends it); a file with open ones says how many.
// Every path and name is drawn as text. The status is a word as well as a colour,
// and the picked row is marked with aria-current, not by colour alone.
import { useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";

export const LIST_FIRST = 200;
export const LIST_MORE = 500;

const WORDS = { A: "Added", M: "Modified", D: "Deleted", R: "Renamed", C: "Copied", U: "Conflict" };
const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const count = (v) => (typeof v === "number" && Number.isFinite(v) && v > 0 ? Math.floor(v) : 0);

/** statusWord is the word for a status letter, "Changed" for one this window does not know. */
export function statusWord(status) {
  return WORDS[text(status)] || "Changed";
}

/** isOpenComment says whether a comment is still open: its state is "open", or the desk sent none. */
export function isOpenComment(c) {
  return !!c && typeof c === "object" && (c.state === undefined || c.state === "open");
}

/** openByPath counts the comments that are still open, by file path. */
export function openByPath(comments) {
  const out = new Map();
  for (const c of Array.isArray(comments) ? comments : []) {
    if (!isOpenComment(c) || typeof c.path !== "string") continue;
    out.set(c.path, (out.get(c.path) || 0) + 1);
  }
  return out;
}

/** Files draws the list. Rows come LIST_FIRST at a time, then LIST_MORE. */
export function FileList({ files = [], selected = "", onPick = null, comments = [] }) {
  const [shown, setShown] = useState(LIST_FIRST);
  const list = Array.isArray(files) ? files.filter((f) => f && typeof f.path === "string") : [];
  const open = openByPath(comments);
  const drawn = list.slice(0, shown);
  const left = list.length - drawn.length;
  if (!list.length) return html`<p class="dv-empty">No files changed.</p>`;
  return html`<div class="dv-files">
    <ul class="dv-filelist" aria-label=${"Changed files, " + list.length.toLocaleString("en-US")}>
      ${drawn.map((f) => {
        const n = open.get(f.path) || 0;
        const added = count(f.added);
        const removed = count(f.removed);
        return html`<li key=${f.path} class="dv-fileitem">
          <button type="button" class=${"dv-filebtn" + (selected === f.path ? " is-selected" : "")} aria-current=${selected === f.path ? "true" : null} onClick=${() => onPick && onPick(f.path)}>
            <span class="dv-status" data-status=${text(f.status)}>${statusWord(f.status)}</span>
            <span class="dv-path" title=${text(f.path)}>${f.oldPath && f.oldPath !== f.path ? html`<s>${clip(text(f.oldPath), 300)}</s> ${"→ "}` : null}${clip(text(f.path), 300)}</span>
            ${f.binary ? html`<span class="dv-chip">binary</span>` : null}
            ${f.generated ? html`<span class="dv-chip">generated</span>` : null}
            ${f.conflictWith ? html`<span class="dv-chip dv-chip--warn">also changed elsewhere</span>` : null}
            ${n ? html`<span class="dv-chip dv-chip--note">${n + (n === 1 ? " open comment" : " open comments")}</span>` : null}
            ${added || removed ? html`<span class="dv-counts"><span class="dv-added">${"+" + added.toLocaleString("en-US")}<span class="dv-sr"> added</span></span> <span class="dv-removed">${"−" + removed.toLocaleString("en-US")}<span class="dv-sr"> removed</span></span></span>` : null}
          </button>
        </li>`;
      })}
    </ul>
    ${left > 0
      ? html`<button type="button" class="drawer-btn dv-more" onClick=${() => setShown((s) => s + LIST_MORE)}>${"Show " + Math.min(LIST_MORE, left).toLocaleString("en-US") + " more files (" + left.toLocaleString("en-US") + " not drawn yet)"}</button>`
      : null}
  </div>`;
}
