// The drawer's conversation tab: a read view of what the agent and the person
// said, as a flat list, until stage 3 replaces the drawing and keeps the data
// path.
//
// - Prompts are the person's words, replies the agent's, each as text.
// - A thought is one line (how long it ran, or how long it was).
// - A tool call is a row with an icon for its kind, its title and its status.
//   No diff and no full output is drawn here.
// - The list is the newest page the desktop sent and what came after it. The
//   Older button asks for the page before the first entry held, once at a time,
//   and is not there at the start of the conversation.
// - The list follows new entries when it is already at the bottom, and stays where
//   it is when the person has scrolled up.
//
// Everything on a row is what an agent, a tool or a person wrote. It is drawn as
// text, never as Markdown or markup, and long text is cut.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";

export const MAX_TEXT = 4000;
export const NO_ENTRIES_WORDS = "Nothing has been said yet.";
export const LOADING_WORDS = "Loading the conversation.";

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

function Icon({ kind }) {
  return html`<svg class=${"conv-icon conv-icon--" + kind} viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false"><path d=${ICONS[kind] || ICONS.other} fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>`;
}

const text = (v) => (typeof v === "string" ? v : v == null ? "" : String(v));

/** seconds words a thought's length. */
function thoughtWords(e) {
  if (typeof e.seconds === "number" && e.seconds >= 0) return "Thought for " + e.seconds + (e.seconds === 1 ? " second" : " seconds");
  if (typeof e.chars === "number" && e.chars > 0) return "Thought (" + e.chars + " characters)";
  return "Thought";
}

/** Row is one entry. */
export function Row({ entry: e }) {
  const kind = text(e.kind);
  switch (kind) {
    case "prompt":
      return html`<li class="conv-row conv-prompt" data-kind="prompt" data-entry=${e.id}><span class="conv-who">You</span><p class="conv-text">${clip(text(e.text), MAX_TEXT)}</p></li>`;
    case "reply":
      return html`<li class="conv-row conv-reply" data-kind="reply" data-entry=${e.id}><span class="conv-who">Agent</span><p class="conv-text">${clip(text(e.markdown || e.text), MAX_TEXT)}</p></li>`;
    case "thinking":
      return html`<li class="conv-row conv-thought" data-kind="thinking" data-entry=${e.id}>${thoughtWords(e)}</li>`;
    case "tool": {
      const status = text(e.status);
      const title = text(e.label) || text(e.name) || "Tool";
      return html`<li class=${"conv-row conv-tool" + (status ? " conv-tool--" + status.replace(/[^a-z]/g, "") : "")} data-kind="tool" data-entry=${e.id}>
        <${Icon} kind=${toolKind(e.name)} />
        <span class="conv-tool-title">${clip(title, 160)}</span>
        ${status ? html`<span class="conv-status">${status}</span>` : null}
      </li>`;
    }
    case "subagent": {
      const status = text(e.status);
      return html`<li class=${"conv-row conv-tool" + (status ? " conv-tool--" + status.replace(/[^a-z]/g, "") : "")} data-kind="subagent" data-entry=${e.id}>
        <${Icon} kind="other" />
        <span class="conv-tool-title">${clip("Helper: " + (text(e.description) || text(e.name) || "agent"), 160)}</span>
        ${status ? html`<span class="conv-status">${status}</span>` : null}
      </li>`;
    }
    case "compaction":
      return html`<li class="conv-row conv-note" data-kind="compaction" data-entry=${e.id}>${typeof e.turns === "number" ? "Conversation compacted (" + e.turns + " turns)" : "Conversation compacted"}</li>`;
    case "notice":
      return html`<li class="conv-row conv-note" data-kind="notice" data-entry=${e.id}>${clip(text(e.text), 400)}</li>`;
    case "image": {
      const size = e.width && e.height ? ", " + e.width + " by " + e.height : "";
      return html`<li class="conv-row conv-note" data-kind="image" data-entry=${e.id}>${"Image (" + text(e.mediaType || "unknown type") + size + ")"}</li>`;
    }
    default:
      return html`<li class="conv-row conv-note" data-kind="other" data-entry=${e.id}>${clip(kind || "entry", 80)}</li>`;
  }
}

/** Conversation is the tab's panel. conv is the store's conversation, onOlder asks
 *  for the page before. */
export function Conversation({ conv, onOlder }) {
  const listRef = useRef(null);
  // Whether the list was at the bottom before the last change, and the height it
  // had, so a page added above does not move what is being read.
  const stick = useRef(true);
  const was = useRef({ height: 0, first: "" });
  const entries = conv ? conv.entries : [];
  const first = entries.length > 0 ? entries[0].id : "";

  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    if (was.current.first && first && first !== was.current.first && !stick.current) {
      // Entries were added above: keep the same entries in view.
      el.scrollTop += el.scrollHeight - was.current.height;
    } else if (stick.current) {
      el.scrollTop = el.scrollHeight;
    }
    was.current = { height: el.scrollHeight, first };
  }, [entries.length, first, conv && conv.entries]);

  const onScroll = () => {
    const el = listRef.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  if (!conv) return html`<p class="drawer-stub">${LOADING_WORDS}</p>`;
  if (conv.refused) return html`<p class="drawer-stub conv-refused" role="status">${conv.refused}</p>`;
  if (conv.supported !== true) return html`<p class="drawer-stub">${LOADING_WORDS}</p>`;
  return html`
    <div class="conv" ref=${listRef} onScroll=${onScroll} tabindex="0" role="log" aria-label="Conversation">
      ${conv.atStart ? null : html`<button type="button" class="conv-older" disabled=${conv.older === "loading"} onClick=${onOlder}>${conv.older === "loading" ? "Loading…" : "Older"}</button>`}
      ${entries.length === 0
        ? html`<p class="conv-empty">${NO_ENTRIES_WORDS}</p>`
        : html`<ol class="conv-list">${entries.map((e) => html`<${Row} key=${e.id} entry=${e} />`)}</ol>`}
    </div>
  `;
}
