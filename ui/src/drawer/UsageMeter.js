// The usage meter in the header of a structured pane: a bar and a short line
// (share of the context window used, the cost if the agent reported one), and a
// button that opens the detail under it. Everything shown is a figure the agent
// reported, drawn as text (usageModel.js).
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { usageOf, meterText, detailRows, NOT_REPORTED_WORDS } from "./usageModel.js";

export function UsageMeter({ usage }) {
  const [open, setOpen] = useState(false);
  const line = meterText(usage);
  if (!line) return null;
  const u = usageOf(usage);
  return html`<div class="usage-meter" data-usage="on">
    <button type="button" class="usage-meter-btn" aria-expanded=${open ? "true" : "false"} aria-controls="usage-detail" title="Show the usage detail" onClick=${() => setOpen(!open)}>
      ${u.pct !== null ? html`<span class="usage-bar" role="img" aria-label=${u.pct + "% of the context window used"}><span class="usage-bar-fill" style=${"width:" + u.pct + "%"}></span></span>` : null}
      <span class="usage-line">${line}</span>
    </button>
    ${open
      ? html`<div id="usage-detail" class="usage-detail" role="group" aria-label="Usage detail">
          <dl class="usage-rows">${detailRows(usage).map(([k, v]) => html`<div class="usage-row"><dt>${k}</dt><dd>${v}</dd></div>`)}</dl>
          <p class="usage-note">${NOT_REPORTED_WORDS}</p>
        </div>`
      : null}
  </div>`;
}
