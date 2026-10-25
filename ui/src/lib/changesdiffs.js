// The diffs the team-wide Changes view has read (docs/changes-api.md, section 4): what
// the store keeps of each diffHunks and diffContext answer, and the head each was
// read at. It is pure; the store holds the state and the reads in flight.
//
// A diff is kept under its pane and path. drawnSha is the head of the branch listing
// the window had when it sent diffHunks (the reply carries no sha unless the desk adds
// one, and then that is used), so a comment sent from that diff is guarded by the head
// the person looked at, even when a newer changesTeam has arrived since. `stat` is the
// file's "added:removed" in that listing; a view that finds another head or another stat
// reads the diff again. The unchanged lines asked for with diffContext are kept by the
// gap they were asked for, the shape DiffViewer's `context` prop takes, and belong to the
// entry whose seq they were asked of: a context answer for an entry that has been
// replaced is dropped.
//
// What is held is bounded by lines, not files: MAX_LINES over all entries, and the one
// least recently used goes first.

/** MAX_LINES is the most diff and context lines kept over all files. */
export const MAX_LINES = 40000;

/** MAX_DIFFS is the most files kept, whatever their size. */
export const MAX_DIFFS = 40;

/** MAX_READS is the most reads waited on at once. */
export const MAX_READS = 100;

const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const list = (v) => (Array.isArray(v) ? v : []);

/** diffKey is the key of one file of one pane. */
export const diffKey = (pane, path) => String(pane) + "\n" + String(path);

/** statOf is the file's size as the listing says it, to tell that a file changed under a held diff. */
export const statOf = (file) => (file ? (file.added | 0) + ":" + (file.removed | 0) : "");

/** readOf describes a read being sent: { kind, pane, path, headSha, stat, gap, entrySeq }. */
export function readOf(fields, headSha, gap = "", stat = "", entrySeq = 0) {
  return {
    kind: fields.cmd === "diffContext" ? "context" : "hunks", pane: String(fields.id || ""), path: String(fields.file || ""),
    headSha: typeof headSha === "string" ? headSha : "", stat: String(stat || ""), gap: String(gap || ""), entrySeq,
  };
}

/** readKey identifies a read, so that one is waited on at a time for the same thing. */
export const readKey = (read) => read.kind + "\n" + diffKey(read.pane, read.path) + "\n" + read.gap;

/** linesOf is how many lines an entry holds. */
export function linesOf(entry) {
  let n = 0;
  for (const h of list(entry && entry.hunks && entry.hunks.hunks)) n += list(h && h.lines).length;
  for (const g of Object.values((entry && entry.context) || {})) n += list(g && g.lines).length;
  return n;
}

/** trim drops the least recently used entries until the rest are within the limits; the entry `keep` stays. */
export function trim(diffs, keep = "") {
  const keys = Object.keys(diffs);
  let total = 0;
  for (const k of keys) total += linesOf(diffs[k]);
  if (keys.length <= MAX_DIFFS && total <= MAX_LINES) return diffs;
  const out = { ...diffs };
  for (const k of keys.slice().sort((a, b) => (diffs[a].used | 0) - (diffs[b].used | 0))) {
    if (Object.keys(out).length <= MAX_DIFFS && total <= MAX_LINES) break;
    if (k === keep) continue;
    total -= linesOf(out[k]);
    delete out[k];
  }
  return out;
}

/** touch marks an entry as used now (seq is the store's counter), so it is the last to go. */
export function touch(diffs, pane, path, seq) {
  const key = diffKey(pane, path);
  return diffs[key] && diffs[key].used !== seq ? { ...diffs, [key]: { ...diffs[key], used: seq } } : diffs;
}

/** dropDiffs removes a pane's entries (all of them, or one path's). */
export function dropDiffs(diffs, pane, path = null) {
  let out = diffs;
  for (const k of Object.keys(diffs)) {
    if (k.startsWith(String(pane) + "\n") && (path === null || k === diffKey(pane, path))) {
      if (out === diffs) out = { ...diffs };
      delete out[k];
    }
  }
  return out;
}

/** pruneDiffs removes the entries of panes that are not in the listing any more. */
export function pruneDiffs(diffs, panes) {
  const keep = new Set(panes);
  let out = diffs;
  for (const k of Object.keys(diffs)) {
    if (!keep.has(k.slice(0, k.indexOf("\n")))) {
      if (out === diffs) out = { ...diffs };
      delete out[k];
    }
  }
  return out;
}

/** dropErrors removes the entries that are a failed read: they are tried again after a new hello. */
export function dropErrors(diffs) {
  let out = diffs;
  for (const k of Object.keys(diffs)) {
    if (diffs[k].hunks && diffs[k].hunks.error) {
      if (out === diffs) out = { ...diffs };
      delete out[k];
    }
  }
  return out;
}

const lineId = (l) => (isObject(l) ? (l.newNo == null ? "-" : l.newNo) + ":" + (l.oldNo == null ? "-" : l.oldNo) : "");

/**
 * applyReply folds the answer to a read into the diffs: a diffHunks (or a failed one,
 * as its error) replaces the file's entry; a diffContext adds its lines to the gap it
 * was asked for, merged by line number so a repeat does not draw a line twice, or says
 * why it failed on the gap. A context answer for an entry that is gone or was replaced is dropped.
 */
export function applyReply(diffs, read, msg, seq) {
  const key = diffKey(read.pane, read.path);
  if (read.kind === "hunks") {
    const bad = !isObject(msg) || msg.error || msg.ok === false;
    const hunks = bad ? { type: "diffHunks", pane: read.pane, path: read.path, error: String((msg && msg.error) || "The diff could not be read.") } : msg;
    const drawnSha = !bad && typeof msg.headSha === "string" && msg.headSha ? msg.headSha : read.headSha;
    return trim({ ...diffs, [key]: { hunks, drawnSha, stat: read.stat, context: {}, contextErrors: {}, seq, used: seq } }, key);
  }
  const cur = diffs[key];
  if (!cur || cur.seq !== read.entrySeq) return diffs;
  if (!isObject(msg) || msg.error || msg.ok === false) {
    return { ...diffs, [key]: { ...cur, contextErrors: { ...cur.contextErrors, [read.gap]: String((msg && msg.error) || "Those lines could not be read.") } } };
  }
  const had = cur.context[read.gap] ? list(cur.context[read.gap].lines) : [];
  const seen = new Set(had.map(lineId));
  const lines = had.concat(list(msg.lines).filter((l) => !seen.has(lineId(l))));
  const contextErrors = { ...cur.contextErrors };
  delete contextErrors[read.gap];
  return trim({ ...diffs, [key]: { ...cur, context: { ...cur.context, [read.gap]: { lines, eof: msg.eof === true } }, contextErrors } }, key);
}
