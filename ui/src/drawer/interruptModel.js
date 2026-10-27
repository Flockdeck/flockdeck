// The keys that stop a running turn of a structured pane, without the drawing:
// Escape, and Ctrl+C when nothing is selected. Both do what the Stop button
// does, so they send structuredCancel and nothing else. They never answer a
// permission request with an allow: a cancelled turn ends the open request as
// cancelled.
//
// A key is taken only when it cannot be meant for something else on the page: not
// a key of an input method's composition, not one a menu or a dialog already took,
// not Escape in a field that has text or its own use (a select, the search
// field), and not Ctrl+C over a selection, which is Copy.

/** INTERRUPT_HINT is said under the prompt box while a turn runs. */
export const INTERRUPT_HINT = "Esc stops the turn. Ctrl+C stops it when no text is selected.";

const FIELDS = new Set(["input", "select", "option"]);

const tagOf = (t) => (t && typeof t.localName === "string" ? t.localName.toLowerCase() : "");

/** interruptKey says whether the key event e stops the turn. ctx is:
 *  working (the pane has a turn running or waiting on an answer), desk (a window on the desk),
 *  draft (the prompt box text), menuOpen (the slash or mention list is showing), dialogOpen (a
 *  confirmation or the export panel is showing), stopping (a Stop is already out) and
 *  selected (text is selected anywhere on the page). Anything missing is a no. */
export function interruptKey(e, ctx) {
  if (!e || typeof e !== "object" || !ctx || typeof ctx !== "object") return false;
  if (e.isComposing || e.keyCode === 229 || e.defaultPrevented) return false;
  if (ctx.desk !== true || ctx.working !== true || ctx.stopping === true) return false;
  if (ctx.menuOpen === true || ctx.dialogOpen === true) return false;
  if (e.shiftKey || e.altKey || e.metaKey) return false;
  const tag = tagOf(e.target);
  if (FIELDS.has(tag)) return false;
  if (e.target && e.target.isContentEditable === true) return false;
  const inBox = tag === "textarea";
  if (e.key === "Escape") {
    if (e.ctrlKey) return false;
    // In the prompt box Escape belongs to the text until there is none.
    return !inBox || (typeof ctx.draft === "string" && ctx.draft === "");
  }
  if ((e.key === "c" || e.key === "C") && e.ctrlKey) return ctx.selected === false;
  return false;
}

/** selectedText says whether the person has text selected in window win or in the field el. A page that
 *  cannot say counts as having a selection, so Ctrl+C is left to Copy. */
export function selectedText(win, el) {
  try {
    if (el && typeof el.selectionStart === "number" && typeof el.selectionEnd === "number" && el.selectionStart !== el.selectionEnd) return true;
    const doc = win && win.document;
    let sel = win && typeof win.getSelection === "function" ? win.getSelection() : null;
    if (sel === null || sel === undefined) sel = doc && typeof doc.getSelection === "function" ? doc.getSelection() : null;
    if (sel === null || sel === undefined) return true;
    return String(sel).length > 0;
  } catch {
    return true;
  }
}
