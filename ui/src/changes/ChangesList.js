// The team-wide Changes view's list: one row per agent's branch, the ones that need a
// person first. Drawn from the changesTeam push (lib/changesteam.js); every string in
// it is drawn as text.
import { html } from "../html.js";
import { attentionOf, barOf, conflictPanes, ladderOf, LADDER_NAMES, sortBranches, totalsOf } from "../lib/changesteam.js";

export const NO_BRANCHES_WORDS = "No agent has a branch yet. Branches appear here when an agent starts work in a repository.";
export const READING_WORDS = "Reading what changed...";

const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

/** nameOf is what a row calls an agent: the name the page has for it, else its pane id. */
export const nameOf = (names, pane) => (names && names[pane]) || pane;

/** Ladder is the four steps a branch has climbed, drawn as dots with the words in the label. */
export function Ladder({ branch }) {
  const steps = ladderOf(branch);
  const words = LADDER_NAMES.map((n, i) => n.toLowerCase() + (steps[i] === "done" ? " done" : steps[i] === "now" ? " now" : "")).join(", ");
  return html`<span class="ct-ladder" role="img" aria-label=${"Progress: " + words}>
    ${steps.map((s, i) => html`<span key=${i} class=${"ct-step ct-step--" + s} title=${LADDER_NAMES[i]}></span>`)}
  </span>`;
}

/** Bar is the share of a branch's changed lines that were added. */
export function Bar({ branch }) {
  const share = barOf(branch);
  return html`<span class="ct-bar" aria-hidden="true">${share < 0 ? null : html`<span class="ct-bar-add" style=${"width:" + Math.round(share * 100) + "%"}></span>`}</span>`;
}

function Row({ branch: b, names, selected, onPick }) {
  const at = attentionOf(b);
  const lines = b.files.reduce((n, f) => [n[0] + f.added, n[1] + f.removed], [0, 0]);
  const clash = conflictPanes(b);
  return html`<li>
    <button type="button" class=${"ct-row ct-row--" + at.key + (selected ? " is-open" : "")} data-pane=${b.pane} aria-current=${selected ? "true" : undefined} onClick=${() => onPick(b.pane)}>
      <span class="ct-agent">${nameOf(names, b.pane)}</span>
      ${b.error
        ? html`<span class="ct-branch ct-branch--error">${b.error}</span>`
        : html`<span class="ct-branch">${b.branch || "detached"}${b.taskId ? html` <span class="ct-task">${b.taskId}</span>` : null}</span>
          <span class="ct-lines"><span class="ct-added">+${lines[0]}</span> <span class="ct-removed">-${lines[1]}</span></span>
          <${Bar} branch=${b} />
          <span class="ct-files">${plural(b.files.length + b.omitted, "file", "files")}</span>
          <${Ladder} branch=${b} />
          ${at.text ? html`<span class=${"ct-chip ct-chip--" + at.key}>${at.text}${at.key === "conflict" && clash.length ? " with " + clash.map((p) => nameOf(names, p)).join(", ") : ""}</span>` : null}`}
    </button>
  </li>`;
}

/** ChangesList draws the branches, attention first. selected is a pane id; onPick gets one. */
export function ChangesList({ branches, names, selected, onPick }) {
  const rows = sortBranches(branches);
  const t = totalsOf(branches);
  return html`<div class="ct-list">
    <p class="ct-totals">${plural(t.branches, "branch", "branches")}, ${plural(t.files, "file", "files")}, <span class="ct-added">+${t.added}</span> <span class="ct-removed">-${t.removed}</span>${t.attention ? html`, <span class="ct-need">${t.attention} need${t.attention === 1 ? "s" : ""} attention</span>` : null}</p>
    <ul class="ct-rows" aria-label="Branches">
      ${rows.map((b) => html`<${Row} key=${b.pane} branch=${b} names=${names} selected=${b.pane === selected} onPick=${onPick} />`)}
    </ul>
  </div>`;
}
