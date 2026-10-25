// The state of review comments on one branch: the composer, comments on their way
// to the desk, and closing and reopening. It is a reducer and a hook over it, and
// it knows nothing of the store: the caller gives it `send` (a function that
// writes a command and answers whether it did), the latest `changesResult` it has
// seen, and the branch's `comments` from the desk's listing.
//
// - A comment is sent with expectSha, the head the person looked at, so the desk
//   refuses (headMoved) rather than attach it to code the person did not see. The
//   text stays in the composer when the desk refuses, with the reason beside it.
// - While the desk has not answered, the comment is drawn where it was written, as
//   "Sending…". When it answers ok, the comment it holds replaces that one, and it
//   is kept until the desk's own listing has it.
// - Resolve and reopen send one command each; the answer's comment replaces the
//   listed one until the listing catches up. A refusal is said on that comment.
import { useEffect, useReducer, useRef } from "../../vendor/hooks.mjs";
import { addCommand, closeCommand, checkComment, refusalWords } from "./commentModel.js";

const isObject = (v) => !!v && typeof v === "object" && !Array.isArray(v);
const NOT_CONNECTED = "Not connected to Flockdeck. Nothing was sent.";

export const initialFlow = { composing: null, draft: "", error: "", pending: {}, local: [], overrides: {}, errors: {} };

/** flowReducer is the whole state machine; the actions are the hook's. */
export function flowReducer(s, a) {
  switch (a.type) {
    case "open": {
      const same = s.composing && s.composing.file === a.target.file && s.composing.side === a.target.side && s.composing.line === a.target.line;
      return { ...s, composing: a.target, draft: same ? s.draft : "", error: "" };
    }
    case "draft":
      return { ...s, draft: a.text, error: "" };
    case "cancel":
      // A comment on its way is not taken back by closing the box.
      return Object.values(s.pending).some((p) => p.kind === "add") ? s : { ...s, composing: null, draft: "", error: "" };
    case "refused":
      return { ...s, error: a.error };
    case "closeFailed":
      return { ...s, errors: { ...s.errors, [a.id]: NOT_CONNECTED } };
    case "sent":
      return { ...s, error: "", pending: { ...s.pending, [a.req]: a.pending }, errors: a.pending.kind === "close" ? { ...s.errors, [a.pending.id]: "" } : s.errors };
    case "result": {
      const msg = a.msg;
      const p = isObject(msg) ? s.pending[msg.req] : null;
      if (!p) return s;
      const pending = { ...s.pending };
      delete pending[msg.req];
      if (p.kind === "add") {
        if (!msg.ok || !isObject(msg.comment)) return { ...s, pending, error: refusalWords(msg) };
        return { ...s, pending, local: [...s.local, msg.comment], composing: null, draft: "", error: "" };
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
 * useCommentFlow: { pane, branch, headSha, send, result, comments }. It answers the
 * state and the actions the view calls:
 *   composing, draft, error, busy (a comment is on its way), comments (merged),
 *   errors (by comment id), open(target), setDraft(text), cancel(), submit(),
 *   close(id), reopen(id).
 */
export function useCommentFlow({ pane = "", branch = "", headSha = "", send = null, result = null, comments = [] }) {
  const [s, dispatch] = useReducer(flowReducer, initialFlow);
  const seen = useRef(null);
  useEffect(() => {
    if (result && result !== seen.current) {
      seen.current = result;
      dispatch({ type: "result", msg: result });
    }
  }, [result]);
  useEffect(() => {
    dispatch({ type: "listed", ids: new Set((Array.isArray(comments) ? comments : []).filter(isObject).map((c) => c.id)) });
  }, [comments]);

  const write = (cmd, pending) => {
    if (!send || !send(cmd)) return false;
    dispatch({ type: "sent", req: cmd.req, pending });
    return true;
  };
  return {
    composing: s.composing,
    draft: s.draft,
    error: s.error,
    busy: Object.values(s.pending).some((p) => p.kind === "add"),
    comments: mergeComments(comments, s),
    errors: s.errors,
    open: (target) => dispatch({ type: "open", target }),
    setDraft: (text) => dispatch({ type: "draft", text }),
    cancel: () => dispatch({ type: "cancel" }),
    submit: () => {
      if (!s.composing || Object.values(s.pending).some((p) => p.kind === "add")) return false;
      const why = checkComment(s.draft);
      if (why) {
        dispatch({ type: "refused", error: why });
        return false;
      }
      const t = s.composing;
      const cmd = addCommand({ pane, branch, file: t.file, side: t.side, line: t.line, text: s.draft, expectSha: headSha, req: newReq() });
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
    const cmd = closeCommand(cmdName, { pane, branch, commentId: id, req: newReq() });
    if (!write(cmd, { kind: "close", id })) dispatch({ type: "closeFailed", id });
    return true;
  }
}
