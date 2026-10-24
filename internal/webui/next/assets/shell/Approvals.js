// The requests the desktop is waiting on: a baton that would leave its provider,
// asked for by an agent or a window reached through the relay. Each is a card
// with the desktop's words and one button that allows it. Nothing is allowed by
// anything but pressing that button: the card takes the keyboard from nobody, a
// request the desktop withdraws (answered elsewhere, timed out) goes away, and
// the text is the desktop's own, drawn as text.
//
// The button sends approveBaton with the token the desktop made for this window
// and this request. Only that command is ever sent from here, whatever a notice
// says its button does (store.js approvalOf).
import { html } from "../html.js";

/** approveCommand is what the button sends. */
export const approveCommand = (a) => ({ cmd: "approveBaton", text: a.token });

/** Approvals draws the list. approvals is the store's list, send the store's send,
 *  and dismissed the ids put away in this window (the request still stands on the
 *  desktop until it ends; putting it away is not a refusal). */
export function Approvals({ approvals, dismissed, send, onDismiss }) {
  const shown = approvals.filter((a) => !dismissed.has(a.id));
  if (shown.length === 0) return null;
  return html`
    <div class="approvals" role="region" aria-label="Requests waiting for you">
      ${shown.map((a) => html`
        <section class="approval" key=${a.id} role="alert">
          <p class="approval-text">${a.lead ? html`<strong>${a.lead}</strong> ` : null}${a.text}</p>
          <div class="approval-foot">
            <button type="button" class="dialog-btn dialog-btn--primary approval-yes" onClick=${() => { send(approveCommand(a)); onDismiss(a.id); }}>${a.label}</button>
            <button type="button" class="dialog-btn approval-no" onClick=${() => onDismiss(a.id)}>Not now</button>
          </div>
        </section>`)}
    </div>
  `;
}
