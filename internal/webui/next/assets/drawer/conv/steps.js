// Grouping consecutive tool rows into one "N steps" row.
//
// A run of two or more tool or helper rows in a row is one group. A group before
// the last reply or prompt is closed to its header, because that work is finished
// and the reader came for what was said; a group after it is open, because it is
// what the agent is doing now. A lone tool row is never grouped. Opening or closing
// a group is the reader's, and the choice outlives new entries.
//
// Grouping keeps the order and the count: the entries of the groups, read in
// order, are the entries given.

/** isStep says whether an entry is a tool call or a helper agent. */
export const isStep = (e) => !!e && (e.kind === "tool" || e.kind === "subagent");

/** groupEntries turns entries into items: { entry } for a row on its own, or
 *  { steps: [entries], id } for a run of steps, id being "steps:" and the first
 *  step's id. */
export function groupEntries(entries) {
  const items = [];
  let run = [];
  const flush = () => {
    if (run.length === 1) items.push({ entry: run[0] });
    else if (run.length > 1) items.push({ steps: run, id: "steps:" + run[0].id });
    run = [];
  };
  for (const e of entries) {
    if (isStep(e)) {
      run.push(e);
    } else {
      flush();
      items.push({ entry: e });
    }
  }
  flush();
  return items;
}

/** lastSaid is the index of the last prompt or reply, or -1. */
export function lastSaid(entries) {
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i].kind === "prompt" || entries[i].kind === "reply") return i;
  }
  return -1;
}

/** defaultOpen says whether the group starts open: it is after the last prompt or reply. */
export function defaultOpen(group, entries) {
  const at = lastSaid(entries);
  if (at < 0) return true;
  const first = entries.findIndex((e) => e.id === group.steps[0].id);
  return first > at;
}

/** stepsWords is the header's text: "3 steps", with a note for steps that failed or are still running. */
export function stepsWords(steps) {
  const failed = steps.filter((s) => s.status === "error").length;
  const running = steps.filter((s) => s.status === "running").length;
  let w = steps.length + (steps.length === 1 ? " step" : " steps");
  if (failed) w += ", " + failed + " failed";
  if (running) w += ", " + running + " running";
  return w;
}
