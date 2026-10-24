// What an opened tool row shows below its summary: the tool's output or its diff,
// from the entry (when the desktop sent it inline) or from conversationDetail.
//
// - Output is drawn up to OUTPUT_CAP bytes; past that the row says "Output
//   truncated: N bytes more are not shown."
// - A diff is drawn by Diff.js, up to its own cap, with the same wording.
// - While the detail is asked for the row says so; for an entry the desktop no
//   longer holds it says "No longer available."
// - A helper agent's own entries are drawn by the row (one level deep), not here.
//
// The text is what a tool or an agent said. It is drawn as text.
import { html } from "../../html.js";
import { capBytes, cutWords } from "./cap.js";
import { Diff } from "./Diff.js";

/** OUTPUT_CAP is how many bytes of a tool's output are drawn. */
export const OUTPUT_CAP = 4 << 10;
export const LOADING_DETAIL_WORDS = "Loading the full output…";
export const GONE_WORDS = "No longer available.";
export const NOT_ASKED_WORDS = "Not connected to Flockdeck, so the full output was not asked for.";

/** Output is a tool's output text, cut at OUTPUT_CAP with the cut said. */
export function Output({ text }) {
  const cut = capBytes(text, OUTPUT_CAP);
  return html`<div class="conv-output">
    <pre class="conv-output-text">${cut.text}</pre>
    ${cut.more > 0 ? html`<p class="conv-cut" data-cut="output">${cutWords("Output", cut.more)}</p>` : null}
  </div>`;
}

/** ToolBody is the output or diff of an opened tool row. detail is the store's
 *  { state, detail } for the entry, or undefined when it has not been asked for. */
export function ToolBody({ entry: e, detail }) {
  const inlineDiff = typeof e.diff === "string" ? e.diff : "";
  const got = detail && detail.state === "ok" && detail.detail ? detail.detail : null;
  const diff = (got && got.diff) || inlineDiff;
  const output = got && got.text ? got.text : "";
  if (diff || output) {
    return html`${diff ? html`<${Diff} text=${diff} />` : null}${output ? html`<${Output} text=${output} />` : null}`;
  }
  if (!e.hasDetail) return null;
  if (!detail) return html`<p class="conv-detail-state" role="status">${NOT_ASKED_WORDS}</p>`;
  if (detail.state === "gone") return html`<p class="conv-detail-state" data-detail="gone" role="status">${GONE_WORDS}</p>`;
  if (detail.state === "loading") return html`<p class="conv-detail-state" data-detail="loading" role="status">${LOADING_DETAIL_WORDS}</p>`;
  return null;
}

/** pendingAfter is the id of the entry a permission card is drawn after: the
 *  latest tool still running that has the card's tool name, else the latest
 *  running tool, else "" (the end of the conversation). */
export function pendingAfter(entries, card) {
  const list = Array.isArray(entries) ? entries : [];
  const name = card && typeof card.tool === "string" ? card.tool.toLowerCase() : "";
  let any = "";
  for (let i = list.length - 1; i >= 0; i--) {
    const e = list[i];
    if (!e || e.kind !== "tool" || e.status !== "running") continue;
    if (name && String(e.name || "").toLowerCase() === name) return e.id;
    if (!any) any = e.id;
  }
  return any;
}
