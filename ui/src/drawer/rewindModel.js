// Going back to an earlier prompt of a structured pane, and forking the
// conversation at one. The desktop names the prompts it can go back to
// (rewindPoints of the push, as entry ids); this file puts their text beside
// them and builds the commands. A prompt is always named by its entry id, never
// by its place in the list. Everything shown from an answer is plain text.

export const REWIND_NOT_SENT_WORDS = "Not connected to Flockdeck, so the request was not sent.";
export const REWIND_NOT_OFFERED_WORDS = "This agent cannot go back to an earlier point, so there is nothing to go back to or fork from.";
export const REWIND_BUSY_WORDS = "Stop the turn first. Going back and forking wait until the agent is idle.";
export const REWIND_NONE_WORDS = "No earlier prompt can be gone back to yet. The first prompt of a conversation has no point before it, and neither has one that follows a turn that was stopped.";
export const REWIND_CONFIRM_WORDS = "removes this prompt and everything after it from the conversation and from the agent, including messages other agents sent after it. Files the agent changed are not put back by going back alone, and pictures attached to the prompt are not put back in the box.";
export const REWIND_CUT_WORDS = "The prompt was long; only its start was put back.";
export const FILES_NONE_WORDS = "No saved files exist for this prompt, so only the conversation can go back.";
export const FILES_EMPTY_WORDS = "No files differ from how they were when this prompt was sent.";
export const FILES_SAFE_WORDS = "Only the files listed are changed. Ignored files and the git index are left alone. Before anything is written, the files as they are now are saved in git, and the answer names where.";
export const FILES_ACTION_WORDS = { overwrite: "gets its earlier content back", recreate: "is written back (it was deleted)", remove: "is deleted (it was created later)" };

const clean = (s, n) => String(s == null ? "" : s).replace(/[\p{C}]/gu, " ").replace(/\s+/g, " ").trim().slice(0, n);

/** pointsOf lists the prompts that can be gone back to, oldest first, as
 *  { id, text }. Ids the conversation does not hold (cut off the top, or taken
 *  out) are left out. */
export function pointsOf(st, entries) {
  const ids = st && Array.isArray(st.rewindPoints) ? st.rewindPoints : [];
  if (!ids.length || !Array.isArray(entries)) return [];
  const byId = new Map();
  for (const e of entries) if (e && e.kind === "prompt" && typeof e.id === "string") byId.set(e.id, e);
  const out = [];
  for (const id of ids) {
    const e = byId.get(id);
    if (e) out.push({ id, text: clean(e.text, 120) || "(no text)" });
  }
  return out;
}

/** rewindOffered says whether the agent can go back; the panel says so when not. */
export function rewindOffered(st) {
  return !!st && !!st.capabilities && st.capabilities.rewind === true;
}

/** canAct is false while a turn runs or prompts wait: the desk refuses then. */
export function canAct(st) {
  return !!st && st.status !== "working" && st.status !== "waiting";
}

/** rewindMessage is the command to go back to before entry. It always confirms:
 *  the panel asks the person first. With files (an array of the paths the person
 *  was shown) it also puts those files back. */
export function rewindMessage(pane, entry, req, token, files) {
  if (!pane || !entry || typeof entry !== "string") return null;
  const m = { cmd: "structuredRewind", id: pane, req, entry, confirm: true, rewindToken: typeof token === "string" ? token : "" };
  if (Array.isArray(files)) {
    m.restoreFiles = true;
    m.filePaths = files.filter((f) => typeof f === "string");
  }
  return m;
}

/** filesMessage asks what going back to before entry would change in the files. */
export function filesMessage(pane, entry, req, token) {
  if (!pane || !entry || typeof entry !== "string") return null;
  return { cmd: "structuredRewindFiles", id: pane, req, entry, rewindToken: typeof token === "string" ? token : "" };
}

/** filesOffered says the push lists entry as having saved files. */
export function filesOffered(st, entry) {
  return !!st && Array.isArray(st.rewindFiles) && st.rewindFiles.includes(entry);
}

/** filesWhy is the server's words for why the latest prompt has no saved files, or "". */
export function filesWhy(st) {
  return st && typeof st.rewindFilesWhy === "string" ? clean(st.rewindFilesWhy, 400) : "";
}

/** filesOf keeps the files of an answer that have a path and a known action. */
export function filesOf(list) {
  if (!Array.isArray(list)) return [];
  const out = [];
  for (const f of list) {
    if (f && typeof f.path === "string" && f.path && Object.prototype.hasOwnProperty.call(FILES_ACTION_WORDS, f.action)) out.push({ path: f.path.replace(/[\p{C}]/gu, " ").slice(0, 400), action: f.action });
  }
  return out;
}

/** forkMessage is the command to fork before entry, or the whole conversation
 *  when entry is "". */
export function forkMessage(pane, entry, req, token) {
  if (!pane) return null;
  return { cmd: "structuredFork", id: pane, req, entry: typeof entry === "string" ? entry : "", rewindToken: typeof token === "string" ? token : "" };
}

/** restoredDraft is the prompt box after a prompt is put back: the box as it
 *  was when it held nothing, and otherwise the box with the prompt after a blank
 *  line, so that what the person was typing is not overwritten. */
export function restoredDraft(draft, text) {
  const t = typeof text === "string" ? text : "";
  const d = typeof draft === "string" ? draft : "";
  if (!t) return d;
  return d.trim() === "" ? t : d.replace(/\s+$/u, "") + "\n\n" + t;
}

/** resultWords says how a request went, from the store's rewindResult for this
 *  pane and request: { ok, words, text, pane }. */
export function resultWords(r) {
  if (!r) return null;
  if (!r.ok) return { ok: false, words: clean(r.error, 300) || "That did not work.", text: "", pane: "", files: filesOf(r.files) };
  if (r.op === "structuredFork") return { ok: true, words: "Started a new pane beside this one.", text: "", pane: r.pane || "" };
  if (r.op === "structuredRewindFiles") return { ok: true, words: "", text: "", pane: "", files: filesOf(r.files) };
  return { ok: true, words: "Went back. The prompt is in the box to edit and send again." + (r.cut === true ? " " + REWIND_CUT_WORDS : "") + restoreWords(r.restore), text: typeof r.text === "string" ? r.text : "", pane: "", files: [] };
}

/** restoreWords says what a restore of files did: the counts, the git ref that
 *  holds the files from before it, and what went wrong, if anything. */
export function restoreWords(r) {
  if (!r || typeof r !== "object") return "";
  const n = (v) => (Number.isInteger(v) && v > 0 ? v : 0);
  let w = " Files: " + n(r.written) + " put back, " + n(r.removed) + " deleted.";
  if (typeof r.undo === "string" && r.undo) w += " The files as they were before are saved in git as " + clean(r.undo, 200) + " (git checkout " + clean(r.undo, 200) + " -- . puts them back).";
  if (typeof r.error === "string" && r.error) w += " " + clean(r.error, 300);
  if (Array.isArray(r.failed) && r.failed.length) w += " Not restored: " + r.failed.slice(0, 5).map((f) => clean(f, 200)).join("; ") + (r.failed.length > 5 ? "; and " + (r.failed.length - 5) + " more." : ".");
  return w;
}
