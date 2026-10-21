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

// What the shell is given before the first hello and state. Constants, so the
// shell does not take them for a change on every render.
const NO_HELLO = Object.freeze({ keys: Object.freeze([]) });
const NO_STATE = Object.freeze({ root: "", projects: Object.freeze([]) });
const NO_PANES = new Set();

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
export function Live({ store, matchMedia, window: win, terminalOptions = null }) {
  const narrow = useNarrow(matchMedia);
  const snap = useStore(store);
  const [selected, setSelected] = useState("");
  const [shown, setShown] = useState(null);
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
  }), [current, snap.graphGone, win, drawer.pane]);

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
    drawer=${node
      ? html`<${Drawer} store=${store} snap=${snap} node=${node} pane=${pane} now=${Date.now()} maximised=${drawer.maximised}
          onClose=${drawer.close} onToggleMax=${drawer.toggleMax} terminalOptions=${terminalOptions} />`
      : null}
  ><${Home} snap=${snap} selected=${selected} onShown=${setShown} onOpen=${drawer.open} open=${drawer.pane} narrow=${narrow} /><//>`;
}
