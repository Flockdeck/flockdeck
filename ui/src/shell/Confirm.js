// An inline question before something is stopped. It is not modal: it sits where
// the action was and takes the keyboard on Cancel, so a stray Enter does not
// confirm. Escape cancels. The sentences in it are written by the page and never
// taken from an agent, a task or a person.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";

/** Confirm asks title, lists words as sentences, and has a button each for going
 *  on (label) and for backing out. A word that is { name, cmd } is a command to be
 *  run: it is drawn whole, in a box of its own that wraps and scrolls, never cut. */
export function Confirm({ title, words = [], label, onConfirm, onCancel, disabled = false, note = "" }) {
  const cancel = useRef(null);
  useEffect(() => {
    const el = cancel.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    if (el && el.focus) el.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);
  const onKeyDown = (e) => {
    if (composing(e) || e.key !== LIST.cancel) return;
    e.preventDefault();
    e.stopPropagation();
    onCancel();
  };
  return html`
    <div class="confirm" role="alertdialog" aria-label=${title} onKeyDown=${onKeyDown}>
      <p class="confirm-title">${title}</p>
      ${words.map((w, i) => (w && typeof w === "object"
        ? html`<p class="confirm-line" key=${i}>${w.name ? html`<span class="confirm-name">${w.name}</span>` : null}<code class="confirm-cmd">${w.cmd}</code></p>`
        : html`<p class="confirm-line" key=${i}>${w}</p>`))}
      ${note ? html`<p class="confirm-line set-error" role="alert">${note}</p>` : null}
      <div class="confirm-foot">
        <button type="button" class="dialog-btn dialog-btn--danger" disabled=${disabled} onClick=${onConfirm}>${label}</button>
        <button type="button" class="dialog-btn" ref=${cancel} onClick=${onCancel}>Cancel</button>
      </div>
    </div>
  `;
}
