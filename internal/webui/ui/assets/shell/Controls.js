// The parts the sections of the settings dialog are made of: a setting's row and
// its on/off switch. They live here so that a section in its own file (API keys,
// Remote, Notifications) can use them without importing the dialog that imports it.
import { html } from "../html.js";

/** Row is a setting's name, a line about it, and its control. */
export function Row({ id, label, about, children, error }) {
  return html`
    <div class="set-row">
      <div class="set-text">
        <div class="set-label" id=${id + "-label"}>${label}</div>
        ${about ? html`<div class="set-about" id=${id + "-about"}>${about}</div>` : null}
        ${error ? html`<div class="set-error" role="alert">${error}</div>` : null}
      </div>
      <div class="set-control">${children}</div>
    </div>
  `;
}

/** Switch is an on/off control. */
export function Switch({ id, on, onChange, labelledBy, disabled = false }) {
  return html`<button type="button" role="switch" class=${"set-switch" + (on ? " is-on" : "")} id=${id} disabled=${disabled}
    aria-checked=${on ? "true" : "false"} aria-labelledby=${labelledBy} onClick=${() => onChange(!on)}><span class="set-knob"></span></button>`;
}
