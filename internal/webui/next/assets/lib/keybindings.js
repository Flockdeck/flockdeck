// What the keyboard section of the settings reads and sends, without a DOM. The
// key table (the hello's, and the whole table again whenever a binding is
// remapped or reset) says what each action is bound to and which have been
// changed from the built-in. The desktop checks a new binding against the rest
// and refuses a clash; the dialog also checks it first, with the window's own
// table, so the refusal does not wait on a round trip.
import { keySections } from "./help.js";

/** RISKY_WORDS is said for a binding with no Ctrl or Alt: a terminal would still
 *  receive it. */
export const RISKY_WORDS = "would still reach the agent's terminal; add Ctrl or Alt, or use a function key.";

/** rowsOf is the key table as sections of rows. A row is { id, label, keys,
 *  overridden, range }; a range ("Alt+1 … Alt+9") is shown but cannot be
 *  remapped, and the desktop refuses to try. */
export function rowsOf(table) {
  return keySections(table).map((s) => ({
    name: s.name,
    rows: s.rows.map((k) => ({
      id: k.id,
      label: String(k.label || k.id),
      keys: typeof k.keys === "string" ? k.keys : "",
      overridden: k.overridden === true,
      range: typeof k.keys === "string" && k.keys.includes("…"),
    })),
  }));
}

/** anyChanged says a binding has been remapped, so Reset all has something to do. */
export const anyChanged = (table) => (table || []).some((k) => k && k.overridden === true);

export const setCommand = (id, chord) => ({ cmd: "setKeybinding", id, text: chord });
export const resetCommand = (id) => ({ cmd: "resetKeybinding", id });
export const resetAllCommand = () => ({ cmd: "resetKeybindings" });
