// Tab inside a dialog. The dialogs trap Tab, so the list of what Tab can reach
// has to be the browser's: links count, and a node that is only focusable by
// script (tabindex -1, the other members of a roving group) does not.

/** focusables are the controls Tab can reach inside root, in document order. */
export function focusables(root) {
  if (!root || !root.querySelectorAll) return [];
  return [...root.querySelectorAll("select, textarea, input, button, a, [tabindex]")].filter((n) => {
    if (n.disabled || n.hidden) return false;
    const t = n.getAttribute("tabindex");
    if (t !== null && Number(t) < 0) return false;
    const tag = (n.localName || "").toLowerCase();
    if (tag === "a") return n.getAttribute("href") !== null || t !== null;
    if (tag === "input" && n.getAttribute("type") === "hidden") return false;
    return true;
  });
}

/** trapTab keeps Tab and Shift+Tab inside root. */
export function trapTab(e, root) {
  const list = focusables(root);
  if (list.length === 0) return;
  const doc = root.ownerDocument;
  const at = list.indexOf(doc.activeElement);
  const to = e.shiftKey ? (at <= 0 ? list.length - 1 : at - 1) : (at < 0 || at === list.length - 1 ? 0 : at + 1);
  e.preventDefault();
  list[to].focus();
}
