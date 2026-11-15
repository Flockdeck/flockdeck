// The panel behind /resume and the Conversations item of a structured pane: the past conversations of the
// pane's folder, newest first, and New conversation. Choosing one resumes it in this pane with its
// transcript replayed; the desk restarts the pane's agent on it. A turn that is running or prompts that
// are waiting are ended by a move, so the first press says so and a second one confirms. The list is
// asked for when the panel opens. Desk only. For an agent that does not list its conversations the panel
// says so, and New conversation is still there.
import { useState, useEffect } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { showControls } from "./diff/diffModel.js";
import { sessionsOffered, busyWords, sessionsMessage, resumeMessage, newConversationMessage, sessionsOf, timeWords, convResultWords, PICKER_NOT_SENT_WORDS, PICKER_NOT_OFFERED_WORDS, PICKER_NONE_WORDS, PICKER_LOADING_WORDS, PICKER_NEW_WORDS } from "./convPickerModel.js";

let pickerSeq = 0;

/** ConvPicker draws the panel. result is the store's conversationResult; it is shown only when its req is
 *  the latest this panel sent for this pane. onMoved runs when a resume or a new conversation went through. */
export function ConvPicker({ pane, store, st, result = null, onMoved = null, onClose }) {
  const offered = sessionsOffered(st);
  const [listReq, setListReq] = useState("");
  const [req, setReq] = useState("");
  const [asking, setAsking] = useState("");
  const [note, setNote] = useState("");
  const [list, setList] = useState(null);
  const [listError, setListError] = useState("");
  const send = (msg) => {
    const next = "c" + ++pickerSeq;
    if (!msg || !store.send({ ...msg, req: next })) {
      setNote(PICKER_NOT_SENT_WORDS);
      return "";
    }
    setNote("");
    return next;
  };
  useEffect(() => {
    if (!offered) return;
    const next = send(sessionsMessage(pane, ""));
    setListReq(next);
  }, [pane, offered]);
  useEffect(() => {
    if (!result || result.id !== pane) return;
    if (result.req === listReq && result.op === "structuredSessions") {
      if (result.ok) {
        setList(sessionsOf(result.sessions));
        setListError("");
      } else {
        setListError(convResultWords(result).words);
      }
    }
    if (result.req === req && result.ok && onMoved) onMoved();
  }, [result]);
  const mine = result && req && result.req === req && result.id === pane ? convResultWords(result) : null;
  const waiting = req !== "" && !mine;
  const busy = busyWords(st);
  // First press asks when something is running; the second, or the first with nothing running, sends.
  const move = (key, build) => {
    if (busy && asking !== key) {
      setAsking(key);
      return;
    }
    setAsking("");
    setReq(send(build(!!busy)));
  };
  const now = Date.now();
  return html`<div class="conv-picker" role="group" aria-label="Conversations of this folder">
    <div class="conv-picker-actions">
      <button type="button" class="drawer-btn conv-new" disabled=${waiting} onClick=${() => move("new", (c) => newConversationMessage(pane, "", c))}>New conversation</button>
      <button type="button" class="drawer-btn conv-close" onClick=${onClose}>Close</button>
    </div>
    <p class="structured-note" role="status">${PICKER_NEW_WORDS}</p>
    ${asking ? html`<p class="structured-note conv-confirm" role="alert" data-confirm=${asking}>${busy} Press the same button again to go on.</p>` : null}
    ${!offered ? html`<p class="structured-note" role="status" data-conv="not-offered">${PICKER_NOT_OFFERED_WORDS}</p>` : null}
    ${offered && list === null && !listError ? html`<p class="structured-note" role="status">${PICKER_LOADING_WORDS}</p>` : null}
    ${listError ? html`<p class="structured-note structured-note--unsent" role="status" data-conv="list-failed">${showControls(listError)}</p>` : null}
    ${offered && list && list.length === 0 ? html`<p class="structured-note" role="status" data-conv="none">${PICKER_NONE_WORDS}</p>` : null}
    ${list && list.length
      ? html`<ol class="conv-list">${list.map((s) => html`<li key=${s.id} class="conv-item" data-session=${s.id} data-current=${s.current ? "yes" : "no"}>
          <span class="conv-title">${showControls(s.title)}</span>
          <span class="conv-time">${timeWords(s.updated, now)}</span>
          ${s.current
            ? html`<span class="conv-here">This conversation</span>`
            : html`<button type="button" class="drawer-btn conv-resume" disabled=${waiting} onClick=${() => move("r:" + s.id, (c) => resumeMessage(pane, s.id, "", c))}>${asking === "r:" + s.id ? "Resume and end it" : "Resume"}</button>`}
        </li>`)}</ol>`
      : null}
    ${note ? html`<p class="structured-note structured-note--unsent" role="status">${note}</p>` : null}
    ${waiting ? html`<p class="structured-note" role="status">Working on it.</p>` : null}
    ${mine && mine.words ? html`<p class=${"structured-note" + (mine.ok ? "" : " structured-note--unsent")} role="status" data-conv=${mine.ok ? "ok" : "failed"}>${showControls(mine.words)}</p>` : null}
  </div>`;
}
