// The pure parts of the conversation history (Resume): reading the desktop's
// listing defensively, narrowing it to the words typed, and the command that
// reopens one conversation.
//
// Every string in a listing came from an agent or a person (the opening prompt of
// a conversation is a summary of what was typed), so the dialog draws each as
// text. Nothing here builds markup.

/** HISTORY_REMOTE_WORDS is what a window reached through the relay says in place
 *  of the history. The desktop sends it nothing and the page asks for nothing. */
export const HISTORY_REMOTE_WORDS = "The history of conversations is shown on the desktop only for now.";

/** STRUCTURED_RESUME_WORDS says why a structured agent has nothing to resume. */
export const STRUCTURED_RESUME_WORDS =
  "Resume reopens a conversation that an agent kept in its own store. A structured agent starts a new session each time it is restarted, so none of its earlier sessions can be resumed.";

/** wordsOf splits what was typed into the lower-case words a conversation must
 *  all hold. */
export const wordsOf = (q) => String(q || "").trim().toLowerCase().split(/\s+/).filter(Boolean);

/** holdsAll says whether text holds every one of the words, ignoring case. */
export function holdsAll(text, words) {
  const hay = String(text || "").toLowerCase();
  return words.every((w) => hay.includes(w));
}

const text = (v) => (typeof v === "string" ? v : "");
const count = (v) => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? Math.floor(v) : 0);

/** listingOf reads a conversations message: the directory, the rows that have an
 *  id, and the error that stands in place of the list. A row that is not an object
 *  with an id is left out, and a field of the wrong type reads as empty. */
export function listingOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  const items = [];
  for (const c of Array.isArray(m.items) ? m.items : []) {
    if (!c || typeof c !== "object" || typeof c.id !== "string" || !c.id) continue;
    items.push({
      id: c.id, agent: text(c.agent), summary: text(c.summary), ago: text(c.ago),
      messages: count(c.messages), kb: count(c.kb), open: c.open === true,
    });
  }
  return { cwd: text(m.cwd), items, error: text(m.error) };
}

/** filterConversations keeps the conversations whose summary or id holds every
 *  word of the query. An empty query keeps them all. */
export function filterConversations(items, query) {
  const words = wordsOf(query);
  return words.length === 0 ? items : items.filter((c) => holdsAll(c.summary + " " + c.id, words));
}

/** statsOf is the small print under a row: how long ago, how many entries, how
 *  big, and the first characters of the id (a transcript file name carries them). */
export function statsOf(c) {
  const out = [];
  if (c.ago) out.push(c.ago);
  out.push(c.messages === 1 ? "1 entry" : c.messages + " entries");
  if (c.kb) out.push(c.kb + " KB");
  out.push(c.id.slice(0, 8));
  if (c.open) out.push("already open");
  return out;
}

/** resumeCommand is what resuming one row sends. The agent the listing says kept
 *  the conversation is the one it reopens with: every API agent's conversations
 *  are stored in one place. A conversation already open in a pane is not resumed. */
export function resumeCommand(listing, c) {
  if (!listing || !c || c.open) return null;
  const cmd = { cmd: "resumeConversation", id: c.id, path: listing.cwd, text: c.summary };
  if (c.agent) cmd.agent = c.agent;
  return cmd;
}

/** conversationsCommand asks for the history of a project. The desktop uses the
 *  project it has active when no folder is named. */
export function conversationsCommand(root) {
  return root ? { cmd: "conversations", path: root } : { cmd: "conversations" };
}
