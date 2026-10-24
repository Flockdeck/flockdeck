// The handle on the border between the main area and the drawer. Dragging it, or
// the arrow keys while it has the keyboard, set the drawer's width; a double click
// or Home puts it back to the default. The width in effect is drawn as the pointer
// moves and is kept when it is let go. The drawer keeps at least DRAWER_MIN, and
// the main area at least CENTRE_MIN.
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { CENTRE_MIN, DRAWER_DEFAULT, DRAWER_H_DEFAULT, DRAWER_H_MAX, DRAWER_H_MIN, DRAWER_MAX, DRAWER_MIN, MAIN_H_MIN } from "../lib/prefs.js";

/** STEP and BIG_STEP are how far an arrow key, and an arrow key with Shift, move the border. */
export const STEP = 16;
export const BIG_STEP = 64;

// The limits of a drawer beside the main area (widths) and below it (heights).
const SIDE = { min: DRAWER_MIN, max: DRAWER_MAX, rest: CENTRE_MIN, size: "offsetWidth", dflt: DRAWER_DEFAULT };
const STACK = { min: DRAWER_H_MIN, max: DRAWER_H_MAX, rest: MAIN_H_MIN, size: "offsetHeight", dflt: DRAWER_H_DEFAULT };

// The biggest the drawer may be now: what it has plus the main area's room over its minimum.
function widest(el, size, lim) {
  const shell = el && el.closest ? el.closest(".shell") : null;
  const main = shell && shell.querySelector ? shell.querySelector(".main") : null;
  const has = main && Number.isFinite(main[lim.size]) && main[lim.size] > 0;
  return Math.max(lim.min, Math.min(lim.max, has ? size + main[lim.size] - lim.rest : lim.max));
}

/** clampWidth keeps a size between the drawer's minimum and the most there is room for. */
export function clampWidth(px, max = DRAWER_MAX, min = DRAWER_MIN) {
  return Math.max(min, Math.min(max, Math.round(px)));
}

/** DrawerResize is the handle; width is the width now, onWidth keeps one, onDrag shows one. */
export function DrawerResize({ width, onWidth, onDrag = null, vertical = false }) {
  const lim = vertical ? STACK : SIDE;
  const grow = vertical ? LIST.up : LIST.left;
  const shrink = vertical ? LIST.down : LIST.right;
  const onKeyDown = (e) => {
    if (composing(e) || e.ctrlKey || e.altKey || e.metaKey) return;
    const step = e.shiftKey ? BIG_STEP : STEP;
    // The border moves toward the main area to make the drawer bigger: left, or up when it is below.
    const to = e.key === grow ? width + step : e.key === shrink ? width - step : e.key === LIST.home ? lim.dflt : null;
    if (to === null) return;
    e.preventDefault();
    e.stopPropagation();
    const next = e.key === LIST.home ? lim.dflt : clampWidth(to, widest(e.currentTarget, width, lim), lim.min);
    if (next !== width) onWidth(next);
  };
  const onPointerDown = (e) => {
    if (e.button) return;
    const el = e.currentTarget;
    e.preventDefault();
    const from = vertical ? e.clientY : e.clientX;
    const start = width;
    const max = widest(el, width, lim);
    let last = start;
    if (el.setPointerCapture) {
      try { el.setPointerCapture(e.pointerId); } catch { /* the pointer is already gone */ }
    }
    const move = (m) => {
      last = clampWidth(start + from - (vertical ? m.clientY : m.clientX), max, lim.min);
      if (onDrag) onDrag(last);
    };
    const done = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", done);
      el.removeEventListener("pointercancel", done);
      onWidth(last);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", done);
    el.addEventListener("pointercancel", done);
  };
  return html`<div class=${"drawer-resize" + (vertical ? " drawer-resize--h" : "")} role="separator" aria-orientation=${vertical ? "horizontal" : "vertical"} aria-label=${vertical ? "Drawer height" : "Drawer width"} tabindex="0"
    aria-valuemin=${lim.min} aria-valuemax=${lim.max} aria-valuenow=${width} aria-valuetext=${width + " pixels"}
    title="Drag to resize. Double-click to reset."
    onKeyDown=${onKeyDown} onPointerDown=${onPointerDown} onDblClick=${() => onWidth(lim.dflt)}></div>`;
}
