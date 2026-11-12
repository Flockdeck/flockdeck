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
 *  from; onClose(true) is called by Escape, which should give the button the keyboard;
 *  onClose(false) by a press or focus outside; onClose("gone") when the button left the page. */
export function Popover({ anchor, label, side = "below", onClose, win = null, children }) {
  const box = useRef(null);
  // The latest onClose: the owner makes a new one on every render, and the
  // listeners below are not set again for that.
  const closing = useRef(onClose);
  closing.current = onClose;
  // A press outside, or focus leaving the panel, closes it. It takes the
  // keyboard when it opens, and again when it comes from another button.
  useEffect(() => {
    const el = box.current;
    const pick = el ? el.querySelector("button") : null;
    if (pick && pick.focus) pick.focus();
    else if (el && el.focus) el.focus();
    const doc = el && el.ownerDocument;
    const inside = (n) => !!n && ((box.current && box.current.contains(n)) || !!(anchor && anchor.contains && anchor.contains(n)));
    const away = (e) => { if (!inside(e.target)) closing.current(false); };
    // Tab out of the panel (or a click that moves focus) leaves with a target that is not in it. A null target is the window losing focus, which keeps the panel.
    const left = (e) => { if (e.relatedTarget && !inside(e.relatedTarget)) closing.current(false); };
    if (doc && doc.addEventListener) doc.addEventListener("mousedown", away);
    if (el && el.addEventListener) el.addEventListener("focusout", left);
    return () => {
      if (doc && doc.removeEventListener) doc.removeEventListener("mousedown", away);
      if (el && el.removeEventListener) el.removeEventListener("focusout", left);
    };
  }, [anchor]);
  // The badge it came from can go while the panel is open (the count fell to
  // nothing and the owner drew no button). The panel goes with it, and onClose("gone")
  // says the keyboard cannot go back to that button.
  useEffect(() => {
    const doc = anchor && anchor.ownerDocument;
    if (!anchor || !doc || !doc.body || !doc.body.contains(anchor)) closing.current("gone");
  });
  const rows = () => (box.current ? [...box.current.querySelectorAll("button")].filter((b) => !b.disabled) : []);
  const onKeyDown = (e) => {
    if (composing(e) || e.ctrlKey || e.altKey || e.metaKey) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onClose(true);
      return;
    }
    // Tab walks the rows, as the browser does; leaving the panel closes it (focusout above).
    const down = e.key === LIST.down, up = e.key === LIST.up, home = e.key === LIST.home, end = e.key === LIST.end;
    if (!down && !up && !home && !end) return;
    const list = rows();
    if (list.length === 0) return;
    e.preventDefault();
    e.stopPropagation();
    const at = list.indexOf(e.target);
    const to = home ? 0 : end ? list.length - 1 : down ? (at + 1) % list.length : (at - 1 + list.length) % list.length;
    if (list[to] && list[to].focus) list[to].focus();
  };
  const rect = anchor && anchor.getBoundingClientRect ? anchor.getBoundingClientRect() : null;
  return html`<div class="popover" role="dialog" aria-label=${label} tabindex="-1" ref=${box} style=${placeOf(rect, side, win || (typeof window !== "undefined" ? window : null)) || undefined} onKeyDown=${onKeyDown}>${children}</div>`;
}
