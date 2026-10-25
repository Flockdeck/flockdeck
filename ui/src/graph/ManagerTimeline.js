// The manager's panel as one timeline: what the manager said (its conversation),
// what the team sent it (the graph's message edges) and what happened to the team's
// work (the activity feed's finished and ended entries), oldest first, so the
// newest is at the bottom next to the send box.
//
// This is the model only. It reads data the page already has and sends nothing.
// Two limits come from the desk and are carried to the draw: the graph keeps the
// last 100 message edges of the project (not of one pane), and an edge's text is
// its first line cut to 140 characters. A mail item says when its text was cut,
// so the panel can point at the conversation for the rest.
import { labelOf } from "./model.js";

/** EDGE_CUT is how many characters the desk keeps of a message in a graph push. */
export const EDGE_CUT = 140;

/** EDGE_CAP is how many message edges a graph push holds for the whole project. */
export const EDGE_CAP = 100;

const when = (iso) => {
  const t = Date.parse(iso);
  return Number.isFinite(t) ? t : NaN;
};

/** cutOf says an edge's text was shortened by the desk: it fills the cut or ends in the cut mark. */
export function cutOf(text) {
  const s = typeof text === "string" ? text : "";
  return Array.from(s).length >= EDGE_CUT - 1 || s.endsWith("…");
}

/**
 * managerTimeline merges the three sources for the pane `manager`.
 *   entries: the manager's conversation entries in their own order ({ id, ts, ... }).
 *   model: the graph model, for the message edges and the labels of the senders.
 *   events: the feed's entries ({ kind, pane, text, at }), any order.
 * It answers items oldest first. An item is { key, at, kind, ... } where kind is
 *   "entry": entry is the conversation entry, unchanged;
 *   "mail": from, label, text, delivery, waiting (not yet read), cut;
 *   "event": tone ("ship" or "down"), pane, text.
 * The conversation keeps its own order: an entry with no usable time takes the time
 * of the one before it, so the merge never moves an entry past its neighbours.
 */
export function managerTimeline({ entries = [], model = null, manager = "", events = [] } = {}) {
  const conv = [];
  let last = -Infinity;
  for (const e of entries) {
    const t = when(e && e.ts);
    const at = Number.isNaN(t) ? last : Math.max(t, last);
    if (Number.isFinite(at)) last = at;
    conv.push({ key: "c:" + (e && e.id), at, kind: "entry", entry: e });
  }
  const other = [];
  if (model && model.ok && manager) {
    for (const e of model.edges.message) {
      if (e.to !== manager) continue;
      other.push({ key: "m:" + e.id, at: when(e.time), kind: "mail", from: e.from, label: labelOf(model, e.from), text: e.text, delivery: e.delivery, waiting: e.delivery !== "read", cut: cutOf(e.text) });
    }
  }
  for (const v of events) {
    if (!v || (v.kind !== "ship" && v.kind !== "down")) continue;
    if (manager && v.pane === manager) continue;
    other.push({ key: "e:" + v.at + ":" + v.pane + ":" + v.text, at: Number(v.at), kind: "event", tone: v.kind, pane: v.pane, text: v.text });
  }
  const timed = other.filter((x) => Number.isFinite(x.at)).sort((a, b) => a.at - b.at);
  // Items with no time are left out: there is nowhere honest to put them.
  const out = [];
  let i = 0;
  for (const c of conv) {
    while (i < timed.length && Number.isFinite(c.at) && timed[i].at < c.at) out.push(timed[i++]);
    out.push(c);
  }
  while (i < timed.length) out.push(timed[i++]);
  return out;
}
