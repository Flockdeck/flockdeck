// The queue of a structured pane, without the drawing: the prompts waiting
// behind the running turn, as the push lists them (`queue`), and the commands
// that change them. A prompt is named by the id the push gave it and never by
// its place, since the list shortens as turns end. Text in the list is the
// person's own (kind "user") or another agent's message (kind "mail"); both are
// cut, counted and shown as text only, and a message from another agent keeps its
// sender in a field of its own, so nothing in a body can pass for one.

export const QUEUE_MAX = 16;
export const QUEUE_TEXT_MAX = 2000;
export const PEER_MAX = 100;

const cutLine = (s, n) => {
  const t = typeof s === "string" ? s.slice(0, n + 1).replace(/\p{Cf}/gu, "").replace(/[\p{C}\s]+/gu, " ").trim() : "";
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

// A body keeps its line breaks; other control characters go.
const cutText = (s, n) => {
  const t = typeof s === "string" ? s.slice(0, n + 1).replace(/[^\S\n]+/g, " ").replace(/[^\P{C}\n]/gu, "") : "";
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

/** queueOf reads the waiting prompts of a push, next first. A row that is not well formed is left out;
 *  a prompt that was cut, or is a message from another agent, is not editable. */
export function queueOf(st) {
  const list = st && Array.isArray(st.queue) ? st.queue.slice(0, QUEUE_MAX) : [];
  const out = [];
  const seen = new Set();
  for (const q of list) {
    if (!q || !Number.isInteger(q.id) || q.id <= 0 || seen.has(q.id)) continue;
    if (q.kind !== "user" && q.kind !== "mail" && q.kind !== "flockdeck") continue;
    seen.add(q.id);
    const mail = q.kind === "mail";
    const system = q.kind === "flockdeck";
    const own = q.kind === "user";
    const clipped = q.clipped === true;
    out.push({
      id: q.id,
      pos: out.length + 1,
      mail,
      system,
      peer: mail ? cutLine(q.peer, PEER_MAX) : "",
      // text is for drawing: whitespace folded and format characters removed. raw is what the push carried, the
      // person's own text exactly, and is what an edit starts from, so saving it changes nothing the person typed.
      text: cutText(q.text, QUEUE_TEXT_MAX),
      raw: own && typeof q.text === "string" ? q.text : "",
      clipped,
      editable: own && !clipped,
      removable: own,
    });
  }
  return out;
}

/** hasUserItems says Clear would remove something. */
export const hasUserItems = (rows) => rows.some((r) => r.removable);

/** queueMessage makes the command for op ("edit", "remove", "clear" or "sendNow") about the prompt id of
 *  rows, the list the person is looking at. It is null for a prompt that is not in the list, for an edit
 *  or removal of a message from another agent or of a cut prompt, and for an edit to nothing: the desk
 *  would refuse them, so they are not sent. */
export function queueMessage(pane, rows, op, id, text, req) {
  if (!pane) return null;
  if (op === "clear") return hasUserItems(rows) ? { cmd: "structuredQueueClear", id: pane, req } : null;
  const row = rows.find((r) => r.id === id);
  if (!row) return null;
  if (op === "sendNow") return { cmd: "structuredQueueSendNow", id: pane, item: id, req };
  if (op === "remove") return row.removable ? { cmd: "structuredQueueRemove", id: pane, item: id, req } : null;
  if (op === "edit") {
    if (!row.editable || typeof text !== "string" || text.trim() === "") return null;
    return { cmd: "structuredQueueEdit", id: pane, item: id, text, req };
  }
  return null;
}

/** stopMessage makes Stop for the pane. With keepQueue the waiting prompts stay and the next is sent. */
export function stopMessage(pane, keepQueue, req) {
  return keepQueue ? { cmd: "structuredCancel", id: pane, keepQueue: true, req } : { cmd: "structuredCancel", id: pane, req };
}

/** QUEUE_GONE_WORDS is said when an edit was open for a prompt the agent has since been sent. */
export const QUEUE_GONE_WORDS = "That prompt was sent before the change could be saved.";
