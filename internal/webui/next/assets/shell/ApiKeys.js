// The API keys section of the settings: each agent that talks to a model API,
// whether it has a key, and where that key came from.
//
// The desktop never sends a key, so there is nothing here that could show one.
// The one place a key is typed is a password field that starts empty every time
// and is emptied the moment the key is sent; the row then says only that a key is
// set. Escape puts the field away without closing the dialog (a half-typed key
// would otherwise go with it and leave the person unsure what was saved).
//
// A window reached through the relay lists the agents and is told that keys are
// set on the machine itself, since a key typed there would pass through the
// relay. Names and the variables to export came from the catalogue and are drawn
// as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Confirm } from "./Confirm.js";
import { KEYS_INTRO, KEYS_NONE_WORDS, KEYS_RELAY_WORDS, canClear, clearKeyCommand, keyNotes, keyStatus, keysOf, setKeyCommand } from "../lib/apikeys.js";

/** Keys is the section. keys is the store's latest keys answer (or null), remote
 *  says this window was reached through the relay, send is the store's send, and
 *  news is the latest notice since the dialog opened (the desktop says what was
 *  saved or why not). */
export function Keys({ keys, remote, send, news = "" }) {
  const [editing, setEditing] = useState("");
  const [failed, setFailed] = useState(false);
  const [asking, setAsking] = useState("");
  const field = useRef(null);
  const buttons = useRef({});
  const rows = keys ? keysOf(keys) : null;
  useEffect(() => {
    setFailed(send({ cmd: "keys" }) === false);
  }, []);
  // The field takes the keyboard when it opens.
  useEffect(() => {
    if (editing && field.current && field.current.focus) field.current.focus();
  }, [editing]);
  // When the field goes the row's own button gets the keyboard back, where the
  // next Enter replaces the key again.
  const done = (agent) => {
    setEditing("");
    const btn = buttons.current[agent];
    if (btn && btn.focus) btn.focus();
  };
  const save = (agent) => {
    const el = field.current;
    const cmd = setKeyCommand(agent, el ? el.value : "");
    if (el) el.value = "";
    if (cmd) setFailed(send(cmd) === false);
    done(agent);
  };
  const cancel = (agent) => {
    if (field.current) field.current.value = "";
    done(agent);
  };
  const onFieldKey = (e, agent) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      save(agent);
    } else if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      cancel(agent);
    }
  };
  const clear = (agent) => {
    setAsking("");
    const cmd = clearKeyCommand(agent);
    if (cmd) setFailed(send(cmd) === false);
  };
  return html`
    <h3 class="set-head">API keys</h3>
    <p class="set-intro" id="keys-about">${KEYS_INTRO}</p>
    ${news ? html`<p class="set-status" role="status" id="keys-news">${news}</p>` : null}
    ${failed ? html`<p class="set-error" role="alert">The request could not be sent. Check the connection.</p>` : null}
    ${!rows ? html`<p class="set-intro" role="status">Loading…</p>` : null}
    ${rows && rows.length === 0 ? html`<p class="set-intro" role="status">${KEYS_NONE_WORDS}</p>` : null}
    ${rows && rows.length
      ? html`<ul class="key-list" aria-label="Agents and their keys">
          ${rows.map((k) => html`<li key=${k.agent} class="key-row" data-key=${k.agent}>
            <div class="key-main">
              <div class="key-title"><span class="key-name">${k.name}</span>${k.name !== k.agent ? html`<span class="key-id">${k.agent}</span>` : null}</div>
              <div class="key-status">${keyStatus(k)}</div>
              ${keyNotes(k).map((n, i) => html`<div class="key-note" key=${i}>${n}</div>`)}
            </div>
            ${remote || k.notNeeded
              ? null
              : html`<div class="key-actions">
                  <button type="button" class="dialog-btn" id=${"key-set-" + encodeURIComponent(k.agent)} ref=${(el) => { buttons.current[k.agent] = el; }} aria-label=${(k.set ? "Replace" : "Set") + " the key for " + k.name}
                    aria-expanded=${editing === k.agent ? "true" : "false"} onClick=${() => { setAsking(""); setEditing(editing === k.agent ? "" : k.agent); }}>${k.set ? "Replace…" : "Set…"}</button>
                  ${canClear(k)
                    ? html`<button type="button" class="dialog-btn dialog-btn--danger" aria-label=${"Clear the stored key for " + k.name}
                        onClick=${() => { setEditing(""); setAsking(asking === k.agent ? "" : k.agent); }}>Clear</button>`
                    : null}
                </div>`}
            ${editing === k.agent && !remote
              ? html`<div class="key-form">
                  <input type="password" class="dialog-field" id="key-field" ref=${field} autocomplete="off" spellcheck="false"
                    placeholder=${"Paste the key for " + k.agent} aria-label=${"Key for " + k.name} onKeyDown=${(e) => onFieldKey(e, k.agent)} />
                  <button type="button" class="dialog-btn" id="key-save" onClick=${() => save(k.agent)}>Save</button>
                  <button type="button" class="dialog-btn" id="key-cancel" onClick=${() => cancel(k.agent)}>Cancel</button>
                </div>`
              : null}
            ${asking === k.agent && !remote
              ? html`<${Confirm} title=${"Clear the stored key for " + k.name + "?"}
                  words=${["Flockdeck forgets the key it holds. " + (k.source === "env" ? "The variable in its environment is not touched, and goes on being used." : "The agent has no key until you set one.")]}
                  label="Clear the key" onConfirm=${() => clear(k.agent)} onCancel=${() => setAsking("")} />`
              : null}
          </li>`)}
        </ul>`
      : null}
    ${remote ? html`<p class="set-intro" id="keys-relay">${KEYS_RELAY_WORDS}</p>` : null}
  `;
}
