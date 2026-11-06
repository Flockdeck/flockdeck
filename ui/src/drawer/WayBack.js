// The way back to a terminal pane: a structured pane that is not running (it failed to start,
// needs signing in, or its agent ended) offers to restart as the terminal version of the same
// agent. The desktop says when it is on offer, in the pane's terminalWayBack, and leaves it out
// for an agent that has no terminal version; this file never works that out itself.
import { html } from "../html.js";

/** WAY_BACK_WORDS is the line under the reason. */
export const WAY_BACK_WORDS = "You can run this agent in a terminal instead. The pane keeps its place and folder; the conversation starts afresh.";

/** wayBackOf is the id of the terminal agent a pane could be restarted as, or "". */
export function wayBackOf(pane) {
  return pane && typeof pane.terminalWayBack === "string" ? pane.terminalWayBack : "";
}

/** wayBackCommand is the command that restarts the pane as a terminal agent, or null. */
export function wayBackCommand(paneId) {
  return typeof paneId === "string" && paneId !== "" ? { cmd: "restartPane", id: paneId, kind: "terminal" } : null;
}

/** WayBack is the notice and its button. why is the reason the pane is not running, if it has
 *  one; the text is drawn as text. */
export function WayBack({ paneId, why = "", onRestart }) {
  const cmd = wayBackCommand(paneId);
  return html`
    <div class="way-back" role="alert">
      ${why ? html`<div class="way-back-why">${why}</div>` : null}
      <div class="way-back-words">${WAY_BACK_WORDS}</div>
      <button type="button" class="chip primary way-back-btn" disabled=${!cmd} onClick=${() => cmd && onRestart(cmd)}>Restart as terminal</button>
    </div>`;
}
