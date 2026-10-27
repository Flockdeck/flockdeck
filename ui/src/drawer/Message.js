// The message box at the foot of a terminal pane's drawer: text for the agent
// sent with paneSend by the pane's id, as a structured pane's box does. The desk
// types it into the pane (or queues it while the agent is busy); a window through
// the relay has no box, because paneSend is not sent there. A shell has none
// either: the desk refuses text into a shell unless allowShell is sent, and a
// shell is typed into in its terminal.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { composing } from "../shell/keys.js";
import { checkText, newReq, outcomeOf, NOT_CONNECTED_WORDS, SENT_SHOWN } from "../graph/ManagerBar.js";

/** MAX_LINES is how many lines the box grows to before it scrolls. LINE_PX and PAD_PX are the line height and the vertical padding in drawer.css. */
export const MAX_LINES = 4;
const LINE_PX = 18;
const PAD_PX = 12;

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
  // One line that grows with the text, up to MAX_LINES, with no grip: the conversation above takes the rest.
  const box = useRef(null);
  useEffect(() => {
    const el = box.current;
    if (!el || !el.style) return;
    el.style.height = "auto";
    const max = LINE_PX * MAX_LINES + PAD_PX;
    // A hidden panel measures 0: leave the height to the stylesheet until it is shown.
    el.style.height = el.scrollHeight ? Math.min(el.scrollHeight, max) + "px" : "";
    el.style.overflowY = (el.scrollHeight || 0) > max ? "auto" : "hidden";
  }, [draft]);
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
    const mark = { pane, notice: state.notice ? state.notice.seq : 0, sent: state.sent ? state.sent.seq : 0, hello: state.hello || null, req: newReq() };
    if (store.send({ cmd: "paneSend", id: pane, text, req: mark.req })) {
      busy.current = true;
      setPending(mark);
    } else {
      setPending(null);
      setLocal(NOT_CONNECTED_WORDS);
    }
  };
  const onKey = (e) => {
    if (e.key !== "Enter" || e.shiftKey || e.ctrlKey || e.altKey || e.metaKey || composing(e)) return;
    e.preventDefault();
    submit();
  };
  const say = local ? { kind: "refused", text: local } : outcome;
  const kind = say ? say.kind : "idle";
  return html`<form class="structured-prompt drawer-message" data-state=${kind} onSubmit=${submit}>
    <textarea ref=${box} class="structured-input" aria-label=${"Message " + clip(name || pane, 60) + " directly"} placeholder=${"Message " + clip(name || pane, 60) + " directly"} rows="1" readOnly=${sending} aria-busy=${sending ? "true" : undefined} value=${draft}
      onInput=${(e) => { setDraft(e.target.value); if (local) setLocal(""); if (outcome && outcome.kind === "refused") setPending(null); }} onKeyDown=${onKey}></textarea>
    <button type="submit" class="drawer-btn structured-send" disabled=${sending || draft.trim() === ""}>Send</button>
    ${!say && /\n/.test(draft) ? html`<p class="structured-note" role="status">Line breaks are sent as spaces.</p>` : null}
    <p key="status" class="structured-note structured-note--live" role="status">${say && kind !== "sending" && kind !== "refused" ? say.text : ""}</p>
    ${say && kind === "refused" ? html`<p key="refused" class="structured-note structured-note--unsent" role="alert">${showControls(say.text)}</p>` : null}
  </form>`;
}
