// What the drawer offers to do with the pane it shows: restart it, lock it so it
// cannot be closed, and close it. The desk does each, and a refusal (a locked
// pane cannot be closed) arrives as a notice, so this sends a command and does
// not guess at the result. Zoom is the drawer's Maximise button, which stays in
// the header.
//
// Closing and restarting stop an agent, so each asks first when there is
// something worth saying: the agent is waiting for an answer, its checkout has
// uncommitted changes, or the agent is structured and starts a new session. The
// sentences are written in lifecycle.js and never contain anything an agent
// wrote.
import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Confirm } from "../shell/Confirm.js";
import { closeWarnings, restartWarnings } from "../lib/lifecycle.js";

/** LOCKED_WORDS says why Close does nothing for a locked pane. */
export const LOCKED_WORDS = "This pane is locked. Unlock it to close it.";

/** nextAsk is the question to ask for a request, or null to go ahead at once.
 *  A close always asks for a pane with anything to say; a restart asks for an
 *  agent that is still running, which is every one that is not a shell and not
 *  already ended. */
export function askFor(kind, node, pane) {
  const words = kind === "close" ? closeWarnings(node, pane) : restartWarnings(node, pane);
  if (words.length === 0) return null;
  return kind === "close"
    ? { title: "Close this pane?", words, label: "Close pane" }
    : { title: "Restart this agent?", words, label: "Restart" };
}

/** commandFor is the command a request sends. */
export function commandFor(kind, pane) {
  return kind === "close" ? { cmd: "closePane", id: pane } : { cmd: "restartPane", id: pane };
}

/** useLifecycle holds the request waiting for an answer and sends the commands.
 *  begin(kind, node, pane) sends at once when there is nothing to ask and opens
 *  the question otherwise; confirm sends the open one; cancel drops it; and
 *  toggleLock(pane, locked) sends lockPane. A request for another pane than the
 *  one in the drawer is not shown, and dropped when the pane changes. */
export function useLifecycle(send) {
  const [request, setRequest] = useState(null);
  const sendRef = useRef(send);
  sendRef.current = send;
  return {
    request,
    begin(kind, node, pane) {
      if (askFor(kind, node, pane)) setRequest({ kind, pane: node.pane });
      else sendRef.current(commandFor(kind, node.pane));
    },
    confirm() {
      if (request) sendRef.current(commandFor(request.kind, request.pane));
      setRequest(null);
    },
    cancel() {
      setRequest(null);
    },
    toggleLock(pane, locked) {
      sendRef.current({ cmd: "lockPane", id: pane, locked });
    },
  };
}

/** Lifecycle is the row of buttons and, under them, the question for a request
 *  waiting on an answer. life is the page's state for it (useLifecycle): the
 *  request open and what to do with it. */
export function Lifecycle({ node, pane = null, life }) {
  const locked = !!(pane && pane.locked);
  const ask = life.request && life.request.pane === node.pane ? askFor(life.request.kind, node, pane) : null;
  return html`
    <div class="life" role="group" aria-label="This pane">
      <button type="button" class="drawer-btn life-restart" onClick=${() => life.begin("restart", node, pane)}>Restart</button>
      <button type="button" class="drawer-btn life-lock" aria-pressed=${locked ? "true" : "false"} onClick=${() => life.toggleLock(node.pane, !locked)}>
        ${locked ? "Unlock" : "Lock"}
      </button>
      <button type="button" class="drawer-btn life-close" aria-describedby=${locked ? "life-locked" : undefined} disabled=${locked}
        onClick=${() => life.begin("close", node, pane)}>Close pane</button>
      ${locked ? html`<span class="life-note" id="life-locked">${LOCKED_WORDS}</span>` : null}
      ${ask
        ? html`<${Confirm} title=${ask.title} words=${ask.words} label=${ask.label} onConfirm=${life.confirm} onCancel=${life.cancel} />`
        : null}
    </div>
  `;
}
