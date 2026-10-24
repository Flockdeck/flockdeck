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
import { LIST } from "../shell/keys.js";

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

// How wide a character is, per unit of font size, for the three faces the
// stylesheet uses, rounded up so a text is cut a little early rather than late:
// the mono face (JetBrains Mono, 0.6 em), the sans face and its bold. The box
// is 216 wide, so these decide how many characters each line can hold. The
// layout of a line is pinned by a test that estimates each text's width the
// same way.
export const CHAR_EM = { mono: 0.62, sans: 0.56, bold: 0.58 };
export const SIZES = { name: 14, state: 12, role: 12, mono: 12, chip: 11 };
export const PAD = 12;
export const NAME_X = 32;

/** limitOf is how many characters of a line fit between the box's padding. */
export function limitOf(w, kind) {
  const em = kind === "mono" ? CHAR_EM.mono * SIZES.mono : CHAR_EM.sans * SIZES.role;
  return Math.max(1, Math.floor((w - 2 * PAD) / em));
}

/** nameLimit is how many characters of the name fit between the mark and the state word, which is drawn at the right edge. */
export function nameLimit(word, w) {
  const stateW = Array.from(word).length * CHAR_EM.bold * SIZES.state;
  const room = w - PAD - stateW - 8 - NAME_X;
  return Math.max(1, Math.floor(room / (CHAR_EM.bold * SIZES.name)));
}

/** chipOf is the task chip's text, cut to fit, and the width of its pill. */
export function chipOf(task) {
  const text = clip(task.id + " " + task.state, 22);
  return { text, w: Math.ceil(Array.from(text).length * CHAR_EM.mono * SIZES.chip) + 16 };
}

/** branchLine is the branch and the changed-file count. The count is kept whole and the branch is cut to leave room for it. */
export function branchLine(branch, dirty, limit) {
  if (!dirty) return clip(branch, limit);
  const room = limit - Array.from(dirty).length - 2;
  return branch && room >= 4 ? clip(branch, room) + "  " + dirty : clip(dirty, limit);
}

/** opener is the attributes and handlers that make a box open its pane. */
function opener(onOpen, pane, open) {
  return {
    tabindex: 0,
    "aria-expanded": open ? "true" : "false",
    onClick: () => onOpen(pane),
    onKeyDown: (e) => {
      if (e.key !== LIST.accept && e.key !== " ") return;
      e.preventDefault();
      onOpen(pane);
    },
  };
}

/** hover is the handlers that tell the graph which pane the pointer or the keyboard is on. */
function hover(onHot, pane) {
  return { onMouseEnter: () => onHot(pane), onMouseLeave: () => onHot(""), onFocus: () => onHot(pane), onBlur: () => onHot("") };
}

/** countsLine is the faint count of messages to and from a pane. */
export function countsLine(c) {
  return c.in + " in, " + c.out + " out";
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
 * shows the team's spend; other nodes ignore it. With onOpen the box is a
 * button that opens the pane's drawer, by a click or Enter or Space; open says
 * this pane's drawer is the one open.
 */
export function Node({ node, box, team = null, onOpen = null, open = false, counts = null, dim = false, rel = false, onHot = null }) {
  const state = stateOf(node);
  const isManager = team !== null && team.manager === node.pane;
  const spend = isManager ? spendOf(team) : "";
  const dirty = dirtyOf(node);
  const w = box.w;
  const label = describe(node, state, spend);
  const chip = node.task ? chipOf(node.task) : null;
  const cls = ["gnode", "gnode--" + state.key, node.structured ? "gnode--structured" : "", node.shell ? "gnode--shell" : "", isManager ? "gnode--manager" : "", open ? "gnode--open" : "", dim ? "gnode--dim" : ""]
    .filter(Boolean)
    .join(" ");
  return html`
    <g
      class=${cls}
      data-pane=${node.pane}
      transform=${`translate(${box.x} ${box.y})`}
      role=${onOpen ? "button" : "group"}
      aria-label=${label}
      ...${onOpen ? opener(onOpen, node.pane, open) : {}}
      ...${onHot ? hover(onHot, node.pane) : {}}
    >
      <title>${label}</title>
      ${rel ? html`<rect class="gnode-rel" x="-4" y="-4" width=${w + 8} height=${box.h + 8} rx="9" />` : null}
      <rect class="gnode-box" x="0" y="0" width=${w} height=${box.h} rx="6" />
      <${Mark} shape=${state.shape} x=${18} y=${22} />
      <text class="gnode-name" x=${NAME_X} y="27">${clip(node.name || node.pane, nameLimit(state.word, w))}</text>
      <text class="gnode-state" x=${w - PAD} y="27" text-anchor="end">${state.word}</text>
      <text class="gnode-role" x=${PAD} y="47">${clip([node.role || (node.shell ? "shell" : ""), node.agent].filter(Boolean).join(" · "), limitOf(w, "role"))}</text>
      <text class="gnode-live" x=${PAD} y="67">${node.live ? clip(node.live, limitOf(w, "mono")) : ""}</text>
      <text class="gnode-branch" x=${PAD} y="87">${branchLine(node.branch, dirty, limitOf(w, "mono"))}</text>
      ${chip
        ? html`<g class="gchip" transform=${`translate(${PAD} 94)`}>
            <rect x="0" y="0" width=${chip.w} height="14" rx="7" />
            <text x="8" y="11">${chip.text}</text>
          </g>`
        : null}
      ${spend ? html`<text class="gnode-spend" x=${PAD} y="123">${clip(spend, limitOf(w, "mono"))}</text>` : null}
      ${counts
        ? html`<text class="gnode-msgs" x=${PAD} y=${box.h - 6}>${countsLine(counts)}${counts.undelivered > 0 ? html`<tspan class="gnode-und" dx="8">${counts.undelivered} undelivered</tspan>` : null}</text>`
        : null}
    </g>
  `;
}
