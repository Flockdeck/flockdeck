// The panel behind /rewind and /fork of a structured pane. It lists the prompts
// the agent can go back to, with the prompt's start. "Go back to before this"
// asks once, saying what is removed, then sends the command with confirm; the
// prompt's text is put in the box. "Fork from before this" starts a new pane
// beside this one. Desk only. When the agent cannot go back it says so, and the
// whole conversation can still be forked if the agent forks at all.
import { useState, useEffect } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { showControls } from "./diff/diffModel.js";
import { pointsOf, rewindOffered, canAct, rewindMessage, forkMessage, resultWords, REWIND_NOT_SENT_WORDS, REWIND_NOT_OFFERED_WORDS, REWIND_BUSY_WORDS, REWIND_NONE_WORDS, REWIND_CONFIRM_WORDS } from "./rewindModel.js";

let rewindSeq = 0;

/** RewindPanel draws the list. result is the store's rewindResult; it is shown
 *  only when its req is the one this panel sent for this pane. onText gets the
 *  prompt's text after a successful rewind. */
export function RewindPanel({ pane, store, st, entries = [], result = null, onText = null, onClose }) {
  const [req, setReq] = useState("");
  const [asking, setAsking] = useState("");
  const [note, setNote] = useState("");
  const [handled, setHandled] = useState("");
  const mine = result && req && result.req === req && result.id === pane ? resultWords(result) : null;
  useEffect(() => {
    if (mine && mine.ok && mine.text && handled !== req) {
      setHandled(req);
      if (onText) onText(mine.text);
    }
  }, [mine && mine.ok, req]);
  const offered = rewindOffered(st);
  const idle = canAct(st);
  const points = offered ? pointsOf(st, entries) : [];
  const go = (msg) => {
    if (!msg) return;
    const next = "r" + ++rewindSeq;
    if (store.send({ ...msg, req: next })) {
      setReq(next);
      setNote("");
      setAsking("");
    } else {
      setReq("");
      setNote(REWIND_NOT_SENT_WORDS);
    }
  };
  const waiting = req !== "" && !mine;
  const off = !idle || waiting;
  return html`<div class="rewind-panel" role="group" aria-label="Go back or fork">
    ${!offered ? html`<p class="structured-note" role="status">${REWIND_NOT_OFFERED_WORDS}</p>` : null}
    ${offered && !idle ? html`<p class="structured-note" role="status">${REWIND_BUSY_WORDS}</p>` : null}
    ${offered && points.length === 0 ? html`<p class="structured-note" role="status">${REWIND_NONE_WORDS}</p>` : null}
    ${points.length ? html`<ol class="rewind-points">${points.map((p) => html`<li key=${p.id} class="rewind-point" data-entry=${p.id}>
      <span class="rewind-text">${showControls(p.text)}</span>
      ${asking === p.id
        ? html`<span class="rewind-confirm"><span class="rewind-warn">Going back ${REWIND_CONFIRM_WORDS}</span>
            <button type="button" class="drawer-btn rewind-yes" disabled=${off} onClick=${() => go(rewindMessage(pane, p.id, "", st.rewindToken))}>Go back</button>
            <button type="button" class="drawer-btn rewind-no" onClick=${() => setAsking("")}>Keep</button></span>`
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
    ${mine ? html`<p class=${"structured-note" + (mine.ok ? "" : " structured-note--unsent")} role="status" data-rewind=${mine.ok ? "ok" : "failed"}>${showControls(mine.words)}</p>` : null}
  </div>`;
}
