// Pane info: what names one pane in files, logs and commands (ids, process id,
// folders, the conversation it holds, the transcript file), each value with a
// button to copy it. The values come from the desktop on request, for this window
// only; they are not in the state every window is sent.
//
// The transcript file has one more button, Show in folder, which sends
// revealTranscript for the pane. With no file there is nothing to show, so the
// button is off and the reason is written under it.
//
// Every value is drawn as text. A refusal that comes in place of the answer (the
// pane closed meanwhile) takes the place of "Loading…".
import { html } from "../html.js";
import { Dialog } from "./Dialog.js";
import { NOT_SET, REVEAL_NONE_WORDS, infoOf, infoText } from "../lib/paneinfo.js";

/** PaneInfo is the dialog. pane is the id asked about, info the store's latest
 *  paneInfo answer and floor the seq it had when this dialog asked (an answer at or
 *  before it is an older one). failure is the desktop's refusal, if one came after
 *  the ask. copy(text, what) puts text on the clipboard; send is the store's send. */
export function PaneInfo({ pane, info, floor, failure = "", copy, send, onClose }) {
  const got = info && info.seq > floor && info.id === pane ? infoOf(info) : null;
  return html`
    <${Dialog} id="pane-info" title="Pane info" onClose=${onClose} wide=${true}>
      ${!got && !failure ? html`<p class="dialog-note" role="status">Loading…</p>` : null}
      ${!got && failure ? html`<p class="dialog-note dialog-note--error" role="alert">${failure}</p>` : null}
      ${got
        ? html`<p class="dialog-note">What names ${got.name ? "“" + got.name + "”" : "this pane"} in files, logs and commands. Nothing here is a secret, but a path can name your folders.</p>
          <dl class="pi-list">
            ${got.fields.map((f) => html`<div class="pi-row" key=${f.label} data-key=${f.label}>
              <div class="pi-head">
                <dt class="pi-label">${f.label}</dt>
                ${f.label === "Transcript file"
                  ? html`<button type="button" class="dialog-btn pi-btn" disabled=${!f.value} aria-describedby=${f.value ? undefined : "pane-info-reveal-why"}
                      aria-label="Show the transcript file in your file manager" onClick=${() => send({ cmd: "revealTranscript", id: pane })}>Show in folder</button>`
                  : null}
                ${f.value
                  ? html`<button type="button" class="dialog-btn pi-btn" aria-label=${"Copy " + f.label.toLowerCase()} onClick=${() => copy(f.value, f.label.toLowerCase())}>Copy</button>`
                  : null}
              </div>
              <dd class=${"pi-value" + (f.value ? "" : " is-unset")}>${f.value || NOT_SET}</dd>
              ${f.note ? html`<div class="pi-note">${f.note}</div>` : null}
              ${f.label === "Transcript file" && !f.value
                ? html`<div class="pi-note" id="pane-info-reveal-why">${got.transcriptOff || REVEAL_NONE_WORDS}</div>`
                : null}
            </div>`)}
          </dl>`
        : null}
      <div class="dialog-foot">
        ${got ? html`<button type="button" class="dialog-btn dialog-btn--primary" id="pane-info-copy-all" onClick=${() => copy(infoText(got.fields), "all pane info")}>Copy all</button>` : null}
        <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
      </div>
    <//>
  `;
}
