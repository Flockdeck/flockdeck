// TypeSafe's Jev: the key it needs and the switch that lets it read a pane.
//
// Two separate things, and the page keeps them apart. Saving a key sends nothing
// anywhere. The switch is what sends the last lines of a quiet pane's terminal to
// TypeSafe, a third party, so it is off until it is turned on, says what is then
// sent, and can only be turned on on the machine Flockdeck runs on: a window reached
// through the relay may switch it off but not on, since the relay is meant to be
// blind to terminal content.
//
// The desktop never sends the key, only whether one is set and whether
// TYPESAFE_API_KEY is in its environment. A window reached through the relay does
// not set or clear the key (it would pass through the relay) and is not even asked
// whether one is set.

export const JEV_KEY_WORDS =
  "Used only for Status detection's Jev setting. Saving a key sends nothing: it stays off until you turn that on, " +
  "and what it sends is described there, including that it goes to TypeSafe, a third party. " +
  "The key is kept on this machine in Flockdeck's key store, is only ever sent to api.typesafe.ai, and is never shown again. " +
  "If none is set here, TYPESAFE_API_KEY from the environment is used. It can only be set on the machine itself.";

export const JEV_STATUS_WORDS =
  "Off by default. On, and with a TypeSafe API key set (in API keys, or TYPESAFE_API_KEY in the environment Flockdeck runs in), the last " +
  "30 lines (at most 2,000 bytes) of a pane's terminal output are SENT TO TYPESAFE, a third party, " +
  "whenever an agent that reports no status of its own goes quiet and Flockdeck cannot tell finished from stopped on a question. " +
  "Nothing else is sent: not the scrollback, the pane's name, or its folder. " +
  "API keys, tokens, password values and secret file paths are redacted first, by pattern, so a secret with no recognisable shape is still sent. " +
  "Panes of agents that report their own status, and shell panes, are never sent. " +
  "It can only turn a quiet pane into waiting or blocked, never the other way.";

export const JEV_RELAY_WORDS =
  "This can only be turned on on the machine Flockdeck runs on, not from a window reached through the relay. It can be turned off from here.";
export const JEV_KEY_RELAY_WORDS = "Set on the machine Flockdeck runs on, not from a window reached through the relay.";

/** jevOf reads the desktop's jevKey answer: whether a key is set in Settings and
 *  whether the environment has one. null until the desktop has answered. */
export function jevOf(msg) {
  if (!msg || typeof msg !== "object") return null;
  return { set: msg.set === true, env: msg.env === true };
}

/** jevWords is the key's state in a sentence. */
export function jevWords(info, remote) {
  if (remote) return JEV_KEY_RELAY_WORDS;
  if (!info) return "Checking…";
  if (info.set && info.env) return "Set. It is used in place of TYPESAFE_API_KEY from the environment.";
  if (info.set) return "Set.";
  if (info.env) return "Not set here; TYPESAFE_API_KEY from the environment is used.";
  return "Not set.";
}

export function jevGetCommand() {
  return { cmd: "jevKey", kind: "get" };
}

/** jevSetCommand saves a key, or is null for an empty one. */
export function jevSetCommand(value) {
  const text = typeof value === "string" ? value.trim() : "";
  return text ? { cmd: "jevKey", kind: "set", text } : null;
}

export function jevClearCommand() {
  return { cmd: "jevKey", kind: "clear" };
}
