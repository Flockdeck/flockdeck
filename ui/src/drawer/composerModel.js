// The prompt box of a structured pane, without the drawing: which key sends,
// the history of what the pane was sent, the @ mention, and what is kept of the
// draft and the history for a pane between visits.
//
// Everything kept is the person's own text, kept in this window's storage and
// nowhere else, per pane, and bounded: a number of prompts, a length for each,
// a length for the draft and a number of panes. Storage may be missing or full
// (a private window, a blocked profile); every use of it is guarded and the
// pane's record is then kept in memory only, which still survives a switch of
// tab.

/** HISTORY_MAX is how many sent prompts a pane remembers. ENTRY_MAX is the longest one that is
 *  remembered (longer ones are sent but not kept), DRAFT_MAX the longest draft kept, and PANES_MAX
 *  how many panes' records are kept, the least recently used going first. */
export const HISTORY_MAX = 50;
export const ENTRY_MAX = 8 * 1024;
export const DRAFT_MAX = 16 * 1024;
export const PANES_MAX = 30;
export const PANE_ID_MAX = 80;
/** MENTION_MAX is the longest query after an @ that is looked up. */
export const MENTION_MAX = 200;
export const ROWS_MIN = 2;
export const ROWS_MAX = 10;

const PREFIX = "flockdeck.composer.v1:";
const INDEX = "flockdeck.composer.v1.index";

/** sendKey says what a keydown on the box means: "send" for Enter and for Ctrl or Command with Enter,
 *  "newline" for Shift with Enter, and null for anything else, including a key that belongs to an
 *  input method's composition. */
export function sendKey(e) {
  if (!e || e.key !== "Enter" || e.isComposing || e.keyCode === 229) return null;
  if (e.shiftKey) return "newline";
  if (e.altKey) return null;
  return "send";
}

/** rowsFor is how many rows the box is drawn with: one per line of the draft, between ROWS_MIN and ROWS_MAX. */
export function rowsFor(draft) {
  const lines = typeof draft === "string" ? draft.split("\n").length : 1;
  return Math.max(ROWS_MIN, Math.min(ROWS_MAX, lines));
}

// ---------------------------------------------------------------- history

/** pushHistory is the history with text added as the latest. Empty text, text too long to keep and a
 *  repeat of the latest are not added. The oldest go past HISTORY_MAX. */
export function pushHistory(history, text) {
  const list = Array.isArray(history) ? history : [];
  if (typeof text !== "string" || text.trim() === "" || text.length > ENTRY_MAX) return list;
  if (list.length && list[list.length - 1] === text) return list;
  const next = [...list, text];
  return next.length > HISTORY_MAX ? next.slice(next.length - HISTORY_MAX) : next;
}

/** NO_RECALL is the state of a box that is not showing an earlier prompt. */
export const NO_RECALL = Object.freeze({ at: -1, stash: "" });

/** onFirstLine and onLastLine say whether the caret is where Up and Down would leave the text, so
 *  that in a draft of several lines the arrows move through the lines first. A selection is not at
 *  either. */
export function onFirstLine(text, start, end = start) {
  return start === end && !text.slice(0, start).includes("\n");
}
export function onLastLine(text, start, end = start) {
  return start === end && !text.slice(end).includes("\n");
}

/** recall steps through the history: dir -1 is back to an older prompt, 1 forward to a newer one and
 *  at last to the draft that was being written. It returns { text, nav } or null when there is
 *  nowhere to go. */
export function recall(history, nav, dir, draft) {
  const list = Array.isArray(history) ? history : [];
  const cur = nav && Number.isInteger(nav.at) ? nav : NO_RECALL;
  if (dir < 0) {
    if (cur.at + 1 >= list.length) return null;
    const at = cur.at + 1;
    return { text: list[list.length - 1 - at], nav: { at, stash: cur.at < 0 ? String(draft || "") : cur.stash } };
  }
  if (cur.at < 0) return null;
  const at = cur.at - 1;
  return { text: at < 0 ? cur.stash : list[list.length - 1 - at], nav: at < 0 ? NO_RECALL : { at, stash: cur.stash } };
}

// ---------------------------------------------------------------- mention

/** mentionAt is the @ word the caret is in: { start, end, query }, where start is the index of the @
 *  and end the end of the word, or null. The @ must open the text or follow white space, so an
 *  address like a@b is not a mention. */
export function mentionAt(text, caret) {
  if (typeof text !== "string") return null;
  const at = Math.max(0, Math.min(typeof caret === "number" ? caret : text.length, text.length));
  const before = /(^|\s)@([^\s]*)$/u.exec(text.slice(0, at));
  if (!before) return null;
  const start = at - before[2].length - 1;
  const after = /^[^\s]*/u.exec(text.slice(at))[0];
  const query = before[2] + after;
  if (query.length > MENTION_MAX || query.includes("@")) return null;
  return { start, end: at + after.length, query };
}

/** mentionText is what choosing a file writes for the mention: "@path" and a space, with the path
 *  in quotes when it has white space in it. */
export function mentionText(path) {
  const p = String(path);
  return /\s/u.test(p) && !p.includes('"') ? '@"' + p + '" ' : "@" + p + " ";
}

/** insertMention replaces the mention m in text with the chosen path: { text, caret }. */
export function insertMention(text, m, path) {
  const ins = mentionText(path);
  return { text: text.slice(0, m.start) + ins + text.slice(m.end), caret: m.start + ins.length };
}

/** cleanFiles is the file names of a reply as the picker may show them: strings of a sane length,
 *  without control characters, at most max. The desktop sent only such names; this holds if it is
 *  ever sent others. */
export function cleanFiles(list, max = 50) {
  const out = [];
  if (!Array.isArray(list)) return out;
  for (const f of list) {
    if (out.length >= max) break;
    if (typeof f !== "string" || f === "" || f.length > 300 || /[\p{C}]/u.test(f) || f.startsWith("/") || f.split("/").includes("..")) continue;
    out.push(f);
  }
  return out;
}

// ---------------------------------------------------------------- keeping

const memory = new Map();

const okPane = (pane) => typeof pane === "string" && pane !== "" && pane.length <= PANE_ID_MAX;

const guarded = (fn, otherwise) => {
  try {
    return fn();
  } catch (_) {
    return otherwise;
  }
};

/** defaultStorage is this window's local storage, or null where there is none. */
export function defaultStorage() {
  return guarded(() => (typeof localStorage === "undefined" ? null : localStorage), null);
}

function readRecord(storage, pane) {
  const raw = storage ? guarded(() => storage.getItem(PREFIX + pane), null) : null;
  if (typeof raw !== "string" || raw.length > 2 * (DRAFT_MAX + HISTORY_MAX * ENTRY_MAX)) return null;
  const v = guarded(() => JSON.parse(raw), null);
  if (!v || typeof v !== "object") return null;
  const history = Array.isArray(v.history) ? v.history.filter((h) => typeof h === "string" && h.length <= ENTRY_MAX).slice(-HISTORY_MAX) : [];
  const draft = typeof v.draft === "string" ? v.draft.slice(0, DRAFT_MAX) : "";
  return { draft, history };
}

/** loadComposer is the draft and the history kept for a pane: { draft, history }, both empty when
 *  nothing is. What was kept in this session wins over storage. */
export function loadComposer(pane, storage = defaultStorage()) {
  if (!okPane(pane)) return { draft: "", history: [] };
  const held = memory.get(pane);
  if (held) return { draft: held.draft, history: [...held.history] };
  const rec = readRecord(storage, pane) || { draft: "", history: [] };
  memory.set(pane, rec);
  return { draft: rec.draft, history: [...rec.history] };
}

/** saveComposer keeps the draft and the history of a pane. A draft past DRAFT_MAX is cut for
 *  storage. The records of the panes used longest ago are dropped past PANES_MAX. */
export function saveComposer(pane, rec, storage = defaultStorage()) {
  if (!okPane(pane)) return;
  const draft = typeof rec.draft === "string" ? rec.draft.slice(0, DRAFT_MAX) : "";
  const history = (Array.isArray(rec.history) ? rec.history : []).filter((h) => typeof h === "string" && h.length <= ENTRY_MAX).slice(-HISTORY_MAX);
  memory.delete(pane);
  memory.set(pane, { draft, history });
  while (memory.size > PANES_MAX) memory.delete(memory.keys().next().value);
  if (!storage) return;
  guarded(() => {
    let order = guarded(() => JSON.parse(storage.getItem(INDEX) || "[]"), []);
    order = (Array.isArray(order) ? order : []).filter((p) => okPane(p) && p !== pane);
    order.push(pane);
    while (order.length > PANES_MAX) storage.removeItem(PREFIX + order.shift());
    if (draft === "" && history.length === 0) {
      storage.removeItem(PREFIX + pane);
      order.pop();
    } else {
      storage.setItem(PREFIX + pane, JSON.stringify({ draft, history }));
    }
    storage.setItem(INDEX, JSON.stringify(order));
  });
}

/** forgetComposerMemory empties what is held in memory; a test uses it to start from storage. */
export function forgetComposerMemory() {
  memory.clear();
}
