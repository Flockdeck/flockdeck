// The roster of a window the graph is not sent to (reached through the relay):
// the agents of the state push, one level, in the push's order, after the strip of
// those that wait on the person (NeedsYou.js). The desktop line is the caller's.
//
// Every text here came from an agent, a task or a person, and is drawn as text.
import { html } from "../html.js";
import { NeedsYou } from "./NeedsYou.js";
import { clip } from "./Node.js";

/** needsOfRoster turns the state push's panes that wait on the person into the
 *  model NeedsYou draws: a permission or a question, in the push's order. The
 *  push a relay window gets is already made fit for it, so the summary and the
 *  question are what the row shows. */
export function needsOfRoster(state) {
  const panes = state && state.panes && typeof state.panes === "object" ? Object.values(state.panes) : [];
  const byPane = new Map();
  const needs = [];
  for (const p of panes) {
    if (!p || typeof p !== "object" || !p.id) continue;
    const id = String(p.id);
    if (byPane.has(id)) continue; // one entry for an id, the first
    byPane.set(id, { name: String(p.name || id) });
    const q = p.ask && Array.isArray(p.ask.questions) && p.ask.questions[0];
    if (p.permission && typeof p.permission === "object") {
      needs.push({ id: "p:" + id, pane: id, kind: "permission", text: String(p.permission.summary || p.permission.tool || "") });
    } else if (q && typeof q === "object") {
      needs.push({ id: "a:" + id, pane: id, kind: "ask", text: String(q.question || "") });
    }
  }
  return { byPane, needs, teams: [], soloNeedsHidden: 0 };
}

/** RosterList draws the agents of a state push: state is the push, rows are Home's rosterOf rows of it. */
export function RosterList({ state, rows, onOpen = null, open = "" }) {
  const model = needsOfRoster(state);
  return html`
    <div class="list list--roster">
      <${NeedsYou} model=${model} onSelect=${onOpen ? (pane) => onOpen(pane, "strip") : null} />
      <ul class="home-roster" aria-label="Agents in this project">
        ${rows.map((r) => {
          const body = html`
            <span class="home-agent-name">${clip(r.name, 60)}</span>
            ${r.role ? html`<span class="home-agent-role">${clip(r.role, 30)}</span>` : null}
            <span class="home-agent-doing">${clip(r.doing, 20)}</span>
          `;
          return html`<li class="home-agent" data-pane=${r.id} key=${r.id}>
            ${onOpen
              ? html`<button type="button" class=${"list-pick" + (open === r.id ? " is-open" : "")} aria-expanded=${open === r.id ? "true" : "false"} onClick=${() => onOpen(r.id)}>${body}</button>`
              : body}
          </li>`;
        })}
      </ul>
    </div>
  `;
}
