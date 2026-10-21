// The home screen: what the main area shows, drawn from the store's snapshot.
//
// - Connecting: no hello yet. One line, and the wait while the socket retries.
// - The graph is refused to this window (graphRefused: it is reached through the
//   relay, which never gets a graph command): the roster of the project's agents
//   from the state push, and a line saying the team graph is on the desktop.
// - No project is open.
// - teamGone: the project can no longer be read. The picture is cleared.
// - A graph command failed for another reason: the desk's sentence.
// - Otherwise the graph with its edges, badges and needs-you strip, from the
//   latest graph push. A push that cannot be read leaves the last good picture of
//   the same project, and a push of another project than the one on screen is
//   not drawn (the store keeps whichever came last).
//
// Every text here came from the desktop, an agent or a person, and is drawn as
// text.
import { useEffect, useMemo, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { buildGraphModel } from "../graph/model.js";
import { GraphView } from "../graph/Graph.js";
import { clip } from "../graph/Node.js";

/** usable says a graph push can be drawn: it is a graph, and its lists are lists. */
export function usable(g) {
  return !!g && g.type === "graph" && Array.isArray(g.nodes) && Array.isArray(g.teams) && Array.isArray(g.edges);
}

/** sameProject compares the root a graph names with the project on screen. The
 *  server spells a root its own way, so slashes, a trailing one and case are not
 *  told apart. */
export function sameProject(a, b) {
  const norm = (s) => String(s || "").replace(/\\/g, "/").replace(/\/+$/, "").toLowerCase();
  return norm(a) === norm(b);
}

/** rosterOf is the agents of the project in the state push, in the order the
 *  push has them: id, name and what each is doing. */
export function rosterOf(state) {
  const panes = state && state.panes && typeof state.panes === "object" ? Object.values(state.panes) : [];
  return panes
    .filter((p) => p && typeof p === "object")
    .map((p) => ({
      id: String(p.id || ""),
      name: String(p.name || p.id || ""),
      role: String(p.role || ""),
      doing: String(p.activity || p.status || ""),
    }));
}

function Line({ children, kind }) {
  return html`<p class=${"home-line" + (kind ? " home-line--" + kind : "")} role="status">${children}</p>`;
}

function Roster({ state }) {
  const rows = rosterOf(state);
  if (rows.length === 0) return null;
  return html`<ul class="home-roster" aria-label="Agents in this project">
    ${rows.map((r) => html`<li class="home-agent" key=${r.id}>
      <span class="home-agent-name">${clip(r.name, 60)}</span>
      ${r.role ? html`<span class="home-agent-role">${clip(r.role, 30)}</span>` : null}
      <span class="home-agent-doing">${clip(r.doing, 20)}</span>
    </li>`)}
  </ul>`;
}

/**
 * Home draws the main area. snap is the store's snapshot; selected is the root
 * of the project the window is watching ("" when there is none). onShown is
 * called with the model of the picture on screen (or null) when it changes, so
 * the page can open a drawer for a node of it; onOpen and open are the graph's
 * (a click or key on a box, and the pane whose drawer is open).
 */
export function Home({ snap, selected, onShown, onOpen = null, open = "" }) {
  const last = useRef(null); // { root, model } of the last graph push that could be drawn
  const g = snap.graph;
  const model = useMemo(() => (usable(g) ? buildGraphModel(g) : null), [g]);
  if (model && model.ok && (!selected || sameProject(g.root, selected))) {
    last.current = { root: g.root, model };
  } else if (last.current && selected && !sameProject(last.current.root, selected)) {
    // The window moved to another project: nothing of the old one stays, and a
    // push of the old one that was in flight is not drawn.
    last.current = null;
  }
  const shown = last.current ? last.current.model : null;
  useEffect(() => {
    if (onShown) onShown(shown);
  }, [shown]);

  const projects = (snap.state && snap.state.projects) || [];

  // With no hello the connection has dropped, or has not been made. A picture
  // already drawn stays, called out of date, and is not hidden by this line.
  if (!snap.hello && !shown) {
    const waiting = snap.connection === "waiting" && snap.retryInMs > 0;
    return html`<div class="home">
      <${Line}>${waiting ? "Not connected to Flockdeck. Trying again in " + Math.max(1, Math.round(snap.retryInMs / 1000)) + " s." : "Connecting to Flockdeck…"}<//>
    </div>`;
  }
  if (snap.graphRefused) {
    return html`<div class="home home--roster">
      <${Line}>${snap.remote
        ? "The team graph is shown on the desktop."
        : "The desktop would not send the team graph to this window. It is shown on the desktop itself."}<//>
      <${Roster} state=${snap.state} />
    </div>`;
  }
  if (snap.state && !selected && projects.length === 0) {
    return html`<div class="home"><${Line}>No project is open.<//></div>`;
  }
  if (snap.graphGone) {
    return html`<div class="home">
      <${Line} kind="error">This project can no longer be read.<//>
      ${snap.graphGone.error ? html`<p class="home-detail">${clip(snap.graphGone.error, 200)}</p>` : null}
    </div>`;
  }
  if (snap.graphError && !shown) {
    return html`<div class="home">
      <${Line} kind="error">The team graph could not be read.<//>
      ${snap.graphError.error ? html`<p class="home-detail">${clip(snap.graphError.error, 200)}</p>` : null}
    </div>`;
  }
  if (!shown) {
    return html`<div class="home"><${Line}>Reading the team graph…<//></div>`;
  }
  return html`<div class="home home--graph">
    ${snap.connection !== "open" ? html`<${Line} kind="stale">Out of date. Reconnecting.<//>` : null}
    ${snap.graphError ? html`<${Line} kind="error">The team graph could not be updated: ${clip(snap.graphError.error || "the desktop gave no reason", 200)}<//>` : null}
    <${GraphView} model=${shown} onOpen=${onOpen} onSelect=${onOpen ? (pane) => onOpen(pane, "strip") : undefined} open=${open} />
  </div>`;
}
