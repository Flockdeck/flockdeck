// The window's settings: what the desktop keeps (the preferences of the hello
// and of every prefs push), read defensively, put into effect on the page and
// the terminals, and turned into the commands that change them.
//
// A stored value this window does not understand (a file written by another
// build, or damaged) reads as the default, so a bad value cannot leave the page
// unreadable or a terminal at a size nobody chose. The desktop checks the same
// ranges before it keeps a value; they are repeated here so the dialog can say
// why a value was not sent.

import { PUSH_DELAY_MAX, PUSH_DELAY_MIN, pushOf, pushPrefs } from "./attention.js";
import { wakeOf } from "./wake.js";

/** DEFAULT_FONT is the terminals' font list when none is chosen. */
export const DEFAULT_FONT = '"Cascadia Mono", "JetBrains Mono", Consolas, "SF Mono", Menlo, monospace';

/** THEMES are the palettes: the stored value ("dark" is stored as empty), and the
 *  words for it. */
export const THEMES = [["dark", "Dark"], ["light", "Light"], ["system", "Follow system"]];

/** ACCENTS are the fixed swatches: the stored value (empty is the default), what
 *  a screen reader calls it, and the colour app.css gives it on the dark palette.
 *  There is no free colour: one picked by hand could go illegible on a palette. */
export const ACCENTS = [
  ["", "Cyan (default)", "#4fd1db"],
  ["blue", "Blue", "#6cb0ff"],
  ["purple", "Purple", "#b99af5"],
  ["green", "Green", "#64c97f"],
  ["orange", "Orange", "#f0a552"],
  ["pink", "Pink", "#f28fc0"],
];

/** CURSORS are the shapes of the terminal cursor. */
export const CURSORS = [["block", "Block"], ["bar", "Bar"], ["underline", "Underline"]];

export const FONT_MIN = 8;
export const FONT_MAX = 28;
export const FONT_DEFAULT = 13;
export const SCROLLBACK_MIN = 1000;
export const SCROLLBACK_MAX = 200000;
export const SCROLLBACK_DEFAULT = 10000;
export const FONT_FAMILY_MAX = 200;
/** RAIL_MIN, RAIL_MAX and RAIL_DEFAULT are the widths the rail is drawn at, in pixels. The
 *  desktop keeps a width inside the same bounds. */
export const RAIL_MIN = 180;
export const RAIL_MAX = 480;
export const RAIL_DEFAULT = 220;
/** STATUS_LINES are the choices for Claude Code's usage limits: the stored value
 *  (empty is "auto" to the desktop) and the words for it. */
export const STATUS_LINES = [["auto", "Only where I have a status line"], ["on", "Always"], ["off", "Never"]];
/** SCROLLBACK_COUNTS are the lines offered in the list. */
export const SCROLLBACK_COUNTS = [1000, 5000, 10000, 25000, 50000, 100000, 200000];
/** FONT_SUGGESTIONS are the names the font field offers. */
export const FONT_SUGGESTIONS = ["Cascadia Mono", "Cascadia Code", "JetBrains Mono", "Fira Code", "Consolas",
  "SF Mono", "Menlo", "Source Code Pro", "Ubuntu Mono", "DejaVu Sans Mono"];

const whole = (v) => typeof v === "number" && Number.isInteger(v);

/** prefsOf reads the preferences of a hello or a prefs push into the values the
 *  window uses. Anything missing, of the wrong type or out of range is the
 *  default. */
export function prefsOf(raw) {
  const r0 = raw && typeof raw === "object" ? raw : {};
  // The settings that moved into the interface's own blob (ui) are read from it,
  // and from the old top-level field where the blob has none (an older desktop).
  const ui = r0.ui && typeof r0.ui === "object" && !Array.isArray(r0.ui) ? r0.ui : {};
  const r = { ...r0 };
  for (const k of UI_KEYS) if (Object.prototype.hasOwnProperty.call(ui, k)) r[k] = ui[k];
  const accent = ACCENTS.some(([v]) => v && v === r.accentColor) ? r.accentColor : "";
  const family = typeof r.fontFamily === "string" ? r.fontFamily.trim() : "";
  return {
    ui,
    theme: r.theme === "light" || r.theme === "system" ? r.theme : "dark",
    accent,
    fontSize: whole(r.fontSize) && r.fontSize >= FONT_MIN && r.fontSize <= FONT_MAX ? r.fontSize : FONT_DEFAULT,
    fontFamily: family.length <= FONT_FAMILY_MAX ? family : "",
    scrollback: whole(r.scrollback) && r.scrollback >= SCROLLBACK_MIN && r.scrollback <= SCROLLBACK_MAX ? r.scrollback : SCROLLBACK_DEFAULT,
    cursorStyle: r.cursorStyle === "bar" || r.cursorStyle === "underline" ? r.cursorStyle : "block",
    cursorSteady: r.cursorSteady === true,
    screenReader: r.screenReader === true,
    autoReviewDefault: r.autoReviewDefault === true,
    notificationsOff: r.notificationsOff === true,
    jevStatus: r.jevStatus === true,
    conflictRadar: r.conflictRadar === true,
    push: pushOf(r.push),
    statusLine: r.spend && (r.spend.statusLine === "on" || r.spend.statusLine === "off") ? r.spend.statusLine : "auto",
    railExpanded: r.railExpanded === true,
    railWidth: whole(r.railWidth) && r.railWidth >= RAIL_MIN && r.railWidth <= RAIL_MAX ? r.railWidth : RAIL_DEFAULT,
    wake: wakeOf(r.wake),
    helpSeen: r.helpSeen === true,
    dismissedTips: Array.isArray(r.dismissedTips) ? r.dismissedTips.filter((t) => typeof t === "string").length : 0,
  };
}

/** fontStack is the font list the terminals use: the one chosen, with monospace
 *  behind it so a font this machine lacks falls back to a fixed-width one. */
export function fontStack(family) {
  return family ? family + ", monospace" : DEFAULT_FONT;
}

/** termOptionsOf is what the preferences set on a terminal. reduced says the
 *  person asked the system for less motion, which stops the cursor blinking. */
export function termOptionsOf(p, reduced = false) {
  return {
    fontFamily: fontStack(p.fontFamily),
    fontSize: p.fontSize,
    cursorStyle: p.cursorStyle,
    cursorBlink: !p.cursorSteady && !reduced,
    scrollback: p.scrollback,
    screenReaderMode: p.screenReader,
  };
}

/** reducedMotion asks the page whether the person wants less motion. */
export function reducedMotion(win) {
  try {
    return !!(win && win.matchMedia && win.matchMedia("(prefers-reduced-motion: reduce)").matches);
  } catch {
    return false;
  }
}

/** applyLook puts the palette and accent on the document, which is what app.css's
 *  [data-theme] and [data-accent] rules key off. "Follow system" leaves no
 *  theme, so the stylesheet's media query decides. */
export function applyLook(win, p) {
  const root = win && win.document && win.document.documentElement;
  if (!root || !root.dataset) return;
  if (p.theme === "system") delete root.dataset.theme;
  else root.dataset.theme = p.theme;
  if (p.accent) root.dataset.accent = p.accent;
  else delete root.dataset.accent;
}

/** PATCH_WAIT_MS is how long a change shown before the desktop answers is kept
 *  if no answer ever matches it (another window changed the same setting). */
export const PATCH_WAIT_MS = 5000;

const bare = (v) => (v === undefined || v === null || v === "" || v === false || (Array.isArray(v) && v.length === 0) ? null : v);

const same = (a, b) => JSON.stringify(bare(a) ?? null) === JSON.stringify(bare(b) ?? null);

/** addPending records the values this window just sent (patch, by preference) at
 *  the end of what it is already waiting on, in the order they were sent. The
 *  desktop answers each command in turn, so its pushes come back as a stale
 *  answer to an earlier one before the last. */
export function addPending(pending, patch) {
  const next = { ...pending };
  for (const [k, v] of Object.entries(patch)) next[k] = [...(next[k] || []), v];
  return next;
}

/** latestOf is what the window shows while it waits: the last value sent for
 *  each setting. */
export function latestOf(pending) {
  const out = {};
  for (const [k, list] of Object.entries(pending)) out[k] = list[list.length - 1];
  return out;
}

/** settledPending is what is still waiting after a prefs push (raw) arrived, when
 *  the push before it was prev. A push that matches one of the values sent for a
 *  setting (the first that does, as the answers come in order) answers up to and
 *  including that one, and the later ones keep waiting.
 *  A push that matches none, and moved the setting from where it was, is another
 *  window's word and ends the wait. A push that left the setting as it was is
 *  about something else, and the wait goes on. A missing or empty value is the
 *  same as the default's, as the desktop leaves those out. */
export function settledPending(pending, raw, prev) {
  const r = raw && typeof raw === "object" ? raw : {};
  const q = prev && typeof prev === "object" ? prev : {};
  const kept = {};
  for (const [k, list] of Object.entries(pending)) {
    let at = -1;
    list.forEach((v, i) => { if (at < 0 && same(r[k], v)) at = i; });
    if (at >= 0) {
      if (at < list.length - 1) kept[k] = list.slice(at + 1);
    } else if (same(r[k], q[k])) {
      kept[k] = list;
    }
  }
  return Object.keys(kept).length === Object.keys(pending).length && Object.keys(kept).every((k) => kept[k] === pending[k]) ? pending : kept;
}

/** WHY says why a value was not sent. */
export const WHY = {
  fontFamily: "That is too long for a list of fonts: " + FONT_FAMILY_MAX + " characters at most.",
  scrollback: "Scrollback is between " + SCROLLBACK_MIN.toLocaleString("en") + " and " + SCROLLBACK_MAX.toLocaleString("en") + " lines.",
};

/** UI_KEYS are the settings kept in the interface's own blob (prefs.ui) and not as
 *  fields of their own: the desktop stores and returns them without reading them. */
export const UI_KEYS = ["fontSize", "fontFamily", "scrollback", "helpSeen"];

/** UI_CAP is the name of the feature that says the desktop takes the blob. */
export const UI_CAP = "uiPrefs";

// uiSetting is the change of one key of the blob: the uiPrefs command, and the
// blob as the desktop will return it (keys in order, as it sends them). A value of
// null removes the key. Without the feature the old command is used instead.
function uiSetting(name, value, current, caps, legacy) {
  if (!Array.isArray(caps) || !caps.includes(UI_CAP)) return legacy;
  const was = current && current.ui && typeof current.ui === "object" && !Array.isArray(current.ui) ? current.ui : {};
  const next = { ...was };
  if (value === null) delete next[name];
  else next[name] = value;
  const ordered = {};
  for (const k of Object.keys(next).sort()) ordered[k] = next[k];
  return { command: { cmd: "uiPrefs", ui: { [name]: value } }, patch: { ui: ordered } };
}

/** setting is what changing one setting does: the command for the desktop and
 *  the preference as the desktop will keep it, which the window shows at once
 *  and until the desktop's own push arrives. It is null for a value that would
 *  not be kept. */
export function setting(name, value, current = null, caps = null) {
  switch (name) {
    case "theme":
      if (!THEMES.some(([v]) => v === value)) return null;
      return { command: { cmd: "theme", text: value }, patch: { theme: value === "dark" ? "" : value } };
    case "accent":
      if (!ACCENTS.some(([v]) => v === value)) return null;
      return { command: { cmd: "accentColor", text: value }, patch: { accentColor: value } };
    case "fontSize": {
      if (!whole(value) || value < FONT_MIN || value > FONT_MAX) return null;
      return uiSetting("fontSize", value, current, caps, { command: { cmd: "fontSize", size: value }, patch: { fontSize: value } });
    }
    case "fontFamily": {
      const family = typeof value === "string" ? value.trim() : null;
      if (family === null || family.length > FONT_FAMILY_MAX) return null;
      return uiSetting("fontFamily", family === "" ? null : family, current, caps, { command: { cmd: "fontFamily", text: family }, patch: { fontFamily: family } });
    }
    case "scrollback":
      if (!whole(value) || value < SCROLLBACK_MIN || value > SCROLLBACK_MAX) return null;
      return uiSetting("scrollback", value, current, caps, { command: { cmd: "scrollback", size: value }, patch: { scrollback: value } });
    case "cursorStyle":
      if (!CURSORS.some(([v]) => v === value)) return null;
      return { command: { cmd: "cursorStyle", text: value }, patch: { cursorStyle: value === "block" ? "" : value } };
    case "cursorSteady":
      return { command: { cmd: "cursorBlink", kind: value ? "off" : "on" }, patch: { cursorSteady: !!value } };
    case "screenReader":
      return { command: { cmd: "screenReader", kind: value ? "on" : "off" }, patch: { screenReader: !!value } };
    case "autoReviewDefault":
      return { command: { cmd: "autoReviewDefault", kind: value ? "on" : "off" }, patch: { autoReviewDefault: !!value } };
    case "conflictRadar":
      return { command: { cmd: "conflictRadar", kind: value ? "on" : "off" }, patch: { conflictRadar: !!value } };
    case "jevStatus":
      return { command: { cmd: "jevStatus", kind: value ? "on" : "off" }, patch: { jevStatus: !!value } };
    case "notificationsOff":
      return { command: { cmd: "notifications", kind: value ? "off" : "on" }, patch: { notificationsOff: !!value } };
    case "pushOff":
    case "pushAnonymous":
    case "pushDelay": {
      // The push preference is one object on the desktop. The change is made on
      // what is shown now, and is kept as the desktop will keep it.
      const now = pushOf(current && current.push);
      if (name === "pushDelay") {
        if (!whole(value) || value < PUSH_DELAY_MIN || value > PUSH_DELAY_MAX) return null;
        return { command: { cmd: "pushDelay", size: value }, patch: { push: pushPrefs({ ...now, delaySeconds: value }) } };
      }
      if (name === "pushOff") return { command: { cmd: "pushNotify", kind: value ? "off" : "on" }, patch: { push: pushPrefs({ ...now, off: !!value }) } };
      return { command: { cmd: "pushAnonymous", kind: value ? "on" : "off" }, patch: { push: pushPrefs({ ...now, anonymous: !!value }) } };
    }
    case "statusLine": {
      if (!STATUS_LINES.some(([v]) => v === value)) return null;
      const spend = current && current.spend && typeof current.spend === "object" ? current.spend : {};
      return { command: { cmd: "statusLine", text: value }, patch: { spend: { ...spend, statusLine: value === "auto" ? "" : value } } };
    }
    case "railExpanded":
      return { command: { cmd: "railExpanded", kind: value ? "on" : "off" }, patch: { railExpanded: !!value } };
    case "railWidth":
      if (!whole(value) || value < RAIL_MIN || value > RAIL_MAX) return null;
      return { command: { cmd: "railWidth", size: value }, patch: { railWidth: value } };
    case "helpSeen":
      return uiSetting("helpSeen", true, current, caps, { command: { cmd: "helpSeen" }, patch: { helpSeen: true } });
    case "tabs":
      // The goal tabs' choices: an object, or null for none. With no uiPrefs there is nowhere to keep them.
      if (value !== null && (typeof value !== "object" || Array.isArray(value))) return null;
      return uiSetting("tabs", value, current, caps, null);
    case "resetTips":
      return { command: { cmd: "resetTips" }, patch: { dismissedTips: [] } };
    default:
      return null;
  }
}
