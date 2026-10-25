// Makes the page behind a modal dialog inert while it is open, so the keyboard, a
// pointer and a screen reader's virtual cursor stay in the dialog. Every modal
// calls useInert on its box (or ModalScrim, for a bare scrim).
//
// It walks from the scrim up to the body and marks the siblings at each level.
// What must stay usable is left alone: an element marked data-keep-live (the
// pending requests that wait for an answer, the toasts) or one that is itself a
// status, an alert or a live region. Nodes added to the page after the dialog
// opened are not marked and stay live. Dialogs can overlap, so a node is
// released only when the last dialog that marked it lets go.
import { useEffect, useRef } from "../vendor/hooks.mjs";
import { html } from "../html.js";

/** holds is how many open dialogs have marked each element inert. */
const holds = new Map();

/** keepsLive says an element must stay usable under a modal dialog. */
function keepsLive(el) {
  const get = (k) => (el.getAttribute ? el.getAttribute(k) : null);
  const role = get("role");
  const live = get("aria-live");
  return get("data-keep-live") != null || role === "status" || role === "alert" || (!!live && live !== "off");
}

/** makeInert marks everything on the page outside the scrim inert and returns the function that puts it back. */
export function makeInert(scrim) {
  const marked = [];
  const body = scrim.ownerDocument && scrim.ownerDocument.body;
  for (let node = scrim; node && node.parentNode && node !== body; node = node.parentNode) {
    for (const sib of Array.from(node.parentNode.childNodes || []).filter((n) => n.nodeType === 1)) {
      if (sib === node || !sib.setAttribute || keepsLive(sib)) continue;
      const tag = String(sib.localName || "");
      if (tag === "script" || tag === "style" || tag === "link") continue;
      if (holds.has(sib)) holds.set(sib, holds.get(sib) + 1);
      else if (sib.getAttribute("inert") != null) continue;
      else {
        sib.setAttribute("inert", "");
        holds.set(sib, 1);
      }
      marked.push(sib);
    }
  }
  let released = false;
  return () => {
    if (released) return;
    released = true;
    for (const el of marked) {
      const n = (holds.get(el) || 1) - 1;
      if (n > 0) holds.set(el, n);
      else {
        holds.delete(el);
        if (el.removeAttribute) el.removeAttribute("inert");
      }
    }
  };
}

/** useInert makes the page inert while the component is mounted. ref is the dialog box, a child of the scrim; with self set it is the scrim. */
export function useInert(ref, self = false) {
  useEffect(() => {
    const el = ref.current;
    const scrim = el && (self ? el : el.parentNode);
    return scrim ? makeInert(scrim) : undefined;
  }, []);
}

/** ModalScrim is a bare scrim around a question, with the page behind it inert. */
export function ModalScrim({ children, ...props }) {
  const ref = useRef(null);
  useInert(ref, true);
  return html`<div class="dialog-scrim" ...${props} ref=${ref}>${children}</div>`;
}
