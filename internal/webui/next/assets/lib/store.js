// store.js holds what the new shell knows about the Flockdeck it is talking to:
// the latest hello, state and team graph, and whether the connection is up.
//
// It is a plain store with a subscribe function, not a signal. The shell has no
// signals library and Preact's hooks are enough: use-store.js turns a store
// into a hook. Every change replaces the whole snapshot with a new object, so a
// view can compare by identity, and nothing in a snapshot is changed in place.
//
// Public API (T75 and the views use nothing else):
//
//   const store = createStore({ url });      // url from controlUrl(location)
//   store.start() / store.stop()
//   store.getState()                         // the snapshot, below
//   store.subscribe(fn)                      // fn(snapshot); returns unsubscribe
//   store.send(cmd)                          // a command object; false if not sent
//   store.watchGraph(root)                   // "" is the project on screen
//   store.unwatchGraph()
//   store.watchStructured(id)                // one structured pane, local window only
//   store.unwatchStructured()
//   store.watchConversation(id)              // one pane's conversation, for the drawer's tab
//   store.olderConversation()                // the page before the entries held
//   store.unwatchConversation()
//
// The snapshot:
//
//   connection    "idle" | "connecting" | "open" | "waiting"
//   retryInMs     the wait before the next attempt, while connection is "waiting"
//   hello         the latest hello (keys, prefs, remote, e2ePublicKey, role...)
//                 or null before the first one of this connection. hello and
//                 state are emptied when the connection drops. graph, graphGone
//                 and notice are not: they stay as the last thing seen, so a
//                 view reads connection and shows them as out of date while it
//                 is not "open".
//   remote        true when the window was reached through the relay
//   state         the latest state push, or null
//   graph         the latest graph push, or null. A graph is one whole project:
//                 each push replaces the last. After watchGraph(B) a push of
//                 project A may still arrive, so a view compares graph.root
//                 with the project it asked for and does not draw another. It can hold a permission that is
//                 scrubbed (redacted: true, no command, empty options), a pane
//                 with detailsOnDesktop, a prompt with no ask, and a structured
//                 pane that is only in the roster. The views draw what is there.
//   graphGone     { root, error } after the watched project went away, else null
//   graphRefused  true when the graph cannot be shown here: the window is
//                 through the relay, or the desktop refused a graph command. The
//                 views say the team graph is shown on the desktop and do not retry.
//   graphError    { op, error } after a graph command failed for another reason
//                 (the usual one is that no project is open at that directory)
//   notice        the latest notice as { seq, text, error, ... }, or null. seq
//                 grows with each one, so a view can tell a new notice from a
//                 repeat of the same text.
//   sent          the latest accepted paneSend as { seq, id, queued, shell,
//                 waiting, routed }, or null. A refused one arrives as a notice
//                 with error true. seq grows with each one.
//   structured    the latest structured message of the pane watchStructured
//                 named (its card among the rest), or null. A push of another
//                 pane than the one watched is not kept, and one with gone: true
//                 clears it.
//   structuredError  { error } after structuredWatch was refused, else null
//   cancelError      { error } after structuredCancel was refused, null after one that worked
//   answer        the latest permissionAnswer reply as { seq, ok, error, id,
//                 cardSeq, requestId, req }, or null. A view matches it to the
//                 card it answered by id, cardSeq and requestId.
//   conversation  the conversation of the pane watchConversation named, or null:
//                 { id, supported, entries, atStart, older, refused }. supported is
//                 null until the desktop has answered, then true, or false for a
//                 pane with no stored conversation (a shell, or an agent with no
//                 transcript reader), which has no tab. entries are the
//                 transcript.Entry objects the desktop sent, oldest first; atStart
//                 says the first of them is the start of the conversation; older
//                 is "loading" while a page before them was asked for, else "idle".
//                 refused is the desktop's text when it refused this device the
//                 conversation, else "": it is shown once and not asked for again
//                 until the next connection.
//   dropped       how many frames were dropped as malformed
//
// The structured watch and permissionAnswer are for a window on the desk, as the
// graph is: in a remote window the store sends neither, and the desktop would
// refuse them. A structured pane is watched one at a time, and the watch is asked
// for again after a reconnect.
//
// The graph is never asked for when remote is true, and never before the hello
// has said whether it is. A watch survives a reconnect: when the next hello
// arrives and says remote is false, graphWatch is sent again for the project
// that was last asked for. The server holds one watch at a time, so watching
// another project replaces the first.

import { createSocket } from "./socket.js";

/** DESK_ONLY_CODE is the code the desktop puts on the refusal it gives a window
 *  reached through the relay (internal/server/teamui.go, teamDeskOnlyCode).
 *  DESK_ONLY_PHRASE is part of that refusal's text, which an older desktop sends
 *  without the code. A Go test pins both against the server. */
export const DESK_ONLY_CODE = "deskOnly";
export const DESK_ONLY_PHRASE = "machine Flockdeck runs on";

/** VIEWER_REFUSED_PHRASE starts the notice the desktop sends a device that may only
 *  view, for a command it may not send (internal/server/viewerrole.go,
 *  viewerRefused). A Go test pins it against the server. */
export const VIEWER_REFUSED_PHRASE = "This device can only view";

/** MAX_ENTRIES bounds the entries held for one conversation. Past it the oldest
 *  are dropped, and the conversation no longer starts at the first of them. */
export const MAX_ENTRIES = 500;

const GRAPH_OPS = new Set(["graphSnapshot", "graphWatch", "graphUnwatch"]);

/** DESK_ONLY says a command is one a remote window may not send: the graph
 *  commands, the structured pane ones and permissionAnswer. The desktop refuses
 *  them there, so the store does not send them. */
const DESK_ONLY = (cmd) => typeof cmd === "string" && (cmd.startsWith("graph") || cmd.startsWith("structured") || cmd === "permissionAnswer");

const initial = () => ({
  connection: "idle", retryInMs: 0,
  hello: null, remote: false, state: null,
  graph: null, graphGone: null, graphRefused: false, graphError: null,
  notice: null, sent: null, structured: null, structuredError: null, cancelError: null, answer: null, conversation: null, dropped: 0,
});

/** upsert returns entries with each of the new ones in place of the one with its
 *  id, or after the last. The desktop sends an entry again when it changed (a
 *  tool that finished). */
function upsert(entries, more) {
  const out = entries.slice();
  const at = new Map(out.map((e, i) => [e.id, i]));
  for (const e of more) {
    if (at.has(e.id)) out[at.get(e.id)] = e;
    else {
      at.set(e.id, out.length);
      out.push(e);
    }
  }
  return out;
}

/** goodEntries keeps the entries that are objects with an id and a kind. */
const goodEntries = (list) => (Array.isArray(list) ? list.filter((e) => e && typeof e === "object" && typeof e.id === "string" && e.id && typeof e.kind === "string") : []);

export function createStore(opts) {
  let snap = initial();
  const listeners = new Set();
  // The project last asked for with watchGraph, or null when none. "" is a
  // valid root (the project on screen), so null and "" are different.
  let watching = null;
  let reqSeq = 0;
  let noticeSeq = 0;
  let sentSeq = 0;
  let answerSeq = 0;
  // The structured pane last asked for with watchStructured, or null.
  let watchingPane = null;
  // The pane whose conversation is open, or null.
  let watchingConv = null;

  const notify = () => {
    for (const fn of [...listeners]) {
      try { fn(snap); } catch (err) { console.error("store listener failed", err); }
    }
  };
  const set = (patch) => {
    snap = { ...snap, ...patch };
    notify();
  };

  const socket = createSocket({
    ...opts,
    onPhase(phase, info) {
      const patch = { connection: phase, retryInMs: phase === "waiting" ? info.retryInMs : 0 };
      // What was read from a connection that has gone is not shown as current
      // while the next is made. The hello and the first state of the new one
      // replace them.
      if (phase !== "open") {
        patch.hello = null;
        patch.state = null;
      }
      set(patch);
    },
    onFrame: handle,
    onDrop() { set({ dropped: snap.dropped + 1 }); },
  });

  function sendGraphWatch() {
    return socket.send({ cmd: "graphWatch", root: watching, req: "g" + ++reqSeq });
  }

  function sendStructuredWatch() {
    return socket.send({ cmd: "structuredWatch", id: watchingPane, req: "s" + ++reqSeq });
  }

  function sendConversationOpen() {
    const c = snap.conversation;
    const cmd = { cmd: "conversationOpen", id: watchingConv };
    // After a reconnect the desktop is asked only for what came since.
    if (c && c.entries.length > 0) cmd.after = c.entries[c.entries.length - 1].id;
    return socket.send(cmd);
  }

  function handle(msg) {
    switch (msg.type) {
      case "hello": {
        const remote = !!msg.remote;
        // A new hello starts the connection over. What a graph command
        // refused before is tried again unless this window is remote.
        set({ hello: msg, remote, graphRefused: remote, graphError: null });
        if (!remote && watching !== null) sendGraphWatch();
        if (!remote && watchingPane !== null) sendStructuredWatch();
        if (watchingConv !== null) {
          if (snap.conversation && snap.conversation.refused) set({ conversation: { ...snap.conversation, refused: "" } });
          sendConversationOpen();
        }
        break;
      }
      case "state":
        set({ state: msg });
        break;
      case "prefs":
        if (snap.hello && msg.prefs) set({ hello: { ...snap.hello, prefs: msg.prefs } });
        break;
      case "keyTable":
        if (snap.hello && Array.isArray(msg.keys)) set({ hello: { ...snap.hello, keys: msg.keys } });
        break;
      case "graph":
        // A push that was in flight when the watch was dropped is not wanted.
        if (watching !== null && !snap.remote) set({ graph: msg, graphGone: null, graphError: null });
        break;
      case "teamGone":
        // Only the project that is shown. With no graph yet its root cannot be
        // compared (the server spells it its own way), so the message is taken
        // for the watch that is open, and ignored when none is. The watch is
        // given up, so a reconnect does not ask for a project that is not
        // there; the view calls watchGraph again when there is another one.
        if (snap.graph === null ? watching !== null : msg.root === snap.graph.root) {
          watching = null;
          set({ graph: null, graphGone: { root: String(msg.root || ""), error: String(msg.error || "") } });
        }
        break;
      case "teamResult":
        if (!msg.ok && msg.op === "structuredWatch" && watchingPane !== null) {
          set({ structuredError: { error: String(msg.error || "") } });
        }
        if (msg.op === "structuredCancel") {
          set({ cancelError: msg.ok ? null : { error: String(msg.error || "") } });
        }
        if (!msg.ok && GRAPH_OPS.has(msg.op)) {
          const error = String(msg.error || "");
          if (msg.code === DESK_ONLY_CODE || error.includes(DESK_ONLY_PHRASE)) {
            watching = null;
            set({ graph: null, graphRefused: true, graphError: null });
          } else {
            set({ graphError: { op: msg.op, error } });
          }
        }
        break;
      case "structured":
        if (watchingPane !== null && msg.id === watchingPane && !snap.remote) {
          set(msg.gone ? { structured: null, structuredError: null } : { structured: msg, structuredError: null });
        }
        break;
      case "permissionAnswer":
        set({ answer: { ...msg, seq: ++answerSeq } });
        break;
      case "paneSend":
        // The answer to an accepted paneSend. A refusal comes as a notice.
        set({ sent: { ...msg, seq: ++sentSeq } });
        break;
      case "conversationPage": {
        const c = snap.conversation;
        if (watchingConv === null || msg.id !== watchingConv || !c) break;
        if (msg.supported !== true) {
          set({ conversation: { ...c, supported: false, entries: [], atStart: true, older: "idle", refused: "" } });
          break;
        }
        const page = goodEntries(msg.entries);
        let entries;
        let atStart = !!msg.atStart;
        if (c.older === "loading" && !msg.reset) {
          // The page before the ones held.
          entries = upsert(page, c.entries);
        } else if (msg.reset || c.entries.length === 0) {
          entries = page;
        } else {
          // The answer to an open that asked only for what came since.
          entries = upsert(c.entries, page);
          atStart = c.atStart;
        }
        set({ conversation: { ...c, supported: true, entries, atStart, older: "idle", refused: "" } });
        break;
      }
      case "conversationAppend": {
        const c = snap.conversation;
        if (watchingConv === null || msg.id !== watchingConv || !c || c.supported !== true) break;
        let entries = upsert(c.entries, goodEntries(msg.entries));
        let atStart = c.atStart;
        if (entries.length > MAX_ENTRIES) {
          entries = entries.slice(entries.length - MAX_ENTRIES);
          atStart = false;
        }
        set({ conversation: { ...c, entries, atStart } });
        break;
      }
      case "notice":
        // The refusal of a conversation command is shown on the tab, once. The
        // notice is kept as well, like any other.
        if (watchingConv !== null && snap.conversation && snap.conversation.supported === null && msg.error && String(msg.text || "").startsWith(VIEWER_REFUSED_PHRASE)) {
          set({ notice: { ...msg, seq: ++noticeSeq }, conversation: { ...snap.conversation, refused: String(msg.text) } });
          break;
        }
        set({ notice: { ...msg, seq: ++noticeSeq } });
        break;
      default:
        break;
    }
  }

  return {
    start() { socket.start(); },
    stop() {
      if (watching !== null) socket.send({ cmd: "graphUnwatch" });
      if (watchingPane !== null) socket.send({ cmd: "structuredUnwatch", id: watchingPane });
      watching = null;
      watchingPane = null;
      if (watchingConv !== null) socket.send({ cmd: "conversationClose", id: watchingConv });
      watchingConv = null;
      socket.stop();
      snap = initial();
      notify();
    },
    getState() { return snap; },
    subscribe(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    /** send writes a command. It returns false when nothing was sent: the
     *  socket is not open, or the command is a graph command and this window is
     *  remote. */
    send(cmd) {
      if (snap.remote && DESK_ONLY(cmd.cmd)) return false;
      return socket.send(cmd);
    },
    /** watchGraph asks for the graph of a project, now and whenever it
     *  changes. Before the hello has arrived the ask is kept and sent with it.
     *  It returns false, and asks for nothing, in a remote window. */
    watchGraph(root) {
      if (snap.remote) {
        set({ graphRefused: true });
        return false;
      }
      watching = String(root || "");
      set({ graphGone: null, graphError: null });
      if (snap.hello) sendGraphWatch();
      return true;
    },
    /** watchStructured asks for the structured messages of one pane, now and
     *  whenever it changes, for its permission card. It replaces the pane watched
     *  before, and returns false, asking for nothing, in a remote window. */
    watchStructured(id) {
      if (snap.remote || !id) return false;
      const was = watchingPane;
      if (was === id) return true;
      if (was !== null) socket.send({ cmd: "structuredUnwatch", id: was });
      watchingPane = id;
      set({ structured: null, structuredError: null });
      if (snap.hello) sendStructuredWatch();
      return true;
    },
    unwatchStructured() {
      if (watchingPane === null) return;
      const was = watchingPane;
      watchingPane = null;
      if (!snap.remote) socket.send({ cmd: "structuredUnwatch", id: was });
      set({ structured: null, structuredError: null });
    },
    /** watchConversation asks for the conversation of one pane, now and as it
     *  grows, for the drawer's tab. It replaces the pane watched before (that one
     *  is closed) and does nothing for the pane already watched. */
    watchConversation(id) {
      if (!id || watchingConv === id) return false;
      if (watchingConv !== null) socket.send({ cmd: "conversationClose", id: watchingConv });
      watchingConv = id;
      set({ conversation: { id, supported: null, entries: [], atStart: false, older: "idle", refused: "" } });
      if (snap.hello) sendConversationOpen();
      return true;
    },
    /** olderConversation asks for the page before the entries held. It asks once
     *  at a time, and not at all at the start of the conversation. */
    olderConversation() {
      const c = snap.conversation;
      if (watchingConv === null || !c || c.supported !== true || c.atStart || c.older === "loading" || c.entries.length === 0) return false;
      if (!socket.send({ cmd: "conversationOlder", id: watchingConv, before: c.entries[0].id })) return false;
      set({ conversation: { ...c, older: "loading" } });
      return true;
    },
    unwatchConversation() {
      if (watchingConv === null) return;
      const was = watchingConv;
      watchingConv = null;
      socket.send({ cmd: "conversationClose", id: was });
      set({ conversation: null });
    },
    unwatchGraph() {
      if (watching === null) return;
      watching = null;
      if (!snap.remote) socket.send({ cmd: "graphUnwatch" });
      set({ graph: null, graphGone: null, graphError: null });
    },
  };
}
