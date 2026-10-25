// The state of review comments on one branch: the composer, comments on their way
// to the desk, and closing and reopening. It is a reducer and a hook over it, and
// it knows nothing of the store: the caller gives it `send` (a function that
// writes a command and answers whether it did), the changesResult messages it has
// seen, and the branch's `comments` from the desk's listing.
//
// - Two heads, which are not the same value. drawnSha is the head of the listing
//   the diff on screen was read at: the caller records it when it sends diffHunks
//   (the reply carries no sha), and it is what a comment is sent with as expectSha,
//   so the desk refuses (headMoved) rather than attach a comment to a line number
//   from code the person did not see. headSha (a DiffViewer prop) is the branch's
//   head now, and only marks comments written at another head as outdated. When a
//   newer changesTeam arrives and the diff is not read again, drawnSha stays the old
//   head and the desk's guard still bites. A drawnSha that is "" or not a string
//   sends nothing and says so.
// - The text stays in the composer when the desk refuses, with the reason beside
//   it. A draft is kept for each place, so opening another "+" does not lose it.
// - While the desk has not answered, the comment is drawn where it was written, as
//   "Sending…". It stays "Sending…" until the desk's listing has it, because a
//   listed comment with no delivery has not been written to the owner yet. When the
//   desk answers ok, the comment it holds replaces that one, and it is kept until
//   the desk's own listing has it.
// - If the connection drops (connected goes false, or session changes) while a
//   comment, resolve or reopen is out, the wait ends with a message to check the
//   listing; the text is kept and nothing is sent again by itself.
// - Resolve and reopen send one command each, and not a second while the first is
//   out; the answer's comment replaces the listed one until the listing catches up.
//   A refusal is said on that comment.
import { useEffect, useReducer, useRef } from "../../vendor/hooks.mjs";
import { addCommand, closeCommand, checkComment, refusalWords } from "./commentModel.js";

const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const NOT_CONNECTED = "Not connected to Flockdeck. Nothing was sent.";
const DROPPED = "The connection dropped; check whether the comment appears before sending again.";
const NO_HEAD = "The diff was not read at a known head, so nothing was sent. Look at the changes again.";

export const initialFlow = { composing: null, draft: "", drafts: {}, error: "", pending: {}, local: [], overrides: {}, errors: {} };

const keyOf = (t) => (t ? t.file + "\n" + t.side + ":" + t.line : "");
const isAdd = (s) => Object.values(s.pending).some((p) => p.kind === "add");

/** flowReducer is the whole state machine; the actions are the hook's. */
export function flowReducer(s, a) {
  switch (a.type) {
    case "open": {
      if (s.composing && keyOf(s.composing) === keyOf(a.target)) return { ...s, composing: a.target, error: "" };
      const drafts = { ...s.drafts };
      if (s.composing && s.draft) drafts[keyOf(s.composing)] = s.draft;
      const next = drafts[keyOf(a.target)] || "";
      delete drafts[keyOf(a.target)];
      return { ...s, composing: a.target, draft: next, drafts, error: "" };
    }
    case "draft":
      return { ...s, draft: a.text, error: "" };
    case "cancel": {
      // A comment on its way is not taken back by closing the box.
      if (isAdd(s)) return s;
      const drafts = { ...s.drafts };
      delete drafts[keyOf(s.composing)];
      return { ...s, composing: null, draft: "", drafts, error: "" };
    }
    case "refused":
      return { ...s, error: a.error };
    case "closeFailed":
      return { ...s, errors: { ...s.errors, [a.id]: NOT_CONNECTED } };
    case "sent":
      return { ...s, error: "", pending: { ...s.pending, [a.req]: a.pending }, errors: a.pending.kind === "close" ? { ...s.errors, [a.pending.id]: "" } : s.errors };
    case "dropped": {
      const waiting = Object.values(s.pending);
      if (!waiting.length) return s;
      const errors = { ...s.errors };
      for (const p of waiting) if (p.kind === "close") errors[p.id] = DROPPED;
      return { ...s, pending: {}, error: waiting.some((p) => p.kind === "add") ? DROPPED : s.error, errors };
    }
    case "result": {
      const msg = a.msg;
      const p = isObject(msg) ? s.pending[msg.req] : null;
      if (!p) return s;
      const pending = { ...s.pending };
      delete pending[msg.req];
      if (p.kind === "add") {
        if (!msg.ok || !isObject(msg.comment)) return { ...s, pending, error: refusalWords(msg) };
        const drafts = { ...s.drafts };
        delete drafts[keyOf(p.target)];
        const here = s.composing && keyOf(s.composing) === keyOf(p.target);
        return { ...s, pending, drafts, local: [...s.local, msg.comment], composing: here ? null : s.composing, draft: here ? "" : s.draft, error: "" };
      }
      if (!msg.ok || !isObject(msg.comment)) return { ...s, pending, errors: { ...s.errors, [p.id]: refusalWords(msg) } };
      return { ...s, pending, overrides: { ...s.overrides, [p.id]: msg.comment }, errors: { ...s.errors, [p.id]: "" } };
    }
    case "listed": {
      const local = s.local.filter((c) => !a.ids.has(c.id));
      const keys = Object.keys(s.overrides).filter((id) => a.ids.has(id));
      if (local.length === s.local.length && !keys.length) return s;
      const overrides = { ...s.overrides };
      for (const id of keys) delete overrides[id];
      return { ...s, local, overrides };
    }
    default:
      return s;
  }
}

/** mergeComments is the listing with the answers not yet in it, and the comments still being written. */
export function mergeComments(listed, s) {
  const base = (Array.isArray(listed) ? listed : []).filter(isObject).map((c) => s.overrides[c.id] || c);
  const have = new Set(base.map((c) => c.id));
  const extra = s.local.filter((c) => !have.has(c.id));
  const writing = Object.entries(s.pending).filter(([, p]) => p.kind === "add").map(([req, p]) => ({
    id: "pending:" + req, path: p.target.file, side: p.target.side, line: p.target.line, text: p.text, author: "you", state: "open", createdAt: p.at, pending: true,
  }));
  return [...base, ...extra, ...writing];
}

let counter = 0;
const newReq = () => "cm" + Date.now().toString(36) + (++counter).toString(36);

/**
 * useCommentFlow: { pane, drawnSha, send, result, results, comments, connected, session }.
 * result is the latest changesResult; results, when the caller keeps them, is every
 * one it has not dropped, so two that land between renders are both seen. It
 * answers the state and the actions the view calls:
 *   composing, draft, error, busy (a comment is on its way), comments (merged),
 *   errors (by comment id), closing (ids with a resolve or reopen out),
 *   open(target), setDraft(text), cancel(), submit(), close(id), reopen(id).
 */
export function useCommentFlow({ pane = "", drawnSha = "", send = null, result = null, results = null, comments = [], connected = true, session = null }) {
  const [s, dispatch] = useReducer(flowReducer, initialFlow);
  const seen = useRef(new WeakSet());
  useEffect(() => {
    const incoming = (Array.isArray(results) ? results : []).concat(result ? [result] : []);
    for (const msg of incoming) {
      if (!isObject(msg) || seen.current.has(msg)) continue;
      seen.current.add(msg);
      dispatch({ type: "result", msg });
    }
  }, [result, results]);
  useEffect(() => {
    dispatch({ type: "listed", ids: new Set((Array.isArray(comments) ? comments : []).filter(isObject).map((c) => c.id)) });
  }, [comments]);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      if (connected !== false) return;
    }
    dispatch({ type: "dropped" });
  }, [connected === false, session]);

  const write = (cmd, pending) => {
    if (!send || !send(cmd)) return false;
    dispatch({ type: "sent", req: cmd.req, pending });
    return true;
  };
  const closing = {};
  for (const p of Object.values(s.pending)) if (p.kind === "close") closing[p.id] = true;
  return {
    composing: s.composing,
    draft: s.draft,
    error: s.error,
    busy: isAdd(s),
    comments: mergeComments(comments, s),
    errors: s.errors,
    closing,
    open: (target) => dispatch({ type: "open", target }),
    setDraft: (text) => dispatch({ type: "draft", text }),
    cancel: () => dispatch({ type: "cancel" }),
    submit: () => {
      if (!s.composing || isAdd(s)) return false;
      const why = checkComment(s.draft);
      if (why) {
        dispatch({ type: "refused", error: why });
        return false;
      }
      if (typeof drawnSha !== "string" || !drawnSha) {
        dispatch({ type: "refused", error: NO_HEAD });
        return false;
      }
      const t = s.composing;
      const cmd = addCommand({ pane, file: t.file, side: t.side, line: t.line, text: s.draft, expectSha: drawnSha, req: newReq() });
      if (!write(cmd, { kind: "add", target: t, text: s.draft, at: new Date().toISOString() })) {
        dispatch({ type: "refused", error: NOT_CONNECTED });
        return false;
      }
      return true;
    },
    close: (id) => closeWith("commentResolve", id),
    reopen: (id) => closeWith("commentReopen", id),
  };
  function closeWith(cmdName, id) {
    if (closing[id]) return false;
    const cmd = closeCommand(cmdName, { pane, commentId: id, req: newReq() });
    if (!write(cmd, { kind: "close", id })) dispatch({ type: "closeFailed", id });
    return true;
  }
}
