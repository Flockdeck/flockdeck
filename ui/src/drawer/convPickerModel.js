// The conversation picker of a structured pane: the past conversations of the pane's folder, resuming
// one in the same pane, and starting a clean conversation. The desk lists the folder's conversations
// (structuredSessions) and moves the pane (structuredResume, structuredNewConversation); this file
// builds the commands and cleans what comes back. Titles are the agent's words, so each is cut and
// stripped of control characters again here. Desk only.

import { queueOf } from "./queueModel.js";

export const PICKER_NOT_SENT_WORDS = "Not connected to Flockdeck, so the request was not sent.";
export const PICKER_NOT_OFFERED_WORDS = "This agent does not list its past conversations, so there is nothing to choose from. New conversation still works.";
export const PICKER_NONE_WORDS = "No earlier conversations in this folder.";
export const PICKER_LOADING_WORDS = "Looking for conversations.";
export const PICKER_NEW_WORDS = "Starts a clean conversation in this pane. The agent does not remember this one; it stays above and can be resumed from the list if the agent keeps it.";
export const PICKER_TITLE_MAX = 120;
export const PICKER_SHOWN_MAX = 30;

const clean = (s, n) => {
  const t = String(s == null ? "" : s).slice(0, n * 4).replace(/[\p{C}\s]+/gu, " ").trim();
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

/** sessionsOffered says the agent lists its conversations and can reopen one. */
export function sessionsOffered(st) {
  const c = st && st.capabilities;
  return !!c && c.listSessions === true && (c.loadSession === true || c.resumeSession === true);
}

/** busyCount is how many things a move ends: the running turn (or the agent's own work) and the waiting prompts. */
export function busyCount(st) {
  if (!st) return 0;
  let n = queueOf(st).length;
  if (st.status === "working" || st.status === "waiting" || st.background === true) n += 1;
  return n;
}

/** busyWords says what moving the pane ends, or "" when nothing is running. */
export function busyWords(st) {
  if (!st) return "";
  const queued = queueOf(st).length;
  const running = st.status === "working" || st.status === "waiting" || st.background === true;
  if (!running && !queued) return "";
  const parts = [];
  if (running) parts.push(st.status === "waiting" ? "ends the turn that is running, and the permission card it has open" : "ends the turn that is running");
  if (queued) parts.push("drops the " + queued + " prompt" + (queued === 1 ? "" : "s") + " waiting");
  return "This " + parts.join(" and ") + ".";
}

export function sessionsMessage(pane, req) {
  return pane ? { cmd: "structuredSessions", id: pane, req } : null;
}

/** resumeMessage moves the pane to the conversation id. confirm is sent only after the person saw busyWords. */
export function resumeMessage(pane, sessionId, req, confirm = false) {
  if (!pane || typeof sessionId !== "string" || !sessionId) return null;
  const m = { cmd: "structuredResume", id: pane, req, sessionId };
  if (confirm) m.confirm = true;
  return m;
}

export function newConversationMessage(pane, req, confirm = false) {
  if (!pane) return null;
  const m = { cmd: "structuredNewConversation", id: pane, req };
  if (confirm) m.confirm = true;
  return m;
}

/** timeWords is a short local date for an RFC 3339 time, or "" when it does not parse. */
export function timeWords(iso, now = Date.now()) {
  const t = typeof iso === "string" ? Date.parse(iso) : NaN;
  if (!Number.isFinite(t)) return "";
  const d = new Date(t);
  const ago = now - t;
  if (ago >= 0 && ago < 60 * 1000) return "just now";
  if (ago >= 0 && ago < 3600 * 1000) return Math.floor(ago / 60000) + " min ago";
  if (ago >= 0 && ago < 86400 * 1000) return Math.floor(ago / 3600000) + " h ago";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

/** sessionsOf keeps the conversations of an answer: at most PICKER_SHOWN_MAX, each with an id and a clean one-line title. */
export function sessionsOf(list) {
  const out = [];
  if (!Array.isArray(list)) return out;
  for (const s of list) {
    if (out.length >= PICKER_SHOWN_MAX) break;
    if (!s || typeof s.id !== "string" || !s.id || s.id.length > 128) continue;
    out.push({ id: s.id, title: clean(s.title, PICKER_TITLE_MAX) || "(no title)", updated: typeof s.updated === "string" ? s.updated : "", current: s.current === true });
  }
  return out;
}

/** resultWords says how a request went: { ok, words }. */
export function convResultWords(r) {
  if (!r) return null;
  if (!r.ok) return { ok: false, words: clean(r.error, 300) || "That did not work." };
  if (r.op === "structuredResume") return { ok: true, words: "Resuming the conversation." };
  if (r.op === "structuredNewConversation") return { ok: true, words: "Started a new conversation." };
  return { ok: true, words: "" };
}
