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
import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { useStore } from "../lib/use-store.js";
import { Drawer, useDrawer } from "../drawer/Drawer.js";
import { focusNode } from "../drawer/state.js";
import { ManagerBar, managerOf } from "../graph/ManagerBar.js";
import { nextNeedsYou, paneAt } from "../graph/keys.js";
import { Shell, useNarrow } from "./app.js";
import { Home, sameProject, rosterOf } from "./Home.js";
import { Start } from "./Start.js";
import { Confirm } from "./Confirm.js";
import { LIST } from "./keys.js";
import { Updates, trapTab } from "./Updates.js";
import { Tabs } from "./Tabs.js";
import { Settings } from "./Settings.js";
import { useLifecycle } from "../drawer/Lifecycle.js";
import { FONT_DEFAULT, FONT_MAX, FONT_MIN, PATCH_WAIT_MS, applyLook, prefsOf, reducedMotion, addPending, latestOf, settledPending, setting, termOptionsOf } from "../lib/prefs.js";
import { RELAY_WORDS, quitQuestion, updateOf } from "../lib/updates.js";
import { catalogOf, closeTabWarnings, moveTabCommand, newTabCommand, tabsOf } from "../lib/lifecycle.js";

// What the shell is given before the first hello and state. Constants, so the
// shell does not take them for a change on every render.
const NO_HELLO = Object.freeze({ keys: Object.freeze([]) });
const NO_STATE = Object.freeze({ root: "", projects: Object.freeze([]) });
const NO_PANES = new Set();

/** OPEN_AN_AGENT_WORDS is what a key that acts on the agent in the drawer says
 *  when no agent is open. */
export const OPEN_AN_AGENT_WORDS = "Open an agent first, then use this on it.";

/** nodeOfPane is a node for the drawer from the state push's entry for a pane, for
 *  a window with no graph: what the push says of the agent and nothing it does
 *  not (no branch, task or live line). */
function nodeOfPane(p) {
  const id = String(p.id || "");
  return {
    pane: id, kind: p.structured ? "structured" : "terminal", structured: !!p.structured,
    name: String(p.name || id), role: String(p.role || ""), agent: String(p.agent || ""), shell: !!p.shell,
    status: String(p.status || "unknown"), activity: String(p.activity || ""), since: "", live: "", branch: "",
    dirty: 0, untracked: 0, ahead: 0, behind: 0, task: null, unread: 0,
    waitingOn: { kind: p.permission || (p.ask && p.ask.questions) ? "user" : "none", text: "", ref: "" },
  };
}

/** Live draws the shell for a store. matchMedia and window are the page's,
 *  passed for a test, and terminalOptions reach the drawer's terminal (a test
 *  gives it fakes for xterm and the socket). */
export function Live({ store, matchMedia, window: win, terminalOptions = null, patchWaitMs = PATCH_WAIT_MS }) {
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
  const [patch, setPatch] = useState({});
  // The updates dialog (with the seq of the notice when it opened, so it shows
  // only newer ones) and the question before quit.
  const [updatesAt, setUpdatesAt] = useState(-1);
  const [quitAsk, setQuitAsk] = useState(false);
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
  const drawer = useDrawer({ mainRef, paneIds });
  const pane = drawer.pane ? (statePanes || {})[drawer.pane] || null : null;
  let node = drawer.pane && current ? current.byPane.get(drawer.pane) : null;
  if (!node && rosterOnly && pane) node = nodeOfPane(pane);

  // Restart, lock and close, for the agent in the drawer. The question before a
  // close or restart belongs to the pane it was asked for.
  const send = (c) => store.send(c);
  const life = useLifecycle(send);
  useEffect(() => {
    life.cancel();
  }, [drawer.pane]);

  // An agent started from the dialog is opened in the drawer once the graph
  // has it. The desktop answers startAgent with the pane it made.
  const startedSeq = snap.started ? snap.started.seq : 0;
  useEffect(() => {
    if (snap.started && snap.started.paneId) setOpening(snap.started.paneId);
  }, [startedSeq]);
  useEffect(() => {
    if (opening && paneIds.has(opening)) {
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
    const s = setting(name, value);
    if (!s) return false;
    send(s.command);
    setPatch((p) => addPending(p, s.patch));
    return true;
  };
  const bumpFont = (to) => {
    if (to < FONT_MIN || to > FONT_MAX) return;
    if (change("fontSize", to)) say("Font size " + to + "px");
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
  const bar = current && !snap.graphGone
    ? html`<${ManagerBar} snap=${snap} manager=${managerOf(current)} send=${(c) => store.send(c)} inputRef=${barRef} />`
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
  const actions = useMemo(() => ({
    nextNeedsYou: () => {
      if (!current || snap.graphGone) return;
      const doc = (win || (typeof window !== "undefined" ? window : null) || {}).document;
      const at = (doc && paneAt(doc.activeElement)) || drawer.pane;
      const to = nextNeedsYou(current, at);
      if (to) focusNode(mainRef.current, to);
    },
    managerBar: () => {
      const el = barRef.current;
      if (el && el.focus) el.focus();
    },
    settings: () => setSettingsOpen(true),
    fontUp: () => bumpFont(prefs.fontSize + 1),
    fontDown: () => bumpFont(prefs.fontSize - 1),
    fontReset: () => bumpFont(FONT_DEFAULT),
    newAgentTab: () => send(newTabCommand({ kind: "agent" })),
    newAgentTabChoose: () => setStarting("agent"),
    newShellTab: () => send(newTabCommand({ kind: "shell" })),
    startTeam: () => setStarting("team"),
    nextTab: () => send({ cmd: "nextTab" }),
    prevTab: () => send({ cmd: "prevTab" }),
    moveTabLeft: () => {
      const cur = tabs.find((t) => t.active);
      const cmd = cur ? moveTabCommand(tabs, cur.id, -1) : null;
      if (cmd) send(cmd);
    },
    moveTabRight: () => {
      const cur = tabs.find((t) => t.active);
      const cmd = cur ? moveTabCommand(tabs, cur.id, 1) : null;
      if (cmd) send(cmd);
    },
    detach: () => (snap.remote ? say(RELAY_WORDS) : send({ cmd: "detach" })),
    quit: () => (snap.remote ? say(RELAY_WORDS) : setQuitAsk(true)),
    zoomPane: needPane(() => drawer.toggleMax()),
    restartPane: needPane(() => life.begin("restart", node, pane)),
    lockPane: needPane(() => life.toggleLock(node.pane, !(pane && pane.locked))),
    closePane: needPane(() => (pane && pane.locked ? say("This pane is locked. Unlock it to close it.") : life.begin("close", node, pane))),
  }), [current, snap.graphGone, snap.remote, win, drawer.pane, node, pane, tabs, life.request, prefs.fontSize]);

  return html`<${Shell}
    hello=${kept.current.hello}
    state=${kept.current.state}
    onWatch=${onWatch}
    top=${bar}
    narrow=${narrow}
    matchMedia=${matchMedia}
    window=${win}
    mainRef=${mainRef}
    actions=${actions}
    maximised=${drawer.maximised}
    blocked=${!!starting || settingsOpen || updatesAt >= 0 || quitAsk}
    onSettings=${() => setSettingsOpen(true)}
    railFoot=${snap.remote
      ? null
      : html`<button type="button" class=${"rail-updates" + (update ? " has-update" : "")} onClick=${() => setUpdatesAt(noticeSeq)}>
          ${update ? "Update " + update.version : "Updates"}
        </button>`}
    notice=${noticeText}
    onDismissNotice=${() => { setLocal(""); setDismissed(noticeSeq); }}
    drawer=${node
      ? html`<${Drawer} store=${store} snap=${snap} node=${node} pane=${pane} now=${Date.now()} maximised=${drawer.maximised}
          onClose=${drawer.close} onToggleMax=${drawer.toggleMax} terminalOptions=${termProps} life=${life} />`
      : null}
  >${snap.state ? html`<${Tabs} tabs=${tabs} send=${send} show=${onDesk} warnFor=${(t) => closeTabWarnings(t, current ? current.byPane : null, statePanes)} onChoose=${() => setStarting("agent")} />` : null}
    <${Home} snap=${snap} selected=${selected} onShown=${setShown} onOpen=${drawer.open} open=${drawer.pane} narrow=${narrow} />
    ${settingsOpen ? html`<${Settings} prefs=${prefs} change=${change} onClose=${() => setSettingsOpen(false)} />` : null}
    ${starting
      ? html`<${Start} mode=${starting} catalog=${catalog} project=${project} send=${send} onClose=${() => setStarting("")} />`
      : null}
    ${updatesAt >= 0
      ? html`<${Updates} update=${update} versions=${snap.versions} updatesOff=${!!(kept.current.hello.prefs && kept.current.hello.prefs.updatesOff)}
          notice=${snap.notice && snap.notice.seq > updatesAt ? String(snap.notice.text || "") : ""} send=${send} onClose=${() => setUpdatesAt(-1)} />`
      : null}
    ${quitAsk
      ? html`<div class="dialog-scrim" onKeyDown=${(e) => e.key === LIST.tab && trapTab(e, e.currentTarget)}>
          <${Confirm} title=${quitQuestion(kept.current.hello.keys)} words=${["The layout is saved first. Agents that are running stop."]} label="Quit and stop everything"
            onConfirm=${() => { setQuitAsk(false); send({ cmd: "quit" }); }} onCancel=${() => setQuitAsk(false)} />
        </div>`
      : null}
  <//>`;
}
