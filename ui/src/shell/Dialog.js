// The frame of a modal dialog: the scrim, the titled box, and the keyboard rules
// every dialog here keeps. The keyboard goes to the field named by focus (or the
// first control) when it opens and back to where it was when it closes, Tab stays
// inside, and Escape closes it. A child that wants Escape for itself (a search
// field with words in it) stops the event before it gets here.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./Start.js";

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
    if (target && target.focus) target.focus();
    return () => {
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
