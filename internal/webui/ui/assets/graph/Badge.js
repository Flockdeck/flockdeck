// The reason a pane needs the person, as words and as a badge on its box.
//
// The reason class is the kind of the needs-you item the graph carries; this
// file adds no class of its own. Each kind has a short fixed phrase, and a kind
// this file does not know is shown as the graph spelled it. A badge carries an
// icon whose shape differs per kind (so no reason rests on colour), the phrase,
// and the first line of the item's text, clipped. The whole text is never in
// the badge: a command waiting for approval is read in the drawer, where it can
// be answered, and a graph pushed to a relay has it replaced already.
//
// Every text here is untrusted and is drawn as text.
import { html } from "../html.js";
import { clip } from "./Node.js";
import { QUIET_KINDS } from "./model.js";

export { QUIET_KINDS, isLoud } from "./model.js";

/** The phrases for the kinds the graph has. A down item's phrase is taken from its text, below. */
const PHRASES = {
  permission: "asks to run a command",
  ask: "asks a question",
  returned: "task returned from review",
  conflict: "files overlap",
  "stale-unread": "has unread mail",
  stuck: "has gone quiet",
  spend: "near its spend limit",
  setup: "needs setting up",
};

/** The block a pane's request is drawn in, in the box's own coordinates (the box is 216 wide and the live line is not drawn under it), and the characters its detail line holds. */
export const BADGE = { w: 200, h: 46, x: 8, top: 66, detailMax: 21 };

/** firstLine is the first non-empty line of a text, trimmed. */
export function firstLine(text) {
  for (const line of String(text == null ? "" : text).split(/\r\n|\r|\n/)) {
    const t = line.trim();
    if (t !== "") return t;
  }
  return "";
}

/**
 * reasonOf turns a needs-you item (from graph/model.js) into { kind, word,
 * detail }: word is the phrase for the reason, and detail the first line of the
 * item's text, not clipped, or "" when the word already says it. A down item says
 * "exited with code N" or "failed" and, for a failure, the reason after it.
 */
export function reasonOf(need) {
  const kind = need.kind;
  const line = firstLine(need.text);
  if (kind === "down") {
    const code = /^exited with code (-?\d+)/.exec(line);
    if (code) return { kind, word: "exited with code " + code[1], detail: "" };
    const failed = /^failed\b[\s:,-]*(.*)$/i.exec(line);
    return { kind, word: failed ? "failed" : "down", detail: failed ? failed[1] : line };
  }
  if (Object.prototype.hasOwnProperty.call(PHRASES, kind)) return { kind, word: PHRASES[kind], detail: line };
  return { kind, word: clip(kind, 24), detail: line };
}

const ICONS = {
  permission: "M-6 -5 H6 V5 H-6 Z M-3 -2 L0 0 L-3 2 M1 2 H4",
  ask: "M-2.5 -2 Q-2.5 -4.5 0 -4.5 Q2.5 -4.5 2.5 -2 Q2.5 0 0 1 V2.5 M0 4.5 V5",
  returned: "M-6 -2 L-2 -5.5 V-3.5 H2 A3.2 3.2 0 0 1 2 3.5 H-2",
  conflict: "M-6 -6 H2 V2 H-6 Z M-2 -2 H6 V6 H-2 Z",
  down: "M-3 -3 L3 3 M3 -3 L-3 3",
  "stale-unread": "M-6 -4 H6 V4 H-6 Z M-6 -4 L0 1 L6 -4",
  stuck: "M0 -6 V0 L4 3 M-6 0 A6 6 0 1 0 6 0 A6 6 0 1 0 -6 0",
  spend: "M0 -6 V6 M3.5 -3.5 Q3.5 -5 0 -5 Q-3.5 -5 -3.5 -2.5 Q-3.5 0 0 0 Q3.5 0 3.5 2.5 Q3.5 5 0 5 Q-3.5 5 -3.5 3.5",

  setup: "M0 -6 L6.5 5 H-6.5 Z M0 -2 V1.5 M0 3.5 V4",
};

/** Icon draws the shape of a kind, centred on x, y, as an SVG group. down also has a ring, and an unknown kind a dashed one. */
export function Icon({ kind, x, y }) {
  const known = Object.prototype.hasOwnProperty.call(ICONS, kind);
  const ring = kind === "down" || kind === "ask" || !known;
  return html`
    <g class=${"gicon gicon--" + (known ? kind : "unknown")} transform=${`translate(${x} ${y})`} aria-hidden="true">
      ${ring ? html`<circle r="7" />` : null}
      ${known ? html`<path d=${ICONS[kind]} />` : html`<path d="M0 -2.5 V0.5 M0 3 V3.3" />`}
    </g>
  `;
}

/**
 * Badge is drawn on a node, in the node's own coordinates, as a block inside the
 * box. needs are the pane's items; the first one is shown, and a count of the
 * others when there are more. A quiet kind is drawn without the amber.
 */
export function Badge({ needs }) {
  if (!needs || needs.length === 0) return null;
  const r = reasonOf(needs[0]);
  const more = needs.length - 1;
  const detail = r.detail === "" ? "" : clip(r.detail, BADGE.detailMax);
  return html`
    <g class=${"gbadge gbadge--" + (r.kind === "down" ? "down" : QUIET_KINDS.includes(r.kind) ? "quiet" : "ask")} data-kind=${r.kind} role="img" aria-label=${"Needs you: " + r.word}>
      <rect class="gbadge-box" x=${BADGE.x} y=${BADGE.top} width=${BADGE.w} height=${BADGE.h} rx="4" />
      <${Icon} kind=${r.kind} x=${BADGE.x + 16} y=${BADGE.top + 14} />
      <text class="gbadge-word" x=${BADGE.x + 32} y=${BADGE.top + 18}>${r.word}</text>
      ${more > 0 ? html`<text class="gbadge-more" x=${BADGE.x + BADGE.w - 8} y=${BADGE.top + 36} text-anchor="end">+${more}</text>` : null}
      ${detail !== "" ? html`<text class="gbadge-detail" x=${BADGE.x + 32} y=${BADGE.top + 36}>${detail}</text>` : null}
    </g>
  `;
}
