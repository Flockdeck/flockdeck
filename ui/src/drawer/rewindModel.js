// Going back to an earlier prompt of a structured pane, and forking the
// conversation at one. The desktop names the prompts it can go back to
// (rewindPoints of the push, as entry ids); this file puts their text beside
// them and builds the commands. A prompt is always named by its entry id, never
// by its place in the list. Everything shown from an answer is plain text.

export const REWIND_NOT_SENT_WORDS = "Not connected to Flockdeck, so the request was not sent.";
export const REWIND_NOT_OFFERED_WORDS = "This agent cannot go back to an earlier point, so there is nothing to go back to or fork from.";
export const REWIND_BUSY_WORDS = "Stop the turn first. Going back and forking wait until the agent is idle.";
export const REWIND_NONE_WORDS = "No earlier prompt can be gone back to yet. The first prompt of a conversation has no point before it, and neither has one that follows a turn that was stopped.";
export const REWIND_CONFIRM_WORDS = "removes this prompt and everything after it from the conversation and from the agent. Files the agent changed are not put back.";

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
 *  the panel asks the person first. */
export function rewindMessage(pane, entry, req) {
  if (!pane || !entry || typeof entry !== "string") return null;
  return { cmd: "structuredRewind", id: pane, req, entry, confirm: true };
}

/** forkMessage is the command to fork before entry, or the whole conversation
 *  when entry is "". */
export function forkMessage(pane, entry, req) {
  if (!pane) return null;
  return { cmd: "structuredFork", id: pane, req, entry: typeof entry === "string" ? entry : "" };
}

/** resultWords says how a request went, from the store's rewindResult for this
 *  pane and request: { ok, words, text, pane }. */
export function resultWords(r) {
  if (!r) return null;
  if (!r.ok) return { ok: false, words: clean(r.error, 300) || "That did not work.", text: "", pane: "" };
  if (r.op === "structuredFork") return { ok: true, words: "Started a new pane beside this one.", text: "", pane: r.pane || "" };
  return { ok: true, words: "Went back. The prompt is in the box to edit and send again.", text: typeof r.text === "string" ? r.text : "", pane: "" };
}
