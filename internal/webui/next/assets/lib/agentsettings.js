// What the Agents & models section of the settings reads and sends, without a DOM.
// The agent catalog rides on the state push (lifecycle.js reads it); this is the
// part that changes it: which agent and model a pane starts with when nobody
// chooses, for every project and for the one on screen, and the address of an
// agent that talks to one.
//
// The desktop keeps the choices in agents.json and sends the catalog again. An
// address can send a key to wherever it points, so it is set at the desk and never
// from a window reached through the relay; the desktop refuses it there, and the
// store does not send it.
import { catalogOf, optionsOf, splitValue, valueOf } from "./lifecycle.js";

/** NO_CHOICE is the entry of "this project" that gives the project no choice of its
 *  own, so it starts what every project does. */
export const NO_CHOICE = "\n";

/** RELAY_ADDRESS_WORDS says why an address is not changed from a window reached
 *  through the relay. */
export const RELAY_ADDRESS_WORDS = "An agent's address is changed on the machine Flockdeck runs on, not from a window reached through the relay.";

/** modelsOf reads the catalog of a state's agents, with the choices as the entries
 *  of a select and the agents that take an address. */
export function modelsOf(agents) {
  const c = catalogOf({ agents });
  const own = c.project && (c.project.agent || c.project.model) ? c.project : null;
  return {
    ready: c.items.length > 0,
    err: c.err,
    options: optionsOf(c),
    every: valueOf(c.default && c.default.agent, c.default && c.default.model),
    project: own ? valueOf(own.agent, own.model) : NO_CHOICE,
    addressable: c.items
      .filter((a) => a.addressable === true)
      .map((a) => ({ id: a.id, name: String(a.name || a.id), address: typeof a.address === "string" ? a.address : "" })),
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

/** addressCommand sets an agent's address. An address that is the same as the one
 *  shown is not sent: the shown one may have had a name or password masked, and
 *  sending it back would write the mask over the real thing. */
export function addressCommand(id, text, current) {
  const value = String(text || "").trim();
  return value === (current || "") ? null : { cmd: "setAgentAddress", id, text: value };
}

/** answerOf reads the desktop's answer to an address: the agent, the address it
 *  saved or was given, and the reason it was refused, or null. */
export function answerOf(msg) {
  if (!msg || typeof msg !== "object" || typeof msg.id !== "string") return null;
  return { id: msg.id, address: typeof msg.address === "string" ? msg.address : "", error: typeof msg.error === "string" ? msg.error : "" };
}
