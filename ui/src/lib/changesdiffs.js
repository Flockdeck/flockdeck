// The diffs the team-wide Changes view has read (docs/changes-api.md, section 4): what
// the store keeps of each diffHunks and diffContext answer, and the head each was
// read at. It is pure; the store holds the state and the reads in flight.
//
// A diff is kept under its pane and path. drawnSha is the head of the branch listing
// the window had when it sent diffHunks (the reply carries no sha), so a comment
// sent from that diff is guarded by the head the person looked at, even when a newer
// changesTeam has arrived since. The unchanged lines asked for with diffContext are
// kept by the gap they were asked for, the shape DiffViewer's `context` prop takes.

/** MAX_DIFFS is the most files kept; the oldest read goes first. */
export const MAX_DIFFS = 40;

/** MAX_READS is the most reads waited on at once. */
export const MAX_READS = 100;

const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const list = (v) => (Array.isArray(v) ? v : []);

/** diffKey is the key of one file of one pane. */
export const diffKey = (pane, path) => String(pane) + "\n" + String(path);

/** readOf describes a read being sent: { kind, pane, path, headSha, gap }. */
export function readOf(fields, headSha, gap = "") {
  return { kind: fields.cmd === "diffContext" ? "context" : "hunks", pane: String(fields.id || ""), path: String(fields.file || ""), headSha: typeof headSha === "string" ? headSha : "", gap: String(gap || "") };
}

function trim(diffs) {
  const keys = Object.keys(diffs);
  if (keys.length <= MAX_DIFFS) return diffs;
  const out = { ...diffs };
  keys.sort((a, b) => diffs[a].seq - diffs[b].seq);
  for (const k of keys.slice(0, keys.length - MAX_DIFFS)) delete out[k];
  return out;
}

/**
 * applyReply folds the answer to a read into the diffs: a diffHunks (or a failed
 * one, as its error) replaces the file's entry; a diffContext adds its lines to the
 * gap it was asked for. A context answer for a file not held is dropped.
 */
export function applyReply(diffs, read, msg, seq) {
  const key = diffKey(read.pane, read.path);
  if (read.kind === "hunks") {
    const hunks = isObject(msg) && !msg.error && msg.ok !== false ? msg : { type: "diffHunks", pane: read.pane, path: read.path, error: String((msg && msg.error) || "The diff could not be read.") };
    return trim({ ...diffs, [key]: { hunks, drawnSha: read.headSha, context: {}, seq } });
  }
  const cur = diffs[key];
  if (!cur || !isObject(msg) || msg.error || msg.ok === false) return diffs;
  const had = cur.context[read.gap] ? list(cur.context[read.gap].lines) : [];
  return { ...diffs, [key]: { ...cur, context: { ...cur.context, [read.gap]: { lines: had.concat(list(msg.lines)), eof: msg.eof === true } } } };
}
