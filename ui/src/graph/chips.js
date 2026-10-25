// The chips on a row of the outline (Tree.js), in the order the build brief gives:
// what the pane asks (or that it is down), a file overlap, what it waits on,
// messages not told yet, and a quiet "stuck". A chip with `to` is a link: it names
// the row at the other end, and hovering or focusing it lights both rows.
//
// Every text is an agent's or a person's and is drawn as text.
import { clip } from "./Node.js";
import { stateOf } from "./Node.js";
import { stuckOf, labelOf, spanOf } from "./model.js";
import { depsOf, openBlockers, isFinished } from "./deps.js";

/** The words on the chip of each kind of need that asks something of the person. */
const ASKS = { permission: "Permission", ask: "Question", returned: "Returned", setup: "Setup" };

/** nameOf is the name a row shows for a pane, for a chip that names another. */
const nameOf = labelOf;

/**
 * chipsOf lists the chips of one pane: { kind, text, to, title }. kind is ask,
 * down, overlap, wait, undelivered or stuck; to is the pane a link chip goes to
 * ("" for a plain chip). needs are the needs the row is drawn for (needsOf).
 */
export function chipsOf(model, node, needs, now) {
  const out = [];
  const list = needs || [];
  const ask = list.find((n) => Object.prototype.hasOwnProperty.call(ASKS, n.kind));
  const key = stateOf(node).key;
  if (ask) out.push({ kind: "ask", text: ASKS[ask.kind], to: "", title: ask.label });
  else if (key === "exited" || key === "failed" || list.some((n) => n.kind === "down")) out.push({ kind: "down", text: "Down", to: "", title: "" });
  // A conflict names two panes; the model has it under both. A pane in two conflicts with the same other one gets one chip, so the row's keys are unique.
  const linked = new Set();
  for (const n of model.needsByPane.get(node.pane) || []) {
    if (n.kind !== "conflict") continue;
    const other = n.pane === node.pane ? n.other : n.pane;
    if (other && other !== node.pane && model.byPane.has(other) && !linked.has("overlap|" + other) && linked.add("overlap|" + other)) out.push({ kind: "overlap", text: "overlaps " + clip(nameOf(model, other), 14), to: other, toName: nameOf(model, other), title: n.text });
  }
  const w = node.waitingOn;
  if (w.kind === "helpers") {
    const to = w.ref && w.ref !== node.pane && model.byPane.has(w.ref) ? w.ref : "";
    out.push({ kind: "wait", text: to ? "waiting on " + clip(nameOf(model, to), 14) : "waiting on a helper", to, toName: to ? nameOf(model, to) : "", title: w.text });
  } else if (w.kind === "reviewer") out.push({ kind: "wait", text: "waiting on a reviewer", to: "", title: w.text });
  else if (w.kind === "task") out.push({ kind: "wait", text: "waiting on a task", to: "", title: w.text });
  if (node.undelivered > 0) out.push({ kind: "undelivered", text: node.undelivered + " undelivered", to: "", title: "" });
  const stuck = stuckOf(node, now);
  if (stuck) out.push({ kind: "stuck", text: stuck.minutes > 0 ? "stuck " + spanOf(stuck.minutes) : "stuck", to: "", title: "" });
  return out;
}

/**
 * dependencyChipsOf lists the chips a row has from the task board: "blocked by
 * T3, T5" while tasks it depends on are open, and its estimate while it is not
 * finished. tasks is the team push's list (none on the relay, so none here).
 */
export function dependencyChipsOf(tasks, node) {
  if (!node || !node.task || !Array.isArray(tasks) || tasks.length === 0) return [];
  const idx = depsOf(tasks);
  const mine = idx.byId.get(node.task.id);
  if (!mine || isFinished(mine)) return [];
  const out = [];
  const open = openBlockers(idx, mine.id);
  if (open.length > 0) {
    const shown = open.slice(0, 2).map((b) => clip(b.id, 8)).join(", ");
    out.push({ kind: "blocked", text: "blocked by " + shown + (open.length > 2 ? " +" + (open.length - 2) : ""), to: "", title: open.map((b) => b.id + (b.title ? " " + b.title : "") + " (" + b.state + ")").join("\n") });
  }
  if (mine.estimateMin > 0) out.push({ kind: "est", text: "est " + mine.estimateMin + "m", to: "", title: "" });
  return out;
}
