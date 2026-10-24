// The handle on the border between the main area and the drawer. Dragging it, or
// the arrow keys while it has the keyboard, set the drawer's width; a double click
// or Home puts it back to the default. The width in effect is drawn as the pointer
// moves and is kept when it is let go. The drawer keeps at least DRAWER_MIN, and
// the main area at least CENTRE_MIN.
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { CENTRE_MIN, DRAWER_DEFAULT, DRAWER_MAX, DRAWER_MIN } from "../lib/prefs.js";

/** STEP and BIG_STEP are how far an arrow key, and an arrow key with Shift, move the border. */
export const STEP = 16;
export const BIG_STEP = 64;

// The widest the drawer may be now: what it has plus the main area's room over its minimum.
function widest(el, width) {
  const shell = el && el.closest ? el.closest(".shell") : null;
  const main = shell && shell.querySelector ? shell.querySelector(".main") : null;
  const room = main && Number.isFinite(main.offsetWidth) && main.offsetWidth > 0 ? width + main.offsetWidth - CENTRE_MIN : DRAWER_MAX;
  return Math.max(DRAWER_MIN, Math.min(DRAWER_MAX, room));
}

/** clampWidth keeps a width between the drawer's minimum and the most there is room for. */
export function clampWidth(px, max = DRAWER_MAX) {
  return Math.max(DRAWER_MIN, Math.min(max, Math.round(px)));
}

/** DrawerResize is the handle; width is the width now, onWidth keeps one, onDrag shows one. */
export function DrawerResize({ width, onWidth, onDrag = null }) {
  const onKeyDown = (e) => {
    if (composing(e) || e.ctrlKey || e.altKey || e.metaKey) return;
    const step = e.shiftKey ? BIG_STEP : STEP;
    // The border moves left to widen the drawer.
    const to = e.key === LIST.left ? width + step : e.key === LIST.right ? width - step : e.key === LIST.home ? DRAWER_DEFAULT : null;
    if (to === null) return;
    e.preventDefault();
    e.stopPropagation();
    const next = e.key === LIST.home ? DRAWER_DEFAULT : clampWidth(to, widest(e.currentTarget, width));
    if (next !== width) onWidth(next);
  };
  const onPointerDown = (e) => {
    if (e.button) return;
    const el = e.currentTarget;
    e.preventDefault();
    const from = e.clientX;
    const start = width;
    const max = widest(el, width);
    let last = start;
    if (el.setPointerCapture) {
      try { el.setPointerCapture(e.pointerId); } catch { /* the pointer is already gone */ }
    }
    const move = (m) => {
      last = clampWidth(start + from - m.clientX, max);
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
  return html`<div class="drawer-resize" role="separator" aria-orientation="vertical" aria-label="Drawer width" tabindex="0"
    aria-valuemin=${DRAWER_MIN} aria-valuemax=${DRAWER_MAX} aria-valuenow=${width} aria-valuetext=${width + " pixels"}
    title="Drag to resize. Double-click to reset."
    onKeyDown=${onKeyDown} onPointerDown=${onPointerDown} onDblClick=${() => onWidth(DRAWER_DEFAULT)}></div>`;
}
