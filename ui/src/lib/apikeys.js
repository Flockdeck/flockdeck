// API keys: the agents that talk to a model API, whether each has a key and
// where it came from. The desktop never sends a key, only that there is one, so
// nothing here can show one; the form where a key is typed starts empty every
// time and is emptied the moment the key is sent.
//
// A window reached through the relay is shown the rows and is told that keys are
// set on the machine itself: a key typed there would pass through the relay,
// which can read what passes through it. The store does not send keySet or
// keyClear from such a window, and the desktop refuses them as well.

export const KEYS_INTRO =
  "A key is optional: an agent with a login of its own uses that when none is set here. " +
  "A key set here is passed to that agent's process and to nothing else, for panes started after it is set. " +
  "One exported in your environment reaches the agent only if the agent's entry in agents.json lists it in passEnv. " +
  "Anything set here is kept, unencrypted, in Flockdeck's own file in your user folder " +
  "(the agent, and anything it starts, can read it).";

export const KEYS_RELAY_WORDS =
  "Keys are set and cleared on the machine Flockdeck runs on. One typed in a window reached through the relay would pass through the relay, which can read what passes through it.";

export const KEYS_NONE_WORDS = "None of the agents here use an API key.";

const text = (v) => (typeof v === "string" ? v : "");

/** keysOf reads the desktop's keys answer into rows. A row without an agent id
 *  is dropped, since there would be nothing to set a key for. */
export function keysOf(msg) {
  const items = msg && Array.isArray(msg.items) ? msg.items : [];
  return items
    .filter((k) => k && typeof k === "object" && typeof k.agent === "string" && k.agent !== "")
    .map((k) => ({
      agent: k.agent,
      name: text(k.name) || k.agent,
      set: k.set === true,
      source: text(k.source),
      env: text(k.env),
      stored: k.stored === true,
      vars: Array.isArray(k.vars) ? k.vars.filter((v) => typeof v === "string" && v !== "") : [],
      notNeeded: k.notNeeded === true,
    }));
}

/** keyStatus says in words whether a row has a key and where it came from. */
export function keyStatus(k) {
  if (k.notNeeded) return "needs no key";
  if (k.set) return k.source === "env" ? "set via " + (k.env || "the environment") : "set, stored by Flockdeck";
  return "not set";
}

/** keyNotes are the lines under the status: a stored key kept beside a variable
 *  that is used, or the variable names to export for a row without a key. */
export function keyNotes(k) {
  const notes = [];
  if (k.set && k.source === "env" && k.stored) notes.push("a stored key is kept as well");
  if (!k.set && !k.notNeeded && k.vars.length) notes.push("or export " + k.vars.join(" or "));
  return notes;
}

/** canClear says whether Clear is offered: only a key Flockdeck holds. A key
 *  from the environment is the person's own arrangement and is not unset here,
 *  but a stored one a variable shadows is still Flockdeck's. */
export function canClear(k) {
  return k.stored || (k.set && k.source === "store");
}

/** setKeyCommand is the command that saves a key, or null for an empty one. */
export function setKeyCommand(agent, value) {
  const key = typeof value === "string" ? value.trim() : "";
  if (!agent || !key) return null;
  return { cmd: "keySet", id: agent, text: key };
}

export function clearKeyCommand(agent) {
  return agent ? { cmd: "keyClear", id: agent } : null;
}
