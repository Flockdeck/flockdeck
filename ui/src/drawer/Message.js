// The message box at the foot of a terminal pane's drawer: text for the agent
// sent with paneSend by the pane's id, as a structured pane's box does. The desk
// types it into the pane (or queues it while the agent is busy); a window through
// the relay has no box, because paneSend is not sent there. A shell has none
// either: the desk refuses text into a shell unless allowShell is sent, and a
// shell is typed into in its terminal.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { checkText, outcomeOf, NOT_CONNECTED_WORDS, SENT_SHOWN } from "../graph/ManagerBar.js";

export const MESSAGE_NOT_SENT_WORDS = NOT_CONNECTED_WORDS;

// A line break would reach the agent's prompt as a paste with an Enter in it, so
// the box sends one line, as the manager bar does.
const BREAKS = /\s*[\r\n]+\s*/g;

/** Message is the box. name is the agent's name, for the label. The text goes from the box once the desk has taken it; a
 *  refusal, or no answer because the connection dropped, keeps the draft and
 *  says so. Enter sends and Shift+Enter is a new line (sent as a space). */
export function Message({ pane, name, store, shownFor = SENT_SHOWN }) {
  // The box follows the store itself: the desk's answer arrives after the page has drawn the send.
  const [, tick] = useState(0);
  useEffect(() => (store.subscribe ? store.subscribe(() => tick((n) => n + 1)) : undefined), [store]);
  const state = store.getState();
  const [draft, setDraft] = useState("");
  const [local, setLocal] = useState("");
  const [pending, setPending] = useState(null);
  // Set at once on a send, so a second Enter before the page has redrawn does not send again.
  const busy = useRef(false);
  const outcome = outcomeOf(state, pending);
  if (!outcome || outcome.kind !== "sending") busy.current = false;
  const done = !!outcome && outcome.kind === "sent";
  useEffect(() => {
    if (done) setDraft("");
  }, [done, pending]);
  useEffect(() => {
    if (!done) return undefined;
    const t = setTimeout(() => setPending(null), shownFor);
    return () => clearTimeout(t);
  }, [done, pending]);
  const sending = !!outcome && outcome.kind === "sending";
  const submit = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    if (busy.current) return;
    const text = draft.replace(BREAKS, " ").trim();
    if (!text) return;
    const why = checkText(text);
    if (why) {
      setLocal(why);
      return;
    }
    setLocal("");
    const mark = { pane, notice: state.notice ? state.notice.seq : 0, sent: state.sent ? state.sent.seq : 0, hello: state.hello || null };
    if (store.send({ cmd: "paneSend", id: pane, text })) {
      busy.current = true;
      setPending(mark);
    } else {
      setPending(null);
      setLocal(NOT_CONNECTED_WORDS);
    }
  };
  const onKey = (e) => {
    if (e.key !== "Enter" || e.shiftKey || e.ctrlKey || e.altKey || e.metaKey || e.isComposing) return;
    e.preventDefault();
    submit();
  };
  const say = local ? { kind: "refused", text: local } : outcome;
  const kind = say ? say.kind : "idle";
  return html`<form class="structured-prompt drawer-message" data-state=${kind} onSubmit=${submit}>
    <textarea class="structured-input" aria-label=${"Message " + clip(name || pane, 60) + " directly"} placeholder=${"Message " + clip(name || pane, 60) + " directly"} rows="2" readOnly=${sending} aria-busy=${sending ? "true" : undefined} value=${draft}
      onInput=${(e) => { setDraft(e.target.value); if (local) setLocal(""); if (outcome && outcome.kind === "refused") setPending(null); }} onKeyDown=${onKey}></textarea>
    <button type="submit" class="drawer-btn structured-send" disabled=${sending || draft.trim() === ""}>Send</button>
    ${say && kind !== "sending" ? html`<p class=${"structured-note" + (kind === "refused" ? " structured-note--unsent" : "")} role=${kind === "refused" ? "alert" : "status"}>${say.text}</p>` : null}
  </form>`;
}
