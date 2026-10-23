// The window's settings: what the desktop keeps (the preferences of the hello
// and of every prefs push), read defensively, put into effect on the page and
// the terminals, and turned into the commands that change them.
//
// A stored value this window does not understand (a file written by another
// build, or damaged) reads as the default, so a bad value cannot leave the page
// unreadable or a terminal at a size nobody chose. The desktop checks the same
// ranges before it keeps a value; they are repeated here so the dialog can say
// why a value was not sent.

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
  const r = raw && typeof raw === "object" ? raw : {};
  const accent = ACCENTS.some(([v]) => v && v === r.accentColor) ? r.accentColor : "";
  const family = typeof r.fontFamily === "string" ? r.fontFamily.trim() : "";
  return {
    theme: r.theme === "light" || r.theme === "system" ? r.theme : "dark",
    accent,
    fontSize: whole(r.fontSize) && r.fontSize >= FONT_MIN && r.fontSize <= FONT_MAX ? r.fontSize : FONT_DEFAULT,
    fontFamily: family.length <= FONT_FAMILY_MAX ? family : "",
    scrollback: whole(r.scrollback) && r.scrollback >= SCROLLBACK_MIN && r.scrollback <= SCROLLBACK_MAX ? r.scrollback : SCROLLBACK_DEFAULT,
    cursorStyle: r.cursorStyle === "bar" || r.cursorStyle === "underline" ? r.cursorStyle : "block",
    cursorSteady: r.cursorSteady === true,
    screenReader: r.screenReader === true,
    autoReviewDefault: r.autoReviewDefault === true,
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

/** WHY says why a value was not sent. */
export const WHY = {
  fontFamily: "That is too long for a list of fonts: " + FONT_FAMILY_MAX + " characters at most.",
  scrollback: "Scrollback is between " + SCROLLBACK_MIN.toLocaleString("en") + " and " + SCROLLBACK_MAX.toLocaleString("en") + " lines.",
};

/** setting is what changing one setting does: the command for the desktop and
 *  the preference as the desktop will keep it, which the window shows at once
 *  and until the desktop's own push arrives. It is null for a value that would
 *  not be kept. */
export function setting(name, value) {
  switch (name) {
    case "theme":
      if (!THEMES.some(([v]) => v === value)) return null;
      return { command: { cmd: "theme", text: value }, patch: { theme: value === "dark" ? "" : value } };
    case "accent":
      if (!ACCENTS.some(([v]) => v === value)) return null;
      return { command: { cmd: "accentColor", text: value }, patch: { accentColor: value } };
    case "fontSize": {
      if (!whole(value) || value < FONT_MIN || value > FONT_MAX) return null;
      return { command: { cmd: "fontSize", size: value }, patch: { fontSize: value } };
    }
    case "fontFamily": {
      const family = typeof value === "string" ? value.trim() : null;
      if (family === null || family.length > FONT_FAMILY_MAX) return null;
      return { command: { cmd: "fontFamily", text: family }, patch: { fontFamily: family } };
    }
    case "scrollback":
      if (!whole(value) || value < SCROLLBACK_MIN || value > SCROLLBACK_MAX) return null;
      return { command: { cmd: "scrollback", size: value }, patch: { scrollback: value } };
    case "cursorStyle":
      if (!CURSORS.some(([v]) => v === value)) return null;
      return { command: { cmd: "cursorStyle", text: value }, patch: { cursorStyle: value === "block" ? "" : value } };
    case "cursorSteady":
      return { command: { cmd: "cursorBlink", kind: value ? "off" : "on" }, patch: { cursorSteady: !!value } };
    case "screenReader":
      return { command: { cmd: "screenReader", kind: value ? "on" : "off" }, patch: { screenReader: !!value } };
    case "autoReviewDefault":
      return { command: { cmd: "autoReviewDefault", kind: value ? "on" : "off" }, patch: { autoReviewDefault: !!value } };
    case "resetTips":
      return { command: { cmd: "resetTips" }, patch: { dismissedTips: [] } };
    default:
      return null;
  }
}
