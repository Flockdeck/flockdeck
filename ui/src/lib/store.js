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
//   store.watchTeam(root)                    // the Team panel's board and messages
//   store.unwatchTeam()
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
//   versions      the latest listVersions answer as { items, error }, or null
//                 before one (and again after the next listVersions is sent)
//   detached      true once the desktop has detached this window
//   helpers       the latest helpers message as { rows }, or null. The desktop sends
//                 it when the list is asked for and whenever a helper changes.
//   helperPlan    the latest helperPlan answer (what an install would do), or null;
//                 cleared when a new helperPlan is sent
//   recents       the latest recents answer as { items }, or null
//   browse        the latest folder listing the desktop sent, or null
//   approvals     the requests for an answer the desktop is waiting on, oldest first, as
//                 { id, lead, text, label, token, seq }. Only a baton leaving its
//                 provider is asked this way: the desktop sends a notice of kind
//                 "approval" with a button that sends approveBaton and the token, and
//                 withdraws it when the request ends. A token belongs to the connection
//                 it came on, so the list is emptied when the connection drops. At most
//                 MAX_APPROVALS are kept; the oldest is put away to make room.
//   batonDraft    the latest makeBaton answer, or null; the dialog that asked matches
//                 it by req. batonSaved and batonError are the answers to saveBaton,
//                 startFromBaton and restartWithBaton, each with a seq that grows
//                 with each one and the req the command carried.
//   sent          the latest accepted paneSend as { seq, id, queued, shell,
//                 waiting, routed }, or null. A refused one arrives as a notice
//                 with error true. seq grows with each one.
//   structured    the latest structured message of the pane watchStructured
//                 named (its card among the rest), or null. A push of another
//                 pane than the one watched is not kept, and one with gone: true
//                 clears it.
//   structuredError  { error } after structuredWatch was refused, else null
//   cancelError      { id, error } after structuredCancel was refused for pane id, null
//                    after one that worked; a view shows it only for its own pane
//   started       the latest startAgent answer as { seq, paneId, id }, or null: the
//                 pane the desktop started and the id the window gave the command.
//                 seq grows with each one. A refusal arrives as a notice.
//   answer        the latest permissionAnswer reply as { seq, ok, error, id,
//                 cardSeq, requestId, req }, or null. A view matches it to the
//                 card it answered by id, cardSeq and requestId.
//   asking        the card a permissionAnswer was last sent for, as { id, cardSeq,
//                 requestId, after }, or null. Every view of that card reads it, so
//                 a card drawn in the strip and again in the stream is answered
//                 once; a refusal clears it and the card can be answered again.
//   details       the answers to requestDetail for the open conversation, by entry
//                 id: { state: "loading" | "ok" | "gone", detail }. At most
//                 MAX_DETAILS are held; the oldest asked for goes first.
//   changes       the latest listing of a working tree (the desktop's "changes"
//                 message without its type), or null: cwd, branch, ahead, behind,
//                 files [{ path, status, label, added, removed, untracked, stamp }],
//                 omitted, error. diff is the latest "diff" answer: { cwd, file,
//                 text, error } or { file, loading: true }. gitWarn is a question
//                 the desktop put before a commit, push or pull, and changesBusy
//                 the action in flight ("commit", "push", "pull", "fetch") or "".
//                 changesNote is the last notice that came while the panel was open.
//   taskDetail    the answer to requestTask: { id, req, state, task, error }, or null.
//                 state is "loading", "ok" (task is the desktop's task view: body,
//                 notes, owner, branch) or "error". It is for a window on the desk;
//                 a window through the relay is refused and sends none.
//   search        the search of the open conversation, or null: { q, state, matches,
//                 more }. state is "loading" until the desktop has answered, then
//                 "done". matches are { entryId, kind, snippet, offset }, newest
//                 first; more says there were more than were sent. Asked for with
//                 searchConversation and dropped by clearSearch, or when the
//                 conversation closes or changes.
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
//   history       the latest conversations answer (the stored conversations of a
//                 project, for Resume) as { cwd, items, error, seq }, or null. seq
//                 grows with each one, so a dialog that asked can tell its answer
//                 from an older one.
//   paneInfo      the latest paneInfo answer as { id, name, fields, transcriptOff,
//                 seq }, or null; paneMatches the latest findPane answer as
//                 { query, items, seq }, or null. Both are for a window on the
//                 desk: the desktop answers neither through the relay.
//   keys          the latest API keys answer as { items, seq }, or null. An item
//                 says whether an agent has a key and where it came from, never
//                 the key.
//   jevKey        the latest answer about the TypeSafe key as { set, env, seq }:
//                 whether one is set, never the key.
//   remoteDevices the latest roster of Flockdeck Remote, remotePair the latest
//                 pairing link or join code, and remoteOutcome the latest answer
//                 to turning it on or off, each with a seq like the ones above.
//   agentsList    the latest agents answer (every pane in every open project) as
//                 { items, seq }, or null. A remote window is sent it scrubbed.
//   team          the picture of the project watchTeam asked for (roster, task
//                 board, messages) as the desktop sent it plus seq, or null.
//                 teamGone is { root, error } when that project can no longer be
//                 read, teamTasks and teamMessages the latest taskList and
//                 msgList answers, teamResult the latest answer to a command that
//                 changed something, and teamRefused is true in a remote window,
//                 which is never sent any of it.
//   radar         what the version of git came to for the conflict radar, as
//                 { unavailable } ("" when it can run), or null until the desktop
//                 says after the hello; the hello carries the first answer.
//   worktrees     the latest worktrees answer (the listing of the project's
//                 worktrees and branches) plus seq, or null. The desktop sends
//                 it again after every add, remove and prune.
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
import { clientHello, mismatchNotice, unsupportedNotice } from "./apiversion.js";
import { mergeKeys, readsKeyFiles } from "./keytable.js";
import { changesTeamOf, isStale, sameRoot } from "./changesteam.js";

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

/** REMOTE_CONVERSATION_LINE is what a remote window says in place of a conversation, and
 *  how the desktop starts the notice it sends to one that asks anyway
 *  (internal/server/relayscrub.go, conversationsOnDesktop). A Go test pins it. */
export const REMOTE_CONVERSATION_LINE = "Conversations are shown on the desktop only for now";

/** MAX_APPROVALS is how many requests for an answer are kept at once. */
export const MAX_APPROVALS = 6;

/** LOST_APPROVAL_WORDS is what is said when the connection drops with a request
 *  for an answer on screen: its token went with the connection. */
export const LOST_APPROVAL_WORDS = "Connection lost: a baton request was waiting for you. Ask the agent again.";

/** approvalOf reads a notice as a request for an answer, or null when it is not
 *  one. Only an approveBaton button counts: no other command is ever sent because
 *  a notice named it. */
export function approvalOf(msg) {
  const a = msg && msg.kind === "approval" && typeof msg.id === "string" && msg.id && msg.action;
  if (!a || typeof a.label !== "string" || !a.label || !a.send || a.send.cmd !== "approveBaton" || typeof a.send.text !== "string" || !a.send.text) return null;
  return { id: msg.id, lead: typeof msg.lead === "string" ? msg.lead : "", text: typeof msg.text === "string" ? msg.text : "", label: a.label, token: a.send.text };
}

/** MAX_ENTRIES bounds the entries held for one conversation. Past it the oldest
 *  are dropped, and the conversation no longer starts at the first of them. */
export const MAX_ENTRIES = 500;

/** nullMap is an object with no prototype holding the own properties of each
 *  source: a table keyed by an id that came from a transcript or an agent, where
 *  "__proto__", "toString" and "constructor" must be ordinary keys. */
function nullMap(...sources) {
  const m = Object.create(null);
  for (const src of sources) for (const k of Object.keys(src || {})) Object.defineProperty(m, k, { value: src[k], enumerable: true, writable: true, configurable: true });
  return m;
}

/** MAX_DETAILS bounds the entry details held: a tool's output and a diff can each be large. */
export const MAX_DETAILS = 12;

/** MACHINE_ONLY are the commands that change what the machine runs: updating,
 *  restarting, detaching, quitting. A remote window sends none of them, and the
 *  desktop would refuse them if it did. */
const MACHINE_ONLY = ["checkForUpdate", "listVersions", "installVersion", "restart", "updates", "detach", "quit"];

/** MACHINE_FILES are the commands that read or show something on the machine
 *  Flockdeck runs on: the stored conversations, a pane's ids and paths, the folder
 *  a transcript or a recording is in. The desktop answers them to a window on that
 *  machine only, so a remote window sends none of them. */
const MACHINE_FILES = ["conversations", "resumeConversation", "paneInfo", "findPane", "revealPane", "exportTranscript", "revealTranscript", "openRecordings"];

/** HELPER_COMMANDS install, run and open programs on the machine Flockdeck runs
 *  on, and makeBaton reads a pane's work: the desktop refuses each of them from a
 *  window reached through the relay, so the store does not send them. */
const HELPER_COMMANDS = ["approveBaton", "helpers", "helperPlan", "helperInstall", "helperStart", "helperStop", "helperOpen", "helperUninstall", "makeBaton"];

/** DESK_SETTINGS are the commands that set a key, or change how this machine is
 *  reached or who may reach it. Each is the desk's: a key typed in a relay window
 *  passes through the relay, and a window that could turn remote access off or on,
 *  or widen what a device may do, could cut or widen its own way in. The desktop
 *  refuses them there; the store does not send them. The list of keys and of
 *  devices, pairing a device, renaming and unpairing are not here: a relay window
 *  may do those. A join code is held back by the check in send(). */
const DESK_SETTINGS = ["keySet", "keyClear", "jevKey", "setAgentAddress", "remoteEnable", "remoteDisable", "remoteRemove", "remoteMove", "remoteVerify", "remoteUnverify", "setDeviceRole"];

const GRAPH_OPS = new Set(["graphSnapshot", "graphWatch", "graphUnwatch"]);

/** TEAM_COMMANDS are the Team panel's: the board, the messages and the watch that
 *  keeps them current. The desktop refuses them all to a window reached through the
 *  relay (internal/server/teamui.go), so the store does not send them from one. */
const TEAM_COMMANDS = ["teamSnapshot", "teamWatch", "teamUnwatch", "taskList", "taskGet", "taskAdd", "taskAssign", "taskStatus", "taskNote", "taskBranch", "taskPrune", "msgList", "msgSend", "msgSeen", "teamClose"];
const TEAM_OPS = new Set(TEAM_COMMANDS.filter((c) => c !== "teamWatch" && c !== "teamUnwatch"));

/** CHANGES_TEAM_COMMANDS are the team-wide Changes view's (docs/changes-api.md): the watch, the files and the diffs, comments, checks, reviews and the merge gate. All are the desk's. */
const CHANGES_TEAM_COMMANDS = ["changesWatch", "changesUnwatch", "changesSnapshot", "filesPage", "diffHunks", "diffContext", "commentAdd", "commentResolve", "commentReopen", "runChecks", "requestReview", "submitReview", "approveMerge", "unapproveMerge", "mergeBranch"];

/** DESK_ONLY says a command is one a remote window may not send: the graph
 *  commands, the structured pane ones, permissionAnswer, paneSend, MACHINE_ONLY,
 *  MACHINE_FILES and HELPER_COMMANDS. The desktop refuses them there, so the store
 *  does not send them. paneSend is sent from two places, the manager bar and a
 *  structured pane's prompt box, and neither is drawn in a remote window. */
const DESK_ONLY = (cmd) => typeof cmd === "string" && (CHANGES_TEAM_COMMANDS.includes(cmd) || cmd.startsWith("graph") || cmd.startsWith("structured") || cmd === "permissionAnswer" || cmd === "paneSend" || MACHINE_ONLY.includes(cmd) || MACHINE_FILES.includes(cmd) || HELPER_COMMANDS.includes(cmd) || DESK_SETTINGS.includes(cmd) || TEAM_COMMANDS.includes(cmd));

const initial = () => ({
  connection: "idle", retryInMs: 0,
  hello: null, remote: false, state: null,
  graph: null, graphGone: null, graphRefused: false, graphError: null,
  notice: null, versions: null, helpers: null, helperPlan: null, recents: null, browse: null, batonDraft: null, batonSaved: null, batonError: null, agentAddress: null,
  approvals: [], detached: false, sent: null, started: null, structured: null, structuredError: null, cancelError: null, answer: null, asking: null, conversation: null, details: nullMap(), search: null, taskDetail: null, changes: null, diff: null, gitWarn: null, changesBusy: "", changesNote: null, history: null, paneInfo: null, paneMatches: null, keys: null, jevKey: null, radar: null, worktrees: null, agentsList: null, team: null, teamGone: null, teamTasks: null, teamMessages: null, teamResult: null, teamRefused: false, changesTeam: null, changesTeamGone: null, changesTeamRefused: false, remoteDevices: null, remotePair: null, remoteOutcome: null, dropped: 0,
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
  let pendingCommit = null;
  let changesAsked = null;
  let sentSeq = 0;
  let startedSeq = 0;
  let answerSeq = 0;
  let detailSeq = 0;
  let historySeq = 0;
  let infoSeq = 0;
  let matchSeq = 0;
  let keysSeq = 0;
  let jevSeq = 0;
  let rosterSeq = 0;
  let pairSeq = 0;
  let agentsSeq = 0;
  let worktreesSeq = 0;
  let teamSeq = 0;
  let teamResultSeq = 0;
  // The project last asked for with watchTeam, or null when none.
  let watchingTeam = null;
  // The project last asked for with watchChangesTeam, or null when none.
  let watchingChanges = null;
  // True from a watch being sent until its first push is drawn: a new watch starts its revisions again, so none is skipped as old.
  let changesFresh = false;
  let outcomeSeq = 0;
  let batonSeq = 0;
  let addressSeq = 0;
  let approvalSeq = 0;
  // The pane each structuredCancel in flight was for, by its req, so a refusal is
  // kept for that pane and shown only there.
  const cancelPanes = new Map();
  // The structured pane last asked for with watchStructured, or null.
  let watchingPane = null;
  // The pane whose conversation is open, or null.
  let watchingConv = null;
  // The permission cards an answer was sent for, so a second view of one cannot send another.
  const askedCards = new Set();
  const cardKey = (c) => [c.id, c.cardSeq, c.requestId].map((p) => String(p == null ? "" : p)).join("");

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
        // An answer sent on the connection that has gone may never have arrived: its cards can be answered again.
        askedCards.clear();
        patch.asking = null;
        // A request for an answer carries a token of the connection that has gone.
        if (snap.approvals.length) {
          patch.approvals = [];
          patch.notice = { text: LOST_APPROVAL_WORDS, error: true, seq: ++noticeSeq };
        }
      }
      set(patch);
    },
    onFrame: handle,
    onDrop() { set({ dropped: snap.dropped + 1 }); },
  });

  function sendGraphWatch() {
    return socket.send({ cmd: "graphWatch", root: watching, req: "g" + ++reqSeq });
  }

  function sendTeamWatch() {
    return socket.send({ cmd: "teamWatch", root: watchingTeam, req: "t" + ++reqSeq });
  }

  function sendChangesWatch() {
    changesFresh = true;
    return socket.send({ cmd: "changesWatch", root: watchingChanges, req: "w" + ++reqSeq });
  }

  function sendStructuredWatch() {
    return socket.send({ cmd: "structuredWatch", id: watchingPane, req: "s" + ++reqSeq });
  }

  function sendConversationOpen() {
    // A remote window is not sent conversation text until the scrub task is done, so it
    // is not asked, and the drawer says where the conversation is.
    if (snap.remote) {
      if (snap.conversation) set({ conversation: { ...snap.conversation, refused: REMOTE_CONVERSATION_LINE } });
      return false;
    }
    const c = snap.conversation;
    const cmd = { cmd: "conversationOpen", id: watchingConv };
    // After a reconnect the desktop is asked only for what came since.
    if (c && c.entries.length > 0) cmd.after = c.entries[c.entries.length - 1].id;
    return socket.send(cmd);
  }

  // A watch goes out only with the hello, and never in a remote window, so an unwatch
  // is sent only when the hello is in and says this window is not remote.
  const watchSent = () => !!snap.hello && !snap.remote;

  // The defaults of the key table (keys.json), once they have been read. They are
  // asked for on the first hello that offers the table as files, and again on the
  // next one if the answer was not usable; until they are here the window works
  // from the keys the hello carries, if it carries any.
  let keyDefaults = null;
  let keyDefaultsAsked = null;
  function keysFromFiles() {
    if (!opts.keyDefaults) return;
    if (keyDefaults) return applyKeyFiles();
    if (!keyDefaultsAsked) {
      keyDefaultsAsked = Promise.resolve(opts.keyDefaults()).then((d) => { keyDefaults = d; keyDefaultsAsked = null; if (d) applyKeyFiles(); }, () => { keyDefaultsAsked = null; });
    }
  }
  function applyKeyFiles() {
    const h = snap.hello;
    if (!h || !keyDefaults || !readsKeyFiles(h)) return;
    set({ hello: { ...h, keys: mergeKeys(keyDefaults, h.keyOverrides, h.keysHidden) } });
  }

  function handle(msg) {
    switch (msg.type) {
      case "hello": {
        const remote = !!msg.remote;
        // A new hello starts the connection over. What a graph command
        // refused before is tried again unless this window is remote.
        set({ hello: msg, remote, graphRefused: remote, graphError: null, radar: null });
        // A server that offers the key table as keys.json plus changes is read that way.
        if (readsKeyFiles(msg)) keysFromFiles();
        // Tell the desktop which API this page speaks; it answers only if they do not match.
        socket.send(clientHello());
        // A watch asked for before this hello was known is dropped in a remote window, so stop() sends nothing for it.
        if (remote) { watching = null; watchingPane = null; watchingTeam = null; watchingChanges = null; }
        if (!remote && watching !== null) sendGraphWatch();
        if (!remote && watchingPane !== null) sendStructuredWatch();
        if (!remote && watchingTeam !== null) sendTeamWatch();
        if (!remote && watchingChanges !== null) sendChangesWatch();
        if (watchingConv !== null) {
          // What was asked on the connection that has gone is asked again, or is no longer waited for: an older page
          // left "loading" would take the answer to the open below for the page before the entries held.
          const c = snap.conversation;
          if (c && (c.refused || c.older !== "idle")) set({ conversation: { ...c, refused: "", older: "idle" } });
          sendConversationOpen();
          if (!remote) {
            for (const entry of Object.keys(snap.details)) if (snap.details[entry].state === "loading") socket.send({ cmd: "conversationDetail", id: watchingConv, entry });
            if (snap.search && snap.search.state === "loading") socket.send({ cmd: "conversationSearch", id: watchingConv, q: snap.search.q });
          }
        }
        break;
      }
      case "state":
        set({ state: msg });
        break;
      case "prefs":
        if (snap.hello && msg.prefs) set({ hello: { ...snap.hello, prefs: msg.prefs } });
        break;
      case "radarSupport":
        // What the version of git came to, for a hello that came before it was read.
        set({ radar: { unavailable: typeof msg.unavailable === "string" ? msg.unavailable : "" } });
        break;
      case "worktrees":
        set({ worktrees: { ...msg, seq: ++worktreesSeq } });
        break;
      case "keyTable":
        if (snap.hello && (Array.isArray(msg.keys) || readsKeyFiles(snap.hello))) {
          set({ hello: { ...snap.hello, keys: Array.isArray(msg.keys) ? msg.keys : snap.hello.keys, keyOverrides: msg.keyOverrides, keysHidden: msg.keysHidden } });
          if (readsKeyFiles(snap.hello)) keysFromFiles();
        }
        break;
      case "graph":
        // A push that was in flight when the watch was dropped is not wanted.
        if (watching !== null && !snap.remote) set({ graph: msg, graphGone: null, graphError: null });
        break;
      case "agents":
        set({ agentsList: { ...msg, seq: ++agentsSeq } });
        break;
      case "team":
        if (watchingTeam !== null && !snap.remote) set({ team: { ...msg, seq: ++teamSeq }, teamGone: null });
        break;
      case "changesTeam":
        // A push in flight when the watch was dropped is not wanted, nor one the page already drew (the revision is not newer).
        if (watchingChanges !== null && !snap.remote) {
          const next = changesTeamOf(msg);
          // Only the project that is watched ("" is the one on screen, whose root cannot be compared).
          if (next && (watchingChanges === "" || sameRoot(next.root, watchingChanges)) && (changesFresh || !isStale(snap.changesTeam, next))) {
            changesFresh = false;
            set({ changesTeam: next, changesTeamGone: null });
          }
        }
        break;
      case "changesResult":
        if (msg.op === "changesWatch" && !msg.ok) {
          if (msg.code === DESK_ONLY_CODE) {
            watchingChanges = null;
            set({ changesTeam: null, changesTeamRefused: true });
          } else {
            set({ changesTeamGone: { code: String(msg.code || ""), error: String(msg.error || "") } });
          }
        }
        break;
      case "tasks":
        if (!snap.remote) set({ teamTasks: { ...msg, seq: ++teamSeq } });
        break;
      case "messages":
        if (!snap.remote) set({ teamMessages: { ...msg, seq: ++teamSeq } });
        break;
      case "teamGone":
        // The same for the Team panel's project: it is shown as gone, not as it was.
        // The server tells the graph watch and the team watch apart only by their roots, so a message
        // the team watch took is not also taken for a graph watch that has no graph yet.
        let teamTaken = false;
        if (watchingTeam !== null && (snap.team === null || msg.root === snap.team.root)) {
          teamTaken = true;
          watchingTeam = null;
          set({ team: null, teamGone: { root: String(msg.root || ""), error: String(msg.error || "") } });
        }
        // Only the project that is shown. With no graph yet its root cannot be
        // compared (the server spells it its own way), so the message is taken
        // for the watch that is open, and ignored when none is. The watch is
        // given up, so a reconnect does not ask for a project that is not
        // there; the view calls watchGraph again when there is another one.
        if (snap.graph === null ? watching !== null && !teamTaken : msg.root === snap.graph.root) {
          watching = null;
          set({ graph: null, graphGone: { root: String(msg.root || ""), error: String(msg.error || "") } });
        }
        break;
      case "teamResult":
        if (TEAM_OPS.has(msg.op) || msg.op === "teamWatch") {
          const error = String(msg.error || "");
          if (msg.code === DESK_ONLY_CODE || error.includes(DESK_ONLY_PHRASE)) {
            watchingTeam = null;
            set({ team: null, teamRefused: true });
          } else if (msg.op === "teamWatch") {
            if (!msg.ok) set({ teamGone: { root: "", error } });
          } else {
            set({ teamResult: { ...msg, seq: ++teamResultSeq } });
          }
        }
        if (msg.op === "taskGet" && snap.taskDetail && snap.taskDetail.req === msg.req) {
          const t = msg.task && typeof msg.task === "object" ? msg.task : null;
          set({ taskDetail: msg.ok && t && t.id === snap.taskDetail.id ? { ...snap.taskDetail, state: "ok", task: t, error: "" } : { ...snap.taskDetail, state: "error", task: null, error: String(msg.error || "That task could not be read") } });
        }
        if (!msg.ok && msg.op === "structuredWatch" && watchingPane !== null) {
          set({ structuredError: { error: String(msg.error || "") } });
        }
        if (msg.op === "structuredCancel") {
          const id = cancelPanes.get(msg.req) || "";
          cancelPanes.delete(msg.req);
          set({ cancelError: msg.ok ? null : { id, error: String(msg.error || "") } });
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
      case "permissionAnswer": {
        const patch = { answer: { ...msg, seq: ++answerSeq } };
        // A refused answer can be given again.
        if (msg.ok === false) {
          askedCards.delete(cardKey(msg));
          patch.asking = null;
        }
        set(patch);
        break;
      }
      case "conversationSearch": {
        const q = snap.search;
        if (watchingConv === null || msg.id !== watchingConv || !q || Array.from(q.q.trim()).slice(0, 200).join("") !== String(msg.q || "")) break;
        const matches = (Array.isArray(msg.matches) ? msg.matches : [])
          .filter((m) => m && typeof m.entryId === "string" && m.entryId)
          .map((m) => ({ entryId: m.entryId, kind: String(m.kind || ""), snippet: String(m.snippet || ""), offset: Number.isInteger(m.offset) && m.offset >= 0 ? m.offset : 0 }));
        set({ search: { q: q.q, state: "done", matches, more: !!msg.more } });
        break;
      }
      case "conversationDetail": {
        if (watchingConv === null || msg.id !== watchingConv || typeof msg.entry !== "string" || !snap.details[msg.entry]) break;
        const n = snap.details[msg.entry].n;
        const d = msg.gone ? { state: "gone", n } : { state: "ok", n, detail: msg.detail && typeof msg.detail === "object" ? msg.detail : {} };
        set({ details: nullMap(snap.details, { [msg.entry]: d }) });
        break;
      }
      case "agentStarted":
        set({ started: { paneId: String(msg.paneId || ""), id: String(msg.id || ""), seq: ++startedSeq } });
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
        set({ conversation: { ...c, supported: true, entries, atStart, older: "idle", refused: "" }, ...(msg.reset ? { details: nullMap() } : {}) });
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
      case "conversations":
        // Nothing of a conversation reaches a remote window; one that did anyway is not kept.
        if (!snap.remote) set({ history: { ...msg, seq: ++historySeq } });
        break;
      case "paneInfo":
        if (!snap.remote) set({ paneInfo: { ...msg, seq: ++infoSeq } });
        break;
      case "paneMatches":
        if (!snap.remote) set({ paneMatches: { ...msg, seq: ++matchSeq } });
        break;
      case "keys":
        set({ keys: { ...msg, seq: ++keysSeq } });
        break;
      case "jevKey":
        set({ jevKey: { ...msg, seq: ++jevSeq } });
        break;
      case "remoteDevices":
        set({ remoteDevices: { ...msg, seq: ++rosterSeq } });
        break;
      case "remotePair":
        set({ remotePair: { ...msg, seq: ++pairSeq } });
        break;
      case "remoteOutcome":
        set({ remoteOutcome: { ...msg, seq: ++outcomeSeq } });
        break;
      case "versions":
        set({ versions: { items: Array.isArray(msg.items) ? msg.items : [], error: typeof msg.error === "string" ? msg.error : "" } });
        break;
      case "helpers":
        set({ helpers: { rows: Array.isArray(msg.rows) ? msg.rows : [], error: typeof msg.error === "string" ? msg.error : "" } });
        break;
      case "helperPlan":
        set({ helperPlan: msg });
        break;
      case "recents":
        set({ recents: { items: Array.isArray(msg.items) ? msg.items : [] } });
        break;
      case "browse":
        set({ browse: msg });
        break;
      case "batonDraft":
        set({ batonDraft: msg });
        break;
      case "agentAddress":
        set({ agentAddress: { ...msg, seq: ++addressSeq } });
        break;
      case "batonSaved":
        set({ batonSaved: { ...msg, seq: ++batonSeq } });
        break;
      case "batonError":
        set({ batonError: { ...msg, seq: ++batonSeq } });
        break;
      case "detached":
        set({ detached: true });
        break;
      case "apiMismatch":
      case "unsupported": {
        const n = msg.type === "apiMismatch" ? mismatchNotice(msg) : unsupportedNotice(msg);
        if (n) set({ notice: { ...n, seq: ++noticeSeq } });
        break;
      }
      case "noticeWithdraw":
        if (typeof msg.id === "string" && snap.approvals.some((a) => a.id === msg.id)) set({ approvals: snap.approvals.filter((a) => a.id !== msg.id) });
        break;
      case "changes": {
        // A reply for a directory other than the one last asked about is an
        // answer to a question since replaced (the pane changed): not taken.
        // The desktop echoes the directory it was asked about as path.
        // The listing again after an action in the tree on screen is taken too.
        if (typeof msg.path === "string" && msg.path !== changesAsked && !(snap.changes && msg.cwd === snap.changes.cwd)) break;
        const { type, path, ...rest } = msg;
        // The desk says why it sent the listing (internal/server/changes.go, Reason). "follow" is the panel's own re-read: it
        // changes the listing and nothing else, so a git-settings question on screen and an action under way stay. "gitwarn"
        // is the tree sent with a question, which stays open (after a commit that went through and then asked about its push
        // it also says committed: true). Any other listing ends what the window had out.
        if (msg.reason === "follow") set({ changes: rest });
        else if (msg.reason === "gitwarn") set({ changes: rest, changesBusy: "" });
        else set({ changes: rest, changesBusy: "", gitWarn: null });
        break;
      }
      case "diff": {
        if (snap.diff && snap.diff.file === msg.file && snap.diff.cwd === String(msg.cwd || "")) set({ diff: { cwd: String(msg.cwd || ""), file: msg.file, text: String(msg.text || ""), error: String(msg.error || "") } });
        break;
      }
      case "gitWarn":
        set({ gitWarn: { ...msg, seq: ++noticeSeq }, changesBusy: "" });
        break;
      case "notice": {
        // Only while the panel has an action out is an error notice its own; an unrelated one is left to the notice bar.
        if (snap.changes && snap.changesBusy && msg.error !== undefined && !approvalOf(msg)) set({ changesNote: { text: String(msg.text || ""), error: !!msg.error }, changesBusy: "" });
        // A request for an answer is its own thing, not the latest notice: it
        // stays until the desktop withdraws it.
        const ask = approvalOf(msg);
        if (ask) {
          const rest = snap.approvals.filter((a) => a.id !== ask.id);
          const over = rest.length + 1 - MAX_APPROVALS;
          const kept = over > 0 ? rest.slice(over) : rest;
          set({
            approvals: [...kept, { ...ask, seq: ++approvalSeq }],
            ...(over > 0 ? { notice: { text: "Several baton requests are waiting for an answer; the oldest was put away and its agent will be told it timed out.", error: true, seq: ++noticeSeq } } : {}),
          });
          break;
        }
        // The refusal of a conversation command is shown on the tab, once. The
        // notice is kept as well, like any other.
        if (watchingConv !== null && snap.conversation && snap.conversation.supported === null && msg.error && (String(msg.text || "").startsWith(VIEWER_REFUSED_PHRASE) || String(msg.text || "").startsWith(REMOTE_CONVERSATION_LINE))) {
          set({ notice: { ...msg, seq: ++noticeSeq }, conversation: { ...snap.conversation, refused: String(msg.text) } });
          break;
        }
        set({ notice: { ...msg, seq: ++noticeSeq } });
        break;
      }
      default:
        break;
    }
  }

  return {
    start() { socket.start(); },
    stop() {
      if (watching !== null && watchSent()) socket.send({ cmd: "graphUnwatch" });
      if (watchingPane !== null && watchSent()) socket.send({ cmd: "structuredUnwatch", id: watchingPane });
      watching = null;
      watchingPane = null;
      if (watchingTeam !== null && watchSent()) socket.send({ cmd: "teamUnwatch" });
      watchingTeam = null;
      if (watchingChanges !== null && watchSent()) socket.send({ cmd: "changesUnwatch" });
      watchingChanges = null;
      if (watchingConv !== null && snap.hello) socket.send({ cmd: "conversationClose", id: watchingConv });
      watchingConv = null;
      socket.stop();
      snap = initial();
      notify();
    },
    getState() { return snap; },
    /** clearPair lets go of the last pairing link and join code, which the page
     *  would otherwise keep (url, QR, code) for its whole life. */
    clearPair() {
      if (snap.remotePair) set({ remotePair: null });
    },
    subscribe(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    /** send writes a command. It returns false when nothing was sent: the
     *  socket is not open, or the command is a graph command and this window is
     *  remote. */
    send(cmd) {
      // Until the hello says otherwise a window may be remote, so a desk-only command waits for it.
      if ((snap.remote || !snap.hello) && DESK_ONLY(cmd.cmd)) return false;
      // A code that takes another desktop into the account is made at the desk.
      if ((snap.remote || !snap.hello) && cmd.cmd === "remotePair" && cmd.kind === "host") return false;
      // Sending terminal output to TypeSafe is switched on at the desk; a relay window may switch it off.
      if ((snap.remote || !snap.hello) && cmd.cmd === "jevStatus" && cmd.kind === "on") return false;
      if (cmd.cmd === "permissionAnswer" && askedCards.has(cardKey(cmd))) return true;
      const sent = socket.send(cmd);
      if (sent && cmd.cmd === "permissionAnswer") {
        if (askedCards.size >= 50) askedCards.delete(askedCards.values().next().value);
        askedCards.add(cardKey(cmd));
        set({ asking: { id: cmd.id, cardSeq: cmd.cardSeq, requestId: cmd.requestId, after: answerSeq } });
      }
      if (sent && cmd.cmd === "listVersions" && snap.versions) set({ versions: null });
      if (sent && cmd.cmd === "helperPlan" && snap.helperPlan) set({ helperPlan: null });
      if (sent && cmd.cmd === "makeBaton" && snap.batonDraft) set({ batonDraft: null });
      if (sent && cmd.cmd === "structuredCancel" && cmd.req) {
        if (cancelPanes.size >= 20) cancelPanes.delete(cancelPanes.keys().next().value);
        cancelPanes.set(cmd.req, String(cmd.id || ""));
      }
      return sent;
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
    /** watchTeam asks for the task board and messages of a project, now and whenever
     *  they change. It replaces the project watched before, and returns false, asking
     *  for nothing, in a remote window. */
    watchTeam(root) {
      if (snap.remote) {
        set({ teamRefused: true });
        return false;
      }
      watchingTeam = String(root || "");
      set({ teamGone: null, teamRefused: false });
      if (snap.hello) sendTeamWatch();
      return true;
    },
    unwatchTeam() {
      if (watchingTeam !== null && watchSent()) socket.send({ cmd: "teamUnwatch" });
      watchingTeam = null;
      if (snap.team || snap.teamTasks || snap.teamMessages) set({ team: null, teamTasks: null, teamMessages: null });
    },
    /** watchChangesTeam asks for the branches of a project, now and whenever they change.
     *  It replaces the project watched before, and returns false, asking for nothing, in a remote window. */
    watchChangesTeam(root) {
      if (snap.remote) {
        set({ changesTeamRefused: true });
        return false;
      }
      const was = watchingChanges;
      watchingChanges = String(root || "");
      // Branches of another project are not drawn while the new ones are read.
      set({ changesTeamGone: null, changesTeamRefused: false, ...(was !== null && was !== watchingChanges ? { changesTeam: null } : {}) });
      if (snap.hello) sendChangesWatch();
      return true;
    },
    unwatchChangesTeam() {
      if (watchingChanges !== null && watchSent()) socket.send({ cmd: "changesUnwatch" });
      watchingChanges = null;
      if (snap.changesTeam) set({ changesTeam: null });
    },
    /** watchStructured asks for the structured messages of one pane, now and
     *  whenever it changes, for its permission card. It replaces the pane watched
     *  before, and returns false, asking for nothing, in a remote window. */
    watchStructured(id) {
      if (snap.remote || !id) return false;
      const was = watchingPane;
      if (was === id) return true;
      if (was !== null && watchSent()) socket.send({ cmd: "structuredUnwatch", id: was });
      watchingPane = id;
      set({ structured: null, structuredError: null });
      if (snap.hello) sendStructuredWatch();
      return true;
    },
    unwatchStructured() {
      if (watchingPane === null) return;
      const was = watchingPane;
      watchingPane = null;
      if (watchSent()) socket.send({ cmd: "structuredUnwatch", id: was });
      set({ structured: null, structuredError: null });
    },
    /** watchConversation asks for the conversation of one pane, now and as it
     *  grows, for the drawer's tab. It replaces the pane watched before (that one
     *  is closed) and does nothing for the pane already watched. */
    watchConversation(id) {
      if (!id || watchingConv === id) return false;
      if (watchingConv !== null && snap.hello) socket.send({ cmd: "conversationClose", id: watchingConv });
      watchingConv = id;
      set({ conversation: { id, supported: null, entries: [], atStart: false, older: "idle", refused: "" }, details: nullMap(), search: null });
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
    /** requestDetail asks for the full body of one entry of the open conversation:
     *  a tool's output or diff, a helper's own entries. It asks once for an entry
     *  already asked for (the answer, or "loading"), and again after "gone". It
     *  returns false when nothing was asked. */
    requestDetail(entry) {
      if (watchingConv === null || snap.remote || !entry || typeof entry !== "string") return false;
      const have = snap.details[entry];
      if (have && have.state !== "gone") return true;
      if (!socket.send({ cmd: "conversationDetail", id: watchingConv, entry })) return false;
      const items = nullMap(snap.details);
      delete items[entry];
      // Oldest asked for first, by the number each was given (ids may be numbers, which JS orders itself).
      const keys = Object.keys(items).sort((a, b) => items[a].n - items[b].n);
      while (keys.length >= MAX_DETAILS) delete items[keys.shift()];
      Object.defineProperty(items, entry, { value: { state: "loading", n: ++detailSeq }, enumerable: true, writable: true, configurable: true });
      set({ details: items });
      return true;
    },
    /** requestTask asks the desktop for one task of the project on screen: its
     *  text, owner, notes and branch. It returns false when nothing was asked
     *  (a window through the relay, no hello, no project, or the socket is down). */
    requestTask(id) {
      if (!id || typeof id !== "string" || snap.remote || !snap.hello || !snap.graph) return false;
      const req = "t" + ++reqSeq;
      if (!socket.send({ cmd: "taskGet", id, root: snap.graph.root, req })) return false;
      set({ taskDetail: { id, req, state: "loading", task: null, error: "" } });
      return true;
    },
    clearTask() {
      if (snap.taskDetail) set({ taskDetail: null });
    },
    /** requestChanges asks the desktop what has changed in the working tree at
     *  path (a pane's directory). follow is a re-read the panel makes itself. */
    requestChanges(path, follow = false) {
      if (!snap.hello) return false;
      const asked = String(path || "");
      if (!socket.send({ cmd: "changes", path: asked, follow: !!follow })) return false;
      changesAsked = asked;
      return true;
    },
    /** requestDiff asks for one file's diff in the tree last listed. */
    requestDiff(file, again = false) {
      if (!snap.changes || typeof file !== "string" || !file) return false;
      if (!socket.send({ cmd: "diff", path: snap.changes.cwd, text: file })) return false;
      // A re-read of the diff on screen keeps its text until the new one comes, so the view is not swapped for a loading line.
      const keep = again && snap.diff && snap.diff.file === file && snap.diff.cwd === snap.changes.cwd ? snap.diff.text : "";
      set({ diff: { cwd: snap.changes.cwd, file, text: keep, error: "", loading: true } });
      return true;
    },
    /** commitChanges commits the listing as it was last drawn. It sends the files
     *  and their stamps, so the desktop refuses a tree that has moved since. It
     *  returns "" when asked and otherwise the reason nothing was sent. */
    commitChanges(message, push = false) {
      const ch = snap.changes;
      const text = String(message || "").trim();
      if (!ch || ch.error) return "There is no working tree to commit";
      if (!text) return "A commit message is required";
      if (snap.changesBusy) return "Wait for the last action to finish";
      const stamps = nullMap();
      for (const f of ch.files || []) if (f.stamp) stamps[f.path] = f.stamp;
      const cmd = { cmd: "commit", path: ch.cwd, text, push: !!push, files: (ch.files || []).map((f) => f.path), omitted: ch.omitted || 0 };
      if (Object.keys(stamps).length) cmd.stamps = stamps;
      if (!socket.send(cmd)) return "Not connected to Flockdeck";
      pendingCommit = cmd;
      set({ changesBusy: push ? "commit and push" : "commit", changesNote: null });
      return "";
    },
    /** remoteChanges runs a push, pull or fetch in the tree last listed. */
    remoteChanges(action) {
      const ch = snap.changes;
      if (!ch || ch.error || snap.changesBusy) return false;
      let cmd;
      if (action === "push") cmd = { cmd: "gitPush", path: ch.cwd };
      else if (action === "pull") cmd = { cmd: "gitPull", path: ch.cwd };
      else if (action === "fetch") cmd = { cmd: "gitFetch", path: ch.cwd };
      else return false;
      if (!socket.send(cmd)) return false;
      set({ changesBusy: action, changesNote: null });
      return true;
    },
    /** answerGitWarn answers the desktop's question about a repository's programs:
     *  "once", "remember", or anything else to stop. The command is sent again
     *  with the answer and the scan that was shown, as the desktop asked. */
    answerGitWarn(accept) {
      const w = snap.gitWarn;
      if (!w) return false;
      set({ gitWarn: null });
      if (accept !== "once" && accept !== "remember") return false;
      const cmd = w.resend === "commit" && pendingCommit ? { ...pendingCommit } : { cmd: w.resend, path: w.path };
      cmd.gitAccept = accept;
      cmd.gitSeen = w.seen;
      if (!socket.send(cmd)) return false;
      set({ changesBusy: w.resend === "commit" ? "commit" : String(w.action || "") });
      return true;
    },
    clearChanges() {
      pendingCommit = null;
      if (snap.changes || snap.diff || snap.gitWarn || snap.changesBusy || snap.changesNote) set({ changes: null, diff: null, gitWarn: null, changesBusy: "", changesNote: null });
    },
    /** searchConversation asks the desktop to search the open conversation for
     *  text. An empty query clears the search. It returns false when nothing was
     *  asked. */
    searchConversation(q) {
      const text = String(q || "");
      if (text.trim() === "") {
        if (snap.search) set({ search: null });
        return false;
      }
      if (watchingConv === null || snap.remote || !socket.send({ cmd: "conversationSearch", id: watchingConv, q: text })) return false;
      set({ search: { q: text, state: "loading", matches: [], more: false } });
      return true;
    },
    clearSearch() {
      if (snap.search) set({ search: null });
    },
    unwatchConversation() {
      if (watchingConv === null) return;
      const was = watchingConv;
      watchingConv = null;
      if (snap.hello) socket.send({ cmd: "conversationClose", id: was });
      set({ conversation: null, details: nullMap(), search: null });
    },
    unwatchGraph() {
      if (watching === null) return;
      watching = null;
      if (watchSent()) socket.send({ cmd: "graphUnwatch" });
      set({ graph: null, graphGone: null, graphError: null });
    },
  };
}
