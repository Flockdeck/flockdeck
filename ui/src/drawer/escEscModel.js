// Esc Esc in a structured pane's drawer opens the rewind panel, as it does in the
// terminal agent. The first Escape only notes the time; a second one within
// DOUBLE_ESC_MS opens the panel. It is for an idle pane: while a turn runs
// Escape stops the turn (interruptModel.js), and in the prompt box it belongs to
// the text until there is none. A menu or dialog that is open keeps its own Escape.

/** DOUBLE_ESC_MS is how close together the two Escapes must be. */
export const DOUBLE_ESC_MS = 600;

const FIELDS = new Set(["input", "select", "option"]);
const tagOf = (t) => (t && typeof t.localName === "string" ? t.localName.toLowerCase() : "");

/** escEsc takes one key event and the time of the one before, and says whether
 *  this is the second Escape of a pair (open) and the time to remember (last).
 *  ctx is: desk, working (a turn runs or waits), offered (the agent can go back),
 *  draft (the prompt box text), menuOpen and dialogOpen. Anything missing is a no. */
export function escEsc(e, last, now, ctx) {
  const none = { open: false, last: 0 };
  if (!e || typeof e !== "object" || !ctx || typeof ctx !== "object") return none;
  if (e.key !== "Escape") return none;
  if (e.isComposing || e.keyCode === 229 || e.defaultPrevented || e.repeat) return none;
  if (e.ctrlKey || e.shiftKey || e.altKey || e.metaKey) return none;
  if (ctx.desk !== true || ctx.offered !== true || ctx.working === true) return none;
  if (ctx.menuOpen === true || ctx.dialogOpen === true) return none;
  const tag = tagOf(e.target);
  if (FIELDS.has(tag)) return none;
  if (e.target && e.target.isContentEditable === true) return none;
  if (tag === "textarea" && !(typeof ctx.draft === "string" && ctx.draft === "")) return none;
  if (typeof last === "number" && last > 0 && now - last <= DOUBLE_ESC_MS && now >= last) return { open: true, last: 0 };
  return { open: false, last: now };
}
