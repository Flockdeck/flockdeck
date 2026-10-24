// One row of the outline (Tree.js): the agent's glyph and name, its role and task,
// what it is doing now, how long it has been in that state and, on a team's
// manager, the team's spend. The row is the tab stop; Enter, Space or a click opens
// the agent's drawer. A pane that asks something of the person has an amber bar
// and tint, one that ended a red one, and the pane whose drawer is open the accent.
import { html } from "../html.js";
import { clip, stateOf } from "./Node.js";
import { isLoud } from "./Badge.js";

/** The row's state as a class suffix: ask, down or the pane's own state. */
export function toneOf(node, needs) {
  if (isLoud(needs) || stateOf(node).key === "waiting") return "ask";
  if (stateOf(node).key === "exited" || stateOf(node).key === "failed" || (needs || []).some((n) => n.kind === "down")) return "down";
  return "";
}

/** TreeHead is the row of column names over the outline. It is for the eye only. */
export function TreeHead() {
  return html`<div class="tree-head" aria-hidden="true">
    <span class="tree-lead"></span><span class="tree-c tree-c--name">Agent</span><span class="tree-c tree-c--role">Role</span><span class="tree-c tree-c--task">Task</span>
    <span class="tree-c tree-c--doing">Doing now</span><span class="tree-c tree-c--age">Age</span><span class="tree-c tree-c--cost">Cost</span>
  </div>`;
}

/** TreeRow draws one pane. row is from rowsOf, doing the line for "Doing now". */
export function TreeRow({ row, node, needs, doing, stop, selected, age, cost, onFocus, onOpen }) {
  const state = stateOf(node);
  const tone = toneOf(node, needs);
  const name = node.name || node.pane;
  const label = [name, node.role, state.word, doing, node.task ? "task " + node.task.id : ""].filter(Boolean).join(", ");
  return html`<div
    class=${"tree-row" + (tone ? " tree-row--" + tone : "") + (selected ? " is-open" : "")}
    role="treeitem"
    data-pane=${node.pane}
    aria-level=${row.depth + 1}
    aria-expanded=${row.kids > 0 ? (row.collapsed ? "false" : "true") : undefined}
    aria-selected=${selected ? "true" : "false"}
    aria-label=${label}
    tabindex=${stop ? "0" : "-1"}
    style=${"--depth:" + Math.min(row.depth, 12)}
    onFocus=${onFocus}
    onClick=${onOpen ? () => onOpen(node.pane) : undefined}
  >
    ${Array.from({ length: Math.min(row.depth, 12) }, (_, i) => html`<span class="tree-indent" key=${i} aria-hidden="true"></span>`)}
    <span class="tree-toggle" aria-hidden="true">${row.kids > 0 ? (row.collapsed ? "▸" : "▾") : ""}</span>
    <span class=${"tree-glyph tree-glyph--" + state.key} aria-hidden="true"></span>
    <span class="tree-c tree-c--name" title=${name}>${clip(name, 40)}</span>
    <span class="tree-c tree-c--role">${clip(node.role || (node.shell ? "shell" : ""), 12)}</span>
    <span class="tree-c tree-c--task">${node.task ? clip(node.task.id, 6) : ""}</span>
    <span class="tree-c tree-c--doing" title=${doing}>${clip(doing, 160)}</span>
    <span class="tree-c tree-c--age">${age}</span>
    <span class="tree-c tree-c--cost">${cost}</span>
  </div>`;
}
