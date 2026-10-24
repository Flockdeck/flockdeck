// Find pane: a search over every pane in every open project, by what the pane is
// called and by the ids that name it (project, tab, agent, model, directory,
// branch, pane id, conversation id, process id, peer name). The desktop searches;
// the dialog asks again as the words change and shows the answer to the words
// that are in the field now, not to ones typed a moment ago.
//
// Choosing a result sends revealPane, which brings the pane's project and tab to
// the front on the desktop, and then opens the pane in the drawer.
//
// Enter chooses the first result once the results are for what is in the field.
// Every string in a result came from an agent or a person and is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Dialog } from "./Dialog.js";
import { matchesOf, revealCommand, whereOf, whyOf } from "../lib/paneinfo.js";

/** FindPane is the dialog. found is the store's latest findPane answer (or null),
 *  send the store's send, onChoose(result) is told the result chosen (after the
 *  reveal is sent) and onClose closes the dialog. */
export function FindPane({ found, send, onChoose, onClose }) {
  const [query, setQuery] = useState("");
  const [failed, setFailed] = useState(false);
  // An answer from before the dialog opened is for panes that may have gone.
  const [floor] = useState(found ? found.seq : 0);
  const listRef = useRef(null);
  const fieldRef = useRef(null);
  const ask = (text) => setFailed(!send({ cmd: "findPane", text }));
  useEffect(() => {
    ask("");
  }, []);
  // The answer to what is in the field: the desktop echoes the words it searched.
  const answer = found && found.seq > floor && found.query === query ? matchesOf(found) : null;

  const rows = () => (listRef.current && listRef.current.querySelectorAll ? [...listRef.current.querySelectorAll("button")] : []);
  const choose = (r) => {
    send(revealCommand(r));
    onChoose(r);
    onClose();
  };
  const onFieldKey = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel && query) {
      e.preventDefault();
      e.stopPropagation();
      setQuery("");
      ask("");
    } else if (e.key === LIST.accept) {
      e.preventDefault();
      if (answer && answer.items[0]) choose(answer.items[0]);
    } else if (e.key === LIST.down) {
      const first = rows()[0];
      if (first) {
        e.preventDefault();
        first.focus();
      }
    }
  };
  const onRowKey = (e, i) => {
    if (composing(e)) return;
    const all = rows();
    if (e.key === LIST.down && all[i + 1]) {
      e.preventDefault();
      all[i + 1].focus();
    } else if (e.key === LIST.up) {
      e.preventDefault();
      (all[i - 1] || fieldRef.current || { focus() {} }).focus();
    }
  };
  return html`
    <${Dialog} id="find-pane" title="Find pane" onClose=${onClose} focus="#find-pane-input">
      <input id="find-pane-input" class="dialog-field" type="text" autocomplete="off" spellcheck="false" ref=${fieldRef} value=${query}
        placeholder="Name, project, tab, agent, id, conversation, process or peer name" aria-label="Find a pane"
        onInput=${(e) => { setQuery(e.target.value); ask(e.target.value); }} onKeyDown=${onFieldKey} />
      ${failed ? html`<p class="dialog-note dialog-note--error" role="alert">The request could not be sent. Check the connection.</p>` : null}
      <div id="find-pane-results" aria-live="polite">
        ${answer && answer.items.length === 0
          ? html`<p class="dialog-note">${query.trim() ? "No pane matches “" + query.trim() + "”." : "No panes open."}</p>`
          : null}
        ${answer && answer.items.length > 0
          ? html`<ul class="pf-list" ref=${listRef} aria-label="Panes found">
              ${answer.items.map((r, i) => html`<li key=${r.paneId}>
                <button type="button" class="pf-row" data-pane=${r.paneId} onClick=${() => choose(r)} onKeyDown=${(e) => onRowKey(e, i)}>
                  <span class="pf-name">${r.name || r.paneId}</span>
                  <span class="pf-where">${whereOf(r)}</span>
                  ${whyOf(r) ? html`<span class="pf-why">${whyOf(r)}</span>` : null}
                </button>
              </li>`)}
            </ul>`
          : null}
      </div>
      <div class="dialog-foot">
        <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
      </div>
    <//>
  `;
}
