// TypeSafe's Jev, in two parts of the settings: the key (under the API keys) and
// the switch that lets it read a quiet pane (Status detection). See lib/jev.js for
// why they are separate and what each says.
//
// The key is typed in a password field that starts empty every time and is emptied
// the moment it is sent. The desktop says only whether one is set. A window
// reached through the relay is shown neither the field nor a Set button.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Row, Switch } from "./Controls.js";
import { JEV_KEY_WORDS, JEV_RELAY_WORDS, JEV_STATUS_WORDS, jevClearCommand, jevGetCommand, jevOf, jevSetCommand, jevWords } from "../lib/jev.js";

/** JevKey is the key's row. jev is the store's latest jevKey answer (or null),
 *  remote says this window came through the relay, send is the store's send. */
export function JevKey({ jev, remote, send }) {
  const field = useRef(null);
  const info = jevOf(jev);
  useEffect(() => {
    if (!remote) send(jevGetCommand());
  }, [remote]);
  const save = () => {
    const el = field.current;
    const cmd = jevSetCommand(el ? el.value : "");
    if (el) el.value = "";
    if (cmd) send(cmd);
  };
  const onKey = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      save();
    }
  };
  return html`
    <${Row} id="set-jev-key" label="TypeSafe API key" about=${JEV_KEY_WORDS}>
      ${remote
        ? null
        : html`<div class="key-form">
            <input type="password" class="dialog-field" id="set-jev-key-field" ref=${field} autocomplete="off" spellcheck="false"
              placeholder=${info && info.set ? "Paste a new key to replace it" : "Paste your TypeSafe API key"} aria-label="TypeSafe API key" onKeyDown=${onKey} />
            <button type="button" class="dialog-btn" id="set-jev-key-save" onClick=${save}>${info && info.set ? "Replace" : "Save"}</button>
            ${info && info.set
              ? html`<button type="button" class="dialog-btn dialog-btn--danger" id="set-jev-key-clear"
                  onClick=${() => { if (field.current) field.current.value = ""; send(jevClearCommand()); }}>Clear</button>`
              : null}
          </div>`}
      <div class="key-status" id="set-jev-key-status">Key status: ${jevWords(info, remote)}</div>
    <//>
  `;
}

/** JevStatus is the section: the key's state with the way to it, and the switch. */
export function JevStatus({ prefs, change, remote, jev, send }) {
  const info = jevOf(jev);
  useEffect(() => {
    if (!remote && send) send(jevGetCommand());
  }, [remote]);
  const on = prefs.jevStatus;
  return html`
    <h3 class="set-head">Status detection</h3>
    <p class="set-intro">Optional help from TypeSafe's Jev reading a pane's state. Off unless you turn it on; nothing is sent until then.</p>
    <${Row} id="set-jev-key-link" label="TypeSafe API key" about=${"Used for the setting below. It is set under API keys. Status: " + (remote ? "set on the machine Flockdeck runs on" : !info ? "checking" : info.set ? "set" : "not set") + "."}><//>
    <${Row} id="set-jev-status" label="Let TypeSafe's Jev help read pane status" about=${JEV_STATUS_WORDS + " " + JEV_RELAY_WORDS}>
      <${Switch} id="set-jev-status-switch" on=${on} labelledBy="set-jev-status-label"
        disabled=${remote && !on} onChange=${(next) => change("jevStatus", next)} />
    <//>
  `;
}
