// Review comments, drawn: the composer, a thread of comments at one place, and the
// comment drawer, the list of all of a branch's comments. They draw what they are
// given and call back; the state is commentFlow.js's.
//
// - Composer { target, to, draft, error, busy, onDraft, onSend, onCancel }: a box
//   named for the place it is on, where the comment goes ("to api, the owner of the
//   file", from `to`), Send and Cancel, a counter near the limit, Ctrl or Command
//   with Enter to send, Escape to cancel. The box takes the keyboard when it
//   opens. The error line is one live region that stays in the page.
// - Thread { comments, headSha, errors, nameOf, onResolve, onReopen }: each comment
//   with who wrote it and to whom, how far it got ("Sending…", "Delivered", "Read"),
//   its state (open, fixed, resolved, dismissed), "outdated" when the branch has
//   moved on since it was written, and Resolve or Reopen.
// - CommentDrawer { comments, headSha, nameOf, onJump, onResolve, onReopen }: open
//   comments first, then done ones; a comment's place is a button that calls
//   onJump(comment).
// All text is the desk's or an agent's, and is drawn as text.
import { useEffect, useRef } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";
import { composing } from "../../shell/keys.js";
import { MAX_COMMENT, DELIVERY_WORDS, STATE_WORDS, deliveryOf, stateOf, isOutdated, forDrawer, whereWords } from "./commentModel.js";

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));
const WARN_AT = MAX_COMMENT - 200;

/** Composer draws the box for a new comment. */
export function Composer({ target, to = "", draft = "", error = "", busy = false, onDraft = null, onSend = null, onCancel = null }) {
  const box = useRef(null);
  useEffect(() => {
    if (box.current && box.current.focus) box.current.focus();
  }, [target ? target.file + ":" + target.side + ":" + target.line : ""]);
  if (!target) return null;
  const place = target.line ? (target.side === "old" ? "old line " : "line ") + target.line + " of " : "";
  const label = "Comment on " + place + target.file;
  const n = Array.from(draft).length;
  const onKey = (e) => {
    if (composing(e)) return;
    if (e.key === "Escape") {
      e.stopPropagation();
      if (onCancel) onCancel();
    } else if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      if (onSend && !busy) onSend();
    }
  };
  return html`<div class="dv-composer" role="group" aria-label=${label}>
    <textarea ref=${box} class="dv-composer-text" rows="3" aria-label=${label} placeholder=${to ? "Say what should change. It goes to " + clip(text(to), 40) + " as a message." : "Say what should change. It goes to the owner of the file as a message."}
      value=${draft} disabled=${busy} onInput=${(e) => onDraft && onDraft(e.target.value)} onKeyDown=${onKey}></textarea>
    <div class="dv-composer-row">
      <button type="button" class="drawer-btn dv-send" disabled=${busy} onClick=${() => onSend && onSend()}>${busy ? "Sending…" : "Send"}</button>
      <button type="button" class="drawer-btn dv-cancel" onClick=${() => onCancel && onCancel()}>Cancel</button>
      ${n >= WARN_AT ? html`<span class="dv-count">${n.toLocaleString("en-US") + " of " + MAX_COMMENT.toLocaleString("en-US")}</span>` : null}
    </div>
    <p class="dv-composer-error" role="status">${error}</p>
  </div>`;
}

function Card({ c, headSha, error, nameOf, onResolve, onReopen }) {
  const state = stateOf(c);
  const delivery = deliveryOf(c);
  const who = c.author === "you" ? "You" : clip(text(nameOf ? nameOf(c.author) : c.author), 40);
  const outdated = isOutdated(c, headSha);
  const done = state !== "open";
  const canAct = !c.pending && !!c.id;
  return html`<li class=${"dv-cmt" + (done ? " is-done" : "")} data-state=${state}>
    <p class="dv-cmt-text">${clip(text(c.text), MAX_COMMENT)}</p>
    <p class="dv-cmt-meta">
      <span>${who}${c.to ? " to " + clip(text(nameOf ? nameOf(c.to) : c.to), 40) : ""}</span>
      ${c.author === "you" ? html`<span class=${"dv-chip dv-delivery dv-delivery--" + delivery}>${DELIVERY_WORDS[delivery]}</span>` : null}
      <span class=${"dv-chip dv-state dv-state--" + state}>${STATE_WORDS[state]}</span>
      ${outdated ? html`<span class="dv-chip dv-chip--warn" title="The branch has new commits since this was written">outdated</span>` : null}
      ${canAct && state === "open" ? html`<button type="button" class="drawer-btn dv-resolve" aria-label=${"Resolve the comment on " + whereWords(c)} onClick=${() => onResolve && onResolve(c.id)}>Resolve</button>` : null}
      ${canAct && (state === "resolved" || state === "dismissed" || state === "fixed") ? html`<button type="button" class="drawer-btn dv-reopen" aria-label=${"Reopen the comment on " + whereWords(c)} onClick=${() => onReopen && onReopen(c.id)}>Reopen</button>` : null}
    </p>
    ${error ? html`<p class="dv-cmt-error" role="alert">${error}</p>` : null}
  </li>`;
}

/** Thread draws the comments at one place. */
export function Thread({ comments = [], headSha = "", errors = {}, nameOf = null, onResolve = null, onReopen = null, label = "Comments" }) {
  const list = (Array.isArray(comments) ? comments : []).filter((c) => c && typeof c === "object");
  if (!list.length) return null;
  return html`<ul class="dv-thread" aria-label=${label}>${list.map((c) => html`<${Card} key=${c.id || c.createdAt} c=${c} headSha=${headSha} error=${errors[c.id] || ""} nameOf=${nameOf} onResolve=${onResolve} onReopen=${onReopen} />`)}</ul>`;
}

/** CommentDrawer draws all of a branch's comments, open ones first. */
export function CommentDrawer({ comments = [], headSha = "", errors = {}, nameOf = null, onJump = null, onResolve = null, onReopen = null }) {
  const { open, done } = forDrawer(comments);
  if (!open.length && !done.length) return html`<p class="dv-empty">No review comments on this branch.</p>`;
  const section = (title, items) => (items.length
    ? html`<section class="dv-cd-section" aria-label=${title + ", " + items.length}>
        <h4 class="dv-cd-title">${title + " (" + items.length + ")"}</h4>
        <ul class="dv-cd-list">${items.map((c) => html`<li key=${c.id} class="dv-cd-item">
          <button type="button" class="dv-cd-where" onClick=${() => onJump && onJump(c)}>${clip(whereWords(c), 200)}</button>
          <ul class="dv-thread"><${Card} c=${c} headSha=${headSha} error=${errors[c.id] || ""} nameOf=${nameOf} onResolve=${onResolve} onReopen=${onReopen} /></ul>
        </li>`)}</ul>
      </section>`
    : null);
  return html`<div class="dv-cd">${section("Open", open)}${section("Done", done)}</div>`;
}
