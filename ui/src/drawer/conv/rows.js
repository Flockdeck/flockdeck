// The rows of a conversation: one component per entry, for any pane kind. The
// entry kinds are the ones transcript.Stream carries: prompt, reply, thinking,
// tool, subagent, compaction, notice and image. A kind the page does not know is
// drawn as an "Unknown entry" row, so a new kind on the desktop shows up as
// something to report and not as a gap.
//
// - A prompt is the person's words, drawn as text in a bubble on the right; a long one is cut, with Show all and Copy.
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
import { CopyReply } from "./CopyReply.js";
import { ToolBody } from "./Tool.js";
import { ImageBody, imageWords } from "./Image.js";
import { PromptPics } from "./PromptPics.js";
import { stepsWords } from "./steps.js";
import { chunkText } from "./chunks.js";
import { rowTime } from "./time.js";

/** MAX_TEXT cuts a prompt, a notice or a title. */
export const MAX_TEXT = 4000;
/** MAX_REPLY is how much of one reply is drawn as Markdown. */
export const MAX_REPLY = 20000;
/** MAX_SUMMARY cuts the summary an opened tool row shows. */
export const MAX_SUMMARY = 2000;
export const NO_SUMMARY_WORDS = "Nothing more is held for this call.";
/** Show all draws a long prompt or reply to these ceilings, in pieces of CHUNK_TEXT and CHUNK_REPLY
 *  characters; past a ceiling the row says how much is left out, and Copy still gives the whole text. */
export const EXPAND_MAX_TEXT = 100000;
export const EXPAND_MAX_REPLY = 200000;
export const CHUNK_TEXT = 4000;
export const CHUNK_REPLY = 20000;
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

/** Foot is the line under a prompt or reply: its time, then its actions. actions says there is at
 *  least one (the children are an array, which is truthy even when every action is null). keep says
 *  an action is on it that must stay visible (Show all, Retry), so the line is not left for hover and focus. */
function Foot({ ts, actions = false, keep = false, children }) {
  const t = rowTime(ts);
  if (!t && !actions) return null;
  return html`<div class=${"conv-foot" + (keep ? " is-kept" : "")}>${t ? html`<time class="conv-time" datetime=${t.iso} title=${t.full}>${t.label}</time>` : null}${children}</div>`;
}

function ShowAll({ all, onToggle }) {
  return html`<button type="button" class="conv-showall" aria-expanded=${all ? "true" : "false"} onClick=${onToggle}>${all ? "Show less" : "Show all"}</button>`;
}

/** PromptRow is the person's words in a bubble on the right. A long one is cut with Show all; Copy
 *  gives the whole text. onRetry is given to the newest prompt only, and sends its text again. Retry
 *  is drawn only on a prompt the person typed (entry.typed, which the app sets) and that has no
 *  pictures: the pictures may be gone by now, so a retry would send the text without them. A prompt
 *  with pictures draws them as thumbnails (PromptPics), or says how many it had when none can be asked for. */
function PromptRow({ entry: e, nested, onRetry, details, onDetail }) {
  const [all, setAll] = useState(false);
  const full = text(e.text);
  const cut = full.length > MAX_TEXT;
  const shown = !cut ? full : all ? full.slice(0, EXPAND_MAX_TEXT) : full.slice(0, MAX_TEXT);
  const left = full.length - shown.length;
  const pics = Array.isArray(e.images) ? e.images.length : 0;
  const retry = !!onRetry && !nested && e.typed === true && pics === 0;
  const body = !cut ? clip(full, MAX_TEXT) : all ? chunkText(shown, CHUNK_TEXT).map((c, i) => html`<span key=${i}>${c}</span>`) : shown;
  return html`<li class="conv-row conv-prompt" data-ctx="convRow" data-kind="prompt" data-entry=${nested ? null : e.id} tabindex="-1">
    <div class="conv-bubble"><span class="conv-sr">You: </span><p class="conv-text">${body}</p></div>
    ${left > 0 ? html`<p class="conv-cut">${left.toLocaleString("en-US")} more characters are not shown.</p>` : null}
    ${pics > 0 ? html`<${PromptPics} entry=${e} details=${details} onDetail=${nested ? null : onDetail} />` : null}
    <${Foot} ts=${e.ts} actions=${cut || !nested || retry} keep=${cut || retry}>
      ${cut ? html`<${ShowAll} all=${all} onToggle=${() => setAll(!all)} />` : null}
      ${nested ? null : html`<${CopyReply} text=${full} label="Copy this prompt" />`}
      ${retry ? html`<button type="button" class="conv-retry" aria-label="Retry this prompt" onClick=${() => onRetry(full)}>Retry</button>` : null}
    <//>
  </li>`;
}

/** ReplyRow is the agent's words as Markdown, the width of the list and with no bubble. A long one is
 *  cut with Show all, which draws it in pieces, each its own Markdown node. */
function ReplyRow({ entry: e, nested }) {
  const [all, setAll] = useState(false);
  const full = text(e.markdown || e.text);
  const cut = full.length > MAX_REPLY;
  const shown = !cut ? full : all ? full.slice(0, EXPAND_MAX_REPLY) : full.slice(0, MAX_REPLY);
  const left = full.length - shown.length;
  return html`<li class="conv-row conv-reply" data-ctx="convRow" data-kind="reply" data-entry=${nested ? null : e.id} tabindex="-1">
    <span class="conv-sr">Agent: </span>
    ${cut && all
      ? chunkText(shown, CHUNK_REPLY, true).map((c, i) => html`<${Markdown} key=${i} text=${c} />`)
      : html`<${Markdown} text=${shown} />`}
    ${left > 0 ? html`<p class="conv-cut">${left.toLocaleString("en-US")} more characters are not shown.</p>` : null}
    <${Foot} ts=${e.ts} actions=${cut || !nested} keep=${cut}>
      ${cut ? html`<${ShowAll} all=${all} onToggle=${() => setAll(!all)} />` : null}
      ${nested ? null : html`<${CopyReply} text=${full} />`}
    <//>
  </li>`;
}

/** Row is one entry. open, onToggle, detail and nested are for a tool or helper row. */
export function Row({ entry: e, open = false, onToggle = null, detail = null, nested = false, onRetry = null, details = null, onDetail = null }) {
  const kind = text(e.kind);
  switch (kind) {
    case "prompt":
      return html`<${PromptRow} entry=${e} nested=${nested} onRetry=${onRetry} details=${details} onDetail=${onDetail} />`;
    case "reply":
      return html`<${ReplyRow} entry=${e} nested=${nested} />`;
    case "thinking":
      return html`<li class="conv-row conv-thought" data-kind="thinking" data-entry=${nested ? null : e.id} tabindex="-1">${thoughtWords(e)}</li>`;
    case "tool":
      return html`<${ToolRow} entry=${e} label=${text(e.label) || text(e.name) || "Tool"} kind=${toolKind(e.name)} open=${open} onToggle=${onToggle} detail=${detail} nested=${nested} />`;
    case "subagent":
      return html`<${ToolRow} entry=${e} label=${"Helper: " + (text(e.description) || text(e.name) || "agent")} kind="other" open=${open} onToggle=${onToggle} detail=${detail} nested=${nested} />`;
    case "compaction":
      return html`<li class="conv-row conv-note" data-kind="compaction" data-entry=${nested ? null : e.id} tabindex="-1">${typeof e.turns === "number" ? "Conversation compacted (" + e.turns + " turns)" : "Conversation compacted"}</li>`;
    case "notice":
      // A tone the page does not know is a plain notice.
      if (e.tone === "failure") return html`<li class="conv-row conv-note conv-note--failure" data-kind="notice" data-tone="failure" data-entry=${nested ? null : e.id} tabindex="-1">${clip(text(e.text), 400)}</li>`;
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
