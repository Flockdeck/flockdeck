// The time on a row. Every entry carries ts, RFC 3339. A row shows it as the
// local clock time, with the whole date in the tooltip, or shows nothing for a ts
// that is not a time.

/** rowTime is { label, full, iso } for a ts, or null when it is not one. */
export function rowTime(ts) {
  if (typeof ts !== "string" || ts.length < 10 || ts.length > 40) return null;
  const d = new Date(ts);
  const n = d.getTime();
  if (!Number.isFinite(n) || n <= 0) return null;
  return {
    label: d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
    full: d.toLocaleString(),
    iso: d.toISOString(),
  };
}
