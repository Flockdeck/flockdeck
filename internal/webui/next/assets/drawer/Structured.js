// The drawer's view of a structured pane, which has no terminal. It draws the
// latest structured push of the pane being watched (store.structured): the
// activity line, the context used of its size and the cost when the agent
// reported one, the last MAX_ENTRIES entries of the conversation, and a prompt
// box with a Stop button.
//
// - The activity line is the tool still running, with an icon for its kind and
//   the first file its summary names; with none running it is the push's own
//   activity text, and with that empty it says the agent is idle.
// - Only the last MAX_ENTRIES entries are drawn, however many the push holds.
//   There is no Older button: the push carries a fixed window and asking for
//   more would grow the list without limit. The full history is in the
//   Conversation tab, which pages.
// - The prompt box sends paneSend and Stop sends structuredCancel. Both are
//   desk-only commands, so a window reached through the relay draws neither.
//
// Everything on a row came from an agent, a tool or a person. Row draws it as
// text, never as Markdown or markup.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { Row, Icon, toolKind } from "./Conversation.js";

export const MAX_ENTRIES = 50;
/** STOP_NOT_SENT_WORDS is what Stop says when the store did not send it: the socket is down or the hello is not in. */
export const STOP_NOT_SENT_WORDS = "Not connected to Flockdeck, so Stop was not sent.";
// Each Stop carries its own req, so the store can tell which pane a refusal is for.
let cancelSeq = 0;

export const IDLE_WORDS = "Idle";
export const NO_ENTRIES_WORDS = "Nothing has been said yet.";

const finite = (n) => typeof n === "number" && Number.isFinite(n) && n > 0;

/** contextText is "12,400 of 200,000 tokens used", or an empty string when the
 *  agent reported no size. */
export function contextText(usage) {
  if (!usage || !finite(usage.size)) return "";
  const used = finite(usage.used) ? usage.used : 0;
  return used.toLocaleString("en-US") + " of " + usage.size.toLocaleString("en-US") + " tokens used";
}

/** costText is the cost the agent reported, or an empty string. */
export function costText(usage) {
  if (!usage || !finite(usage.cost)) return "";
  const cur = typeof usage.currency === "string" ? usage.currency.replace(/[^A-Za-z$€£¥]/g, "").slice(0, 8) : "";
  return usage.cost.toFixed(2) + (cur ? " " + cur : "");
}

/** fileOf is the first word of a tool summary that looks like a file: it has a
 *  path separator or a short extension. */
export function fileOf(summary) {
  if (typeof summary !== "string") return "";
  for (const w of summary.split(/\s+/)) {
    if (w && (/[\/]/.test(w) || /\.[A-Za-z0-9]{1,6}$/.test(w))) return w;
  }
  return "";
}

/** activityOf is what the agent is doing now: { title, kind, file }. */
export function activityOf(st) {
  const entries = Array.isArray(st && st.entries) ? st.entries : [];
  let running = null;
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i] && entries[i].kind === "tool" && entries[i].status === "running") {
      running = entries[i];
      break;
    }
  }
  const title = (st && typeof st.activity === "string" && st.activity) || (running && (running.label || running.name)) || "";
  if (!title) return null;
  return { title, kind: running ? toolKind(running.name) : "other", file: running ? fileOf(running.summary) : "" };
}

/** lastEntries is the entries to draw: the last MAX_ENTRIES of the push. */
export function lastEntries(st) {
  const entries = Array.isArray(st && st.entries) ? st.entries : [];
  return entries.length > MAX_ENTRIES ? entries.slice(entries.length - MAX_ENTRIES) : entries;
}

/** Structured is the panel. st is the store's structured push, or null before the
 *  first one; error is the store's structuredError; desk is false in a window
 *  reached through the relay. */
export function Structured({ pane, st, error = null, cancelError = null, desk = true, store }) {
  const [draft, setDraft] = useState("");
  // The pane a Stop that was not sent was pressed for, so the words do not follow the drawer to another pane.
  const [stopFailed, setStopFailed] = useState("");
  if (error) return html`<p class="drawer-stub conv-refused" role="status">${clip(String(error.error || ""), 300)}</p>`;
  if (!st || st.id !== pane) return html`<p class="drawer-stub">Loading the agent.</p>`;
  const act = activityOf(st);
  const ctx = contextText(st.usage);
  const cost = costText(st.usage);
  const entries = lastEntries(st);
  const submit = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    const text = draft.trim();
    if (!text) return;
    if (store.send({ cmd: "paneSend", id: pane, text })) setDraft("");
  };
  const stop = () => {
    setStopFailed(store.send({ cmd: "structuredCancel", id: pane, req: "c" + ++cancelSeq }) ? "" : pane);
  };
  return html`
    <div class="structured" data-structured-pane=${pane}>
      <div class="structured-facts">
        <p class="structured-activity" data-activity=${act ? "on" : "off"}>
          ${act
            ? html`<${Icon} kind=${act.kind} /><span class="structured-title">${clip(act.title, 160)}</span>${act.file ? html`<span class="structured-file">${clip(act.file, 160)}</span>` : null}`
            : html`<span class="structured-title">${IDLE_WORDS}</span>`}
        </p>
        ${ctx || cost
          ? html`<p class="structured-usage">${ctx ? html`<span class="structured-context">${ctx}</span>` : null}${cost ? html`<span class="structured-cost">${cost}</span>` : null}</p>`
          : null}
      </div>
      <div class="conv structured-log" tabindex="0" role="log" aria-label="Conversation">
        ${entries.length === 0
          ? html`<p class="conv-empty">${NO_ENTRIES_WORDS}</p>`
          : html`<ol class="conv-list">${entries.map((e) => html`<${Row} key=${e.id} entry=${e} />`)}</ol>`}
      </div>
      ${desk
        ? html`<form class="structured-prompt" onSubmit=${submit}>
            <textarea class="structured-input" aria-label="Message to the agent" rows="2" value=${draft} onInput=${(e) => setDraft(e.target.value)}></textarea>
            <button type="submit" class="drawer-btn structured-send" disabled=${draft.trim() === ""}>Send</button>
            <button type="button" class="drawer-btn structured-stop" onClick=${stop}>Stop</button>
            ${stopFailed === pane ? html`<p class="structured-note structured-note--unsent" role="status">${STOP_NOT_SENT_WORDS}</p>` : null}
            ${cancelError && cancelError.id === pane ? html`<p class="structured-note" role="status">${clip(String(cancelError.error || ""), 300)}</p>` : null}
          </form>`
        : null}
    </div>
  `;
}
