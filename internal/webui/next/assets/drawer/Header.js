// The drawer's header: what the old pane header carried that matters, in one
// place. The name, role and agent; the status with its mark and for how long;
// the branch with its changed and ahead counts; the model; the spend, marked
// partial when some of it was not priced; and whether the pane is recording.
//
// The graph node supplies the first group and the state push's pane the rest
// (model, spend, recording). Every text came from an agent or a task, so each is
// a child of an element and so drawn as text.
import { html } from "../html.js";
import { stateOf, dirtyOf } from "../graph/Node.js";

/** UNASKED_WORDS is the standing warning for an agent the manifest records as
 *  possibly running commands and edits without sending a permission request
 *  (opencode: Flockdeck starts it to ask, but a repository's own settings can
 *  allow). It is a constant: nothing from the agent is in it. */
export const UNASKED_WORDS = "This agent may run commands and edit files without asking you first: in this repository Flockdeck could not make it ask, or could not check. A project's opencode.json or .opencode folder can also load plugins and MCP servers that run code, so no permission setting makes an opencode pane safe in a repository you do not trust.";

/** sinceOf says how long a status has lasted, as "3m", "2h 5m" or "45s", from
 *  an RFC 3339 time and the current time in milliseconds. A time that is not
 *  one, or is in the future, gives an empty string. */
export function sinceOf(since, now) {
  const t = Date.parse(since);
  if (!Number.isFinite(t) || !Number.isFinite(now) || t > now) return "";
  const s = Math.floor((now - t) / 1000);
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m";
  const h = Math.floor(m / 60);
  if (h < 48) return h + "h" + (m % 60 ? " " + (m % 60) + "m" : "");
  return Math.floor(h / 24) + "d";
}

/** spendText is a pane's spend, or an empty string when it reported none. The
 *  figure is a floor when some of the tokens had no price. */
export function spendText(spend) {
  if (!spend || typeof spend.usd !== "number" || !Number.isFinite(spend.usd) || spend.usd <= 0) {
    return spend && spend.unpriced ? "unpriced" : "";
  }
  const money = "$" + spend.usd.toFixed(2);
  return spend.unpriced ? money + " partial" : money;
}

/** countsOf is the branch's changed, ahead and behind counts as words. */
export function countsOf(node) {
  const bits = [];
  const d = dirtyOf(node);
  if (d) bits.push(d);
  if (node.ahead > 0) bits.push(node.ahead + " ahead");
  if (node.behind > 0) bits.push(node.behind + " behind");
  return bits.join(", ");
}

/** Header is the top of the drawer. node is the graph node; pane is the state
 *  push's entry for the same pane, or null; now is the time in milliseconds. */
export function Header({ node, pane = null, now = Date.now(), maximised = false, onClose, onToggleMax }) {
  const state = stateOf(node);
  const since = sinceOf(node.since, now);
  const spend = spendText(pane && pane.spend);
  const model = (pane && typeof pane.model === "string" && pane.model) || "";
  const recording = !!(pane && pane.recording);
  const counts = countsOf(node);
  const who = [node.role || (node.shell ? "shell" : ""), node.agent].filter(Boolean).join(" · ");
  return html`
    <header class="drawer-head">
      <div class="drawer-title">
        <h2 class="drawer-name">${node.name || node.pane}</h2>
        ${node.structured || node.kind === "structured" ? html`<span class="drawer-kind">structured</span>` : null}
        <div class="drawer-actions">
          <button type="button" class="drawer-btn drawer-max" aria-pressed=${maximised ? "true" : "false"} onClick=${onToggleMax}>
            ${maximised ? "Restore" : "Maximise"}
          </button>
          <button type="button" class="drawer-btn drawer-close" aria-label="Hide the drawer" onClick=${onClose}>Hide</button>
        </div>
      </div>
      ${pane && pane.runsUnasked === true ? html`<p class="drawer-warn" role="note">${UNASKED_WORDS}</p>` : null}
      <dl class="drawer-facts">
        ${who ? html`<div class="fact fact--who"><dt>Agent</dt><dd>${who}</dd></div>` : null}
        <div class=${"fact fact--status fact--" + state.key}>
          <dt>Status</dt>
          <dd>${state.word}${since ? " for " + since : ""}</dd>
        </div>
        ${node.branch ? html`<div class="fact fact--branch"><dt>Branch</dt><dd>${node.branch}${counts ? " · " + counts : ""}</dd></div>` : null}
        ${model ? html`<div class="fact fact--model"><dt>Model</dt><dd>${model}</dd></div>` : null}
        ${spend ? html`<div class="fact fact--spend"><dt>Spend</dt><dd>${spend}</dd></div>` : null}
        ${recording ? html`<div class="fact fact--recording"><dt>Recording</dt><dd>on</dd></div>` : null}
      </dl>
    </header>
  `;
}
