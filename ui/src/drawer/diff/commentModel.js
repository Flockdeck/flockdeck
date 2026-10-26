// Review comments, as data (docs/changes-api.md, section 5): the words for their
// states, the commands that make and close them, and the answers to a refusal. It
// reads and builds; it sends nothing.
//
// A comment is { id, pane, path, side ("old" | "new"), line (0 is the whole file),
// atSha, text, author ("you" or a pane id), to (the pane it went to), state
// ("open" | "fixed" | "resolved" | "dismissed"), delivery ("queued" | "notified" |
// "woken" | "read", absent while the message is being written), createdAt,
// fixedAtSha }. Everything in it is text from the desk or an agent.
import { showControls } from "./diffModel.js";
import { clipText } from "./gateModel.js";

/** MAX_COMMENT is the longest comment the desk takes, in characters. */
export const MAX_COMMENT = 4096;

const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const list = (v) => (Array.isArray(v) ? v : []);
const text = (v) => (typeof v === "string" ? v : "");

/** checkComment answers why a text cannot be sent, or "" when it can. */
export function checkComment(value) {
  const t = text(value);
  if (!t.trim()) return "Write a comment first.";
  const n = Array.from(t).length;
  if (n > MAX_COMMENT) return "A comment is at most " + MAX_COMMENT.toLocaleString("en-US") + " characters; this one has " + n.toLocaleString("en-US") + ".";
  return "";
}

/** deliveryOf is how far the message to the owner got: "sending" (not written yet), "delivered" or "read". */
export function deliveryOf(c) {
  const d = isObject(c) ? c.delivery : "";
  if (d === "read") return "read";
  if (!d) return "sending";
  return "delivered";
}

export const DELIVERY_WORDS = { sending: "Sending…", delivered: "Delivered", read: "Read" };
export const STATE_WORDS = { open: "Open", fixed: "Fixed", resolved: "Resolved", dismissed: "Dismissed" };

/** stateOf is the comment's state, "open" when the desk sent none or one this window does not know. */
export function stateOf(c) {
  const s = isObject(c) ? c.state : "";
  return STATE_WORDS[s] ? s : "open";
}

/** isOutdated says the comment was written at another head than the branch is at now. */
export function isOutdated(c, headSha) {
  return isObject(c) && typeof c.atSha === "string" && c.atSha !== "" && typeof headSha === "string" && headSha !== "" && c.atSha !== headSha;
}

/** lineKey is the key of a place in a file: "old:12", "new:7", or "file" for the whole file. */
export function lineKey(side, line) {
  return !line ? "file" : (side === "old" ? "old" : "new") + ":" + line;
}

/** commentsAt groups one file's comments by place, each group oldest first. A renamed file's comments may carry its old path. */
export function commentsAt(comments, path, oldPath = "") {
  const out = new Map();
  for (const c of list(comments)) {
    if (!isObject(c) || !(c.path === path || (oldPath && c.path === oldPath))) continue;
    const k = lineKey(c.side, c.line);
    out.set(k, (out.get(k) || []).concat(c));
  }
  for (const [k, v] of out) out.set(k, v.slice().sort((a, b) => text(a.createdAt).localeCompare(text(b.createdAt))));
  return out;
}

/** forDrawer splits all of a branch's comments into those still open and those done, each by path and line. */
export function forDrawer(comments) {
  const open = [];
  const done = [];
  for (const c of list(comments)) if (isObject(c)) (stateOf(c) === "open" ? open : done).push(c);
  const order = (a, b) => text(a.path).localeCompare(text(b.path)) || (a.line | 0) - (b.line | 0) || text(a.createdAt).localeCompare(text(b.createdAt));
  return { open: open.sort(order), done: done.sort(order) };
}

/** whereWords says where a comment is: "path, line 12", "path, old line 12" or "path". */
export function whereWords(c) {
  if (!isObject(c)) return "";
  if (!c.line) return showControls(text(c.path));
  return showControls(text(c.path)) + ", " + (c.side === "old" ? "old line " : "line ") + c.line;
}

/** addCommand builds the commentAdd command. expectSha is the head the person looked at. */
export function addCommand({ pane, file, side, line, text: body, expectSha, req }) {
  return { cmd: "commentAdd", id: pane, file, side: side === "old" ? "old" : "new", line: line | 0, text: body, expectSha, req };
}

/** closeCommand builds commentResolve or commentReopen. */
export function closeCommand(cmd, { pane, commentId, req }) {
  return { cmd: cmd === "commentReopen" ? "commentReopen" : "commentResolve", id: pane, commentId, req };
}

// said is the desk's own sentence, as text: invisible characters shown and at most 400 code points.
const said = (v) => clipText(showControls(text(v)), 400);

/** refusalWords is what to say to the person for a changesResult that was not ok. */
export function refusalWords(msg) {
  const m = isObject(msg) ? msg : {};
  switch (m.code) {
    case "headMoved": return "The branch has a new commit since you looked, so nothing was sent. Look at the changes again.";
    case "deskOnly": return "Review comments are added on the machine Flockdeck runs on, not from this window.";
    case "unsupported": return "This version of Flockdeck does not take review comments yet.";
    case "notFound": return "That comment is gone.";
    case "noBranch": return "That pane is no longer on a branch, so nothing was sent.";
    case "badRequest": return said(m.error) || "The desk did not accept that comment.";
    default: return said(m.error) || "The comment was not sent.";
  }
}
