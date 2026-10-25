// Rename is the box in the pane's More panel that gives the pane a label: the short
// name of the work it is on ("rate limiter PR"). Empty puts the pane's own name back.
// The label is not the Doing line, which is what the pane is doing at this moment.
//
// The box is kept while the pane's label changes under it (a manager or the label
// made from the task renames the pane): what is typed stays unless nothing was typed
// yet. An automatic label is shown as the placeholder, not filled in, so Save on an
// untouched box does not turn it into a chosen one. The status line stays in the page,
// empty first, so a screen reader announces what is put in it.
import { useEffect, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { isViewer } from "../lib/role.js";

/** LABEL_MAX is the most characters a label has; the desktop counts characters (code points) and cuts it there. */
export const LABEL_MAX = 40;

/** labelText is text as a label: spaces trimmed and runs of them made one, cut to LABEL_MAX characters without splitting a pair. */
export function labelText(text, max = LABEL_MAX) {
  return Array.from(String(text || "").replace(/\s+/g, " ").trim()).slice(0, max).join("");
}

/** renameCommand is the command that sets pane id's label. */
export function renameCommand(id, text) {
  return { cmd: "paneName", id, text: labelText(text) };
}

/** canRename says whether this window may offer the box: not through the relay, and not as a viewer. */
export const canRename = (state) => !state.remote && !isViewer(state);

/** chosenLabel is the label of a node when a person or an agent chose it, "" for none or one made from the task. */
export const chosenLabel = (node) => (node.label && !node.labelAuto ? node.label : "");

/** Rename draws the box for a pane. send is the store's send; it returns false when the command was not sent. */
export function Rename({ node, send }) {
  const chosen = chosenLabel(node);
  const [text, setText] = useState(chosen);
  const [dirty, setDirty] = useState(false);
  const [status, setStatus] = useState({ text: "", error: false });
  useEffect(() => {
    if (!dirty) setText(chosen);
  }, [chosen]);
  const go = (next, done) => {
    if (send(renameCommand(node.pane, next)) === false) setStatus({ text: "The label could not be sent from this window.", error: true });
    else {
      setDirty(false);
      setStatus({ text: done, error: false });
    }
  };
  const submit = (e) => {
    e.preventDefault();
    const next = labelText(text);
    go(next, next ? "Label saved" : "Label cleared");
  };
  const clear = () => {
    setText("");
    go("", "Label cleared");
  };
  return html`
    <form class="rename" aria-label="Name this pane" onSubmit=${submit}>
      <label class="rename-label" for="rename-text">Label</label>
      <input id="rename-text" class="rename-input" type="text" value=${text}
        placeholder=${node.label && node.labelAuto ? node.label : node.name || "the work this pane is on"}
        onInput=${(e) => { setText(Array.from(e.target.value).slice(0, LABEL_MAX).join("")); setDirty(true); }} />
      <button type="submit" class="drawer-btn rename-save">Save</button>
      <button type="button" class="drawer-btn rename-clear" disabled=${!chosen} onClick=${clear}>Clear</button>
      <span class=${"rename-status" + (status.error ? " rename-status--error" : "")} role=${status.error ? "alert" : "status"} aria-live="polite">${status.text}</span>
    </form>
  `;
}
