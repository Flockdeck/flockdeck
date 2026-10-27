// The rows of a conversation: one component per entry, for any pane kind. The
// entry kinds are the ones transcript.Stream carries: prompt, reply, thinking,
// tool, subagent, compaction, notice and image. A kind the page does not know is
// drawn as an "Unknown entry" row, so a new kind on the desktop shows up as
// something to report and not as a gap.
//
// - A prompt is the person's words, drawn as text.
// - A reply is the agent's words, drawn as sanitised Markdown (markdown.js); over
//   MAX_REPLY characters the rest is left out and the row says how much.
// - A message is mail between agents (kind "message"): a message from a peer, or one
//   this agent sent, with its delivery state. Its words are plain text on a row of
//   their own and are never drawn as the person's turn, whatever they say.
// - A thought is one line (how long it ran, or how long it was).
// - A tool call is a row with an icon for its kind, its title and its status.
//   Opening it (a click, or Enter on the row) shows its summary and its output or
//   diff (Tool.js, Diff.js), and a helper agent's own entries one level down.
// - Everything else is a short line.
//
// Everything on a row is what an agent, a tool or a person wrote. None of it is
// put into the page as markup.
import { html } from "../../html.js";
import { clip } from "../../graph/Node.js";
import { useState } from "../../vendor/hooks.mjs";
import { Markdown } from "./markdown.js";
import { ToolBody } from "./Tool.js";
import { ImageBody, imageWords } from "./Image.js";
import { stepsWords } from "./steps.js";

/** MAX_TEXT cuts a prompt, a notice or a title. */
export const MAX_TEXT = 4000;
/** MAX_REPLY is how much of one reply is drawn as Markdown. */
export const MAX_REPLY = 20000;
/** MAX_SUMMARY cuts the summary an opened tool row shows. */
export const MAX_SUMMARY = 2000;
export const NO_SUMMARY_WORDS = "Nothing more is held for this call.";
/** MAX_NESTED is how many of a helper agent's own entries are drawn. */
export const MAX_NESTED = 200;

/** toolKind is the kind of icon a tool's row gets, from its name. */
export function toolKind(name) {
  const n = String(name || "").toLowerCase();
  if (/^(bash|shell|run|exec|task|powershell)/.test(n)) return "run";
  if (/^(edit|multiedit|write|notebookedit|patch|apply)/.test(n)) return "edit";
  if (/^(read|view|open|ls|list)/.test(n)) return "read";
  if (/^(grep|glob|search|find)/.test(n)) return "search";
  if (/^(web|fetch|http|browse)/.test(n)) return "web";
  return "other";
}

// One path for each kind, in a 16 by 16 box. They are drawn with the text's colour.
const ICONS = {
  run: "M2 3h12v10H2zM4.5 6l2 2-2 2M8 10h3.5",
  edit: "M3 13l.7-3L10.5 3.2l2.3 2.3L6 12.3zM9.4 4.3l2.3 2.3",
  read: "M3 2.5h7l3 3v8H3zM10 2.5v3h3",
  search: "M7 2.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM10.3 10.3l3.2 3.2",
  web: "M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2zM2 8h12M8 2c2 2 2 10 0 12M8 2c-2 2-2 10 0 12",
  other: "M3 3h10v10H3zM6 8h4",
};

export function Icon({ kind }) {
  return html`<svg class=${"conv-icon conv-icon--" + kind} viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false"><path d=${ICONS[kind] || ICONS.other} fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>`;
}

/** DELIVERY_WORDS are the four delivery states of a message row. Any other value is not shown. */
export const DELIVERY_WORDS = { queued: "Queued", delivered: "Delivered", handled: "Handled", undeliverable: "Not delivered" };

// Controls, line breaks and bidirectional marks: a label is one plain line.
// eslint-disable-next-line no-control-regex
const NOT_PLAIN = /[\u0000-\u001f\u007f-\u009f\u200b-\u200f\u2028-\u202e\u00ad\u061c\u180e\u2060-\u2069\u3164\ufeff]/g;

/** plainLabel is a peer's label as one clean line of at most 60 characters. */
export function plainLabel(v, fallback) {
  const s = String(v == null ? "" : v).replace(NOT_PLAIN, " ").replace(/\s+/g, " ").trim();
  return clip(s || fallback, 60);
}

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));

/** thoughtWords words a thought's length. */
function thoughtWords(e) {
  if (typeof e.seconds === "number" && e.seconds >= 0) return "Thought for " + e.seconds + (e.seconds === 1 ? " second" : " seconds");
  if (typeof e.chars === "number" && e.chars > 0) return "Thought (" + e.chars + " characters)";
  return "Thought";
}

/** statusClass is a status as a class suffix: letters only. */
const statusClass = (status) => (status ? " conv-tool--" + status.replace(/[^a-z]/g, "") : "");

// A helper agent's own entries, one level: the rows in it are given no detail, so a helper inside a helper opens to its summary and no list.
function Nested({ entries }) {
  const [opened, setOpened] = useState({});
  const list = entries.filter((n) => n && typeof n === "object" && typeof n.id === "string" && n.id);
  const toggle = (id) => setOpened((o) => ({ ...o, [id]: !o[id] }));
  return html`<ol class="conv-nested" aria-label="The helper's steps">
    ${list.slice(0, MAX_NESTED).map((n) => html`<${Row} key=${n.id} entry=${n} nested=${true} open=${!!opened[n.id]} onToggle=${toggle} />`)}
    ${list.length > MAX_NESTED ? html`<li class="conv-cut">${(list.length - MAX_NESTED).toLocaleString("en-US")} more steps are not shown.</li>` : null}
  </ol>`;
}

/** ToolRow is a tool call or a helper agent. open and onToggle are the list's, so
 *  the keyboard and the mouse change the same thing. detail is the store's answer
 *  for the entry. nested says the row is inside a helper's list. */
function ToolRow({ entry: e, label, kind, open, onToggle, detail, nested }) {
  const status = text(e.status);
  const summary = text(e.summary);
  const inner = detail && detail.state === "ok" && detail.detail && Array.isArray(detail.detail.entries) ? detail.detail.entries : null;
  return html`<li class=${"conv-row conv-tool" + statusClass(status) + (open ? " is-open" : "")} data-ctx="convRow" data-kind=${e.kind} data-entry=${nested ? null : e.id} data-nested=${nested ? "" : null} tabindex="-1">
    <button type="button" class="conv-tool-head" aria-expanded=${open ? "true" : "false"} onClick=${onToggle ? () => onToggle(e.id) : null}>
      <${Icon} kind=${kind} />
      <span class="conv-tool-title">${clip(label, 160)}</span>
      ${status ? html`<span class="conv-status">${status}</span>` : null}
    </button>
    ${open
      ? html`<p class="conv-tool-summary">${summary ? clip(summary, MAX_SUMMARY) : NO_SUMMARY_WORDS}</p>
        ${e.kind === "subagent" && e.report ? html`<p class="conv-tool-report">${clip(text(e.report), MAX_TEXT)}</p>` : null}
        <${ToolBody} entry=${nested ? { ...e, hasDetail: false } : e} detail=${detail} />
        ${inner ? html`<${Nested} entries=${inner} />` : null}`
      : null}
  </li>`;
}

/** Row is one entry. open, onToggle, detail and nested are for a tool or helper row. */
export function Row({ entry: e, open = false, onToggle = null, detail = null, nested = false }) {
  const kind = text(e.kind);
  switch (kind) {
    case "prompt":
      return html`<li class="conv-row conv-prompt" data-ctx="convRow" data-kind="prompt" data-entry=${nested ? null : e.id} tabindex="-1"><span class="conv-who">You</span><p class="conv-text">${clip(text(e.text), MAX_TEXT)}</p></li>`;
    case "reply": {
      const full = text(e.markdown || e.text);
      const cut = full.length > MAX_REPLY;
      return html`<li class="conv-row conv-reply" data-ctx="convRow" data-kind="reply" data-entry=${nested ? null : e.id} tabindex="-1"><span class="conv-who">Agent</span>
        <${Markdown} text=${cut ? full.slice(0, MAX_REPLY) : full} />
        ${cut ? html`<p class="conv-cut">${(full.length - MAX_REPLY).toLocaleString("en-US")} more characters are not shown.</p>` : null}
      </li>`;
    }
    case "thinking":
      return html`<li class="conv-row conv-thought" data-kind="thinking" data-entry=${nested ? null : e.id} tabindex="-1">${thoughtWords(e)}</li>`;
    case "tool":
      return html`<${ToolRow} entry=${e} label=${text(e.label) || text(e.name) || "Tool"} kind=${toolKind(e.name)} open=${open} onToggle=${onToggle} detail=${detail} nested=${nested} />`;
    case "subagent":
      return html`<${ToolRow} entry=${e} label=${"Helper: " + (text(e.description) || text(e.name) || "agent")} kind="other" open=${open} onToggle=${onToggle} detail=${detail} nested=${nested} />`;
    case "compaction":
      return html`<li class="conv-row conv-note" data-kind="compaction" data-entry=${nested ? null : e.id} tabindex="-1">${typeof e.turns === "number" ? "Conversation compacted (" + e.turns + " turns)" : "Conversation compacted"}</li>`;
    case "notice":
      return html`<li class="conv-row conv-note" data-kind="notice" data-entry=${nested ? null : e.id} tabindex="-1">${clip(text(e.text), 400)}</li>`;
    case "message": {
      const out = e.dir === "out";
      const state = Object.prototype.hasOwnProperty.call(DELIVERY_WORDS, e.delivery) ? e.delivery : "";
      return html`<li class=${"conv-row conv-message conv-message--" + (out ? "out" : "in") + (state === "undeliverable" ? " is-failed" : "")} data-ctx="convRow" data-kind="message" data-entry=${nested ? null : e.id} tabindex="-1">
        <span class="conv-who">${out ? `Message to ${plainLabel(e.peer, "another agent")}` : `Message from ${plainLabel(e.peer, "an agent")}`}${state ? html` <span class="conv-mail-chip">${DELIVERY_WORDS[state]}</span>` : null}</span>
        <p class="conv-text">${clip(text(e.text), MAX_TEXT)}</p>
      </li>`;
    }
    case "mail":
      return html`<li class=${"conv-row conv-mail" + (e.waiting ? " is-waiting" : "")} data-ctx="convRow" data-kind="mail" data-entry=${nested ? null : e.id} tabindex="-1">
        <span class="conv-who">${"From " + clip(text(e.label) || text(e.from) || "an agent", 60)}${e.waiting ? html` <span class="conv-mail-chip">not read yet</span>` : null}</span>
        <p class="conv-text">${clip(text(e.text), MAX_TEXT)}</p>
        ${e.cutKnown && !e.maybeCut ? null : html`<p class="conv-cut">${e.cutKnown ? "More of this message. " : e.maybeCut ? "May be cut. " : "First line only. "}The whole message is in ${clip(text(e.label) || text(e.from) || "an agent", 60)}'s conversation.</p>`}
      </li>`;
    case "event":
      return html`<li class=${"conv-row conv-note conv-event conv-event--" + (e.tone === "down" ? "down" : "ship")} data-kind="event" data-entry=${nested ? null : e.id} tabindex="-1">${clip(text(e.text), 300)}</li>`;
    case "image":
      return html`<li class=${"conv-row conv-image-row" + (open ? " is-open" : "")} data-kind="image" data-entry=${nested ? null : e.id} data-nested=${nested ? "" : null} tabindex="-1">
        ${e.hasDetail && onToggle
          ? html`<button type="button" class="conv-tool-head" aria-expanded=${open ? "true" : "false"} onClick=${() => onToggle(e.id)}>${imageWords(e)}</button>`
          : html`<span class="conv-note">${imageWords(e)}</span>`}
        ${open ? html`<${ImageBody} entry=${e} detail=${detail} />` : null}
      </li>`;
    default:
      return html`<li class="conv-row conv-note conv-unknown" data-kind="other" data-entry=${nested ? null : e.id} tabindex="-1">${"Unknown entry" + (kind ? " (" + clip(kind, 80) + ")" : "")}</li>`;
  }
}

/** StepsRow is the header of a run of tool rows: "N steps", open or closed. The
 *  rows themselves are drawn by the list after it when it is open. */
export function StepsRow({ id, steps, open, onToggle }) {
  return html`<li class=${"conv-row conv-steps" + (open ? " is-open" : "")} data-kind="steps" data-entry=${id} tabindex="-1">
    <button type="button" class="conv-tool-head conv-steps-head" aria-expanded=${open ? "true" : "false"} onClick=${() => onToggle(id)}>
      <span class="conv-steps-caret" aria-hidden="true">${open ? "▾" : "▸"}</span>
      <span class="conv-tool-title">${stepsWords(steps)}</span>
    </button>
  </li>`;
}
