// The activity feed's entries, made by comparing one graph model with the one
// before it. The desktop does not send an event stream, so this is what the feed
// has: a thing that happened between two pushes shows up as a difference.
// Pure functions, no DOM, no socket and no clock (the time is passed in).
//
// What it can and cannot say, because of that:
// - A change that came and went between two pushes is not seen.
// - The time of an entry is the time the push was received, not the time the
//   thing happened. For a need the push carries its own time; that is not used,
//   so the feed's order is the order the pushes arrived in.
// - The first push, and a push for another project, gives no entries: it is the
//   starting point, not news.
//
// Every text in an entry is made from names and words the graph model already
// holds as strings. Nothing is read as markup.
import { needLabel, labelOf } from "./model.js";

/** FEED_CAP is how many entries the feed keeps, newest first. */
export const FEED_CAP = 200;

const TEXT_CUT = 140;

// How a need kind reads in the feed. A kind this file does not know falls back
// to the model's label.
const NEED_LINES = {
  permission: (who, text) => (text ? who + " asks to run " + text : who + " asks to run something"),
  ask: (who, text) => (text ? who + " asks: " + text : who + " asks a question"),
  returned: (who) => "A task came back from review to " + who,
  conflict: (who, text, other) => who + " and " + (other || "another pane") + " both change " + (text || "the same file"),
  down: (who) => who + " ended",
  setup: (who) => who + " needs setting up",
  "stale-unread": (who) => who + " has unread mail and is idle",
  stuck: (who) => who + " is working but has written nothing for a while",
};

const NEED_FEED_KIND = { permission: "need", ask: "need", returned: "need", conflict: "need", setup: "need", down: "down", stuck: "warn", "stale-unread": "warn" };

// How far a message has got, as the feed says it. Only a step forward is news.
const DELIVERY_LINES = {
  notified: (to, sender) => to + " was told about " + sender + "'s message",
  woken: (to, sender) => to + " was woken for " + sender + "'s message",
  read: (to, sender) => to + " read " + sender + "'s message",
};

// Task states that end the task's work. Other states are said as they are.
const TASK_SHIP = ["done", "merged", "closed"];

function cut(s, n = TEXT_CUT) {
  const t = String(s || "").replace(/\s+/g, " ").trim();
  return t.length > n ? t.slice(0, n - 1) + "…" : t;
}

function stop(t) {
  return /[.?!…]$/.test(t) ? t : t + ".";
}

function nameOf(model, prev, pane) {
  // The model the pane is in names it (a pane that left is named by the one it left); panes that share a name read as what they are on.
  const src = model && model.byPane.has(pane) ? model : prev && prev.byPane.has(pane) ? prev : null;
  if (!src) return pane;
  const n = src.byPane.get(pane);
  return !n.name && n.peer ? n.peer : labelOf(src, pane);
}

function sameProject(a, b) {
  return a && b && a.ok && b.ok && a.root === b.root;
}

/**
 * diffGraphs says what happened between two graph models, as entries oldest
 * first. An entry is { kind, pane, other, text, at }: kind is "need", "warn",
 * "down", "ship" or "info", pane the pane it is about (a row to focus), other a
 * second pane when there is one, text one line and at the time in ms.
 */
export function diffGraphs(prev, next, now) {
  if (!sameProject(prev, next)) return [];
  const out = [];
  // Entries end with a full stop, as the brief writes them.
  const add = (kind, pane, text, other = "") => out.push({ kind, pane, other, text: stop(cut(text)), at: now });
  const who = (pane) => nameOf(next, prev, pane);

  // Panes that came and went, and the helpers a manager started.
  for (const n of next.nodes) {
    const was = prev.byPane.get(n.pane);
    if (!was) {
      const parent = n.parent && next.byPane.has(n.parent) ? who(n.parent) : "";
      if (n.shell) add("info", n.pane, "A shell " + who(n.pane) + " opened");
      else add("info", n.pane, parent ? parent + " started " + who(n.pane) : who(n.pane) + " started", n.parent);
      continue;
    }
    if (was.status !== n.status && !n.shell) {
      statusEntry(add, who, was, n);
    }
    const a = was.task, b = n.task;
    if (b && b.state && (!a || a.id !== b.id || a.state !== b.state)) {
      const word = b.state.replace(/[-_]/g, " ");
      add(TASK_SHIP.includes(b.state) ? "ship" : "info", n.pane, who(n.pane) + ": " + b.id + " is " + word);
    }
  }
  for (const was of prev.nodes) {
    if (!next.byPane.has(was.pane) && !was.shell) add("info", was.pane, who(was.pane) + " was closed");
  }

  // Things that ask something of the person, and the quiet kinds.
  const had = new Set(prev.needs.map((n) => n.id));
  for (const n of next.needs) {
    if (!n.id || had.has(n.id)) continue;
    // A pane that ended has its own entry above.
    if (n.kind === "down" && next.byPane.get(n.pane) && next.byPane.get(n.pane).status === "exited" && prev.byPane.has(n.pane) && prev.byPane.get(n.pane).status !== "exited") continue;
    const line = NEED_LINES[n.kind];
    const text = n.textHidden ? "" : cut(n.text, 80);
    const msg = line ? line(who(n.pane), text, n.other ? who(n.other) : "") : who(n.pane) + ": " + needLabel(n.kind);
    add(NEED_FEED_KIND[n.kind] || "info", n.pane, msg, n.other || "");
  }

  // Messages sent, and how far each has got.
  const before = new Map(prev.edges.message.filter((e) => e.id).map((e) => [e.id, e]));
  for (const e of next.edges.message) {
    if (!e.id) continue;
    const was = before.get(e.id);
    if (!was) {
      const gist = e.text ? ": " + cut(e.text, 60) : "";
      add("info", e.to, who(e.from) + " sent " + who(e.to) + " a message" + gist, e.from);
      continue;
    }
    if (e.deliveryRank > was.deliveryRank && DELIVERY_LINES[e.delivery]) {
      add("info", e.to, DELIVERY_LINES[e.delivery](who(e.to), who(e.from)), e.from);
    }
  }
  return out;
}

// A change of status is news only when the pane ended. A turn that finished or a
// wait that ended is not an entry: the feed's brief lists its kinds, and a feed
// that said every idle and working flip would push real news out of its 200 in
// minutes. A wait that begins is told by its needs item.
function statusEntry(add, who, was, n) {
  if (n.status === "exited") add("down", n.pane, who(n.pane) + " exited");
}

/**
 * pushEntries adds new entries to the feed, newest first, and keeps the cap.
 * Each entry gets an id that is not reused, from the feed's own counter. It
 * returns a new feed { seq, entries }, and the same one when nothing is added.
 */
export function pushEntries(feed, fresh, cap = FEED_CAP) {
  const cur = feed || { seq: 0, entries: [] };
  if (!fresh || fresh.length === 0) return cur;
  let seq = cur.seq;
  const made = [];
  for (const e of fresh) made.push({ ...e, id: ++seq });
  made.reverse();
  return { seq, entries: made.concat(cur.entries).slice(0, cap) };
}

/** feedStep is one turn of the feed: the entries the change from prev to next makes, added to feed. */
export function feedStep(feed, prev, next, now, cap = FEED_CAP) {
  return pushEntries(feed, diffGraphs(prev, next, now), cap);
}

/**
 * agoOf is how long ago an entry was, in a few characters: "now", "3m", "2h",
 * "4d". A time ahead of now counts as now.
 */
export function agoOf(at, now) {
  const s = Math.floor((now - at) / 1000);
  if (!Number.isFinite(s) || s < 45) return "now";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m";
  const h = Math.floor(m / 60);
  if (h < 48) return h + "h";
  return Math.floor(h / 24) + "d";
}
