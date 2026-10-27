// What the Agents & models section of the settings reads and sends, without a DOM.
// The agent catalog rides on the state push (lifecycle.js reads it); this is the
// part that changes it: which agent and model a pane starts with when nobody
// chooses, for every project and for the one on screen.
//
// The desktop keeps the choices in agents.json and sends the catalog again.
import { catalogOf, optionsOf, splitValue, valueOf } from "./lifecycle.js";

/** NO_CHOICE is the entry of "this project" that gives the project no choice of its
 *  own, so it starts what every project does. */
export const NO_CHOICE = "\n";

/** modelsOf reads the catalog of a state's agents, with the choices as the entries
 *  of a select. */
export function modelsOf(agents) {
  const c = catalogOf({ agents });
  const own = c.project && (c.project.agent || c.project.model) ? c.project : null;
  return {
    ready: c.items.length > 0,
    err: c.err,
    options: optionsOf(c),
    every: valueOf(c.default && c.default.agent, c.default && c.default.model),
    project: own ? valueOf(own.agent, own.model) : NO_CHOICE,
  };
}

/** groupsOf puts the options under their agent, in the order the catalog has them. */
export function groupsOf(options) {
  const groups = [];
  for (const o of options) {
    let g = groups.find((x) => x.name === o.group);
    if (!g) groups.push((g = { name: o.group, items: [] }));
    g.items.push(o);
  }
  return groups;
}

/** defaultCommand is the command for a choice made in one of the two selects. every
 *  true is the choice for every project; otherwise it is the active project's, and
 *  an empty choice gives the project up. A choice with no agent is not sent for
 *  every project: there is nothing to start. */
export function defaultCommand(value, every) {
  const [agent, model] = splitValue(value);
  if (every) return agent ? { cmd: "setAgentDefault", agent, model, kind: "all" } : null;
  return { cmd: "setAgentDefault", agent, model };
}
