// Which file overlaps the manager has been asked about. The need comes from the
// radar and clears only when the two worktrees stop overlapping, so without a
// record the item would stay at full priority after the ask and the button
// would be offered again. The record is per overlapping pair (the need id) and
// is kept in the browser, so a redraw, a remount or a reload does not lose it.
//
// A record holds the files the manager was told about. If the overlap grows to
// a file that is not in it, the item is not covered and returns to full priority.

const KEY = "flockdeck.overlapAsked";
const MAX_RECORDS = 200;

let memory = {};
let loaded = false;
const listeners = new Set();

function store() {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch (e) {
    return null;
  }
}

function load() {
  if (loaded) return;
  loaded = true;
  const s = store();
  if (!s) return;
  try {
    const v = JSON.parse(s.getItem(KEY) || "{}");
    if (v && typeof v === "object" && !Array.isArray(v)) memory = v;
  } catch (e) {
    memory = {};
  }
}

function save() {
  const s = store();
  if (!s) return;
  try {
    s.setItem(KEY, JSON.stringify(memory));
  } catch (e) {
    // The record stays in memory for this page.
  }
}

/** overlapFiles reads a conflict's text ("a, b and 3 more") into { paths, more }. */
export function overlapFiles(text) {
  let t = String(text || "").trim();
  let more = 0;
  const m = /(?:^|\s)and (\d+) more$/.exec(t);
  if (m) {
    more = Number(m[1]);
    t = t.slice(0, m.index).trim();
  }
  const paths = t === "" ? [] : t.split(", ").map((p) => p.trim()).filter((p) => p !== "");
  return { paths, more };
}

/** isCovered says the files now overlapping are all ones the manager was told about. */
export function isCovered(record, text) {
  if (!record || !Array.isArray(record.paths)) return false;
  const now = overlapFiles(text);
  const told = new Set(record.paths);
  return now.paths.every((p) => told.has(p)) && now.more <= (record.more || 0);
}

/** askedRecords is the whole record, id of a conflict need to { paths, more, at }. */
export function askedRecords() {
  load();
  return memory;
}

/** markAsked notes that the manager was asked about the conflict need id, whose files are in text. */
export function markAsked(id, text, now = Date.now()) {
  if (!id) return;
  load();
  const f = overlapFiles(text);
  memory = { ...memory, [id]: { paths: f.paths, more: f.more, at: now } };
  const keys = Object.keys(memory);
  if (keys.length > MAX_RECORDS) {
    keys.sort((a, b) => (memory[a].at || 0) - (memory[b].at || 0));
    for (const k of keys.slice(0, keys.length - MAX_RECORDS)) delete memory[k];
  }
  save();
  for (const fn of Array.from(listeners)) fn();
}

/** clearAsked forgets the ask for one conflict need, so it is asked again. */
export function clearAsked(id) {
  load();
  if (!Object.prototype.hasOwnProperty.call(memory, id)) return;
  const next = { ...memory };
  delete next[id];
  memory = next;
  save();
  for (const fn of Array.from(listeners)) fn();
}

/** subscribeAsked calls fn after the record changes; the result stops it. */
export function subscribeAsked(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** resetAskedForTest drops the in-memory record and the stored copy. */
export function resetAskedForTest() {
  memory = {};
  loaded = true;
  const s = store();
  if (s) {
    try {
      s.removeItem(KEY);
    } catch (e) {
      // nothing to remove
    }
  }
  for (const fn of Array.from(listeners)) fn();
}
