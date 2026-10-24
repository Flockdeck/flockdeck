// The teams of the project, one line each, beside the Needs you cards: what the
// team is for, how many of its agents are working and how many need the person.
// The panes in no team are one line. A line for a team with a manager that is a
// node of the graph is a button that opens the manager.
//
// Every text here is untrusted and is drawn as text.
import { html } from "../html.js";
import { clip, stateOf } from "./Node.js";

function workingOf(model, ids) {
  let n = 0;
  for (const id of ids) {
    const node = model.byPane.get(id);
    if (node && stateOf(node).key === "working") n++;
  }
  return n;
}

/** summaryOf is one line per team, then one for the panes in no team, from a graph model: { id, name, pane, working, needs, agents }. */
export function summaryOf(model) {
  const out = [];
  for (const t of model.teams) {
    const manager = t.manager && model.byPane.has(t.manager) ? t.manager : "";
    out.push({ id: t.id, name: clip(t.goal || "Team", 28), pane: manager, working: workingOf(model, t.members), needs: t.needs.length + t.needsHidden, agents: t.members.length });
  }
  if (model.solo.length > 0) {
    out.push({ id: "", name: "Not in a team", pane: "", working: workingOf(model, model.solo), needs: model.soloNeeds.length + model.soloNeedsHidden, agents: model.solo.length });
  }
  return out;
}

export function words(s) {
  if (s.needs > 0) return s.needs + " need you";
  if (s.working > 0) return s.working + " working";
  return s.agents + (s.agents === 1 ? " agent" : " agents");
}

/** Teams draws the lines. It draws nothing for a project with one team and no other panes, which the graph already says. */
export function Teams({ model, onSelect }) {
  const lines = summaryOf(model);
  if (lines.length < 2) return null;
  return html`
    <section class="teams" aria-label="Teams">
      <h2 class="teams-head">Teams and solo</h2>
      <ul class="teams-list">
        ${lines.map((s, i) => {
          const body = html`<span class="teams-name">${s.name}</span><span class=${"teams-stat" + (s.needs > 0 ? " teams-stat--needs" : "")}>${words(s)}</span>`;
          return html`<li class="teams-row" key=${s.id || "solo-" + i}>
            ${onSelect && s.pane ? html`<button type="button" class="teams-pick" onClick=${() => onSelect(s.pane)}>${body}</button>` : html`<div class="teams-pick">${body}</div>`}
          </li>`;
        })}
      </ul>
    </section>
  `;
}
