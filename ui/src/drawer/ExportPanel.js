// The panel that saves a structured pane's conversation as a Markdown file. The
// person types the full path of a folder; the desktop writes a new file there
// (structuredExport) and says where. It says first what the file holds. Desk
// only: the file is written on this machine.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { showControls } from "./diff/diffModel.js";
import { loadDir, saveDir, exportMessage, resultWords, EXPORT_NOT_SENT_WORDS, EXPORT_NO_DIR_WORDS, EXPORT_INCLUDES_WORDS } from "./exportModel.js";

let exportSeq = 0;

/** ExportPanel draws the form. result is the store's exportResult; it is shown
 *  only when its req is the one this panel sent for this pane. */
export function ExportPanel({ pane, store, result = null, onClose }) {
  const [dir, setDir] = useState(() => loadDir());
  const [thinking, setThinking] = useState(false);
  const [req, setReq] = useState("");
  const [note, setNote] = useState("");
  const mine = result && req && result.req === req && result.id === pane ? resultWords(result) : null;
  const save = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    const next = "x" + ++exportSeq;
    const msg = exportMessage(pane, dir, thinking, next);
    if (!msg) {
      setNote(EXPORT_NO_DIR_WORDS);
      return;
    }
    saveDir(dir);
    if (store.send(msg)) {
      setReq(next);
      setNote("");
    } else {
      setReq("");
      setNote(EXPORT_NOT_SENT_WORDS);
    }
  };
  const waiting = req !== "" && !mine;
  return html`<form class="export-panel" aria-label="Export the conversation" onSubmit=${save}>
    <p class="export-includes">${EXPORT_INCLUDES_WORDS}</p>
    <label class="export-field"><span>Folder</span>
      <input type="text" class="export-dir" value=${dir} maxlength="1024" spellcheck="false" placeholder="C:\Users\you\Documents" onInput=${(e) => { setDir(e.target.value); setNote(""); }} /></label>
    <label class="export-field export-check"><input type="checkbox" class="export-thinking" checked=${thinking} onChange=${(e) => setThinking(e.target.checked)} /><span>Include the agent's thinking, where it is still held</span></label>
    <div class="export-actions">
      <button type="submit" class="drawer-btn export-save" disabled=${waiting}>Save as Markdown</button>
      <button type="button" class="drawer-btn export-close" onClick=${onClose}>Close</button>
    </div>
    ${note ? html`<p class="structured-note structured-note--unsent" role="status">${note}</p>` : null}
    ${waiting ? html`<p class="structured-note" role="status">Saving.</p>` : null}
    ${mine ? html`<p class=${"structured-note" + (mine.ok ? "" : " structured-note--unsent")} role="status" data-export=${mine.ok ? "ok" : "failed"}>${showControls(mine.words)}</p>` : null}
  </form>`;
}
