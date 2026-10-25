// The frame of a modal dialog: the scrim, the titled box, and the keyboard rules
// every dialog here keeps. The keyboard goes to the field named by focus (or the
// first control) when it opens and back to where it was when it closes, Tab stays
// inside, and Escape closes it. A child that wants Escape for itself (a search
// field with words in it) stops the event before it gets here.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./Start.js";

/** isLive says an element announces changes (a status line, an alert, a toast), so a dialog leaves it alone when it makes the rest of the page inert. */
function isLive(el) {
  const role = el.getAttribute && el.getAttribute("role");
  const live = el.getAttribute && el.getAttribute("aria-live");
  return role === "status" || role === "alert" || (!!live && live !== "off") || /(^|\s)toasts?(\s|$)/.test(String(el.getAttribute && el.getAttribute("class")));
}

/** makeInert marks everything on the page outside the dialog's scrim inert, so the keyboard, a pointer and a screen reader's virtual cursor stay in the dialog. It walks from the scrim up to the body and marks the siblings at each level, leaving what announces changes alone and what was already inert as it was. It returns the function that puts them back. */
export function makeInert(scrim) {
  const marked = [];
  const body = scrim.ownerDocument && scrim.ownerDocument.body;
  for (let node = scrim; node && node.parentNode && node !== body; node = node.parentNode) {
    for (const sib of Array.from(node.parentNode.childNodes || []).filter((n) => n.nodeType === 1)) {
      if (sib === node || !sib.setAttribute || sib.getAttribute("inert") != null || isLive(sib)) continue;
      const tag = String(sib.localName || "");
      if (tag === "script" || tag === "style" || tag === "link") continue;
      sib.setAttribute("inert", "");
      marked.push(sib);
    }
  }
  return () => marked.forEach((el) => el.removeAttribute && el.removeAttribute("inert"));
}

/** Dialog draws its children in the frame. id names the title for a screen
 *  reader, focus is a selector for the control that takes the keyboard first, and
 *  wide gives the box more room. */
export function Dialog({ id, title, onClose, focus = "", wide = false, children }) {
  const rootRef = useRef(null);
  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    const target = el && el.querySelector ? (focus && el.querySelector(focus)) || el.querySelector("input, button, select, textarea") : null;
    // The page behind is inert while the box is open; the focus is put back after it is live again.
    const scrim = el && el.parentNode;
    const release = scrim ? makeInert(scrim) : () => {};
    if (target && target.focus) target.focus();
    return () => {
      release();
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onClose();
    } else if (e.key === LIST.tab) {
      trapTab(e, rootRef.current);
    }
  };
  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class=${"dialog" + (wide ? " dialog--wide" : "")} role="dialog" aria-modal="true" aria-labelledby=${id + "-title"} ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id=${id + "-title"} class="dialog-title">${title}</h2>
        ${children}
      </div>
    </div>
  `;
}
