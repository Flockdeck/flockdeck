// A unified diff, drawn as rows of text.
//
// - Each line keeps its first character (+, - or a space) in the text, so an
//   added or removed line is told apart without its colour. The colour and a
//   tint are on top of that, never instead of it.
// - Lines before the first hunk header (the file names and the like) are drawn
//   as a header; lines starting @@ as a hunk header.
// - Only DIFF_CHUNK lines are drawn at first, and a button draws that many more,
//   so a diff of thousands of lines keeps a bounded number of elements until the
//   reader asks for the rest.
// - A diff over DIFF_CAP bytes is cut there, and the row says how many bytes
//   more there are.
//
// Every line is what a tool or a file said. It is drawn as text, never as markup.
import { useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { capBytes, cutWords } from "./cap.js";

/** DIFF_CAP is how many bytes of a diff are drawn. */
export const DIFF_CAP = 64 << 10;
/** DIFF_CHUNK is how many lines are drawn first, and added by each press of the button. */
export const DIFF_CHUNK = 400;

/** diffLines splits a diff into { text, kind } rows. kind is add, del, ctx, hunk or head. */
export function diffLines(diff) {
  const all = String(diff || "").split("\n");
  if (all.length && all[all.length - 1] === "") all.pop();
  let inHunk = false;
  return all.map((text) => {
    if (text.startsWith("@@")) {
      inHunk = true;
      return { text, kind: "hunk" };
    }
    if (!inHunk) return { text, kind: "head" };
    return { text, kind: text.startsWith("+") ? "add" : text.startsWith("-") ? "del" : "ctx" };
  });
}

/** Diff draws a diff. label names it for a screen reader. */
export function Diff({ text, label = "Changes" }) {
  const [shown, setShown] = useState(DIFF_CHUNK);
  const cut = capBytes(text, DIFF_CAP);
  const rows = diffLines(cut.text);
  const drawn = rows.slice(0, shown);
  const left = rows.length - drawn.length;
  return html`<div class="conv-diff" data-diff="">
    <pre class="conv-diff-lines" role="group" aria-label=${label}>${drawn.map((r, i) => html`<span key=${i} class=${"conv-diff-" + r.kind} data-diff-kind=${r.kind}>${r.text + "\n"}</span>`)}</pre>
    ${left > 0 ? html`<button type="button" class="conv-diff-more" onClick=${() => setShown((n) => n + DIFF_CHUNK)}>${"Show " + Math.min(DIFF_CHUNK, left).toLocaleString("en-US") + " more lines (" + left.toLocaleString("en-US") + " not drawn yet)"}</button>` : null}
    ${cut.more > 0 ? html`<p class="conv-cut" data-cut="diff">${cutWords("Diff", cut.more)}</p>` : null}
  </div>`;
}
