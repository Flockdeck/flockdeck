// What the drawer offers for finding your way about the pane it shows and for
// keeping a record of it: its info, a search of its terminal, recording and
// exporting its transcript.
//
// Recording keeps what the agent and the person said, secrets included, so the
// first recording ever is put to the person first, and every export is. The
// sentences are written in lib/paneinfo.js and contain nothing an agent wrote. The
// desktop checks the answer too, so a window that skipped the question would be
// told so.
//
// A shell has no agent events to record and a structured pane has no terminal
// transcript, so neither is offered recording or export. A window reached through
// the relay is offered neither pane info nor export: the desktop shows those on its
// own machine only.
import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Confirm } from "../shell/Confirm.js";
import { STRUCTURED_RECORD_WORDS, askFor, canTranscribe, transcriptCommand } from "../lib/paneinfo.js";

/** useTranscripts holds the question waiting for an answer and sends the commands.
 *  record(pane, on) starts or stops a recording (asking first for the first one
 *  ever, ack saying whether it has been); exportTranscript(pane) always asks;
 *  confirm sends the open one; cancel drops it. */
export function useTranscripts(send, ack) {
  const [request, setRequest] = useState(null);
  const sendRef = useRef(send);
  sendRef.current = send;
  const ackRef = useRef(ack);
  ackRef.current = ack;
  return {
    request,
    record(pane, on) {
      const ask = on ? askFor("record", ackRef.current) : null;
      if (ask) setRequest({ kind: "record", pane, ask });
      else sendRef.current(transcriptCommand("record", pane, { on }));
    },
    exportTranscript(pane) {
      setRequest({ kind: "export", pane, ask: askFor("export", true) });
    },
    confirm() {
      if (request) sendRef.current(transcriptCommand(request.kind, request.pane, { on: true, confirmed: true }));
      setRequest(null);
    },
    cancel() {
      setRequest(null);
    },
  };
}

/** Tools is the row of buttons and, under them, the question for a request waiting
 *  on an answer. tx is useTranscripts; remote says the window is reached through
 *  the relay; terminal says the pane has a terminal to search. */
/** AUTO_REVIEW_WORDS says what the switch does, as its tooltip and its description. */
export const AUTO_REVIEW_WORDS = "Only a command Flockdeck is confident is read-only is let through without asking; anything else still asks.";
export const AUTO_REVIEW_SHELL_WORDS = "A shell asks no permission, so there is nothing to auto-approve.";

/** autoReviewCommand turns auto-approval of read-only commands on or off for one pane. */
export const autoReviewCommand = (id, on) => ({ cmd: "autoReview", id, autoReview: !!on });

export function Tools({ node, pane = null, tx, remote = false, terminal = true, finding = false, onInfo, onFind, send = null }) {
  const can = canTranscribe(node, pane);
  const recording = !!(pane && pane.recording);
  const ask = tx.request && tx.request.pane === node.pane ? tx.request.ask : null;
  const shell = !!(node.shell || (pane && pane.shell));
  const review = !!((pane && pane.autoReview) || node.autoReview);
  return html`
    <div class="tools" role="group" aria-label="Find and record">
      ${remote ? null : html`<button type="button" class="drawer-btn tools-info" onClick=${onInfo}>Pane info</button>`}
      ${send && !shell
        ? html`<button type="button" class="drawer-btn tools-review" aria-pressed=${review ? "true" : "false"} title=${AUTO_REVIEW_WORDS}
            onClick=${() => send(autoReviewCommand(node.pane, !review))}>Auto-approve read-only: ${review ? "on" : "off"}</button>`
        : null}
      ${terminal ? html`<button type="button" class="drawer-btn tools-find" aria-pressed=${finding ? "true" : "false"} onClick=${onFind}>Find in terminal</button>` : null}
      ${can
        ? html`<button type="button" class="drawer-btn tools-record" aria-pressed=${recording ? "true" : "false"} onClick=${() => tx.record(node.pane, !recording)}>
            ${recording ? "Stop recording" : "Record"}
          </button>`
        : null}
      ${can && !remote ? html`<button type="button" class="drawer-btn tools-export" onClick=${() => tx.exportTranscript(node.pane)}>Export transcript</button>` : null}
      ${!can && !node.shell && !(pane && pane.shell) ? html`<span class="life-note tools-note">${STRUCTURED_RECORD_WORDS}</span>` : null}
      ${ask
        ? html`<${Confirm} title=${ask.title} words=${ask.words} label=${ask.label} onConfirm=${tx.confirm} onCancel=${tx.cancel} />`
        : null}
    </div>
  `;
}
