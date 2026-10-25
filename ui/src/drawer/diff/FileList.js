// The list of a branch's changed files, drawn from the listing the desk sends with
// `changes` (docs/changes-api.md, section 3). It draws and reports a pick; it asks
// for nothing.
//
// - files: [{ path, oldPath, status ("A" "M" "D" "R" "C" "U"), added, removed,
//   binary, generated, conflictWith }], in the order to draw them.
// - selected: the path picked, or "". onPick(path) is called with the path.
// - comments: the review comments, [{ path, state }] (state "open", "fixed",
//   "resolved" or "dismissed", as the desk sends it); a file with open ones says how many.
//
// Keyboard: the list is one tab stop. The picked file (or the first) is in the tab
// order; the arrow keys, Home and End move between files, and Enter or Space picks.
// A status line that stays in the page says how many files are drawn, and when more
// are drawn the keyboard goes to the first new file.
// Every path and name is drawn as text, with characters that reorder text made
// visible. The status is a word as well as a colour, and the picked row is marked
// with aria-current, not by colour alone.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";
import { showControls } from "./diffModel.js";

export const LIST_FIRST = 200;
export const LIST_MORE = 500;

const WORDS = { A: "Added", M: "Modified", D: "Deleted", R: "Renamed", C: "Copied", U: "Conflict" };
const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const count = (v) => (typeof v === "number" && Number.isFinite(v) && v > 0 ? Math.floor(v) : 0);
const NONE = [];

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

/** pathText is a path as drawn: cut, and with the characters that reorder text shown. */
export const pathText = (p, max = 300) => showControls(clip(text(p), max));

/** Files draws the list. Rows come LIST_FIRST at a time, then LIST_MORE. */
export function FileList({ files = NONE, selected = "", onPick = null, comments = NONE }) {
  const [shown, setShown] = useState(LIST_FIRST);
  const [said, setSaid] = useState("");
  const ul = useRef(null);
  const focusAt = useRef(-1);
  const list = Array.isArray(files) ? files.filter((f) => f && typeof f.path === "string") : NONE;
  const open = openByPath(comments);
  const drawn = list.slice(0, shown);
  const left = list.length - drawn.length;
  const stop = Math.max(0, drawn.findIndex((f) => f.path === selected));
  const status = list.length ? (left > 0 ? "Showing " + drawn.length.toLocaleString("en-US") + " of " + list.length.toLocaleString("en-US") + " files." : list.length.toLocaleString("en-US") + (list.length === 1 ? " file." : " files.")) : "No files changed.";
  // The status line is in the page empty first and filled after, so a reader announces the change.
  useEffect(() => { setSaid(status); }, [status]);
  useEffect(() => {
    if (focusAt.current < 0 || !ul.current) return;
    const btn = ul.current.querySelectorAll(".dv-filebtn")[focusAt.current];
    if (btn) {
      btn.focus();
      focusAt.current = -1;
    }
  }, [drawn.length]);
  const onKey = (e) => {
    const k = e.key;
    if (!ul.current || !["ArrowDown", "ArrowUp", "Home", "End"].includes(k)) return;
    const btns = Array.from(ul.current.querySelectorAll(".dv-filebtn"));
    const at = btns.indexOf(e.target);
    if (at < 0) return;
    const to = k === "Home" ? 0 : k === "End" ? btns.length - 1 : Math.max(0, Math.min(btns.length - 1, at + (k === "ArrowDown" ? 1 : -1)));
    e.preventDefault();
    btns[to].focus();
  };
  return html`<div class="dv dv-files">
    <p class="dv-live" role="status">${said}</p>
    ${list.length
      ? html`<ul class="dv-filelist" ref=${ul} aria-label=${"Changed files, " + list.length.toLocaleString("en-US")} onKeyDown=${onKey}>
          ${drawn.map((f, i) => {
            const n = open.get(f.path) || 0;
            const added = count(f.added);
            const removed = count(f.removed);
            return html`<li key=${f.path} class="dv-fileitem">
              <button type="button" tabindex=${i === stop ? 0 : -1} class=${"dv-filebtn" + (selected === f.path ? " is-selected" : "")} aria-current=${selected === f.path ? "true" : null} onClick=${() => onPick && onPick(f.path)}>
                <span class="dv-status" data-status=${text(f.status)}>${statusWord(f.status)}</span>
                <span class="dv-path" title=${showControls(text(f.path))}><bdi>${f.oldPath && f.oldPath !== f.path ? html`<s>${pathText(f.oldPath)}</s> ${"→ "}` : null}${pathText(f.path)}</bdi></span>
                ${f.binary ? html`<span class="dv-chip">binary</span>` : null}
                ${f.generated ? html`<span class="dv-chip">generated</span>` : null}
                ${f.conflictWith ? html`<span class="dv-chip dv-chip--warn">also changed elsewhere</span>` : null}
                ${n ? html`<span class="dv-chip dv-chip--note">${n + (n === 1 ? " open comment" : " open comments")}</span>` : null}
                ${added || removed ? html`<span class="dv-counts"><span class="dv-added">${"+" + added.toLocaleString("en-US")}<span class="dv-sr"> added</span></span> <span class="dv-removed">${"−" + removed.toLocaleString("en-US")}<span class="dv-sr"> removed</span></span></span>` : null}
              </button>
            </li>`;
          })}
        </ul>`
      : html`<p class="dv-empty">No files changed.</p>`}
    ${left > 0
      ? html`<button type="button" class="drawer-btn dv-more" onClick=${() => { focusAt.current = drawn.length; setShown((s) => s + LIST_MORE); }}>${"Show " + Math.min(LIST_MORE, left).toLocaleString("en-US") + " more files (" + left.toLocaleString("en-US") + " not drawn yet)"}</button>`
      : null}
  </div>`;
}
