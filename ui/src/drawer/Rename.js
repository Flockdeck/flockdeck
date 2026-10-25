// Rename is the box in the pane's More panel that gives the pane a label: the short
// name of the work it is on ("rate limiter PR"). Empty puts the pane's own name back.
// The label is not the Doing line, which is what the pane is doing at this moment.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";

/** LABEL_MAX is the most characters a label has; the desktop cuts it there. */
export const LABEL_MAX = 40;

/** renameCommand is the command that sets pane id's label, with the spaces trimmed and runs of them made one. */
export function renameCommand(id, text) {
  return { cmd: "paneName", id, text: String(text || "").replace(/\s+/g, " ").trim().slice(0, LABEL_MAX) };
}

/** Rename draws the box for a pane. send is the store's send; it returns false when the command was not sent. */
export function Rename({ node, send }) {
  const [text, setText] = useState(node.label || "");
  const [failed, setFailed] = useState(false);
  const submit = (e) => {
    e.preventDefault();
    setFailed(send(renameCommand(node.pane, text)) === false);
  };
  const clear = () => {
    setText("");
    setFailed(send(renameCommand(node.pane, "")) === false);
  };
  return html`
    <form class="rename" aria-label="Name this pane" onSubmit=${submit}>
      <label class="rename-label" for="rename-text">Label</label>
      <input id="rename-text" class="rename-input" type="text" maxlength=${LABEL_MAX} value=${text} placeholder=${node.name || "the work this pane is on"}
        onInput=${(e) => setText(e.target.value)} />
      <button type="submit" class="drawer-btn rename-save">Save</button>
      <button type="button" class="drawer-btn rename-clear" disabled=${!node.label} onClick=${clear}>Clear</button>
      ${failed ? html`<span class="rename-note" role="alert">The label could not be sent.</span>` : null}
    </form>
  `;
}
