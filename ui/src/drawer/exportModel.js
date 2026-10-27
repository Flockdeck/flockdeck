// Saving a structured pane's conversation as a Markdown file, without the drawing.
// The desktop writes the file (structuredExport) into the folder the person
// names; the answer says where. Everything shown from the answer is plain text,
// and the folder is kept per window only as a convenience.

export const EXPORT_NOT_SENT_WORDS = "Not connected to Flockdeck, so the export was not sent.";
export const EXPORT_NO_DIR_WORDS = "Type the full path of a folder to save into.";
export const EXPORT_INCLUDES_WORDS = "The file holds your prompts, the agent's replies, each tool call with its summary, helper reports and notices, and mail from other agents, marked as mail. It leaves out tool output, diffs, images and, unless you tick the box, the agent's thinking. Secrets Flockdeck knows are replaced with marks, but read the file before you share it.";

const KEY = "flockdeck.exportDir";

/** loadDir is the folder used last in this window, or "". */
export function loadDir(storage = typeof localStorage === "undefined" ? null : localStorage) {
  try {
    const v = storage && storage.getItem(KEY);
    return typeof v === "string" ? v.slice(0, 1024) : "";
  } catch {
    return "";
  }
}

/** saveDir keeps the folder; a store that refuses is ignored. */
export function saveDir(dir, storage = typeof localStorage === "undefined" ? null : localStorage) {
  try {
    if (storage) storage.setItem(KEY, String(dir).slice(0, 1024));
  } catch { /* the folder is just not remembered */ }
}

/** exportMessage is the command, or null for a folder that is empty. */
export function exportMessage(pane, dir, thinking, req) {
  const d = typeof dir === "string" ? dir.trim() : "";
  if (!d || d.length > 1024 || !pane) return null;
  return { cmd: "structuredExport", id: pane, req, dir: d, thinking: thinking === true };
}

const clean = (s, n) => String(s == null ? "" : s).replace(/[\p{C}]/gu, " ").replace(/\s+/g, " ").trim().slice(0, n);

/** resultWords says how an export went, from the store's exportResult for this
 *  pane and request: { ok, words }. */
export function resultWords(r) {
  if (!r) return null;
  if (!r.ok) return { ok: false, words: clean(r.error, 300) || "The conversation could not be exported." };
  const entries = Number.isInteger(r.entries) ? r.entries : 0;
  const redacted = Number.isInteger(r.redacted) ? r.redacted : 0;
  let w = "Saved " + entries.toLocaleString("en-US") + (entries === 1 ? " entry" : " entries") + " to " + clean(r.path, 400) + ". ";
  w += redacted > 0 ? redacted.toLocaleString("en-US") + (redacted === 1 ? " value was" : " values were") + " replaced. " : "Nothing was replaced. ";
  if (r.warning) w += clean(r.warning, 200) + ". ";
  return { ok: true, words: w + "Read it before you share it." };
}
