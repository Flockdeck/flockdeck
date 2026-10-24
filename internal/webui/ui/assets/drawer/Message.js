// The message box at the foot of a terminal pane's drawer: text for the agent
// sent with paneSend by the pane's id, as a structured pane's box does. The desk
// types it into the pane (or queues it while the agent is busy); a window through
// the relay has no box, because paneSend is not sent there.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";

export const MESSAGE_NOT_SENT_WORDS = "Not connected to Flockdeck, so the text was not sent.";

/** Message is the box. name is the agent's name, for the label. The draft stays
 *  when the store did not send the text. Enter sends and Shift+Enter is a new line. */
export function Message({ pane, name, store }) {
  const [draft, setDraft] = useState("");
  const [failed, setFailed] = useState(false);
  const submit = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    const text = draft.trim();
    if (!text) return;
    if (store.send({ cmd: "paneSend", id: pane, text })) {
      setDraft("");
      setFailed(false);
    } else {
      setFailed(true);
    }
  };
  const onKey = (e) => {
    if (e.key !== "Enter" || e.shiftKey || e.ctrlKey || e.altKey || e.metaKey || e.isComposing) return;
    e.preventDefault();
    submit();
  };
  return html`<form class="structured-prompt drawer-message" onSubmit=${submit}>
    <textarea class="structured-input" aria-label=${"Message " + clip(name || pane, 60) + " directly"} placeholder=${"Message " + clip(name || pane, 60) + " directly"} rows="2" value=${draft} onInput=${(e) => setDraft(e.target.value)} onKeyDown=${onKey}></textarea>
    <button type="submit" class="drawer-btn structured-send" disabled=${draft.trim() === ""}>Send</button>
    ${failed ? html`<p class="structured-note structured-note--unsent" role="status">${MESSAGE_NOT_SENT_WORDS}</p>` : null}
  </form>`;
}
