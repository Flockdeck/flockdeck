// One pane drawn as a box in the graph's SVG. Everything the person needs to
// tell the pane's state is in the box without hover: its name and role, a mark
// and a word for its status, the live line, the branch and the changed-file
// count, the task chip and, on a manager, the team's spend.
//
// Every text here came from an agent, a task or a message, so it is only ever a
// child of a text element (drawn as text) or an attribute value (escaped), never
// markup. A text that is too long for the box is cut with an ellipsis; the
// whole of it is in the box's <title>.
import { html } from "../html.js";

/** The status words and marks. The mark's shape differs per state, so a status never rests on colour alone. */
const STATES = {
  working: { word: "working", shape: "circle" },
  idle: { word: "idle", shape: "ring" },
  waiting: { word: "waiting", shape: "diamond" },
  blocked: { word: "blocked", shape: "square" },
  starting: { word: "starting", shape: "dashed" },
  exited: { word: "down", shape: "cross" },
  failed: { word: "down", shape: "cross" },
};

/** stateOf says which state a node is drawn in: its activity, else its status. A value not in the table is drawn as unknown, with its own words. */
export function stateOf(node) {
  const key = Object.prototype.hasOwnProperty.call(STATES, node.activity) ? node.activity : node.status;
  if (Object.prototype.hasOwnProperty.call(STATES, key)) return { key, ...STATES[key] };
  return { key: "unknown", word: key && key !== "unknown" ? clip(key, 10) : "unknown", shape: "unknown" };
}

/** clip cuts a text to at most max characters, ending in an ellipsis. It counts code points, so a pair is not split. */
export function clip(text, max) {
  const chars = Array.from(String(text == null ? "" : text));
  if (chars.length <= max) return chars.join("");
  return chars.slice(0, Math.max(0, max - 1)).join("") + "…";
}

/** spendOf is a team's spend as the manager's box shows it. */
export function spendOf(team) {
  if (!team) return "";
  const money = "$" + team.spendUSD.toFixed(2);
  return team.spendPartial || team.spendIncludesPartial ? money + " partial" : money;
}

/** dirtyOf is the changed-file part of the branch line, or an empty string. */
export function dirtyOf(node) {
  const parts = [];
  if (node.dirty > 0) parts.push(node.dirty + " changed");
  if (node.untracked > 0) parts.push(node.untracked + " new");
  return parts.join(", ");
}

/** The text of the whole box for a screen reader, which is also its title. */
export function describe(node, state, spend) {
  const bits = [node.name || node.pane, node.role, state.word];
  if (node.live) bits.push(node.live);
  if (node.branch) bits.push("branch " + node.branch);
  const d = dirtyOf(node);
  if (d) bits.push(d);
  if (node.task) bits.push("task " + node.task.id + " " + node.task.state);
  if (spend) bits.push(spend);
  return bits.filter(Boolean).join(", ");
}

function Mark({ shape, x, y }) {
  const cls = "gmark gmark--" + shape;
  switch (shape) {
    case "circle":
      return html`<circle class=${cls} cx=${x} cy=${y} r="6" />`;
    case "ring":
      return html`<circle class=${cls} cx=${x} cy=${y} r="5" />`;
    case "dashed":
      return html`<circle class=${cls} cx=${x} cy=${y} r="5" />`;
    case "diamond":
      return html`<polygon class=${cls} points=${`${x},${y - 7} ${x + 7},${y} ${x},${y + 7} ${x - 7},${y}`} />`;
    case "square":
      return html`<rect class=${cls} x=${x - 5} y=${y - 5} width="10" height="10" />`;
    case "cross":
      return html`<path class=${cls} d=${`M${x - 5} ${y - 5} L${x + 5} ${y + 5} M${x + 5} ${y - 5} L${x - 5} ${y + 5}`} />`;
    default:
      return html`<circle class=${cls} cx=${x} cy=${y} r="5" />`;
  }
}

/**
 * Node draws one pane at the position the layout gave it. box is { x, y, w, h }.
 * team is the node's team from the model, given for a manager so that the box
 * shows the team's spend; other nodes ignore it.
 */
export function Node({ node, box, team = null }) {
  const state = stateOf(node);
  const isManager = team !== null && team.manager === node.pane;
  const spend = isManager ? spendOf(team) : "";
  const dirty = dirtyOf(node);
  const w = box.w;
  const label = describe(node, state, spend);
  const cls = ["gnode", "gnode--" + state.key, node.structured ? "gnode--structured" : "", node.shell ? "gnode--shell" : "", isManager ? "gnode--manager" : ""]
    .filter(Boolean)
    .join(" ");
  return html`
    <g class=${cls} data-pane=${node.pane} transform=${`translate(${box.x} ${box.y})`} role="group" aria-label=${label}>
      <title>${label}</title>
      <rect class="gnode-box" x="0" y="0" width=${w} height=${box.h} rx="6" />
      <${Mark} shape=${state.shape} x=${18} y=${22} />
      <text class="gnode-name" x="32" y="27">${clip(node.name || node.pane, 18)}</text>
      <text class="gnode-state" x=${w - 10} y="27" text-anchor="end">${state.word}</text>
      <text class="gnode-role" x="12" y="47">${clip([node.role || (node.shell ? "shell" : ""), node.agent].filter(Boolean).join(" · "), 32)}</text>
      <text class="gnode-live" x="12" y="67">${node.live ? clip(node.live, 31) : ""}</text>
      <text class="gnode-branch" x="12" y="87">${clip([node.branch, dirty].filter(Boolean).join("  "), 31)}</text>
      ${node.task
        ? html`<g class="gchip" transform="translate(12 94)">
            <rect x="0" y="0" width="118" height="14" rx="7" />
            <text x="8" y="11">${clip(node.task.id + " " + node.task.state, 18)}</text>
          </g>`
        : null}
      ${spend ? html`<text class="gnode-spend" x=${w - 10} y="105" text-anchor="end">${spend}</text>` : null}
    </g>
  `;
}
