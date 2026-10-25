// The notices drawn in the page when an agent newly needs the person while this
// window is in front, where the browser's own notification would not be raised.
// Each says who needs the person and why, with Open (the agent's drawer) and
// Dismiss. A toast goes when the agent no longer waits, answered here or
// elsewhere, so none outlives what it says. At most MAX_TOASTS are drawn, the
// newest last.
//
// Names and reasons came from agents and are drawn as text.
import { html } from "../html.js";
import { clip } from "../graph/Node.js";

export const MAX_TOASTS = 3;

/** toastOf is the toast for one pane that just started waiting (a waitsOf entry). */
export function toastOf(f) {
  return { id: f.id, name: f.name, text: f.blocked ? "a tool call was blocked" : "waiting for input" };
}

/** addToasts adds the toasts for fresh to the current ones. A pane has at most one, the open pane's is left out, and only the newest MAX_TOASTS stay. */
export function addToasts(current, fresh, openPane = "") {
  const out = current.slice();
  for (const f of fresh) {
    if (f.id === openPane) continue;
    const at = out.findIndex((t) => t.id === f.id);
    if (at >= 0) out.splice(at, 1);
    out.push(toastOf(f));
  }
  return out.slice(-MAX_TOASTS);
}

/** keepWaiting drops the toasts of panes that no longer wait. waiting is a Set of pane ids. */
export function keepWaiting(current, waiting) {
  const out = current.filter((t) => waiting.has(t.id));
  return out.length === current.length ? current : out;
}

export function Toasts({ toasts, onOpen, onDismiss }) {
  if (!toasts || toasts.length === 0) return null;
  return html`
    <div class="toasts" data-keep-live role="region" aria-label="Agents that need you">
      ${toasts.map((t) => html`
        <div class="toast" role="status" key=${t.id} data-pane=${t.id}>
          <span class="toast-text"><b>${clip(t.name, 40)}</b> needs you: ${t.text}</span>
          <button type="button" class="toast-btn" onClick=${() => onOpen(t.id)}>Open</button>
          <button type="button" class="toast-btn toast-btn--quiet" onClick=${() => onDismiss(t.id)}>Dismiss</button>
        </div>
      `)}
    </div>
  `;
}
