// Key handling for the new shell, driven by the key table the server sends in
// its hello. A binding is written once, in ui/help/keys.go, and this
// file only reads it: the shell asks "which action is this keydown" and never
// names a key of its own. A test holds it to that.
//
// The one place key names are written is the vocabulary block just below. It
// holds the words the table uses for arrows and the keys that move through a
// list, which are a property of lists and not of any action. The test allows
// key names inside the block and nowhere else in this folder.

// vocabulary:begin
const ARROWS = { "←": "arrowleft", "→": "arrowright", "↑": "arrowup", "↓": "arrowdown" };
const MODIFIERS = ["ctrl", "shift", "alt"];
/** On most layouts + is Shift and =, so Ctrl++ and Ctrl+= are one gesture. */
const PLUS = "+";
const EQUALS = "=";
/** A key the table writes with a symbol or a word of its own, the keys a
 *  recorded binding is not made of, and the ones a terminal leaves to the page. */
const NAMED = { ArrowLeft: "←", ArrowRight: "→", ArrowUp: "↑", ArrowDown: "↓", " ": "Space", Escape: "Esc" };
const MODIFIER_KEYS = ["Control", "Shift", "Alt", "Meta", "OS"];
const FUNCTION_KEY = /^F([1-9]|1[0-9])$/;
const PAGE_KEYS = ["Esc", "Tab"];
/** CLEARS take a binding away while one is being recorded. */
const CLEARS = ["Backspace", "Delete"];
/** keyCode of a key pressed while an input method is composing. */
const IME = 229;

/** composing is whether a keydown belongs to an input method, not to the page. */
export const composing = (e) => !!(e.isComposing || e.keyCode === IME);

/** LIST are the keys that move through, choose from and leave a list. step goes to
 *  the next match of a search, as it does in nearly every program that searches. */
export const LIST = { up: "ArrowUp", down: "ArrowDown", left: "ArrowLeft", right: "ArrowRight", home: "Home", end: "End", accept: "Enter", cancel: "Escape", tab: "Tab", step: "F3" };
// vocabulary:end

/** signature is the shape a binding and a keydown are both reduced to, so that
 *  comparing them is comparing strings. */
export function signature(ctrl, shift, alt, key) {
  return (ctrl ? "c" : "") + (shift ? "s" : "") + (alt ? "a" : "") + ":" + key;
}

/** signatureOf turns a binding as it is written for a reader ("Ctrl+Shift+K")
 *  into a signature. A binding that names a range ("Alt+1 … Alt+9") or has a
 *  modifier this does not know has no single signature and gives "". */
export function signatureOf(spec) {
  if (!spec || spec.includes("…")) return "";
  const parts = spec.split("+").map((p) => p.trim()).filter(Boolean);
  const name = parts.pop();
  if (!name) return "";
  const mods = { ctrl: false, shift: false, alt: false };
  for (const p of parts) {
    const m = p.toLowerCase();
    if (!MODIFIERS.includes(m)) return "";
    mods[m] = true;
  }
  return signature(mods.ctrl, mods.shift, mods.alt, ARROWS[name] || name.toLowerCase());
}

/** signatureOfEvent is the signature of a keydown. */
export function signatureOfEvent(e) {
  let key = (e.key || "").toLowerCase();
  let shift = !!e.shiftKey;
  // Ctrl+= and Ctrl++ are one gesture on most layouts: + arrives with Shift
  // held, as Shift and =.
  if (key === PLUS) {
    key = EQUALS;
    shift = false;
  }
  // A layout that does not type Latin letters reports the key marked D as
  // another character, so a binding can only mean the physical key there.
  if (/^[^\x00-\x7f]$/.test(key)) {
    const m = /^(?:Key([A-Z])|Digit([0-9]))$/.exec(e.code || "");
    if (m) key = (m[1] || m[2]).toLowerCase();
  }
  return signature(!!e.ctrlKey, shift, !!e.altKey, key);
}

/** bindingsFrom reads the hello's key table into a map from signature to
 *  action id. Where two actions share a binding the first in the table wins. */
export function bindingsFrom(table) {
  const out = new Map();
  for (const k of table || []) {
    const sig = signatureOf(k.keys);
    if (sig && !out.has(sig)) out.set(sig, k.id);
  }
  return out;
}

/** rangesFrom reads the bindings of the table that name a run of keys ("Alt+1 …
 *  Alt+9") into { id, prefix, first, last }: the modifiers both ends share and the
 *  two keys. A run whose ends differ in modifiers, or are not one character each,
 *  is not read. */
export function rangesFrom(table) {
  const out = [];
  for (const k of table || []) {
    if (!k.keys || !k.keys.includes("…")) continue;
    const [from, to] = k.keys.split("…").map((p) => signatureOf(p.trim()));
    const a = from && from.split(":");
    const b = to && to.split(":");
    if (!a || !b || a.length !== 2 || b.length !== 2 || a[0] !== b[0] || a[1].length !== 1 || b[1].length !== 1 || a[1] > b[1]) continue;
    out.push({ id: k.id, prefix: a[0], first: a[1], last: b[1] });
  }
  return out;
}

/** rangeAction is { id, n } when a keydown is one key of a run, with n counting
 *  from 1 at the first key, or null. */
export function rangeAction(ranges, e, implemented) {
  if (!ranges.length || composing(e)) return null;
  const [prefix, key] = signatureOfEvent(e).split(":");
  if (key === undefined || key.length !== 1) return null;
  for (const r of ranges) {
    if (r.prefix === prefix && key >= r.first && key <= r.last && implemented.includes(r.id)) return { id: r.id, n: key.charCodeAt(0) - r.first.charCodeAt(0) + 1 };
  }
  return null;
}

/** actionFor is the id of the action a keydown is, or "". A key pressed while
 *  an input method is composing belongs to it. */
export function actionFor(bindings, e) {
  if (composing(e)) return "";
  return bindings.get(signatureOfEvent(e)) || "";
}

/** keyAction is the action to run for a keydown: the table's action for it if
 *  this window runs that action, otherwise null and the key is left alone. */
export function keyAction(bindings, e, implemented) {
  const id = actionFor(bindings, e);
  return id && implemented.includes(id) ? id : null;
}

/** unreadable are the ids of actions the table binds to something this reader
 *  cannot turn into a key (an unknown modifier in a remap, say), so a window
 *  can say so and not leave the person pressing a key that does nothing. A
 *  range and an unbound action are fine. */
export function unreadable(table) {
  return (table || []).filter((k) => k.keys && !k.keys.includes("…") && signatureOf(k.keys) === "").map((k) => k.id);
}

/** chordOf is the binding a keydown would be written as in the table, or null for
 *  a modifier pressed alone (the key that goes with it is still to come). */
export function chordOf(e) {
  if (MODIFIER_KEYS.includes(e.key)) return null;
  let name = e.key;
  let shift = !!e.shiftKey;
  // Ctrl++ and Ctrl+= are one gesture, as in signatureOfEvent.
  if (name === PLUS) {
    name = EQUALS;
    shift = false;
  }
  if (NAMED[name]) name = NAMED[name];
  else if (name.length === 1) name = name.toUpperCase();
  return [e.ctrlKey ? "Ctrl" : "", shift ? "Shift" : "", e.altKey ? "Alt" : "", name].filter(Boolean).join("+");
}

/** isBareKey says a binding has no Ctrl or Alt and is not a function key, Escape or
 *  Tab: it would still reach a terminal's program instead of the window, so it
 *  is not offered. */
export function isBareKey(chord) {
  const parts = String(chord || "").split("+");
  const name = parts[parts.length - 1];
  if (parts.includes("Ctrl") || parts.includes("Alt")) return false;
  return !(FUNCTION_KEY.test(name) || PAGE_KEYS.includes(name));
}

/** recorded says what a keydown means while a binding is being recorded: { kind:
 *  "wait" } for a modifier alone or an input method, "cancel" (Escape, and Tab,
 *  which also goes on moving the keyboard: pass), "clear" for the key that takes
 *  the binding away, and "chord" with the binding. */
export function recorded(e) {
  if (composing(e)) return { kind: "wait" };
  if (e.key === LIST.tab) return { kind: "cancel", pass: true };
  if (e.key === LIST.cancel) return { kind: "cancel" };
  if (CLEARS.includes(e.key)) return { kind: "clear" };
  const chord = chordOf(e);
  return chord === null ? { kind: "wait" } : { kind: "chord", chord };
}

/** keycaps splits a binding into the parts to draw as keys. */
export function keycaps(spec) {
  return spec ? spec.split("+").map((p) => p.trim()).filter(Boolean) : [];
}

/** listMove is what a keydown does in a list: the index it moves to, "accept",
 *  "cancel", "tab" (which a dialog keeps inside itself), or "" when it is none
 *  of those. */
export function listMove(e, index, count) {
  if (e.key === LIST.down) return count ? (index + 1) % count : 0;
  if (e.key === LIST.up) return count ? (index - 1 + count) % count : 0;
  if (e.key === LIST.tab) return "tab";
  if (e.key === LIST.accept) return "accept";
  if (e.key === LIST.cancel) return "cancel";
  return "";
}
