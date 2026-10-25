// The teams of the project, one line each, for the outline's notes (TreeNotes.js):
// what the team is for, how many of its agents are working and how many need the
// person. The panes in no team are one line.
//
// Every text here is untrusted and is drawn as text by the caller.
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
