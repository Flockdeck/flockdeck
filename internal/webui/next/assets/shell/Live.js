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
import { ManagerBar, managerOf } from "../graph/ManagerBar.js";
import { Shell } from "./app.js";
import { Home } from "./Home.js";

// What the shell is given before the first hello and state. Constants, so the
// shell does not take them for a change on every render.
const NO_HELLO = Object.freeze({ keys: Object.freeze([]) });
const NO_STATE = Object.freeze({ root: "", projects: Object.freeze([]) });
const NO_PANES = new Set();

/** Live draws the shell for a store. matchMedia and window are the page's,
 *  passed for a test, and terminalOptions reach the drawer's terminal (a test
 *  gives it fakes for xterm and the socket). */
export function Live({ store, matchMedia, window: win, terminalOptions = null }) {
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

  const mainRef = useRef(null);
  const paneIds = useMemo(() => (shown ? new Set(shown.nodes.map((n) => n.pane)) : NO_PANES), [shown]);
  const drawer = useDrawer({ mainRef, paneIds });
  const node = drawer.pane && shown ? shown.byPane.get(drawer.pane) : null;
  const pane = node ? (kept.current.state.panes || {})[node.pane] || null : null;

  // The manager of the project on screen, from the picture Home draws: a push
  // that cannot be read leaves the last good one, and another project's does not
  // count. Before any graph there is no bar, so "no manager" is not said while
  // the graph is still being read.
  const bar = shown && !snap.graphGone
    ? html`<${ManagerBar} snap=${snap} manager=${managerOf(shown)} send=${(c) => store.send(c)} />`
    : null;

  return html`<${Shell}
    hello=${kept.current.hello}
    state=${kept.current.state}
    onWatch=${onWatch}
    top=${bar}
    matchMedia=${matchMedia}
    window=${win}
    mainRef=${mainRef}
    maximised=${drawer.maximised}
    drawer=${node
      ? html`<${Drawer} store=${store} node=${node} pane=${pane} now=${Date.now()} maximised=${drawer.maximised}
          onClose=${drawer.close} onToggleMax=${drawer.toggleMax} terminalOptions=${terminalOptions} />`
      : null}
  ><${Home} snap=${snap} selected=${selected} onShown=${setShown} onOpen=${drawer.open} open=${drawer.pane} /><//>`;
}
