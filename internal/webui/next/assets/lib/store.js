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
//   dropped       how many frames were dropped as malformed
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

const GRAPH_OPS = new Set(["graphSnapshot", "graphWatch", "graphUnwatch"]);

const initial = () => ({
  connection: "idle", retryInMs: 0,
  hello: null, remote: false, state: null,
  graph: null, graphGone: null, graphRefused: false, graphError: null,
  notice: null, sent: null, dropped: 0,
});

export function createStore(opts) {
  let snap = initial();
  const listeners = new Set();
  // The project last asked for with watchGraph, or null when none. "" is a
  // valid root (the project on screen), so null and "" are different.
  let watching = null;
  let reqSeq = 0;
  let noticeSeq = 0;
  let sentSeq = 0;

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

  function handle(msg) {
    switch (msg.type) {
      case "hello": {
        const remote = !!msg.remote;
        // A new hello starts the connection over. What a graph command
        // refused before is tried again unless this window is remote.
        set({ hello: msg, remote, graphRefused: remote, graphError: null });
        if (!remote && watching !== null) sendGraphWatch();
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
      case "paneSend":
        // The answer to an accepted paneSend. A refusal comes as a notice.
        set({ sent: { ...msg, seq: ++sentSeq } });
        break;
      case "notice":
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
      watching = null;
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
      if (snap.remote && typeof cmd.cmd === "string" && cmd.cmd.startsWith("graph")) return false;
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
    unwatchGraph() {
      if (watching === null) return;
      watching = null;
      if (!snap.remote) socket.send({ cmd: "graphUnwatch" });
      set({ graph: null, graphGone: null, graphError: null });
    },
  };
}
