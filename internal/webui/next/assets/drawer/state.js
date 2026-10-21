// The drawer's own state as a pure function: which pane it shows, whether it
// fills the main area, and where the keyboard goes when it closes. Opening a
// pane while another is shown replaces it; there is never more than one.

/** initialDrawer is a closed drawer. returnTo and seq say where the keyboard
 *  goes after a close: seq grows with each close, so a close of the same pane
 *  twice is two requests. */
export function initialDrawer() {
  return { pane: "", maximised: false, returnTo: "", seq: 0 };
}

/** reduceDrawer is every change to the drawer's state. Events: open (pane),
 *  close, toggleMax, and panes (the ids now in the graph; a drawer whose pane
 *  has left it closes). */
export function reduceDrawer(s, ev) {
  switch (ev.type) {
    case "open":
      if (!ev.pane || ev.pane === s.pane) return s;
      return { ...s, pane: ev.pane };
    case "close":
      if (!s.pane) return s;
      return { pane: "", maximised: false, returnTo: s.pane, seq: s.seq + 1 };
    case "toggleMax":
      return s.pane ? { ...s, maximised: !s.maximised } : s;
    case "panes":
      return s.pane && !ev.ids.has(s.pane) ? { pane: "", maximised: false, returnTo: "", seq: s.seq + 1 } : s;
    default:
      return s;
  }
}

/** focusNode puts the keyboard on the graph box of a pane inside root, or on
 *  root itself when the box is not there (the pane is gone, or never had one). */
export function focusNode(root, pane) {
  if (!root) return false;
  if (pane && root.querySelectorAll) {
    for (const el of root.querySelectorAll("[data-pane]")) {
      if (el.getAttribute("data-pane") === pane && el.getAttribute("tabindex") !== null && el.focus) {
        el.focus();
        return true;
      }
    }
  }
  if (root.focus) root.focus();
  return false;
}
