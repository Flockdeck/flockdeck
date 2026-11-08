// The agent info view of a structured pane: who the agent says it is, how it
// signs in, what it can do, the session, mode and model, the names of the MCP
// servers it was given, the folder and the adapter. Desk only. When the agent is
// waiting for a login the panel offers to sign in from here when the agent's
// adapter can run its login command (a link to the agent's own page; Flockdeck
// never asks for or handles a credential), and otherwise says what to do and
// offers to start the agent again once the person has signed in with the
// agent's own tool.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { showControls } from "./diff/diffModel.js";
import { infoMessage, restartMessage, infoRows, answerFor, needsSignIn, signInMessage, signInCancelMessage, signInRefused, signInView, SIGNIN_REFUSED_WORDS, SIGNIN_COPY_WORDS, SIGNIN_COPIED_WORDS, SIGNIN_COPY_FAILED_WORDS, SIGNIN_START_WORDS, SIGNIN_CANCEL_WORDS, SIGNIN_STARTING_WORDS, SIGNIN_WAITING_WORDS, SIGNIN_DONE_WORDS, SIGNIN_FAILED_WORDS, SIGNIN_NOT_SENT_WORDS, SIGNIN_LINK_WORDS, INFO_NOT_SENT_WORDS, INFO_WAITING_WORDS, INFO_LOGOUT_WORDS, INFO_RESTART_NOT_SENT_WORDS, rulesAvailable, rulesMessage, ruleRevokeMessage, ruleRows, RULES_TITLE_WORDS, RULES_NONE_WORDS, RULES_HINT_WORDS, RULES_REMOVE_WORDS, RULES_WAITING_WORDS, RULES_NOT_SENT_WORDS, RULES_FAILED_WORDS, RULES_REVOKE_FAILED_WORDS } from "./infoModel.js";

let infoSeq = 0;

/** InfoPanel draws the facts. result is the store's infoResult; it is shown only when its req is the one this panel sent for this pane. */
export function InfoPanel({ pane, st, store, result = null, signInResult = null, rulesResult = null, onClose }) {
  const [req, setReq] = useState("");
  const [note, setNote] = useState("");
  // The allow-always rules (API 1.29). list is null until the agent has answered; a rule is removed by the id it came with.
  const rulesOn = rulesAvailable(st);
  const [rulesReq, setRulesReq] = useState("");
  const [rulesNote, setRulesNote] = useState("");
  const [rules, setRules] = useState(null);
  const askRules = () => {
    if (!rulesOn) return;
    const next = "r" + ++infoSeq;
    const msg = rulesMessage(pane, next);
    if (msg && store.send(msg)) setRulesReq(next);
    else {
      setRulesReq("");
      setRulesNote(RULES_NOT_SENT_WORDS);
    }
  };
  const revoke = (ruleId) => {
    const next = "r" + ++infoSeq;
    const msg = ruleRevokeMessage(pane, ruleId, next);
    if (msg && store.send(msg)) {
      setRulesReq(next);
      setRulesNote("");
    } else setRulesNote(RULES_NOT_SENT_WORDS);
  };
  useEffect(() => {
    const r = rulesResult;
    if (!r || r.id !== pane || !rulesReq || r.req !== rulesReq) return;
    if (r.op === "structuredRules") {
      if (r.ok) {
        setRules(ruleRows(r.rules));
        setRulesNote("");
      } else setRulesNote(RULES_FAILED_WORDS);
      return;
    }
    // Whatever the answer to a removal, the list is read again: it shows what the agent holds now.
    setRulesNote(r.ok ? "" : RULES_REVOKE_FAILED_WORDS);
    askRules();
  }, [rulesResult]);
  useEffect(() => {
    setRules(null);
  }, [pane]);
  const ask = () => {
    askRules();
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
  useEffect(ask, [pane, st && st.phase, rulesOn]);
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
  const si = signInView(st);
  const fromHere = login && si && (si.available || si.state !== "");
  const [signReq, setSignReq] = useState("");
  const [copied, setCopied] = useState("");
  const send = (make) => {
    const next = "s" + ++infoSeq;
    const msg = make(pane, next);
    if (msg && store.send(msg)) {
      setNote("");
      setSignReq(next);
    } else setNote(SIGNIN_NOT_SENT_WORDS);
  };
  const copy = async (url) => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(SIGNIN_COPIED_WORDS);
    } catch (_) {
      setCopied(SIGNIN_COPY_FAILED_WORDS);
    }
  };
  const refused = signInRefused(signInResult, pane, signReq);
  const busy = si && (si.state === "starting" || si.state === "waiting");
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
          ${fromHere
            ? html`<div data-signin=${si.state || "idle"}>
                ${si.state === "starting" ? html`<p>${SIGNIN_STARTING_WORDS}</p>` : null}
                ${si.state === "waiting" && si.url
                  ? html`<p>${SIGNIN_WAITING_WORDS}</p>
                      <p><a class="signin-link" href=${si.url} target="_blank" rel="noopener noreferrer">${SIGNIN_LINK_WORDS}</a> <span class="signin-host">(${si.host})</span> <button type="button" class="drawer-btn signin-copy" onClick=${() => copy(si.url)}>${SIGNIN_COPY_WORDS}</button></p>
                      ${copied ? html`<p class="signin-copied" role="status">${copied}</p>` : null}`
                  : null}
                ${si.state === "waiting" && !si.url ? html`<p>${SIGNIN_STARTING_WORDS}</p>` : null}
                ${si.state === "done" ? html`<p>${SIGNIN_DONE_WORDS}</p>` : null}
                ${si.state === "failed" ? html`<p>${SIGNIN_FAILED_WORDS}</p>` : null}
                ${refused ? html`<p class="signin-refused" role="status">${SIGNIN_REFUSED_WORDS}</p>` : null}
                ${busy
                  ? html`<button type="button" class="drawer-btn signin-cancel" onClick=${() => send(signInCancelMessage)}>${SIGNIN_CANCEL_WORDS}</button>`
                  : si.state === "done"
                    ? null
                    : html`<button type="button" class="drawer-btn signin-start" onClick=${() => send(signInMessage)}>${SIGNIN_START_WORDS}</button>`}
              </div>`
            : html`<p>Flockdeck does not sign in for you. Sign in with the agent's own tool, then restart the agent.</p>
          <button type="button" class="drawer-btn info-restart" disabled=${restarting} onClick=${restart}>${restarting ? "Restarting the agent" : "I have signed in: restart the agent"}</button>`}
        </div>`
      : null}
    ${rows.length
      ? html`<dl class="info-rows">${rows.map((r) => html`<div class="info-row"><dt>${r.name}</dt><dd>${r.value}</dd></div>`)}</dl>`
      : null}
    ${rulesOn
      ? html`<div class="info-rules" data-info-rules="on">
          <h4 class="info-rules-title">${RULES_TITLE_WORDS}</h4>
          ${rules === null && !rulesNote ? html`<p class="structured-note" role="status">${RULES_WAITING_WORDS}</p>` : null}
          ${rules !== null && rules.length === 0 ? html`<p class="structured-note">${RULES_NONE_WORDS}</p>` : null}
          ${rules !== null && rules.length
            ? html`<ul class="info-rule-list">${rules.map((r) => html`<li class="info-rule" key=${r.id} data-rule=${r.id}>
                <code class="info-rule-text" title=${r.cut ? "Cut: the command is longer than this" : r.text}>${r.text}${r.cut ? " …" : ""}</code>
                ${r.dir ? html`<span class="info-rule-dir">in ${r.dir}</span>` : null}
                <button type="button" class="drawer-btn info-rule-remove" aria-label=${"Remove the rule for " + r.text.slice(0, 60)} onClick=${() => revoke(r.id)}>${RULES_REMOVE_WORDS}</button>
              </li>`)}</ul>`
            : null}
          ${rulesNote ? html`<p class="structured-note structured-note--unsent" role="status" data-info-rules-note="on">${rulesNote}</p>` : null}
          <p class="structured-note structured-note--hint">${RULES_HINT_WORDS}</p>
        </div>`
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
