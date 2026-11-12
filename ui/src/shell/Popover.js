// A small panel anchored to a button: it holds a list the person picks from. It
// takes the keyboard when it opens, Escape closes it and gives the keyboard back
// to the button, and a press outside closes it. It is positioned over the page
// (fixed), so a strip that scrolls or clips its own children does not cut it.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";

const WIDTH = 340;

/** placeOf is where the panel goes for the box of its button: beside it
 *  ("right") or under it ("below"), kept inside the window. */
export function placeOf(rect, side, win) {
  if (!rect) return "";
  const w = (win && win.innerWidth) || 1024;
  const left = side === "right" ? rect.right + 6 : rect.left;
  const top = side === "right" ? rect.top : rect.bottom + 4;
  return "left:" + Math.max(8, Math.min(left, w - WIDTH - 8)) + "px;top:" + Math.max(8, top) + "px";
}

/** Popover draws children in a panel named `label`. anchor is the button it came
 *  from; onClose(true) is called by Escape, which should give the button the keyboard. */
export function Popover({ anchor, label, side = "below", onClose, win = null, children }) {
  const box = useRef(null);
  useEffect(() => {
    const el = box.current;
    const pick = el ? el.querySelector("button") : null;
    if (pick && pick.focus) pick.focus();
    else if (el && el.focus) el.focus();
    const doc = el && el.ownerDocument;
    const away = (e) => {
      if (box.current && !box.current.contains(e.target) && !(anchor && anchor.contains && anchor.contains(e.target))) onClose(false);
    };
    if (doc && doc.addEventListener) doc.addEventListener("mousedown", away);
    return () => { if (doc && doc.removeEventListener) doc.removeEventListener("mousedown", away); };
  }, []);
  const onKeyDown = (e) => {
    if (composing(e) || e.ctrlKey || e.altKey || e.metaKey) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onClose(true);
    } else if (e.key === LIST.tab) {
      onClose(true);
    }
  };
  const rect = anchor && anchor.getBoundingClientRect ? anchor.getBoundingClientRect() : null;
  return html`<div class="popover" role="dialog" aria-label=${label} tabindex="-1" ref=${box} style=${placeOf(rect, side, win || (typeof window !== "undefined" ? window : null)) || undefined} onKeyDown=${onKeyDown}>${children}</div>`;
}
