// What a structured pane's usage meter and its detail show, without the drawing.
// The numbers are what the agent last reported with usage_update: the tokens in
// the context, the size of the window and, if it said so, a cost. Nothing else is
// worked out from them but a share of the window and what is left. A figure the
// agent did not report is absent here, and the detail says it was not reported;
// tokens in and out are not part of that report and are never estimated.

const MAX_TOKENS = 1e12;
const MAX_COST = 1e9;

const count = (n) => (typeof n === "number" && Number.isFinite(n) && n >= 0 && n <= MAX_TOKENS ? Math.floor(n) : null);

/** usageOf reads the push's usage as { used, size, left, pct, cost, currency },
 *  with null for what was not reported or is not a sane number. pct is a whole
 *  number from 0 to 100 and is null without a size. */
export function usageOf(usage) {
  const u = usage && typeof usage === "object" ? usage : {};
  const size = count(u.size) || null;
  const raw = count(u.used);
  // A used count with no window is a report only if it is above zero: the push says 0 when nothing came.
  const used = raw === null ? null : size !== null || raw > 0 ? raw : null;
  const reported = typeof u.cost === "number" && Number.isFinite(u.cost) && u.cost > 0 && u.cost <= MAX_COST;
  const currency = typeof u.currency === "string" ? u.currency.replace(/[^A-Za-z$€£¥]/g, "").slice(0, 8) : "";
  return {
    used,
    size,
    left: size !== null && used !== null ? Math.max(0, size - used) : null,
    pct: size !== null && used !== null ? Math.min(100, Math.round((used / size) * 100)) : null,
    cost: reported ? u.cost : null,
    currency,
  };
}

const n = (v) => v.toLocaleString("en-US");

/** meterText is the short line beside the bar: the share of the window used, or
 *  "Usage details" when only a cost was reported, or "" when nothing was. The
 *  exact counts and the cost are in the line above it and in the detail. */
export function meterText(usage) {
  const u = usageOf(usage);
  if (u.pct !== null) return u.pct + "% of context";
  return u.cost !== null || u.used !== null ? "Usage details" : "";
}

/** detailRows are the rows of the popover as [label, value]. */
export function detailRows(usage) {
  const u = usageOf(usage);
  const rows = [];
  rows.push(["Context used", u.used === null ? "Not reported" : n(u.used) + " tokens"]);
  rows.push(["Context window", u.size === null ? "Not reported" : n(u.size) + " tokens"]);
  if (u.left !== null) rows.push(["Left in the window", n(u.left) + " tokens"]);
  if (u.pct !== null) rows.push(["Share used", u.pct + "%"]);
  rows.push(["Cost for this session", u.cost === null ? "Not reported" : u.cost.toFixed(2) + (u.currency ? " " + u.currency : "")]);
  return rows;
}

/** NOT_REPORTED_WORDS says what this agent's usage report does not hold. */
export const NOT_REPORTED_WORDS = "These are the figures the agent last reported. Its report has no split of tokens in and out and no breakdown of the context, so none is shown.";
