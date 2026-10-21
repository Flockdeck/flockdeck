// The edges of the team graph, drawn over the boxes' layout (layout.js): what a
// pane is waiting on, the messages between panes with how far each has got, and
// the `sent` claims. Edges never hide a box. They are drawn under the boxes, and
// when there are many only the newest are drawn.
//
// - A waiting-on edge goes from a pane to the pane that holds it up, when the
//   graph names one that is in the graph (a helper). When the holder is not a
//   pane (the person, a task, a review) a short tag hangs from the bottom edge of
//   the box instead, with the same words.
// - A message edge is solid, from the sender to the pane whose inbox it went to,
//   with the delivery state written on it: queued, told, woken or read. A
//   message that is only queued has not been told to the pane yet, and is drawn
//   amber with the words "not told yet".
// - A sent edge is dotted and says "sent". It is a claim by the sending session
//   and is never drawn as read.
//
// No edge is drawn to a pane that is not a node of the graph. The graph holds
// 100 message edges and 100 sent edges at most; when a kind has reached that, or
// not all of its edges are drawn, a line in the corner says so.
//
// Every text here is a fixed word, or text from an agent that is only ever a
// child of a text element or a title, so none is read as markup.
import { html } from "../html.js";
import { clip } from "./Node.js";

/** The most edges of one kind the graph's message holds. */
export const GRAPH_EDGE_CAP = 100;
/** The most edges of one kind drawn; the rest are thinned, newest kept. */
export const MAX_DRAWN = 40;

const LANE = 14;

const DELIVERY_WORDS = {
  queued: "not told yet",
  notified: "told",
  woken: "woken",
  read: "read",
};

/** deliveryWord says in words how far a message has got. A state this file does not know is "delivery unknown". */
export function deliveryWord(state) {
  return Object.prototype.hasOwnProperty.call(DELIVERY_WORDS, state) ? DELIVERY_WORDS[state] : "delivery unknown";
}

// The point where the line from a box's centre towards p leaves the box.
function leave(b, p) {
  const cx = b.x + b.w / 2;
  const cy = b.y + b.h / 2;
  const dx = p.x - cx;
  const dy = p.y - cy;
  if (dx === 0 && dy === 0) return { x: cx, y: cy };
  const t = Math.min(dx === 0 ? Infinity : b.w / 2 / Math.abs(dx), dy === 0 ? Infinity : b.h / 2 / Math.abs(dy));
  return { x: cx + dx * t, y: cy + dy * t };
}

function curve(a, b, offset) {
  const ca = { x: a.x + a.w / 2, y: a.y + a.h / 2 };
  const cb = { x: b.x + b.w / 2, y: b.y + b.h / 2 };
  const mx = (ca.x + cb.x) / 2;
  const my = (ca.y + cb.y) / 2;
  let nx = -(cb.y - ca.y);
  let ny = cb.x - ca.x;
  const len = Math.hypot(nx, ny) || 1;
  nx /= len;
  ny /= len;
  const ctrl = { x: mx + nx * offset * 2, y: my + ny * offset * 2 };
  const s = leave(a, ctrl);
  const e = leave(b, ctrl);
  const mid = { x: 0.25 * s.x + 0.5 * ctrl.x + 0.25 * e.x, y: 0.25 * s.y + 0.5 * ctrl.y + 0.25 * e.y };
  const f = (n) => Math.round(n * 10) / 10;
  return { d: `M${f(s.x)} ${f(s.y)}Q${f(ctrl.x)} ${f(ctrl.y)} ${f(e.x)} ${f(e.y)}`, mid: { x: f(mid.x), y: f(mid.y) } };
}

// The words of a waiting-on edge or tag, from the node's waitingOn.
function holderOf(node, model) {
  const w = node.waitingOn;
  switch (w.kind) {
    case "helpers":
      return { pane: w.ref && w.ref !== node.pane && model.byPane.has(w.ref) ? w.ref : "", word: "waiting on a helper" };
    case "user":
      return { pane: "", word: "waiting on you" };
    case "reviewer":
      return { pane: "", word: "waiting on a reviewer" };
    case "task":
      return { pane: "", word: "waiting on a task" };
    default:
      return null; // none, down, and a kind this file does not know
  }
}

/**
 * edgesOf works out what to draw: { waiting, tags, message, sent, notices }.
 * Each waiting, message and sent entry has its path d, the label place and the
 * words; tags are the holders that are not panes. Pure: the same model and
 * layout always give the same result.
 */
export function edgesOf(model, layout, options = {}) {
  const maxDrawn = options.maxDrawn || MAX_DRAWN;
  const out = { waiting: [], tags: [], message: [], sent: [], notices: [] };
  if (!model || !model.ok || !layout || !layout.ok) return out;
  const box = (pane) => (model.byPane.has(pane) ? layout.byPane.get(pane) : undefined);

  // Edges between one pair of panes, in either direction, are spread so that
  // they do not lie over each other.
  const lanes = new Map();
  const lane = (a, b) => {
    const key = a < b ? a + "\u0000" + b : b + "\u0000" + a;
    const n = lanes.get(key) || 0;
    lanes.set(key, n + 1);
    return n;
  };
  const pending = [];

  for (const node of model.nodes) {
    const h = holderOf(node, model);
    if (!h) continue;
    const from = box(node.pane);
    if (!from) continue;
    if (h.pane && box(h.pane)) {
      pending.push({ list: "waiting", kind: "waiting", from: node.pane, to: h.pane, word: h.word, key: "w:" + node.pane });
    } else {
      out.tags.push({ pane: node.pane, x: from.x + 12, y: from.y + from.h, word: h.word, holder: node.waitingOn.kind, detail: clip(node.waitingOn.text, 120) });
    }
  }

  for (const kind of ["message", "sent"]) {
    const all = model.edges[kind];
    // Newest first, as the graph sends them. Edges to or from a pane that is
    // not a node are left out, and so are edges from a pane to itself.
    const usable = all.filter((e) => e.from !== e.to && box(e.from) && box(e.to));
    const shown = usable.slice(0, maxDrawn);
    for (let i = 0; i < shown.length; i++) {
      const e = shown[i];
      const word = kind === "sent" ? "sent" : deliveryWord(e.delivery);
      pending.push({ list: kind, kind, from: e.from, to: e.to, word, delivery: kind === "message" ? e.delivery : "", text: e.text, time: e.time, key: kind[0] + ":" + i });
    }
    const parts = [];
    const noun = kind === "message" ? "messages" : "sent notes";
    if (all.length >= GRAPH_EDGE_CAP) parts.push(`the graph holds only the newest ${GRAPH_EDGE_CAP}`);
    if (shown.length < usable.length) parts.push(`${shown.length} of ${usable.length} drawn`);
    if (parts.length > 0) out.notices.push(`Edges: ${noun}, ${parts.join("; ")}`);
  }

  for (const p of pending) {
    const a = layout.byPane.get(p.from);
    const b = layout.byPane.get(p.to);
    const n = lane(p.from, p.to);
    const c = curve(a, b, n === 0 ? 0 : (n % 2 === 1 ? 1 : -1) * Math.ceil(n / 2) * LANE);
    out[p.list].push({ ...p, d: c.d, mid: c.mid });
  }
  return out;
}

/** The marker shapes the edges' lines end in. Given once per graph. */
export function EdgeDefs() {
  const arrow = (id, cls) => html`
    <marker id=${id} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
      <path class=${cls} d="M0 0L10 5L0 10z" />
    </marker>
  `;
  return html`<defs>
    ${arrow("gedge-arrow", "gedge-head")}
    ${arrow("gedge-arrow-amber", "gedge-head gedge-head--amber")}
    ${arrow("gedge-arrow-wait", "gedge-head gedge-head--wait")}
  </defs>`;
}

function Label({ at, word, cls }) {
  // The width is a guess from the number of characters; the text is centred.
  const w = Math.round(word.length * 6.4 + 10);
  return html`<g class=${"gedge-label " + cls}>
    <rect x=${at.x - w / 2} y=${at.y - 9} width=${w} height="16" rx="4" />
    <text x=${at.x} y=${at.y + 3} text-anchor="middle">${word}</text>
  </g>`;
}

/**
 * Edges draws what edgesOf worked out. It is drawn before the boxes, so a box
 * is never behind a line.
 */
export function Edges({ e }) {
  return html`<g class="gedges">
    ${e.sent.map(
      (x) => html`<g class="gedge gedge--sent" data-from=${x.from} data-to=${x.to} key=${x.key}>
        <title>${"Sent: " + x.from + " to " + x.to}</title>
        <path d=${x.d} marker-end="url(#gedge-arrow)" />
        <${Label} at=${x.mid} word=${x.word} cls="gedge-label--sent" />
      </g>`,
    )}
    ${e.waiting.map(
      (x) => html`<g class="gedge gedge--waiting" data-from=${x.from} data-to=${x.to} key=${x.key}>
        <title>${x.word}</title>
        <path d=${x.d} marker-end="url(#gedge-arrow-wait)" />
        <${Label} at=${x.mid} word=${x.word} cls="gedge-label--wait" />
      </g>`,
    )}
    ${e.message.map((x) => {
      const undelivered = x.delivery === "queued";
      return html`<g
        class=${"gedge gedge--message" + (undelivered ? " gedge--undelivered" : "")}
        data-from=${x.from}
        data-to=${x.to}
        data-delivery=${x.delivery}
        key=${x.key}
      >
        <title>${clip(x.text, 140) + " (" + x.word + ")"}</title>
        <path d=${x.d} marker-end=${undelivered ? "url(#gedge-arrow-amber)" : "url(#gedge-arrow)"} />
        <${Label} at=${x.mid} word=${x.word} cls=${undelivered ? "gedge-label--amber" : "gedge-label--msg"} />
      </g>`;
    })}
    ${e.tags.map(
      (t) => html`<g class="gedge gedge--tag" data-tagged=${t.pane} data-holder=${t.holder} key=${"t:" + t.pane}>
        <title>${t.detail ? t.word + ": " + t.detail : t.word}</title>
        <path d=${`M${t.x} ${t.y}v8`} />
        <${Label} at=${{ x: t.x + t.word.length * 3.2 + 5, y: t.y + 16 }} word=${t.word} cls="gedge-label--wait" />
      </g>`,
    )}
  </g>`;
}

/** EdgeNotice is the corner line when a kind of edge is capped or thinned. */
export function EdgeNotice({ e, width }) {
  if (e.notices.length === 0) return null;
  return html`<g class="gedge-notice" role="note">
    ${e.notices.map((n, i) => html`<text x=${width - 8} y=${14 + i * 16} text-anchor="end" key=${i}>${n}</text>`)}
  </g>`;
}
