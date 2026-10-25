// The manager's panel as one timeline: what the manager said (its conversation),
// what the team sent it (the graph's message edges) and what happened to the team's
// work (the activity feed's finished and ended entries), oldest first, so the
// newest is at the bottom next to the send box.
//
// This is the model only. It reads data the page already has and sends nothing.
// It is a good order, not an exact one:
// - The graph keeps the last EDGE_CAP message edges of the whole project, not of
//   one pane, so mail older than the oldest kept edge is missing (mailFloor says
//   from when the mail is complete).
// - An edge holds only the first non-empty line of a message, cut to EDGE_CUT
//   characters, with no mark when it was cut. The desk does not say whether more
//   followed, so every mail item is "first line only" (task T173 on the board
//   asks the desk for a field), and a line that fills the cut says it may be cut.
// - A feed event is timed when this page saw the change, not when it happened, so
//   one noticed late sorts after conversation entries it came before.
import { labelOf } from "./model.js";

/** EDGE_CUT is how many characters the desk keeps of a message in a graph push. */
export const EDGE_CUT = 140;

/** EDGE_CAP is how many message edges a graph push holds for the whole project. */
export const EDGE_CAP = 100;

// A time must be a real one: after the year 2000, as the graph model reads times.
const MIN_TIME = Date.UTC(2000, 0, 1);
const timeOf = (v) => {
  const t = typeof v === "number" ? v : Date.parse(v);
  return Number.isFinite(t) && t >= MIN_TIME ? t : NaN;
};
const list = (v) => (Array.isArray(v) ? v : []);
const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);

/** maybeCut says an edge's line fills the desk's cut, so more of it may have been left out. */
export function maybeCut(text) {
  return typeof text === "string" && Array.from(text).length >= EDGE_CUT - 1;
}

/** mailFloor is the time from which the mail is complete: the oldest message edge the graph kept when it is full (the cap was reached, so older ones were dropped), else null. */
export function mailFloor(model) {
  if (!model || !model.ok) return null;
  const edges = model.edges.message;
  if (edges.length < EDGE_CAP) return null;
  const times = edges.map((e) => timeOf(e.time)).filter(Number.isFinite);
  return times.length ? Math.min(...times) : null;
}

/** isManager says whether pane leads a team in the model. */
export function isManager(model, pane) {
  return !!(model && model.ok && pane && model.teams.some((t) => t.manager === pane));
}

/**
 * managerTimeline merges the three sources for the pane `manager`.
 *   entries: the manager's conversation entries in their own order ({ id, ts, ... }).
 *   model: the graph model, for the message edges and the labels of the senders.
 *   events: the feed's entries ({ kind, pane, text, at }), any order; only those of
 *     the manager's own team are used.
 * It answers items oldest first. An item is { key, at, kind, ... } where kind is
 *   "entry": entry is the conversation entry, unchanged;
 *   "mail": from, label, text, delivery, waiting (not yet read), maybeCut;
 *   "event": tone ("ship" or "down"), pane, text.
 * The conversation keeps its own order: an entry with no usable time takes the time
 * of the one before it, so the merge never moves an entry past its neighbours. Mail
 * and events with no usable time are left out: there is nowhere honest to put them.
 */
export function managerTimeline({ entries = [], model = null, manager = "", events = [] } = {}) {
  const conv = [];
  let last = -Infinity;
  list(entries).forEach((e, i) => {
    if (!isObject(e)) return;
    const t = timeOf(e.ts);
    const at = Number.isNaN(t) ? last : Math.max(t, last);
    if (Number.isFinite(at)) last = at;
    conv.push({ key: "c:" + (e.id !== undefined && e.id !== "" ? e.id : "#" + i), at, kind: "entry", entry: e });
  });
  const other = [];
  if (model && model.ok && manager) {
    for (const e of list(model.edges.message)) {
      if (!isObject(e) || e.to !== manager) continue;
      other.push({ key: "m:" + (e.id ? e.id : e.time + ":" + e.from + ":" + e.text), at: timeOf(e.time), kind: "mail", from: e.from, label: labelOf(model, e.from), text: e.text, delivery: e.delivery, waiting: e.delivery !== "read", maybeCut: maybeCut(e.text) });
    }
    const team = model.teams.find((t) => t.manager === manager);
    const mine = new Set(team ? list(team.members) : []);
    for (const v of list(events)) {
      if (!isObject(v) || (v.kind !== "ship" && v.kind !== "down") || v.pane === manager || !mine.has(v.pane)) continue;
      other.push({ key: "e:" + v.at + ":" + v.pane + ":" + v.text, at: timeOf(v.at), kind: "event", tone: v.kind, pane: v.pane, text: v.text });
    }
  }
  const timed = other.filter((x) => Number.isFinite(x.at)).sort((a, b) => a.at - b.at);
  const out = [];
  let i = 0;
  for (const c of conv) {
    while (i < timed.length && Number.isFinite(c.at) && timed[i].at < c.at) out.push(timed[i++]);
    out.push(c);
  }
  while (i < timed.length) out.push(timed[i++]);
  return out;
}

/**
 * timelineEntries is the timeline as entries the conversation list can draw: a
 * conversation entry is itself, and mail and events become entries of kind "mail"
 * and "event" with the item's key as id, so the list's keys, jumps and anchoring
 * work on them as on the rest.
 */
export function timelineEntries(items) {
  return list(items).map((it) => {
    if (it.kind === "entry") return it.entry;
    const ts = Number.isFinite(it.at) ? new Date(it.at).toISOString() : "";
    if (it.kind === "mail") return { id: it.key, kind: "mail", ts, from: it.from, label: it.label, text: it.text, waiting: it.waiting, maybeCut: it.maybeCut };
    return { id: it.key, kind: "event", ts, tone: it.tone, pane: it.pane, text: it.text };
  });
}
