// Typed wake switches, as the desktop keeps them (store.WakePrefs) and sends them
// in the preferences push: off for everything, off for an agent type, off for one
// pane, and the agent types that are typed the message text instead of the fixed
// line. Any switch that is off wins, so a pane is woken only when none of the
// global, its type's and its own switches is off.

const strings = (v) => (Array.isArray(v) ? v.filter((s) => typeof s === "string" && s && s.length <= 128) : []);

/** wakeOf reads the wake part of the preferences, with every list a list of ids. */
export function wakeOf(raw) {
  const r = raw && typeof raw === "object" ? raw : {};
  return { off: r.off === true, agents: strings(r.agents), panes: strings(r.panes), textAgents: strings(r.textAgents) };
}

/** wakeCommand is the command that sets one switch. kind is "global", "agent",
 *  "pane" or "text"; id is the agent or pane (none for global); on is whether wake
 *  is on (for "text": whether the agent is opted in). */
export function wakeCommand(kind, id, on) {
  if (!["global", "agent", "pane", "text"].includes(kind)) return null;
  if (kind !== "global" && !id) return null;
  const cmd = { cmd: "wakeSwitch", kind, text: on ? "on" : "off" };
  if (kind !== "global") cmd.id = String(id);
  return cmd;
}

/** wakeEnabled says whether wake may type into a pane of an agent type. */
export function wakeEnabled(wake, agentId, paneId) {
  if (wake.off) return false;
  if (agentId && wake.agents.includes(agentId)) return false;
  return !(paneId && wake.panes.includes(paneId));
}

/** WAKE_RELAY_WORDS is what a window reached through the relay is told. */
export const WAKE_RELAY_WORDS = "Typed wake can only be changed on the machine Flockdeck runs on, not from a window reached through the relay.";
