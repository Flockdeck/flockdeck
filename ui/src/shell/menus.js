// The right-click menus, without the drawing: which menu a click is on, whether
// the page's own menu should win, the items a menu has and where it goes. The
// component (ContextMenu.js) draws what this decides, and a test can run this
// without a page.
//
// A part of the page opts in with data-ctx="<kind>" (and whatever data-
// attributes the kind reads). A kind is a function in MENUS from that element
// and the page's ctx bag to a list of items. Nothing with no data-ctx gets a
// menu of ours, so the surfaces can be taken on one at a time.

import { keycaps } from "./keys.js";
import { nameOf } from "./palette.js";
import { agentMenu, termMenu } from "./panemenus.js";

/** MARGIN is the least space left between a menu and the edge of the window. */
export const MARGIN = 8;

/** ATTR is the attribute that gives a part of the page a menu. */
export const ATTR = "data-ctx";

/** MENUS is every menu, by the kind a data-ctx names. Each is (el, ctx, ev) => Item[]
 *  where el is the element carrying data-ctx and ctx is the bag Live.js builds
 *  (see Live.js, "menuCtx"), with keys, implemented and act added by the Shell.
 *
 *  An Item is { id, label?, action?, run?, danger?, disabled?, hidden?, separator?,
 *  checked? }. One with an action is an action of the key table: it takes its
 *  label and its key hint from the table, shows only when this window runs the
 *  action, and runs through the same path as a key, so a viewer or a relay is
 *  told no there as it is for the key. One with a run is a function of its own.
 *  { separator: true } draws a rule; checked (true or false) makes the item a
 *  check box. */
export const MENUS = {
  agent: agentMenu,
  term: termMenu,
};

/** ctxElement is the nearest element at or above target that carries data-ctx,
 *  or null. It walks parentNode rather than asking the element to find itself,
 *  so it is the same on a real page and in a test's. */
export function ctxElement(target) {
  for (let n = target; n && n.nodeType === 1; n = n.parentNode) {
    if (n.getAttribute && n.getAttribute(ATTR)) return n;
  }
  return null;
}

/** isField is whether an element is somewhere the person types, where the
 *  window's own menu (spelling, paste, undo) is the right one. */
export function isField(el) {
  for (let n = el; n && n.nodeType === 1; n = n.parentNode) {
    const tag = n.localName;
    // xterm's hidden textarea takes the keyboard for the terminal; it is not
    // somewhere the person types, and the terminal has a menu of its own.
    if (n.getAttribute && /(^|\s)xterm-helper-textarea(\s|$)/.test(n.getAttribute("class") || "")) return false;
    if (tag === "input" || tag === "textarea" || tag === "select") return true;
    if (n.isContentEditable) return true;
    const ce = n.getAttribute && n.getAttribute("contenteditable");
    if (ce !== null && ce !== undefined && ce !== "false") return true;
  }
  return false;
}

/** hasSelection is whether text is selected inside el: a right-click on it then
 *  wants the window's own menu, with Copy. A selection elsewhere in the page
 *  does not count. */
export function hasSelection(win, el) {
  const sel = win && win.getSelection ? win.getSelection() : null;
  if (!sel || !String(sel).trim()) return false;
  const inside = (n) => !!n && !!el && (el === n || (el.contains && el.contains(n)));
  return inside(sel.anchorNode) || inside(sel.focusNode);
}

/** nativeWanted is whether the click should be left to the window's own menu. */
export function nativeWanted(target, win, el) {
  return isField(target) || hasSelection(win, el);
}

/** hintFor is the key bound to an action, drawn as the palette draws it, or "". */
export function hintFor(keys, id) {
  const k = (keys || []).find((r) => r.id === id);
  return k && k.keys ? keycaps(k.keys).join("+") : "";
}

/** resolve turns the items a menu lists into what is drawn: the hidden ones and
 *  the actions this window does not run left out, labels and key hints taken
 *  from the key table, and the rules tidied so none leads, trails or doubles. */
export function resolve(items, ctx) {
  const keys = (ctx && ctx.keys) || [];
  const have = new Set((ctx && ctx.implemented) || []);
  const out = [];
  for (const it of items || []) {
    if (!it || it.hidden) continue;
    if (it.separator) {
      if (out.length && !out[out.length - 1].separator) out.push({ separator: true });
      continue;
    }
    if (it.action && !have.has(it.action)) continue;
    const row = it.action ? keys.find((k) => k.id === it.action) : null;
    const act = it.action;
    out.push({
      id: it.id || act,
      label: it.label || (row ? nameOf(row) : act),
      hint: act ? hintFor(keys, act) : "",
      run: it.run || (() => ctx && ctx.act && ctx.act(act)),
      danger: !!it.danger,
      disabled: !!it.disabled,
      checked: it.checked,
    });
  }
  while (out.length && out[out.length - 1].separator) out.pop();
  return out;
}

/** build is the items for a kind of menu, resolved, or [] for a kind that has
 *  none (the window's own menu is then left alone). */
export function build(kind, el, ctx, ev) {
  const make = MENUS[kind];
  return make ? resolve(make(el, ctx, ev), ctx) : [];
}

const canFocus = (it) => !it.separator && !it.disabled;

/** nextItem is the index of the item to move to from at, by step (1 or -1),
 *  wrapping and passing rules and disabled items, or -1 when there is none. */
export function nextItem(items, at, step) {
  const n = items.length;
  for (let i = 1; i <= n; i++) {
    const to = (((at + step * i) % n) + n) % n;
    if (canFocus(items[to])) return to;
  }
  return -1;
}

/** edgeItem is the first (to 0) or last (any other to) item that can be chosen,
 *  for the keys that go to either end. */
export function edgeItem(items, to) {
  if (to <= 0) return nextItem(items, -1, 1);
  return nextItem(items, items.length, -1);
}

/** typeAhead is the index of the next item after at whose label starts with ch,
 *  ignoring case, or -1. */
export function typeAhead(items, at, ch) {
  const c = String(ch).toLowerCase();
  const n = items.length;
  for (let i = 1; i <= n; i++) {
    const to = (((at + i) % n) + n) % n;
    if (canFocus(items[to]) && items[to].label.toLowerCase().startsWith(c)) return to;
  }
  return -1;
}

/** place is where a menu w by h goes for a click at (x, y) in a window vw by
 *  vh: at the point, flipped to the left of it and above it when it would run
 *  off, then kept MARGIN inside the edges. A menu taller than the window is
 *  given the room there is and scrolls. */
export function place({ x, y, w, h, vw, vh }) {
  const maxHeight = Math.max(vh - 2 * MARGIN, 0);
  const height = Math.min(h, maxHeight);
  let left = x + w + MARGIN > vw ? x - w : x;
  let top = y + height + MARGIN > vh ? y - height : y;
  left = Math.max(MARGIN, Math.min(left, vw - w - MARGIN));
  top = Math.max(MARGIN, Math.min(top, vh - height - MARGIN));
  return { left, top, maxHeight };
}

/** pointOf is where a contextmenu event wants the menu: the pointer, or, for
 *  the keyboard (Shift+F10 and the menu key send an event with no position), the
 *  lower left corner of the element the keyboard is on. */
export function pointOf(e, el) {
  const keyboard = !e.clientX && !e.clientY;
  if (keyboard && el && el.getBoundingClientRect) {
    const r = el.getBoundingClientRect();
    return { x: r.left, y: r.bottom };
  }
  return { x: e.clientX || 0, y: e.clientY || 0 };
}
