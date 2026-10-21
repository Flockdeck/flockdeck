// The page's root component: the shell with the store behind it. The store's
// snapshot gives the shell its hello and state, the rail's choice of project
// goes to the store as the one graph watch, and the main area is the home screen.
// main.js makes the store and renders this; it is a component of its own so a test
// can render it on a fake socket.
import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { useStore } from "../lib/use-store.js";
import { Shell } from "./app.js";
import { Home } from "./Home.js";

// What the shell is given before the first hello and state. Constants, so the
// shell does not take them for a change on every render.
const NO_HELLO = Object.freeze({ keys: Object.freeze([]) });
const NO_STATE = Object.freeze({ root: "", projects: Object.freeze([]) });

/** Live draws the shell for a store. matchMedia and window are the page's,
 *  passed for a test. */
export function Live({ store, matchMedia, window: win }) {
  const snap = useStore(store);
  const [selected, setSelected] = useState("");
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
  return html`<${Shell}
    hello=${kept.current.hello}
    state=${kept.current.state}
    onWatch=${onWatch}
    matchMedia=${matchMedia}
    window=${win}
  ><${Home} snap=${snap} selected=${selected} /><//>`;
}
