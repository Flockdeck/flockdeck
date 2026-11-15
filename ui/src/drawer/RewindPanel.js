// The panel behind /rewind and /fork of a structured pane. It lists the prompts
// the agent can go back to, with the prompt's start. "Go back to before this"
// asks once, saying what is removed, then sends the command with confirm; the
// prompt's text is put in the box. "Fork from before this" starts a new pane
// beside this one. Desk only. When the agent cannot go back it says so, and the
// whole conversation can still be forked if the agent forks at all.
//
// Files: when the push says a prompt has saved files, the confirm step offers
// "Show files to put back". That asks the desk what would change, lists each
// file and what happens to it, and "Go back and put these files back" sends the
// list the person saw. If the files changed in between, the desk refuses and the
// panel shows the new list to confirm again. Without saved files the step says why.
import { useState, useEffect } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { showControls } from "./diff/diffModel.js";
import { pointsOf, rewindOffered, canAct, rewindMessage, forkMessage, filesMessage, filesOffered, filesWhy, resultWords, REWIND_NOT_SENT_WORDS, REWIND_NOT_OFFERED_WORDS, REWIND_BUSY_WORDS, REWIND_NONE_WORDS, REWIND_CONFIRM_WORDS, FILES_NONE_WORDS, FILES_EMPTY_WORDS, FILES_SAFE_WORDS, FILES_ACTION_WORDS, FILES_BLOCKED_WORDS, FILES_FILTER_WORDS, FILES_NESTED_WORDS, hasFilterBlock } from "./rewindModel.js";

let rewindSeq = 0;

/** RewindPanel draws the list. result is the store's rewindResult; it is shown
 *  only when its req is the one this panel sent for this pane. onText gets the
 *  prompt's text after a successful rewind. */
export function RewindPanel({ pane, store, st, entries = [], result = null, onText = null, onClose }) {
  const [req, setReq] = useState("");
  const [asking, setAsking] = useState("");
  const [note, setNote] = useState("");
  const [handled, setHandled] = useState("");
  // The files the person was shown for the prompt in asking, or null.
  const [listing, setListing] = useState(null);
  const mine = result && req && result.req === req && result.id === pane ? resultWords(result) : null;
  useEffect(() => {
    if (mine && mine.ok && mine.text && handled !== req) {
      setHandled(req);
      if (onText) onText(mine.text);
    }
  }, [mine && mine.ok, req]);
  useEffect(() => {
    if (!mine) return;
    // The rewind went through: the confirm step is done.
    if (mine.ok && result.op === "structuredRewind") {
      setAsking("");
      setListing(null);
      return;
    }
    if (!asking || !Array.isArray(mine.files)) return;
    // A list for the prompt being asked about, or the new list of a refusal.
    if ((mine.ok && mine.files !== undefined && result.op === "structuredRewindFiles") || (!mine.ok && (mine.files.length || (mine.blocked && mine.blocked.length)))) setListing({ entry: asking, files: mine.files, blocked: mine.blocked || [], nested: mine.nested || [] });
  }, [req, mine && mine.ok]);
  const offered = rewindOffered(st);
  const idle = canAct(st);
  const points = offered ? pointsOf(st, entries) : [];
  const go = (msg, keepAsking = false) => {
    if (!msg) return;
    const next = "r" + ++rewindSeq;
    if (store.send({ ...msg, req: next })) {
      setReq(next);
      setNote("");
      if (!keepAsking) {
        setAsking("");
        setListing(null);
      }
    } else {
      setReq("");
      setNote(REWIND_NOT_SENT_WORDS);
    }
  };
  const waiting = req !== "" && !mine;
  const off = !idle || waiting;
  // The files part of the confirm step for the prompt id.
  const filesBlock = (id) => {
    if (!filesOffered(st, id)) return html`<span class="rewind-files-note" data-files="none">${filesWhy(st) || FILES_NONE_WORDS}</span>`;
    if (!listing || listing.entry !== id) {
      return html`<button type="button" class="drawer-btn rewind-files-show" disabled=${off} onClick=${() => go(filesMessage(pane, id, "", st.rewindToken), true)}>Show files to put back</button>`;
    }
    const nested = listing.nested && listing.nested.length ? html`<span class="rewind-files-note" data-files="nested">${FILES_NESTED_WORDS} ${listing.nested.map((p) => showControls(p)).join(", ")}</span>` : null;
    if (listing.blocked && listing.blocked.length) {
      return html`<span class="rewind-files" data-files="blocked"><span class="rewind-files-note">${FILES_BLOCKED_WORDS}</span>
        <ul class="rewind-file-list">${listing.blocked.map((b) => html`<li key=${b.path} class="rewind-file" data-action="blocked"><span class="rewind-file-path">${showControls(b.path)}</span> ${b.reason}</li>`)}</ul>${hasFilterBlock(listing.blocked) ? html`<span class="rewind-files-note" data-files="filter">${FILES_FILTER_WORDS}</span>` : null}${nested}</span>`;
    }
    if (listing.files.length === 0) return html`<span class="rewind-files" data-files="empty"><span class="rewind-files-note">${FILES_EMPTY_WORDS}</span>${nested}</span>`;
    return html`<span class="rewind-files" data-files="list">
      <ul class="rewind-file-list">${listing.files.map((f) => html`<li key=${f.path} class="rewind-file" data-action=${f.action}><span class="rewind-file-path">${showControls(f.path)}</span> ${FILES_ACTION_WORDS[f.action]}</li>`)}</ul>
      ${nested}
      <span class="rewind-files-note">${FILES_SAFE_WORDS}</span>
      <button type="button" class="drawer-btn rewind-yes-files" disabled=${off} onClick=${() => go(rewindMessage(pane, id, "", st.rewindToken, listing.files), true)}>Go back and put these files back</button>
    </span>`;
  };
  return html`<div class="rewind-panel" role="group" aria-label="Go back or fork">
    ${!offered ? html`<p class="structured-note" role="status">${REWIND_NOT_OFFERED_WORDS}</p>` : null}
    ${offered && !idle ? html`<p class="structured-note" role="status">${REWIND_BUSY_WORDS}</p>` : null}
    ${offered && points.length === 0 ? html`<p class="structured-note" role="status">${REWIND_NONE_WORDS}</p>` : null}
    ${points.length ? html`<ol class="rewind-points">${points.map((p) => html`<li key=${p.id} class="rewind-point" data-entry=${p.id}>
      <span class="rewind-text">${showControls(p.text)}</span>
      ${asking === p.id
        ? html`<span class="rewind-confirm"><span class="rewind-warn">Going back ${REWIND_CONFIRM_WORDS}</span>
            ${filesBlock(p.id)}
            <button type="button" class="drawer-btn rewind-yes" disabled=${off} onClick=${() => go(rewindMessage(pane, p.id, "", st.rewindToken))}>${listing && listing.entry === p.id ? "Go back, leave the files" : "Go back"}</button>
            <button type="button" class="drawer-btn rewind-no" onClick=${() => { setAsking(""); setListing(null); }}>Keep</button></span>`
        : html`<span class="rewind-actions">
            <button type="button" class="drawer-btn rewind-back" disabled=${off} onClick=${() => setAsking(p.id)}>Go back to before this</button>
            <button type="button" class="drawer-btn rewind-fork" disabled=${off} onClick=${() => go(forkMessage(pane, p.id, "", st.rewindToken))}>Fork from before this</button></span>`}
    </li>`)}</ol>` : null}
    <div class="rewind-actions">
      ${offered ? html`<button type="button" class="drawer-btn rewind-fork-all" disabled=${off} onClick=${() => go(forkMessage(pane, "", "", st.rewindToken))}>Fork the whole conversation</button>` : null}
      <button type="button" class="drawer-btn rewind-close" onClick=${onClose}>Close</button>
    </div>
    ${note ? html`<p class="structured-note structured-note--unsent" role="status">${note}</p>` : null}
    ${waiting ? html`<p class="structured-note" role="status">Working on it.</p>` : null}
    ${mine && mine.words ? html`<p class=${"structured-note" + (mine.ok ? "" : " structured-note--unsent")} role="status" data-rewind=${mine.ok ? "ok" : "failed"}>${showControls(mine.words)}</p>` : null}
  </div>`;
}
