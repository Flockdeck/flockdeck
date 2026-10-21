// The drawer's own state as a pure function: which pane it shows, whether it
// fills the main area, and where the keyboard goes when it closes. Opening a
// pane while another is shown replaces it; there is never more than one.

/** initialDrawer is a closed drawer. returnTo and seq say where the keyboard
 *  goes after a close: seq grows with each close, so a close of the same pane
 *  twice is two requests. via is where the drawer was opened from: "strip" for a
 *  row of the needs-you strip, "" for a box of the graph; returnVia is the via of
 *  the drawer that closed. */
export function initialDrawer() {
  return { pane: "", via: "", maximised: false, returnTo: "", returnVia: "", seq: 0 };
}

/** reduceDrawer is every change to the drawer's state. Events: open (pane),
 *  close, toggleMax, and panes (the ids now in the graph; a drawer whose pane
 *  has left it closes). */
export function reduceDrawer(s, ev) {
  switch (ev.type) {
    case "open": {
      if (!ev.pane) return s;
      const via = ev.via === "strip" ? "strip" : "";
      if (ev.pane === s.pane) return via === s.via ? s : { ...s, via };
      return { ...s, pane: ev.pane, via };
    }
    case "close":
      if (!s.pane) return s;
      return { pane: "", via: "", maximised: false, returnTo: s.pane, returnVia: s.via, seq: s.seq + 1 };
    case "toggleMax":
      return s.pane ? { ...s, maximised: !s.maximised } : s;
    case "panes":
      return s.pane && !ev.ids.has(s.pane) ? { pane: "", via: "", maximised: false, returnTo: "", returnVia: "", seq: s.seq + 1 } : s;
    default:
      return s;
  }
}

/** focusRow puts the keyboard on the strip row of a pane inside root, the first one
 *  if the pane has more than one. It returns false when there is none. */
export function focusRow(root, pane) {
  if (!root || !pane || !root.querySelectorAll) return false;
  for (const el of root.querySelectorAll("[data-pane]")) {
    if (el.getAttribute("data-pane") !== pane || el.localName !== "li") continue;
    const pick = el.querySelectorAll ? el.querySelectorAll(".needs-pick")[0] : null;
    if (pick && pick.localName === "button" && pick.focus) {
      pick.focus();
      return true;
    }
  }
  return false;
}

/** focusNode puts the keyboard on the graph box of a pane inside root, or on
 *  root itself when the box is not there (the pane is gone, or never had one). */
export function focusNode(root, pane) {
  if (!root) return false;
  if (pane && root.querySelectorAll) {
    for (const el of root.querySelectorAll("[data-pane]")) {
      // A row of the narrow list or the roster: its button takes the keyboard.
      if (el.localName === "li" && el.getAttribute("data-pane") === pane) {
        const pick = el.querySelectorAll ? el.querySelectorAll(".list-pick")[0] : null;
        if (pick && pick.localName === "button" && pick.focus) {
          pick.focus();
          return true;
        }
        continue;
      }
      if (el.getAttribute("data-pane") === pane && el.getAttribute("tabindex") !== null && el.focus) {
        el.focus();
        return true;
      }
    }
  }
  if (root.focus) root.focus();
  return false;
}
