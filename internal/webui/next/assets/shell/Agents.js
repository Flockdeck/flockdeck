// The Agents dialog: every pane in every open project, with what needs a person
// first. The rail shows a count for each project; this says which pane it is.
// Choosing one brings its project and tab to the front on the desktop and opens
// the pane in the drawer.
//
// The desktop sorts the list and sends it again when asked; the dialog asks when
// it opens and on Refresh. A window reached through the relay is sent the list with
// the detail text left out. Every string in it came from an agent or a person and
// is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Dialog } from "./Dialog.js";
import { AGENTS_COMMAND, activityWords, agentsOf, byProject, needsYou, revealAgentCommand } from "../lib/agents.js";

/** Agents is the dialog. list is the store's agents answer ({ items, seq } or
 *  null), send the store's send, onCloseFinished (when given) asks to close every
 *  finished pane, onChoose(item) is told the pane chosen (after the
 *  reveal is sent) and onClose closes the dialog. */
export function Agents({ list, send, onChoose, onClose, onCloseFinished = null }) {
  const [grouped, setGrouped] = useState(false);
  const [failed, setFailed] = useState(false);
  // An answer from before the dialog opened is for panes that may have gone.
  const floor = useRef(list ? list.seq : 0);
  const ask = () => setFailed(send(AGENTS_COMMAND) === false);
  useEffect(() => {
    ask();
  }, []);
  const items = list && list.seq > floor.current ? agentsOf(list) : null;
  const choose = (a) => {
    send(revealAgentCommand(a));
    onChoose(a);
    onClose();
  };
  const byId = new Map((items || []).map((a) => [a.paneId, a]));
  const row = (a) => html`<li key=${a.paneId}>
    <button type="button" class=${"ag-row" + (needsYou(a) ? " needs-you" : "")} data-pane=${a.paneId} onClick=${() => choose(a)}>
      <span class="ag-name">${a.name || a.paneId}</span>
      <span class="ag-state">${activityWords(a)}${a.since ? " · " + a.since : ""}</span>
      <span class="ag-where">${[a.project, a.tab, a.branch].filter(Boolean).join(" · ")}${a.parent && byId.get(a.parent) ? " · helper of " + (byId.get(a.parent).name || a.parent) : ""}${a.dirty ? " · " + a.dirty + " changed" : ""}${a.background ? " · " + a.background + " running in the background" : ""}</span>
    </button>
  </li>`;
  return html`
    <${Dialog} id="agents" title="Agents" onClose=${onClose} focus=".ag-row" wide>
      <div class="team-row" role="group" aria-label="Show">
        <button type="button" class="dialog-btn" id="agents-flat" aria-pressed=${grouped ? "false" : "true"} onClick=${() => setGrouped(false)}>Most urgent first</button>
        <button type="button" class="dialog-btn" id="agents-grouped" aria-pressed=${grouped ? "true" : "false"} onClick=${() => setGrouped(true)}>By project</button>
        <button type="button" class="dialog-btn" id="agents-refresh" onClick=${ask}>Refresh</button>
      </div>
      ${failed ? html`<p class="dialog-note dialog-note--error" role="alert">The request could not be sent. Check the connection.</p>` : null}
      ${!items && !failed ? html`<p class="dialog-note" role="status">Loading…</p>` : null}
      ${items && items.length === 0 ? html`<p class="dialog-note" id="agents-empty">No panes open.</p>` : null}
      ${items && items.length && !grouped
        ? html`<p class="dialog-note">${items.length + (items.length === 1 ? " pane" : " panes")}</p><ul class="ag-list" aria-label="Panes">${items.map(row)}</ul>`
        : null}
      ${items && items.length && grouped
        ? byProject(items).map((g) => html`<section key=${g.root + g.project} class="ag-group" aria-label=${g.project}>
            <h4 class="team-group-head">${g.project}</h4><ul class="ag-list">${g.panes.map(row)}</ul></section>`)
        : null}
      <div class="dialog-foot">
        ${onCloseFinished ? html`<button type="button" class="dialog-btn" id="ag-close-finished" onClick=${onCloseFinished}>Close finished panes…</button>` : null}
        <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
      </div>
    <//>
  `;
}
