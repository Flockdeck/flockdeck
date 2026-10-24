// The goal tabs: one tab for each team in the graph push, and one "Solo" tab for
// every pane in no team. This file holds the data and the changes to it, and
// draws nothing.
//
// What the person decides is kept in the interface's preferences (uiPrefs) under
// one key, "tabs": a name they gave a goal, the order they put the tabs in, and
// the goals they archived. Archiving only hides a tab in this interface; the
// team goes on. All three are keyed by team id and are looked up in the graph, so
// an entry for a team the push no longer lists does nothing, and is dropped the
// next time the value is written.
import { needsYouOf } from "./model.js";

/** TABS_KEY is the key of the interface's preferences the tab choices are kept under. */
export const TABS_KEY = "tabs";

/** SOLO is the id of the tab for panes in no team. It is never a team's id. */
export const SOLO = "solo";

/** LABEL_MAX is the longest a tab's label is, in characters, before it is cut. */
export const LABEL_MAX = 24;

/** NAME_MAX is the longest a name the person gives a tab may be. */
export const NAME_MAX = 80;

// The most of each list kept, so a value cannot grow without limit.
const KEEP = 200;

/** labelOf makes a short label from a goal: the first line, its words joined by
 *  single spaces, cut at a word boundary with an ellipsis. A goal with nothing
 *  to read gives "Goal". */
export function labelOf(goal, max = LABEL_MAX) {
  const first = String(goal || "").split(/\r?\n/).find((l) => l.trim() !== "") || "";
  const text = first.replace(/^\s*goal\s*[:\-]\s*/i, "").replace(/\s+/g, " ").trim();
  if (text === "") return "Goal";
  const chars = [...text];
  if (chars.length <= max) return text;
  const cut = chars.slice(0, max - 1).join("");
  const space = cut.lastIndexOf(" ");
  const kept = space >= Math.floor(max / 2) ? cut.slice(0, space) : cut;
  return kept.replace(/[\s.,;:!?-]+$/, "") + "…";
}

const isObject = (v) => v !== null && typeof v === "object" && !Array.isArray(v);
const idList = (v) => (Array.isArray(v) ? v.filter((x) => typeof x === "string" && x !== "" && x !== SOLO) : []).slice(0, KEEP);

function unique(ids) {
  return [...new Set(ids)];
}

/** emptyTabs is the choices of a person who has made none. */
export function emptyTabs() {
  return { names: {}, order: [], archived: [] };
}

/** tabsStateOf reads the choices from the interface's preferences (prefs.ui) and
 *  keeps only what is well formed: a name that is a string, a list of ids. Whatever
 *  else the value holds, or is not, is left out. */
export function tabsStateOf(ui) {
  const raw = isObject(ui) && isObject(ui[TABS_KEY]) ? ui[TABS_KEY] : {};
  const names = {};
  if (isObject(raw.names)) {
    for (const [id, name] of Object.entries(raw.names).slice(0, KEEP)) {
      if (id === SOLO || typeof name !== "string") continue;
      const t = name.trim().slice(0, NAME_MAX);
      if (t !== "") names[id] = t;
    }
  }
  return { names, order: unique(idList(raw.order)), archived: unique(idList(raw.archived)) };
}

// How busy a group of panes is, as the tab says it: waiting when any needs the
// person, else working when any works.
function activityOf(model, panes) {
  let working = false;
  for (const p of panes) {
    if (needsYouOf(model, p)) return "waiting";
    const n = model.byPane.get(p);
    if (n && (n.activity || n.status) === "working") working = true;
  }
  return working ? "working" : "";
}

function needCount(model, panes) {
  let n = 0;
  for (const p of panes) if (needsYouOf(model, p)) n++;
  return n;
}

/**
 * goalTabsOf lists the tabs for a graph model and the person's choices:
 *   tabs: the open ones, the goals in the person's order (a goal they have not
 *     placed goes after those, in the graph's order) and Solo last, each
 *     { id, kind: "team" | "solo", label, named, title, needs, activity }.
 *     needs is how many of its panes wait on the person, and activity "waiting",
 *     "working" or "".
 *   archived: the goals the person put away, as the same objects.
 * Solo is always listed, even with no panes, so the strip does not change shape
 * when the last solo pane ends; it cannot be archived.
 */
export function goalTabsOf(model, state) {
  const s = state || emptyTabs();
  const teams = model && model.ok ? model.teams : [];
  const made = new Map();
  for (const t of teams) {
    if (!t.id || made.has(t.id)) continue;
    const named = typeof s.names[t.id] === "string";
    const label = named ? s.names[t.id] : labelOf(t.goal);
    made.set(t.id, {
      id: t.id,
      kind: "team",
      label,
      named,
      title: t.goal || label,
      needs: needCount(model, t.members),
      activity: activityOf(model, t.members),
    });
  }
  const placed = s.order.filter((id) => made.has(id));
  const rest = [...made.keys()].filter((id) => !placed.includes(id));
  const archived = new Set(s.archived);
  const all = [...placed, ...rest].map((id) => made.get(id));
  const solo = model && model.ok ? model.solo.map((n) => n.pane) : [];
  return {
    tabs: [
      ...all.filter((t) => !archived.has(t.id)),
      { id: SOLO, kind: "solo", label: "Solo", named: false, title: "Panes that are in no goal", needs: needCount(model, solo), activity: activityOf(model, solo) },
    ],
    archived: all.filter((t) => archived.has(t.id)),
  };
}

/** activeTabOf says which tab is open: the one the window chose when it is still
 *  listed, else the first goal that waits on the person, else the first tab. */
export function activeTabOf(tabs, chosen) {
  if (tabs.some((t) => t.id === chosen)) return chosen;
  const waiting = tabs.find((t) => t.kind === "team" && t.needs > 0);
  return waiting ? waiting.id : tabs.length ? tabs[0].id : "";
}

/** teamOfTab is the model's team for a tab, or null for Solo or a tab that is
 *  gone. */
export function teamOfTab(model, id) {
  return model && model.ok && id !== SOLO ? model.teams.find((t) => t.id === id) || null : null;
}

/** renameTab gives a goal a name, or takes the name away when text is empty so
 *  that the label comes from the goal again. Solo has no name to change. */
export function renameTab(state, id, text) {
  if (!id || id === SOLO) return state;
  const names = { ...state.names };
  const t = String(text || "").replace(/\s+/g, " ").trim().slice(0, NAME_MAX);
  if (t === "") delete names[id];
  else names[id] = t;
  return { ...state, names };
}

/** goalIds is the ids of the open goal tabs, the order they are drawn in. */
function goalIds(tabs) {
  return tabs.filter((t) => t.kind === "team").map((t) => t.id);
}

/** moveTab puts a goal tab at an index among the open goals (a drag ends there).
 *  The whole order of the open goals is written, so the move is not undone by a
 *  goal that was not placed before. Moving to where it is, or an unknown tab, is
 *  no change. */
export function moveTab(state, tabs, id, to) {
  const ids = goalIds(tabs);
  const from = ids.indexOf(id);
  if (from < 0 || !Number.isInteger(to)) return state;
  const at = Math.max(0, Math.min(ids.length - 1, to));
  if (at === from) return state;
  ids.splice(from, 1);
  ids.splice(at, 0, id);
  // Archived goals keep their place among those not shown: after the open ones.
  const hidden = state.order.filter((x) => !ids.includes(x));
  return { ...state, order: [...ids, ...hidden] };
}

/** stepTab moves a goal tab one place left (-1) or right (1). */
export function stepTab(state, tabs, id, dir) {
  const at = goalIds(tabs).indexOf(id);
  return at < 0 ? state : moveTab(state, tabs, id, at + (dir < 0 ? -1 : 1));
}

/** canStep says whether stepTab would change anything. */
export function canStep(tabs, id, dir) {
  const ids = goalIds(tabs);
  const at = ids.indexOf(id);
  return at >= 0 && at + (dir < 0 ? -1 : 1) >= 0 && at + (dir < 0 ? -1 : 1) < ids.length;
}

/** archiveTab hides a goal's tab. Solo cannot be hidden. */
export function archiveTab(state, id) {
  if (!id || id === SOLO || state.archived.includes(id)) return state;
  return { ...state, archived: [...state.archived, id].slice(-KEEP) };
}

/** restoreTab shows an archived goal's tab again, where it was. */
export function restoreTab(state, id) {
  return state.archived.includes(id) ? { ...state, archived: state.archived.filter((x) => x !== id) } : state;
}

/**
 * tabsValueOf is the value to write under TABS_KEY, or null when nothing is
 * chosen (the key is then removed). Entries for a team the model does not list are
 * dropped, but only from a model that is a graph: with none, nothing is known and
 * nothing is dropped.
 */
export function tabsValueOf(state, model) {
  const known = model && model.ok ? new Set(model.teams.map((t) => t.id)) : null;
  const keep = (id) => known === null || known.has(id);
  const names = {};
  for (const id of Object.keys(state.names).sort()) if (keep(id)) names[id] = state.names[id];
  const order = state.order.filter(keep);
  const archived = state.archived.filter(keep);
  if (Object.keys(names).length === 0 && order.length === 0 && archived.length === 0) return null;
  const value = {};
  if (Object.keys(names).length > 0) value.names = names;
  if (order.length > 0) value.order = order;
  if (archived.length > 0) value.archived = archived;
  return value;
}
