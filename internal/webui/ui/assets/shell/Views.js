// The views the strip's switch chooses between, other than the graph: the Board
// (the project's tasks in columns by state), Changes (what changed in one pane's
// working tree) and Replay. The graph is Home.
//
// The Board draws what the Team dialog does, from the same store watch
// (watchTeam): the desktop pushes the whole picture and this only draws it. A
// card opens the agent that owns the task when that agent is open, and the Team
// dialog otherwise, where a task is added, moved or given to someone. Every string
// in a task came from an agent or a person and is drawn as text.
import { useEffect, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Changes, NO_TREE_WORDS } from "../drawer/Changes.js";
import { STATES, teamOf, TEAM_RELAY_WORDS } from "../lib/team.js";

/** VIEWS are the views of the switch, in the order it shows them. */
export const VIEWS = [["graph", "Graph"], ["board", "Board"], ["changes", "Changes"], ["replay", "Replay"]];

/** TREE_VIEW is the outline of the team (rows with the activity feed beside it). It
 *  leads the switch and is the page's first view once HAS_TREE is true. */
export const TREE_VIEW = ["tree", "Tree"];

/** HAS_TREE says the outline is built into this page. It is false until the
 *  outline's own change turns it on, so the switch does not offer a view that
 *  draws nothing. */
export const HAS_TREE = false;

/** viewsOf is the views of the switch in order, with the Tree first when the page has it. */
export function viewsOf(hasTree = HAS_TREE) {
  return hasTree ? [TREE_VIEW, ...VIEWS] : VIEWS;
}

/** firstViewOf is the view the page starts on. */
export function firstViewOf(hasTree = HAS_TREE) {
  return hasTree ? TREE_VIEW[0] : "graph";
}

/** REPLAY_WORDS is what the Replay view says: it has nothing to draw yet. */
export const REPLAY_WORDS = "Replay will show a lane for each agent over time, with its status, the messages between agents and the files that overlapped, and a slider to move back along it.";
export const REPLAY_NO_DATA_WORDS = "Flockdeck does not record that history yet, so there is nothing to replay. It will appear here when it does.";

/** Board is the task board of the project on screen. team is the store's team
 *  push. onOpenPane opens an agent in the drawer, onTeam opens the Team dialog. */
export function Board({ team: teamMsg, gone = "", remote = false, onOpenPane, onTeam }) {
  if (remote) return html`<div class="view view--board"><p class="view-note">${TEAM_RELAY_WORDS}</p></div>`;
  const team = teamOf(teamMsg);
  if (gone) return html`<div class="view view--board"><p class="view-note" role="alert">${gone}</p></div>`;
  if (!team) return html`<div class="view view--board"><p class="view-note">Reading the tasks…</p></div>`;
  const columns = STATES.filter(([state]) => state !== "cancelled" || team.tasks.some((t) => t.state === state))
    .map(([state, name]) => ({ state, name, tasks: team.tasks.filter((t) => t.state === state) }));
  return html`<div class="view view--board">
    <div class="view-head">
      <h2 class="view-title">Board <span class="view-count">${team.tasks.length === 1 ? "1 task" : team.tasks.length + " tasks"}</span></h2>
      <button type="button" class="view-btn board-manage" onClick=${onTeam}>Add and manage tasks…</button>
    </div>
    ${team.boardError ? html`<p class="view-note" role="alert">${team.boardError}</p>` : null}
    ${team.tasks.length === 0 && !team.boardError ? html`<p class="view-note board-empty">No tasks yet.</p>` : null}
    <div class="board" role="list">
      ${columns.map((c) => html`<section key=${c.state} class="board-col" data-state=${c.state} aria-label=${c.name} role="listitem">
        <h3 class="board-col-head">${c.name} <span class="view-count">${c.tasks.length}</span></h3>
        <ul class="board-list">
          ${c.tasks.map((t) => html`<li key=${t.id}>
            <button type="button" class="board-card" data-task=${t.id} title=${t.ownerOpen && t.ownerPane ? "Open " + (t.owner || t.ownerPane) : "Open the Team dialog"}
              onClick=${() => (t.ownerOpen && t.ownerPane && onOpenPane ? onOpenPane(t.ownerPane) : onTeam && onTeam())}>
              <span class="board-id">${t.id}</span>
              <span class="board-title">${t.title}</span>
              <span class="board-meta">${t.owner ? t.owner : "no owner"}${t.branch ? " · " + t.branch : ""}${t.notes ? " · " + t.notes + (t.notes === 1 ? " note" : " notes") : ""}</span>
            </button>
          </li>`)}
        </ul>
      </section>`)}
    </div>
    ${team.openHidden ? html`<p class="view-note">${team.openHidden} older open tasks are not listed.</p>` : null}
    ${team.finishedHidden ? html`<p class="view-note">${team.finishedHidden} finished tasks are not listed. The Team dialog has them.</p>` : null}
  </div>`;
}

/** ChangesView is the Changes tab of the drawer as a view of its own: for the open
 *  agent when it has a directory, else the manager, else the first agent that has
 *  one. The agent can be changed here. nodes are the graph's nodes, statePanes the
 *  state push's panes (they carry the directory). */
export function ChangesView({ nodes, statePanes, openPane, snap, store }) {
  const withTree = nodes.filter((n) => statePanes && statePanes[n.pane] && statePanes[n.pane].cwd);
  const [picked, setPicked] = useState("");
  const fallback = withTree.find((n) => n.pane === openPane) || withTree.find((n) => n.role === "manager") || withTree[0] || null;
  const node = withTree.find((n) => n.pane === picked) || fallback;
  useEffect(() => {
    if (picked && !withTree.some((n) => n.pane === picked)) setPicked("");
  }, [withTree.length]);
  if (!node) return html`<div class="view view--changes"><p class="view-note">${NO_TREE_WORDS}</p></div>`;
  const pane = statePanes[node.pane];
  return html`<div class="view view--changes">
    <div class="view-head">
      <h2 class="view-title">Changes</h2>
      <label class="view-pick">For
        <select class="view-select" aria-label="Agent whose changes are shown" value=${node.pane} onChange=${(e) => setPicked(e.target.value)}>
          ${withTree.map((n) => html`<option key=${n.pane} value=${n.pane} selected=${n.pane === node.pane}>${n.name || n.pane}</option>`)}
        </select>
      </label>
    </div>
    <${Changes} key=${node.pane} cwd=${pane.cwd} git=${[pane.dirty, pane.untracked, pane.ahead, pane.behind].join("/")} snap=${snap} store=${store} />
  </div>`;
}

/** Replay is the view that has nothing to draw yet, and says so. */
export function Replay() {
  return html`<div class="view view--replay">
    <div class="view-head"><h2 class="view-title">Replay</h2></div>
    <p class="view-note">${REPLAY_WORDS}</p>
    <p class="view-note">${REPLAY_NO_DATA_WORDS}</p>
  </div>`;
}
