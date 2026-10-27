// The agent info view of a structured pane: who the agent says it is, how it
// signs in, what it can do, the session, mode and model, the names of the MCP
// servers it was given, the folder and the adapter. Desk only. When the agent is
// waiting for a login the panel says what to do and offers to start the agent
// again once the person has signed in with the agent's own tool; Flockdeck never
// asks for or handles a login itself.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { showControls } from "./diff/diffModel.js";
import { infoMessage, restartMessage, infoRows, answerFor, needsSignIn, INFO_NOT_SENT_WORDS, INFO_WAITING_WORDS, INFO_LOGOUT_WORDS, INFO_RESTART_NOT_SENT_WORDS } from "./infoModel.js";

let infoSeq = 0;

/** InfoPanel draws the facts. result is the store's infoResult; it is shown only when its req is the one this panel sent for this pane. */
export function InfoPanel({ pane, st, store, result = null, onClose }) {
  const [req, setReq] = useState("");
  const [note, setNote] = useState("");
  const ask = () => {
    const next = "i" + ++infoSeq;
    const msg = infoMessage(pane, next);
    if (msg && store.send(msg)) {
      setReq(next);
      setNote("");
    } else {
      setReq("");
      setNote(INFO_NOT_SENT_WORDS);
    }
  };
  useEffect(ask, [pane, st && st.phase]);
  const mine = answerFor(result, pane, req);
  const rows = mine && mine.ok ? infoRows(mine.info) : [];
  const login = needsSignIn(st);
  // The phase the restart was sent in. The button stays off until the push says the phase changed, so a second press sends nothing.
  const [restartedIn, setRestartedIn] = useState(null);
  const pressed = useRef(null);
  const phase = st ? st.phase : "";
  const restart = () => {
    if (pressed.current === phase) return;
    const msg = restartMessage(pane);
    if (msg && store.send(msg)) {
      pressed.current = phase;
      setRestartedIn(phase);
    } else {
      setNote(INFO_RESTART_NOT_SENT_WORDS);
    }
  };
  const restarting = restartedIn !== null && restartedIn === phase;
  // A new phase is a new situation: a login wanted again may be answered again.
  useEffect(() => {
    if (restartedIn !== null && restartedIn !== phase) {
      pressed.current = null;
      setRestartedIn(null);
    }
  }, [phase, restartedIn]);
  return html`<section class="info-panel" aria-label="Agent info">
    ${login
      ? html`<div class="structured-note" role="status" data-info-login="on">
          <p>${showControls(String(st.setup.message || "The agent needs you to sign in."))}</p>
          ${st.setup.fix ? html`<p>${showControls(String(st.setup.fix))}</p>` : null}
          <p>Flockdeck does not sign in for you. Sign in with the agent's own tool, then restart the agent.</p>
          <button type="button" class="drawer-btn info-restart" disabled=${restarting} onClick=${restart}>${restarting ? "Restarting the agent" : "I have signed in: restart the agent"}</button>
        </div>`
      : null}
    ${rows.length
      ? html`<dl class="info-rows">${rows.map((r) => html`<div class="info-row"><dt>${r.name}</dt><dd>${r.value}</dd></div>`)}</dl>`
      : null}
    ${req && !mine ? html`<p class="structured-note" role="status">${INFO_WAITING_WORDS}</p>` : null}
    ${mine && !mine.ok ? html`<p class="structured-note structured-note--unsent" role="status" data-info="failed">${showControls(String(mine.error || "The facts could not be read."))}</p>` : null}
    ${note ? html`<p class="structured-note structured-note--unsent" role="status">${note}</p>` : null}
    <p class="structured-note structured-note--hint">${INFO_LOGOUT_WORDS}</p>
    <div class="export-actions">
      <button type="button" class="drawer-btn info-refresh" onClick=${ask}>Refresh</button>
      ${login ? null : html`<button type="button" class="drawer-btn info-close" onClick=${onClose}>Close</button>`}
    </div>
  </section>`;
}
