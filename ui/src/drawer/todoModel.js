// The agent's plan of a structured pane (the push's `plan`: the todo list or plan
// the agent keeps), without the drawing. Every row is the agent's text: it is cut,
// stripped of control and invisible characters, and kept to one line, so it
// reads as an item of the list and as nothing else. The push has already bounded
// the list; this bounds it again.

export const TODO_MAX = 50;
export const TODO_TEXT_MAX = 200;

/** STATUS_WORDS is how a status is named for the person and for a screen reader. */
export const STATUS_WORDS = { pending: "To do", in_progress: "In progress", completed: "Done" };

const oneLine = (s, n) => {
  const t = typeof s === "string" ? s.slice(0, n * 2 + 1).replace(/\p{Cf}/gu, "").replace(/[\p{C}\s]+/gu, " ").trim() : "";
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

const statusOf = (s) => {
  const k = typeof s === "string" ? s.trim().toLowerCase().replace(/[\s-]+/g, "_") : "";
  return k === "completed" || k === "in_progress" ? k : "pending";
};

/** todosOf reads the plan of a push into { rows, more, done, total }. A row that is
 *  not an object or has no text is left out; a status the list does not know is
 *  to do. more counts the rows past TODO_MAX; done and total count the rows kept. */
export function todosOf(st) {
  const list = st && Array.isArray(st.plan) ? st.plan : [];
  const rows = [];
  let more = 0;
  for (const p of list) {
    if (!p || typeof p !== "object") continue;
    const text = oneLine(p.content, TODO_TEXT_MAX);
    if (!text) continue;
    if (rows.length >= TODO_MAX) {
      more++;
      continue;
    }
    rows.push({ text, status: statusOf(p.status) });
  }
  const done = rows.filter((r) => r.status === "completed").length;
  return { rows, more, done, total: rows.length + more };
}

/** summaryText is "2 of 5 done", with the row in progress when there is one. */
export function summaryText(t) {
  if (!t || !t.total) return "";
  const cur = t.rows.find((r) => r.status === "in_progress");
  return t.done + " of " + t.total + " done" + (cur ? ": " + cur.text : "");
}
