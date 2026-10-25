// The team-wide Changes view's list: one row per agent's branch, the ones that need a
// person first. Drawn from the changesTeam push (lib/changesteam.js); every string in
// it is drawn as text.
import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "../drawer/diff/diffModel.js";
import { attentionOf, barOf, conflictPanes, ladderOf, LADDER_NAMES, sortBranches, totalsOf } from "../lib/changesteam.js";

export const NO_BRANCHES_WORDS = "No agent has a branch yet. Branches appear here when an agent starts work in a repository.";
export const READING_WORDS = "Reading what changed...";

const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

/** words is text from an agent or a repository as one clipped line with the characters that reorder or hide text made visible. */
export const words = (s, n = 120) => showControls(clip(typeof s === "string" ? s : "", n));

/** nameOf is what a row calls an agent: the name the page has for it, else its pane id. */
export const nameOf = (names, pane) => words((names && names[pane]) || pane, 60);

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

/** Lines is "+12 -3" for the eye and "12 added, 3 removed" for a screen reader. */
export function Lines({ added, removed }) {
  return html`<span class="ct-lines"><span class="ct-sr">${added} added, ${removed} removed</span><span aria-hidden="true"><span class="ct-added">+${added}</span> <span class="ct-removed">-${removed}</span></span></span>`;
}

function Row({ branch: b, names, selected, stop, onPick, onActive }) {
  const at = attentionOf(b);
  const lines = b.files.reduce((n, f) => [n[0] + f.added, n[1] + f.removed], [0, 0]);
  const clash = conflictPanes(b);
  return html`<li>
    <button type="button" class=${"ct-row ct-row--" + at.key + (selected ? " is-open" : "")} data-pane=${b.pane} tabindex=${stop ? 0 : -1} aria-current=${selected ? "true" : undefined} onFocus=${() => onActive(b.pane)} onClick=${() => onPick(b.pane)}>
      <span class="ct-agent">${nameOf(names, b.pane)}</span>
      ${b.error
        ? html`<span class="ct-branch ct-branch--error">${words(b.error, 160)}</span>`
        : html`<span class="ct-branch">${words(b.branch, 120) || "detached"}${b.taskId ? html` <span class="ct-task">${words(b.taskId, 20)}</span>` : null}</span>
          <${Lines} added=${lines[0]} removed=${lines[1]} />
          <${Bar} branch=${b} />
          <span class="ct-files">${plural(b.files.length + b.omitted, "file", "files")}</span>
          <${Ladder} branch=${b} />
          ${at.text ? html`<span class=${"ct-chip ct-chip--" + at.key}>${at.text}${at.key === "conflict" && clash.length ? " with " + clash.map((p) => nameOf(names, p)).join(", ") : ""}</span>` : null}`}
    </button>
  </li>`;
}

/**
 * ChangesList draws the branches, attention first. selected is a pane id; onPick gets one.
 * The rows are one tab stop: the row last focused (else the open one, else the first) is
 * in the tab order, and Up, Down, Home and End move between rows.
 */
export function ChangesList({ branches, names, selected, onPick }) {
  const rows = sortBranches(branches);
  const [active, setActive] = useState("");
  const ul = useRef(null);
  const stop = rows.some((b) => b.pane === active) ? active : rows.some((b) => b.pane === selected) ? selected : rows.length ? rows[0].pane : "";
  const onKey = (e) => {
    const k = e.key;
    if (!ul.current || !["ArrowDown", "ArrowUp", "Home", "End"].includes(k)) return;
    const btns = Array.from(ul.current.querySelectorAll(".ct-row"));
    const at = btns.indexOf(e.target);
    if (at < 0) return;
    const to = k === "Home" ? 0 : k === "End" ? btns.length - 1 : Math.max(0, Math.min(btns.length - 1, at + (k === "ArrowDown" ? 1 : -1)));
    e.preventDefault();
    btns[to].focus();
    setActive(btns[to].getAttribute("data-pane") || "");
  };
  const t = totalsOf(branches);
  return html`<div class="ct-list">
    <p class="ct-totals">${plural(t.branches, "branch", "branches")}, ${plural(t.files, "file", "files")}, <${Lines} added=${t.added} removed=${t.removed} />${t.attention ? html`, <span class="ct-need">${t.attention} need${t.attention === 1 ? "s" : ""} attention</span>` : null}</p>
    <ul class="ct-rows" aria-label="Branches" ref=${ul} onKeyDown=${onKey}>
      ${rows.map((b) => html`<${Row} key=${b.pane} branch=${b} names=${names} selected=${b.pane === selected} stop=${b.pane === stop} onPick=${onPick} onActive=${setActive} />`)}
    </ul>
  </div>`;
}
