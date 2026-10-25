// The page's root component: the shell with the store behind it. The store's
// snapshot gives the shell its hello and state, the rail's choice of project
// goes to the store as the one graph watch, the main area is the home screen,
// and the drawer slot holds the one agent a click or key on a graph box opened.
// main.js makes the store and renders this; it is a component of its own so a
// test can render it on a fake socket.
//
// The strip along the top holds the manager bar, for the project whose graph is
// on screen. It is not one of the keyboard regions: it is reached with Tab like
// any control, and takes the keyboard once when the graph first names a manager.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { useStore } from "../lib/use-store.js";
import { Drawer, hasTerminal, useDrawer } from "../drawer/Drawer.js";
import { focusNode } from "../drawer/state.js";
import { ManagerBar, managerOf } from "../graph/ManagerBar.js";
import { Feed } from "../graph/Feed.js";
import { feedStep } from "../graph/FeedDiff.js";
import { nextNeedsYou, paneAt, prevNeedsYou } from "../graph/keys.js";
import { Shell, useMedia, useNarrow } from "./app.js";
import { FootButton } from "./rail.js";
import { teamOf } from "../lib/team.js";
import { Tree, totalOf } from "../graph/Tree.js";
import { Deps } from "../graph/DepsView.js";
import { Board, ChangesView, Replay, firstViewOf, viewsOf } from "./Views.js";
import { GoalLine } from "../graph/GoalLine.js";
import { TreeNotes } from "../graph/TreeNotes.js";
import { Home, sameProject, rosterOf } from "./Home.js";
import { Start } from "./Start.js";
import { Confirm } from "./Confirm.js";
import { LIST } from "./keys.js";
import { Updates, trapTab } from "./Updates.js";
import { GoalStrip, ViewBar } from "./GoalStrip.js";
import { SOLO, activeTabOf, archiveTab, canStep, goalTabsOf, moveTab, renameTab, restoreTab, scopeFeed, scopeModel, stepTab, tabsKeysOf, tabsStateOf } from "../graph/GoalTabs.js";
import { Settings } from "./Settings.js";
import { History } from "./History.js";
import { PaneInfo } from "./PaneInfo.js";
import { FindPane } from "./FindPane.js";
import { Helpers } from "./Helpers.js";
import { Team } from "./Team.js";
import { Worktrees } from "./Worktrees.js";
import { Agents } from "./Agents.js";
import { TEAM_RELAY_WORDS } from "../lib/team.js";
import { CLOSE_FINISHED_COMMAND, CLOSE_FINISHED_WORDS } from "../lib/agents.js";
import { Baton } from "./Baton.js";
import { Projects } from "./Projects.js";
import { Approvals } from "./Approvals.js";
import { useLifecycle } from "../drawer/Lifecycle.js";
import { useTranscripts } from "../drawer/Tools.js";
import { HISTORY_REMOTE_WORDS } from "../lib/history.js";
import { capsOf, hasCap } from "../lib/apiversion.js";
import { CHANGES_TEAM_CAP } from "../lib/changesteam.js";
import { DESK_FILES_WORDS, STRUCTURED_RECORD_WORDS, acknowledged, canTranscribe } from "../lib/paneinfo.js";
import { FONT_DEFAULT, FONT_MAX, FONT_MIN, PATCH_WAIT_MS, applyLook, prefsOf, reducedMotion, addPending, latestOf, settledPending, setting, termOptionsOf } from "../lib/prefs.js";
import { RELAY_WORDS, quitQuestion, updateOf } from "../lib/updates.js";
import { catalogOf, newTabCommand, tabsOf } from "../lib/lifecycle.js";
import { RELAY_WORDS as HELPER_RELAY_WORDS } from "../lib/helpers.js";
import { RELAY_WORDS as BATON_RELAY_WORDS, STILL_OUT_WORDS, createInflight, draftOf, makeCommand, savedWords } from "../lib/baton.js";
import { openOf, selectCommand } from "../lib/projects.js";
import { askPermission, noteOf, permissionOf, raise, showAttention, waitingCount, waitsOf, watchPresence } from "../lib/attention.js";
import { Toasts, addToasts, keepWaiting } from "./Toasts.js";
import { viewOf } from "../lib/remote.js";
import { isViewer, VIEWER_WORDS } from "../lib/role.js";

// What the shell is given before the first hello and state. Constants, so the
// shell does not take them for a change on every render.
const NO_HELLO = Object.freeze({ keys: Object.freeze([]) });
const NO_STATE = Object.freeze({ root: "", projects: Object.freeze([]) });
const NO_PANES = new Set();

/** OPEN_AN_AGENT_WORDS is what a key that acts on the agent in the drawer says
 *  when no agent is open. */
export const OPEN_AN_AGENT_WORDS = "Open an agent first, then use this on it.";

/** NO_TERMINAL_WORDS and SHELL_RECORD_WORDS say why an action does nothing for
 *  the pane in the drawer. */
export const NO_TERMINAL_WORDS = "This pane has no terminal to search. A structured pane keeps its output in the drawer.";
export const NO_TREE_WORDS = "This pane has no working folder, so it has no changes to show.";
export const SHELL_RECORD_WORDS = "A shell reports no agent events, so it has nothing to record or export.";

/** nodeOfPane is a node for the drawer from the state push's entry for a pane, for
 *  a window with no graph: what the push says of the agent and nothing it does
 *  not (no branch, task or live line). */
function nodeOfPane(p) {
  const id = String(p.id || "");
  return {
    pane: id, kind: p.structured ? "structured" : "terminal", structured: !!p.structured,
    name: String(p.name || id), label: String(p.label || ""), labelAuto: p.labelAuto === true, role: String(p.role || ""), agent: String(p.agent || ""), shell: !!p.shell,
    status: String(p.status || "unknown"), activity: String(p.activity || ""), since: "", live: "", branch: "",
    dirty: 0, untracked: 0, ahead: 0, behind: 0, task: null, unread: 0,
    waitingOn: { kind: p.permission || (p.ask && p.ask.questions) ? "user" : "none", text: "", ref: "" },
  };
}

/** Live draws the shell for a store. matchMedia and window are the page's,
 *  passed for a test, and terminalOptions reach the drawer's terminal (a test
 *  gives it fakes for xterm and the socket). */
export function Live({ store, matchMedia, window: win, terminalOptions = null, patchWaitMs = PATCH_WAIT_MS, treeView = Tree }) {
  // The Team view is the outline, a component given here: it is drawn with { model,
  // open, onOpen, send } in the home column, beside the activity feed. With none
  // the switch has no Team view and the page starts on the Board.
  const hasTree = !!treeView;
  const views = viewsOf(hasTree);
  const narrow = useNarrow(matchMedia);
  const snap = useStore(store);
  const [selected, setSelected] = useState("");
  const [shown, setShown] = useState(null);
  // The start dialog ("agent", "team" or none), a notice of this page's own, the
  // notice of the store that was dismissed, and the pane a start asked to open.
  const [starting, setStarting] = useState("");
  const [local, setLocal] = useState("");
  const [dismissed, setDismissed] = useState(0);
  const [opening, setOpening] = useState("");
  // The settings dialog, and the changes made in it that the desktop has not
  // answered yet (by preference, as the desktop will keep them).
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsSection, setSettingsSection] = useState("general");
  const [settingsAt, setSettingsAt] = useState(0);
  // A binding is being recorded in the Keyboard section: the shell leaves keys alone.
  const [recording, setRecording] = useState(false);
  const [patch, setPatch] = useState({});
  // The updates dialog (with the seq of the notice when it opened, so it shows
  // only newer ones) and the question before quit.
  const [updatesAt, setUpdatesAt] = useState(-1);
  const [quitAsk, setQuitAsk] = useState(false);
  // The dialogs of L4: the stored conversations, one pane's info (with the answer
  // and notice numbers it started after) and the search over every pane. findAsk
  // grows each time Find in terminal is asked for.
  const [historyOpen, setHistoryOpen] = useState(false);
  const [infoFor, setInfoFor] = useState(null);
  const [findingPane, setFindingPane] = useState(false);
  const [findAsk, setFindAsk] = useState(0);
  // The view the strip's switch has chosen: tree, deps, board, changes or replay.
  const [view, setView] = useState(firstViewOf(hasTree));
  const [tabAsk, setTabAsk] = useState({ tab: "", n: 0 });
  // The helper apps and projects dialogs, and the baton being made: the pane it
  // is for, the name to show, and the request that asked for its draft.
  const [helpersOpen, setHelpersOpen] = useState(false);
  const [projectsOpen, setProjectsOpen] = useState(false);
  const [baton, setBaton] = useState(null);
  // The Team dialog (the task board and messages) and the Agents dialog (every pane
  // in every open project).
  const [teamOpen, setTeamOpen] = useState(false);
  const [agentsOpen, setAgentsOpen] = useState(false);
  // The question before every finished pane is closed.
  const [closeAsk, setCloseAsk] = useState(false);
  const [worktreesOpen, setWorktreesOpen] = useState(false);
  // The requests for an answer put away in this window.
  const [putAway, setPutAway] = useState(() => new Set());
  // The baton commands sent and not answered. A command stays here after the
  // dialog was closed, until the desktop answers or the connection drops, so the
  // same one is not sent again meanwhile.
  const inflight = useRef(null);
  if (!inflight.current) inflight.current = createInflight();
  // The store empties its hello and state when the socket drops. The rail and
  // the project chosen in it are kept through that, so a reconnect does not clear
  // the choice and ask for the graph a second time.
  const kept = useRef({ hello: NO_HELLO, state: NO_STATE });
  if (snap.hello) kept.current.hello = snap.hello;
  if (snap.state) kept.current.state = snap.state;
  // The rail's choice, and the first project the window has. In a window reached
  // through the relay the store refuses the watch and says so in the snapshot.
  const onWatch = (root) => {
    setSelected(root);
    store.watchGraph(root);
    // A project chosen in the rail is the desk's active one too, so the old
    // window and the next start agree. The desk's own choice is not sent back.
    const deskRoot = kept.current.state.root;
    if (deskRoot && root !== deskRoot && !store.getState().remote) store.send(selectCommand(root));
  };
  // A store that was stopped (the page went to the back-forward cache) and is
  // started again has no watch. The project already chosen is asked for once
  // more when the connection comes back, since the shell does not choose it twice.
  const last = useRef(snap.connection);
  useEffect(() => {
    if (last.current === "idle" && snap.connection !== "idle" && selected) store.watchGraph(selected);
    last.current = snap.connection;
  }, [snap.connection]);

  // The picture on screen, for the project the window is on. Home reports it
  // from an effect, so for one render after a switch it is still the old
  // project's: nothing is bound to it until it is this project's.
  const current = shown && selected && sameProject(shown.root, selected) ? shown : null;

  const mainRef = useRef(null);
  // A window the graph is not sent to has the agents of the state push, and its
  // drawer opens on one of those.
  const statePanes = kept.current.state.panes;
  const rosterOnly = snap.graphRefused && !current;
  const paneIds = useMemo(() => {
    if (rosterOnly) return new Set(rosterOf(kept.current.state).map((r) => r.id));
    return shown ? new Set(shown.nodes.map((n) => n.pane)) : NO_PANES;
  }, [shown, rosterOnly, statePanes]);
  // The graph's agents in the order it lists them, for [ and ] in the drawer.
  const order = useMemo(() => (rosterOnly ? rosterOf(kept.current.state).map((r) => r.id) : shown ? shown.nodes.map((n) => n.pane) : []), [shown, rosterOnly, statePanes]);
  const drawer = useDrawer({ mainRef, paneIds });
  const pane = drawer.pane ? (statePanes || {})[drawer.pane] || null : null;
  let node = drawer.pane && current ? current.byPane.get(drawer.pane) : null;
  // The activity feed: what changed between one graph push and the next, kept in
  // this page only. A push for another project starts it again.
  const [feed, setFeed] = useState(null);
  const feedFrom = useRef(null);
  useEffect(() => {
    if (!shown) return;
    const was = feedFrom.current;
    feedFrom.current = shown;
    if (was === shown) return;
    if (was && was.root !== shown.root) setFeed(null);
    else setFeed((f) => feedStep(f, was, shown, Date.now()));
  }, [shown]);
  if (!node && rosterOnly && pane) node = nodeOfPane(pane);

  // Restart, lock and close, for the agent in the drawer. The question before a
  // close or restart belongs to the pane it was asked for.
  const send = (c) => store.send(c);
  const life = useLifecycle(send);
  const tx = useTranscripts(send, acknowledged(kept.current.hello.prefs));
  useEffect(() => {
    life.cancel();
    tx.cancel();
  }, [drawer.pane]);

  // An agent started from the dialog is opened in the drawer once the graph
  // has it. The desktop answers startAgent with the pane it made.
  const startedSeq = snap.started ? snap.started.seq : 0;
  useEffect(() => {
    if (snap.started && snap.started.paneId) setOpening(snap.started.paneId);
  }, [startedSeq]);
  useEffect(() => {
    if (opening && paneIds.has(opening)) {
      reveal(opening);
      drawer.open(opening);
      setOpening("");
    }
  }, [opening, paneIds]);

  const state = kept.current.state;
  const catalog = useMemo(() => catalogOf(state), [state]);
  const tabs = useMemo(() => tabsOf(state), [state]);
  const project = (state.projects || []).find((p) => p.root === selected) || null;
  const onDesk = !!snap.state && sameProject(state.root, selected);
  const noticeSeq = snap.notice ? snap.notice.seq : 0;
  const noticeText = local || (snap.notice && noticeSeq > dismissed ? String(snap.notice.text || "") : "");
  const say = (text) => setLocal(text);
  const viewer = isViewer(snap);

  // The settings: the desktop's, with this window's own changes over them until
  // the desktop answers. A change it refuses (a viewer's) is answered with a
  // notice, which ends the wait and puts the window back to what is kept.
  const page = win || (typeof window !== "undefined" ? window : null);
  const kept_prefs = kept.current.hello.prefs;
  const lastPrefs = useRef(kept_prefs);
  useEffect(() => {
    const before = lastPrefs.current;
    lastPrefs.current = kept_prefs;
    setPatch((p) => settledPending(p, kept_prefs, before));
  }, [kept_prefs]);
  // Only a refusal ends the wait: any other notice ("Agent finished") is about
  // something else, and the change goes on waiting for its answer.
  useEffect(() => {
    if (snap.notice && snap.notice.error) setPatch({});
  }, [noticeSeq]);
  // A change nothing ever answers (another window set the same setting to
  // something else) does not stay on screen for good.
  useEffect(() => {
    if (Object.keys(patch).length === 0) return undefined;
    const t = setTimeout(() => setPatch({}), patchWaitMs);
    return () => clearTimeout(t);
  }, [patch]);
  const prefs = useMemo(() => prefsOf({ ...(kept_prefs || {}), ...latestOf(patch) }), [kept_prefs, patch]);
  useEffect(() => {
    applyLook(page, prefs);
  }, [page, prefs.theme, prefs.accent]);
  const reduced = reducedMotion(page);
  const look = useMemo(() => termOptionsOf(prefs, reduced), [prefs, reduced]);
  const termProps = useMemo(
    () => ({ ...(terminalOptions || {}), termOptions: { ...look, ...((terminalOptions && terminalOptions.termOptions) || {}) } }),
    [terminalOptions, look],
  );
  // change sends one setting and shows it now. It answers whether the value
  // was one the desktop would keep.
  const change = (name, value) => {
    if (viewer) {
      say(VIEWER_WORDS);
      return false;
    }
    const s = setting(name, value, { ...(kept_prefs || {}), ...latestOf(patch) }, capsOf(kept.current.hello));
    if (!s) return false;
    send(s.command);
    setPatch((p) => addPending(p, s.patch));
    return true;
  };
  // The goal tabs: one for each team of the graph, then Solo. What the person chose
  // (names, order, archived goals) is kept in the interface's preferences. A window
  // with nowhere to keep it (a relay window, a desktop without uiPrefs) holds it for
  // the life of the page.
  const [chosenTab, setChosenTab] = useState("");
  const [localTabs, setLocalTabs] = useState(null);
  // The drawer's width is kept in the preferences; where they cannot keep it (a relay
  // window, a desktop without uiPrefs) it is held for the life of the page.
  const [localDrawerW, setLocalDrawerW] = useState(null);
  const [localDrawerH, setLocalDrawerH] = useState(null);
  const [localOrient, setLocalOrient] = useState(null);
  // The drawer goes below the main area in a window taller than it is wide, until the person chooses.
  const portrait = useMedia(matchMedia, "(orientation: portrait)");
  const pickedOrient = localOrient || prefs.orient;
  const orient = pickedOrient === "auto" ? (portrait ? "stack" : "side") : pickedOrient;
  const setDrawerH = (px) => {
    if (!snap.remote && change("drawerH", px)) setLocalDrawerH(null);
    else setLocalDrawerH(px);
  };
  const toggleLayout = () => {
    const to = orient === "stack" ? "side" : "stack";
    if (!snap.remote && change("orient", to)) setLocalOrient(null);
    else setLocalOrient(to);
  };
  const setDrawerW = (px) => {
    if (!snap.remote && change("drawerW", px)) setLocalDrawerW(null);
    else setLocalDrawerW(px);
  };
  const teamTasks = useMemo(() => { const t = snap.remote ? null : teamOf(snap.team); return t ? t.tasks : null; }, [snap.team, snap.remote]);
  const savedTabs = useMemo(() => localTabs || tabsStateOf(prefs.ui), [localTabs, prefs.ui]);
  const goal = useMemo(() => goalTabsOf(current, savedTabs), [current, savedTabs]);
  const activeTab = activeTabOf(goal.tabs, chosenTab);
  const scoped = useMemo(() => scopeModel(current, activeTab), [current, activeTab]);
  const scopedFeed = useMemo(() => scopeFeed(feed, scoped), [feed, scoped]);
  const feedCount = scopedFeed && scopedFeed.entries ? scopedFeed.entries.length : 0;
  const saveTabs = (next) => {
    if (next === savedTabs) return;
    // One command with a key for each choice that changed, so another window's other choices are left alone.
    const writes = tabsKeysOf(next, current, prefs.ui);
    if (!snap.remote && (Object.keys(writes).length === 0 || change("tabsKeys", writes))) setLocalTabs(null);
    else setLocalTabs(next);
  };
  // Choosing a goal tab shows that goal's Team view, unless the page is on a view that
  // follows the tab (the outline and Dependencies): the others are project-wide, so a
  // click there would change nothing the person can see.
  const showTab = (id) => {
    setChosenTab(id);
    if (view !== "tree" && view !== "deps") setView("tree");
  };
  // The tab that holds a pane: its goal, else Solo; "" for a pane the graph does not have.
  const paneTab = (pane) => {
    if (!current || !current.byPane.has(pane)) return "";
    const team = current.teams.find((t) => t.members.includes(pane));
    return team ? team.id : SOLO;
  };
  // reveal opens the tab that holds a pane (bringing an archived goal back) so that
  // what is done to the pane can be seen.
  const reveal = (pane) => {
    const id = paneTab(pane);
    if (!id) return;
    if (goal.tabs.some((t) => t.id === id)) showTab(id);
    else tabDo.restore(id);
  };
  // goTo moves the keyboard to a pane's row or box, once the tab that holds it is drawn.
  const [focusAsk, setFocusAsk] = useState("");
  const goTo = (pane) => {
    reveal(pane);
    setFocusAsk(pane);
  };
  useEffect(() => {
    if (focusAsk && scoped && scoped.byPane.has(focusAsk) && view === "tree") {
      focusNode(mainRef.current, focusAsk);
      setFocusAsk("");
    }
  }, [focusAsk, scoped, view]);
  // A tab or a shell the "+" menu or a key opens has no answer of its own: the next
  // pane the desktop lists is the one, and it is opened in the drawer.
  const newWatch = useRef(null);
  const newWatchTimer = useRef(0);
  const startTab = (cmd) => {
    newWatch.current = new Set(Object.keys(kept.current.state.panes || {}));
    // One timer: a second start inside the window takes over the first one's watch.
    clearTimeout(newWatchTimer.current);
    newWatchTimer.current = setTimeout(() => { newWatch.current = null; }, 15000);
    send(cmd);
  };
  useEffect(() => {
    const seen = newWatch.current;
    if (!seen || !statePanes) return;
    const fresh = Object.keys(statePanes).find((id) => !seen.has(id));
    if (fresh) {
      newWatch.current = null;
      setOpening(fresh);
    }
  }, [statePanes]);
  const tabDo = {
    rename: (id, text) => saveTabs(renameTab(savedTabs, id, text)),
    archive: (id) => {
      saveTabs(archiveTab(savedTabs, id));
      say("Archived. Show it again from the + menu.");
    },
    restore: (id) => { saveTabs(restoreTab(savedTabs, id)); showTab(id); },
    move: (id, to) => saveTabs(moveTab(savedTabs, goal.tabs, id, to)),
    step: (id, dir) => saveTabs(stepTab(savedTabs, goal.tabs, id, dir)),
  };
  // The settings dialog opens on a section. What it says of a save or a refusal is
  // the notice that came after it opened, or this page's own.
  const openSettings = (section) => {
    setSettingsSection(section);
    setSettingsAt(noticeSeq);
    setSettingsOpen(true);
  };
  // The browser's leave to raise notifications, asked for once when the person first
  // does something (a browser answers a request made without a gesture with no),
  // and again from the switch or the button in Notifications.
  const [permission, setPermission] = useState(() => permissionOf(page));
  const ask = () => askPermission(page).then(setPermission);
  const wantNotes = !prefs.notificationsOff;
  useEffect(() => {
    const doc = page && page.document;
    if (!wantNotes || permission !== "default" || !doc || !doc.addEventListener) return undefined;
    const once = () => {
      stop();
      ask();
    };
    const stop = () => {
      doc.removeEventListener("pointerdown", once, true);
      doc.removeEventListener("keydown", once, true);
    };
    doc.addEventListener("pointerdown", once, true);
    doc.addEventListener("keydown", once, true);
    return stop;
  }, [page, wantNotes, permission]);
  // The desktop is told the person is at this computer, from real input, so a
  // phone is not told of a wait the person can see. A window reached through the
  // relay is not the desk, and says nothing.
  useEffect(() => {
    if (snap.remote || !snap.hello) return undefined;
    return watchPresence(page, (c) => store.send(c));
  }, [page, snap.remote, !!snap.hello]);
  // A notification when an agent newly needs the person and this window is behind
  // another. The first state seen is only remembered, never announced.
  const waits = useRef(new Map());
  const lastNote = useRef(null);
  const [toasts, setToasts] = useState([]);
  // The window title and the tab icon say how many agents wait, on every change.
  useEffect(() => {
    showAttention(page && page.document, waitingCount(kept.current.state.panes));
  }, [statePanes]);
  useEffect(() => {
    const panes = kept.current.state.panes;
    const { fresh, next } = waitsOf(waits.current, panes);
    waits.current = next;
    const waiting = new Set([...next].filter(([, v]) => v === "waiting" || v === "blocked").map(([id]) => id));
    setToasts((cur) => keepWaiting(cur, waiting));
    const nothing = ![...next.values()].some((v) => v === "waiting" || v === "blocked");
    if (nothing && lastNote.current) {
      // Answered from here or another window: the notification is stale.
      try { lastNote.current.close(); } catch { /* already gone */ }
      lastNote.current = null;
    }
    if (!fresh.length || prefs.notificationsOff) return;
    const doc = page && page.document;
    if (doc && (doc.hidden === false || doc.hidden === undefined) && typeof doc.hasFocus === "function" && doc.hasFocus()) {
      // In front: the browser raises nothing, so the page says it itself.
      setToasts((cur) => addToasts(cur, fresh, drawer.pane));
      return;
    }
    // A second notification replaces the first: the first is closed, not left on screen.
    if (lastNote.current) { try { lastNote.current.close(); } catch { /* already gone */ } }
    lastNote.current = raise(page, noteOf(fresh), (id) => drawer.open(id));
  }, [statePanes]);
  const bumpFont = (to) => {
    if (to < FONT_MIN || to > FONT_MAX) return;
    if (change("fontSize", to)) say("Font size " + to + "px");
  };
  // copy puts a value on the clipboard and says what it copied.
  const copy = (text, what) => {
    const failed = () => say("The clipboard could not be reached, so nothing was copied");
    try {
      const clip = page && page.navigator && page.navigator.clipboard;
      if (!clip || !clip.writeText) {
        failed();
        return;
      }
      clip.writeText(text).then(() => say("Copied " + what), failed);
    } catch {
      failed();
    }
  };
  const openInfo = () => {
    if (!drawer.pane) {
      say(OPEN_AN_AGENT_WORDS);
      return;
    }
    if (snap.remote) {
      say(DESK_FILES_WORDS);
      return;
    }
    setInfoFor({ pane: drawer.pane, floor: snap.paneInfo ? snap.paneInfo.seq : 0, noticeAt: noticeSeq });
    send({ cmd: "paneInfo", id: drawer.pane });
  };
  // Once the desktop has detached this window the page closes itself, or says it
  // can be closed when the browser will not let a script do it.
  useEffect(() => {
    if (!snap.detached) return;
    const w = win || (typeof window !== "undefined" ? window : null);
    if (w && w.close) w.close();
    setLocal("Flockdeck has detached from this window and the agents keep running. You can close this tab.");
  }, [snap.detached]);
  const update = updateOf(snap.state, snap.remote);
  // A notice from the desk replaces one of this page's own.
  useEffect(() => {
    if (noticeSeq) setLocal("");
  }, [noticeSeq]);

  // The manager of the project on screen, from the picture Home draws: a push
  // that cannot be read leaves the last good one, and another project's does not
  // count. Before any graph there is no bar, so "no manager" is not said while
  // the graph is still being read.
  const barRef = useRef(null);
  // The pane whose Conversation tab shows its own message box: for the manager, that box is the bar, so the bar steps aside.
  const [boxPane, setBoxPane] = useState("");
  const boxFocus = useRef(null);
  const onBox = (pane, focus) => {
    boxFocus.current = focus || null;
    setBoxPane(pane);
  };
  // The feed is for a team with a manager, and gives its place to the drawer while that is open.
  const showFeed = !!current && !snap.graphGone && !node && current.teams.length > 0;
  // What the outline's decision blocks answer with. A relayed view has no store to answer with.
  const decisionCtx = current
    ? {
        snap,
        store: snap.remote || viewer ? null : store,
        panes: new Map(Object.entries(statePanes || {})),
        manager: managerOf(current),
        onOpen: drawer.open,
        onSee: (p) => { drawer.open(p); setTabAsk((a) => ({ tab: "changes", n: a.n + 1, pane: p })); },
        onRestart: snap.remote || viewer ? null : (p) => { const n = current.byPane.get(p); if (n) life.begin("restart", n, (statePanes || {})[p] || null); },
      }
    : null;
  // The bar stays mounted while the drawer's box stands in for it, so a draft or an answer on the way is not lost.
  const standIn = !!(current && managerOf(current) && boxPane && managerOf(current).pane === boxPane);
  const focusDrawerBox = () => { if (boxFocus.current) boxFocus.current(); };
  useLayoutEffect(() => {
    const el = barRef.current;
    const doc = el && el.ownerDocument;
    if (!standIn || !el || !doc || doc.activeElement !== el) return;
    focusDrawerBox();
  }, [standIn]);
  const bar = current && !snap.graphGone && managerOf(current)
    ? html`<${ManagerBar} snap=${snap} manager=${managerOf(current)} send=${(c) => store.send(c)} inputRef=${barRef} lead=${true} hidden=${standIn} />`
    : null;

  // The graph's two keys that no arrow does. The next agent that needs the person
  // goes on from the box the keyboard is on, or from the open drawer's pane, and
  // the manager bar takes the keyboard where it is.
  const needPane = (fn) => () => {
    if (!drawer.pane || !node) {
      say(OPEN_AN_AGENT_WORDS);
      return;
    }
    fn();
  };
  // The Team dialog keeps a watch on the project chosen in the rail while it is open.
  useEffect(() => {
    if (!teamOpen && view !== "board" && view !== "deps") return undefined;
    store.watchTeam(selected);
    return () => store.unwatchTeam();
  }, [teamOpen, view === "board" || view === "deps", selected]);
  // The team-wide Changes view keeps a watch on the project chosen in the rail while it is shown and the desktop offers it.
  const changesTeam = view === "changes" && hasCap(snap.hello, CHANGES_TEAM_CAP) && !snap.remote && !viewer;
  useEffect(() => {
    if (!changesTeam) return undefined;
    store.watchChangesTeam(selected);
    return () => store.unwatchChangesTeam();
  }, [changesTeam, selected]);
  // The desk keeps one focused pane in each tab. A pane opened in the drawer is
  // the one the person is working in, so the desk is told, and its own window and
  // the commands that act on the focused pane agree with this one.
  useEffect(() => {
    if (!drawer.pane || snap.remote || !onDesk) return;
    const t = tabs.find((x) => x.active);
    if (t && t.focus !== drawer.pane && t.panes.includes(drawer.pane)) send({ cmd: "focusPane", id: drawer.pane });
  }, [drawer.pane]);
  // The agent lists are asked for again when the person opens something that
  // chooses from them: a program installed while this window was open is what they
  // are about to look for.
  useEffect(() => {
    if (starting && !snap.remote) send({ cmd: "refreshAgents" });
  }, [starting]);
  useEffect(() => {
    if (settingsOpen && (settingsSection === "routing") && !snap.remote) send({ cmd: "refreshAgents" });
  }, [settingsOpen, settingsSection]);
  const chooseView = (v) => setView(v);
  // What the command palette lists besides the key table's actions: what needs the
  // person (choosing one opens that agent), every agent of the graph, and the views.
  // None of them answers anything: a permission is answered on its card.
  // Rebuilt each render: it reads the tab set, the views and the drawer through closures, and it is cheap.
  const paletteItems = (() => {
    const items = [];
    if (current) {
      const nameOf = (id) => (current.byPane.get(id) && current.byPane.get(id).name) || id;
      for (const n of current.needs) {
        if (!n.pane || !current.byPane.has(n.pane)) continue;
        items.push({ id: "need-" + n.id, group: "Needs you", name: "Open " + nameOf(n.pane) + ": " + n.label, detail: (n.text || "").split("\n")[0].slice(0, 40), run: () => { reveal(n.pane); drawer.open(n.pane); } });
      }
      for (const n of current.nodes) {
        items.push({ id: "agent-" + n.pane, group: "Agents", name: "Open " + (n.name || n.pane), detail: [n.role, n.status].filter(Boolean).join(" · "), run: () => { reveal(n.pane); drawer.open(n.pane); } });
      }
    }
    items.push({ id: "layout", group: "Views", name: "Layout: stack / side by side", detail: orient === "stack" ? "now below" : "now beside", run: toggleLayout });
    for (const [id, name] of views) items.push({ id: "view-" + id, group: "Views", name: "Show " + name, detail: "", run: () => chooseView(id) });
    return items;
  })();
  // step is nextNeedsYou or prevNeedsYou. The pane the keyboard is on, or the open drawer's, is where it starts.
  const jumpNeedsYou = (step) => {
    if (!current || snap.graphGone) return;
    const doc = (win || (typeof window !== "undefined" ? window : null) || {}).document;
    const at = (doc && paneAt(doc.activeElement)) || drawer.pane;
    const to = step(current, at);
    if (!to) return;
    goTo(to);
    drawer.open(to, "key");
  };
  const helperNames = useMemo(() => (current ? current.nodes.map((n) => ({ pane: n.pane, name: n.name })) : []), [current]);
  // Rebuilt each render, as the palette items are: the Shell keeps the latest in a ref.
  const actions = ({
    // The jump lands on the agent's box and opens its drawer, where the prompt is.
    nextNeedsYou: () => jumpNeedsYou(nextNeedsYou),
    prevNeedsYou: () => jumpNeedsYou(prevNeedsYou),
    managerBar: () => {
      if (standIn) {
        focusDrawerBox();
        return;
      }
      const el = barRef.current;
      if (el && el.focus) el.focus();
    },
    settings: () => openSettings("general"),
    apiKeys: () => (viewer ? say(VIEWER_WORDS) : openSettings("keys")),
    remote: () => (viewer ? say(VIEWER_WORDS) : openSettings("remote")),
    helpers: () => (snap.remote ? say(HELPER_RELAY_WORDS) : viewer ? say(VIEWER_WORDS) : setHelpersOpen(true)),
    team: () => (snap.remote ? say(TEAM_RELAY_WORDS) : viewer ? say(VIEWER_WORDS) : setTeamOpen(true)),
    agents: () => (viewer ? say(VIEWER_WORDS) : setAgentsOpen(true)),
    worktrees: () => (viewer ? say(VIEWER_WORDS) : setWorktreesOpen(true)),
    makeBaton: needPane(() => {
      if (viewer) return say(VIEWER_WORDS);
      if (snap.remote) return say(BATON_RELAY_WORDS);
      const req = inflight.current.next();
      inflight.current.add(req, "makeBaton", drawer.pane);
      if (store.send(makeCommand(drawer.pane, req)) === false) inflight.current.delete(req);
      else setBaton({ pane: drawer.pane, name: node.name || drawer.pane, req });
      return undefined;
    }),
    fontUp: () => bumpFont(prefs.fontSize + 1),
    fontDown: () => bumpFont(prefs.fontSize - 1),
    fontReset: () => bumpFont(FONT_DEFAULT),
    newAgentTab: () => (viewer ? say(VIEWER_WORDS) : startTab(newTabCommand({ kind: "agent" }))),
    newAgentTabChoose: () => (viewer ? say(VIEWER_WORDS) : setStarting("agent")),
    newShellTab: () => (viewer ? say(VIEWER_WORDS) : startTab(newTabCommand({ kind: "shell" }))),
    startTeam: () => (viewer ? say(VIEWER_WORDS) : setStarting("team")),
    selectTab: (n) => {
      const t = goal.tabs[(n || 0) - 1];
      if (!t) return say("There is no tab " + n + ".");
      showTab(t.id);
      return undefined;
    },
    closeFinishedPanes: () => (viewer ? say(VIEWER_WORDS) : setCloseAsk(true)),
    nextTab: () => showTab(goal.tabs[(goal.tabs.findIndex((t) => t.id === activeTab) + 1) % goal.tabs.length].id),
    prevTab: () => showTab(goal.tabs[(goal.tabs.findIndex((t) => t.id === activeTab) - 1 + goal.tabs.length) % goal.tabs.length].id),
    moveTabLeft: () => tabDo.step(activeTab, -1),
    moveTabRight: () => tabDo.step(activeTab, 1),
    detach: () => (snap.remote ? say(RELAY_WORDS) : viewer ? say(VIEWER_WORDS) : send({ cmd: "detach" })),
    quit: () => (snap.remote ? say(RELAY_WORDS) : viewer ? say(VIEWER_WORDS) : setQuitAsk(true)),
    history: () => (snap.remote ? say(HISTORY_REMOTE_WORDS) : viewer ? say(VIEWER_WORDS) : setHistoryOpen(true)),
    findPane: () => (snap.remote ? say(DESK_FILES_WORDS) : setFindingPane(true)),
    paneInfo: openInfo,
    changes: needPane(() => (pane && pane.cwd ? setTabAsk((a) => ({ tab: "changes", n: a.n + 1 })) : say(NO_TREE_WORDS))),
    findInTerminal: needPane(() => (hasTerminal(node, pane) ? setFindAsk((n) => n + 1) : say(NO_TERMINAL_WORDS))),
    recordPane: needPane(() => {
      if (viewer) say(VIEWER_WORDS);
      else if (canTranscribe(node, pane)) tx.record(node.pane, !(pane && pane.recording));
      else say(node.shell || (pane && pane.shell) ? SHELL_RECORD_WORDS : STRUCTURED_RECORD_WORDS);
    }),
    exportTranscript: needPane(() => {
      if (viewer) say(VIEWER_WORDS);
      else if (snap.remote) say(DESK_FILES_WORDS);
      else if (canTranscribe(node, pane)) tx.exportTranscript(node.pane);
      else say(node.shell || (pane && pane.shell) ? SHELL_RECORD_WORDS : STRUCTURED_RECORD_WORDS);
    }),
    revealTranscript: needPane(() => (viewer ? say(VIEWER_WORDS) : snap.remote ? say(DESK_FILES_WORDS) : send({ cmd: "revealTranscript", id: node.pane }))),
    openRecordings: () => (viewer ? say(VIEWER_WORDS) : snap.remote ? say(DESK_FILES_WORDS) : send({ cmd: "openRecordings" })),
    zoomPane: needPane(() => drawer.toggleMax()),
    restartPane: needPane(() => (viewer ? say(VIEWER_WORDS) : life.begin("restart", node, pane))),
    lockPane: needPane(() => (viewer ? say(VIEWER_WORDS) : life.toggleLock(node.pane, !(pane && pane.locked)))),
    closePane: needPane(() => (viewer ? say(VIEWER_WORDS) : pane && pane.locked ? say("This pane is locked. Unlock it to close it.") : life.begin("close", node, pane))),
  });

  // The baton answers. A draft is for the dialog whose request it carries. A
  // saved or refused command is taken off the list of those out, and a saved one
  // is said; the dialog reads the same answer to close or to be given back.
  const draft = useMemo(() => {
    const d = snap.batonDraft ? draftOf(snap.batonDraft) : null;
    return d && baton && d.req === baton.req ? d : null;
  }, [snap.batonDraft, baton]);
  const batonSeq = Math.max(snap.batonSaved ? snap.batonSaved.seq : 0, snap.batonError ? snap.batonError.seq : 0);
  const batonAnswer = useMemo(() => {
    const a = snap.batonSaved && snap.batonSaved.seq === batonSeq ? { ...snap.batonSaved, ok: true } : snap.batonError && snap.batonError.seq === batonSeq ? { ...snap.batonError, ok: false } : null;
    return a && typeof a.req === "string" ? a : null;
  }, [batonSeq]);
  useEffect(() => {
    if (!batonAnswer) return;
    const was = inflight.current.get(batonAnswer.req);
    if (!was) return;
    inflight.current.delete(batonAnswer.req);
    if (batonAnswer.ok) say(savedWords(batonAnswer));
    // A draft that could not be made leaves nothing to edit.
    else if (was.cmd === "makeBaton") setBaton((b) => (b && b.req === batonAnswer.req ? null : b));
  }, [batonAnswer]);
  // A command out when the connection went may or may not have been done, and its
  // answer would have come down the connection that has gone.
  useEffect(() => {
    if (snap.connection === "open" || inflight.current.size() === 0) return;
    inflight.current.clear();
    say("The connection to Flockdeck dropped while a baton command was out. It may have finished; check flockdeck baton list before trying again.");
  }, [snap.connection]);
  const runBaton = (build) => {
    const req = inflight.current.next();
    const cmd = build(req);
    if (inflight.current.same(cmd.cmd, cmd.id)) {
      say(STILL_OUT_WORDS);
      return "";
    }
    if (store.send(cmd) === false) return "";
    inflight.current.add(req, cmd.cmd, cmd.id);
    return req;
  };

  const settingsCtx = {
    send,
    keys: snap.keys,
    jev: snap.jevKey,
    agents: kept.current.state.agents || null,
    panes: current ? current.nodes.filter((n) => !n.shell).map((n) => ({ pane: n.pane, name: n.name || n.pane, agent: n.agent })) : [],
    project: ((kept.current.state.projects || []).find((p) => p.active) || {}).name || "",
    roster: snap.remoteDevices,
    pair: snap.remotePair,
    forgetPair: () => store.clearPair(),
    radar: snap.radar || { unavailable: kept.current.hello.radarUnavailable || "", checking: !!kept.current.hello.radarChecking },
    outcome: snap.remoteOutcome,
    status: kept.current.state.remote || null,
    remote: snap.remote,
    viewer,
    copy,
    news: local || (snap.notice && snap.notice.seq > settingsAt ? String(snap.notice.text || "") : ""),
    permission,
    onAsk: ask,
    addressAnswer: snap.agentAddress,
    table: kept.current.hello.keys,
    page,
    onRecording: setRecording,
    machine: (viewOf(kept.current.state.remote) || { name: "" }).name,
    pushError: (viewOf(kept.current.state.remote) || { pushError: "" }).pushError,
  };

  return html`<${Shell}
    hello=${kept.current.hello}
    state=${kept.current.state}
    onWatch=${onWatch}
    bottom=${bar}
    total=${totalOf(current)}
    narrow=${narrow}
    matchMedia=${matchMedia}
    window=${win}
    mainRef=${mainRef}
    actions=${actions}
    maximised=${drawer.maximised}
    keysOff=${recording}
    railExpanded=${snap.remote ? undefined : prefs.railExpanded}
    railWidth=${prefs.railWidth}
    onRailExpanded=${snap.remote ? undefined : (on) => change("railExpanded", on)}
    onRailWidth=${snap.remote ? undefined : (px) => change("railWidth", px)}
    drawerW=${localDrawerW === null ? prefs.drawerW : localDrawerW}
    onDrawerW=${setDrawerW}
    drawerH=${localDrawerH === null ? prefs.drawerH : localDrawerH}
    onDrawerH=${setDrawerH}
    orient=${orient}
    welcome=${!snap.remote && !!snap.hello && !prefs.helpSeen}
    onHelpOpened=${() => { if (!snap.remote && !prefs.helpSeen) change("helpSeen", true); }}
    blocked=${!!starting || settingsOpen || updatesAt >= 0 || quitAsk || historyOpen || !!infoFor || findingPane || helpersOpen || projectsOpen || !!baton || teamOpen || agentsOpen || worktreesOpen || closeAsk}
    onSettings=${() => openSettings("general")}
    onProjects=${viewer ? null : () => setProjectsOpen(true)}
    paletteItems=${paletteItems}
    railFoot=${snap.remote
      ? null
      : html`${viewer ? null : html`<${FootButton} cls="rail-helpers" glyph="▤" label="Helper apps" onClick=${() => setHelpersOpen(true)} />`}
        <${FootButton} cls=${"rail-updates" + (update ? " has-update" : "")} glyph="↑" label=${update ? "Update " + update.version : "Updates"} onClick=${() => setUpdatesAt(noticeSeq)} />`}
    notice=${noticeText}
    onDismissNotice=${() => { setLocal(""); setDismissed(noticeSeq); }}
    drawer=${node
      ? html`<${Drawer} store=${store} snap=${snap} node=${node} pane=${pane} now=${Date.now()} maximised=${drawer.maximised}
          onClose=${drawer.close} onToggleMax=${drawer.toggleMax} terminalOptions=${termProps} life=${viewer ? null : life}
          tx=${tx} onInfo=${openInfo} findAsk=${findAsk} tabAsk=${tabAsk} onOpenPane=${drawer.open} order=${order} helpers=${helperNames} layout=${orient} onLayout=${toggleLayout} onBox=${onBox} team=${current ? { model: current, events: feed ? feed.entries : [] } : null} onStartTeam=${snap.remote || viewer ? null : () => setStarting("team")} />`
      : null}
  >${snap.state
      ? html`<${GoalStrip} tabs=${goal.tabs} archived=${goal.archived} active=${activeTab} onSelect=${showTab}
          onRename=${tabDo.rename} onArchive=${tabDo.archive} onRestore=${tabDo.restore} onMove=${tabDo.move} onStep=${tabDo.step} canStep=${(id, dir) => canStep(goal.tabs, id, dir)}
          onStartTeam=${snap.remote || viewer ? null : () => setStarting("team")}
          onNewAgent=${viewer ? null : () => startTab(newTabCommand({ kind: "agent" }))} onChoose=${viewer ? null : () => setStarting("agent")} onNewShell=${viewer ? null : () => startTab(newTabCommand({ kind: "shell" }))}
          onHistory=${snap.remote || viewer ? null : () => setHistoryOpen(true)} onFind=${snap.remote ? null : () => setFindingPane(true)} />
        <${ViewBar} view=${view} views=${views} onView=${chooseView} />`
      : null}
    <${Home} snap=${snap} selected=${selected} onShown=${setShown} onOpen=${drawer.open} open=${drawer.pane} />
    ${hasTree && treeView && view === "tree"
      ? html`<div class="view-tree">
          <div class=${"home-split" + (showFeed ? " home-split--feed" : "") + (showFeed && feedCount === 0 ? " home-split--feed-empty" : "")}>
            <div class="home-col">
              ${scoped ? html`<${GoalLine} model=${scoped} shownAs=${(goal.tabs.find((t) => t.id === activeTab) || {}).label || ""} />` : null}
              ${scoped ? html`<${TreeNotes} model=${scoped} onOpen=${drawer.open} />` : null}
              ${activeTab === SOLO && scoped && scoped.ok && scoped.nodes.length === 0 ? html`<p class="solo-empty">No panes outside a goal.</p>` : null}
              <${treeView} model=${scoped} tasks=${teamTasks} open=${drawer.pane} onOpen=${drawer.open} send=${snap.remote ? null : send} decisions=${decisionCtx} />
            </div>
            ${showFeed ? html`<${Feed} feed=${scopedFeed} onFocus=${goTo} />` : null}
          </div>
        </div>`
      : null}
    ${view === "deps"
      ? html`<${Deps} team=${snap.team} model=${scoped} whole=${current} remote=${!!snap.remote} onOpen=${drawer.open} send=${snap.remote ? null : send} />`
      : null}
    ${view === "board"
      ? html`<${Board} team=${snap.team} remote=${!!snap.remote} gone=${snap.teamGone ? snap.teamGone.error || "This project can no longer be read." : ""}
          onOpenPane=${drawer.open} onTeam=${viewer ? null : () => setTeamOpen(true)} />`
      : null}
    ${view === "changes"
      ? html`<${ChangesView} nodes=${current ? current.nodes : []} statePanes=${statePanes} openPane=${drawer.pane} snap=${snap} store=${store} />`
      : null}
    ${view === "replay" ? html`<${Replay} />` : null}
    ${settingsOpen
      ? html`<${Settings} prefs=${prefs} change=${change} section=${settingsSection} onClose=${() => setSettingsOpen(false)} ctx=${settingsCtx} />`
      : null}
    ${historyOpen ? html`<${History} history=${snap.history} root=${selected} send=${send} onClose=${() => setHistoryOpen(false)} />` : null}
    ${infoFor
      ? html`<${PaneInfo} pane=${infoFor.pane} info=${snap.paneInfo} floor=${infoFor.floor} send=${send} copy=${copy} onClose=${() => setInfoFor(null)}
          failure=${snap.notice && snap.notice.error && snap.notice.seq > infoFor.noticeAt ? String(snap.notice.text || "") : ""} />`
      : null}
    ${findingPane ? html`<${FindPane} found=${snap.paneMatches} send=${send} onChoose=${(r) => setOpening(r.paneId)} onClose=${() => setFindingPane(false)} />` : null}
    ${starting
      ? html`<${Start} mode=${starting} catalog=${catalog} project=${project} send=${send} onClose=${() => setStarting("")} />`
      : null}
    ${updatesAt >= 0
      ? html`<${Updates} update=${update} versions=${snap.versions} updatesOff=${!!(kept.current.hello.prefs && kept.current.hello.prefs.updatesOff)}
          notice=${snap.notice && snap.notice.seq > updatesAt ? String(snap.notice.text || "") : ""} send=${send} onClose=${() => setUpdatesAt(-1)} />`
      : null}
    <${Toasts} toasts=${toasts} onOpen=${(id) => { drawer.open(id); setToasts((cur) => cur.filter((t) => t.id !== id)); }} onDismiss=${(id) => setToasts((cur) => cur.filter((t) => t.id !== id))} />
    <${Approvals} approvals=${snap.approvals} dismissed=${putAway} send=${send} onDismiss=${(id) => setPutAway((s) => new Set(s).add(id))} />
    ${teamOpen
      ? html`<${Team} team=${snap.team} tasks=${snap.teamTasks} messages=${snap.teamMessages} result=${snap.teamResult} root=${selected}
          gone=${snap.teamGone ? snap.teamGone.error || "This project can no longer be read." : snap.teamRefused ? TEAM_RELAY_WORDS : ""}
          send=${send} onClose=${() => setTeamOpen(false)} />`
      : null}
    ${agentsOpen
      ? html`<${Agents} list=${snap.agentsList} send=${send} onChoose=${(a) => { if (a.root && a.root !== selected) onWatch(a.root); setOpening(a.paneId); }} onClose=${() => setAgentsOpen(false)} onCloseFinished=${() => setCloseAsk(true)} />`
      : null}
    ${worktreesOpen ? html`<${Worktrees} listing=${snap.worktrees} send=${send} onClose=${() => setWorktreesOpen(false)} />` : null}
    ${helpersOpen ? html`<${Helpers} helpers=${snap.helpers} plan=${snap.helperPlan} send=${send} onClose=${() => setHelpersOpen(false)} />` : null}
    ${projectsOpen
      ? html`<${Projects} open=${openOf(snap.state || kept.current.state)} recents=${snap.recents} browse=${snap.browse} root=${state.root || ""} send=${send} onClose=${() => setProjectsOpen(false)} />`
      : null}
    ${baton
      ? html`<${Baton} draft=${draft} pane=${baton.name} run=${runBaton} answer=${batonAnswer} onClose=${() => setBaton(null)} />`
      : null}
    ${closeAsk
      ? html`<div class="dialog-scrim" onKeyDown=${(e) => e.key === LIST.tab && trapTab(e, e.currentTarget)}>
          <${Confirm} title="Close every finished pane?" words=${CLOSE_FINISHED_WORDS} label="Close finished panes"
            onConfirm=${() => { setCloseAsk(false); send(CLOSE_FINISHED_COMMAND); }} onCancel=${() => setCloseAsk(false)} />
        </div>`
      : null}
    ${quitAsk
      ? html`<div class="dialog-scrim" onKeyDown=${(e) => e.key === LIST.tab && trapTab(e, e.currentTarget)}>
          <${Confirm} title=${quitQuestion(kept.current.hello.keys)} words=${["The layout is saved first. Agents that are running stop."]} label="Quit and stop everything"
            onConfirm=${() => { setQuitAsk(false); send({ cmd: "quit" }); }} onCancel=${() => setQuitAsk(false)} />
        </div>`
      : null}
  <//>`;
}
