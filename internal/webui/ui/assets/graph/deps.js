// What waits on what, derived in one place for the Dependencies view (Deps.js).
//
// Inputs are the task list of the team push (lib/team.js: id, title, state,
// owner, ownerPane, depends, estimateMin) and the graph model (model.js: each
// pane's waitingOn). Nothing here is sent by the desk ready-made: blocked, ready,
// the critical path, "see above" and loops are worked out from `depends`, the
// states and the waits. A field the desk does not send gives an empty answer, not
// an error.
//
// Rules this file keeps:
// - A task is finished when it is done or cancelled; a finished blocker blocks
//   nothing.
// - A task is ready when it has not started (planned, with no owner), nothing
//   open blocks it and it is not itself finished.
// - Nothing is recursive: a chain of any length, and a dependency loop in the
//   task list (A needs B needs A), are walked without running away. A task is
//   visited once on the way down a branch and drawn again, collapsed, as "see
//   above" wherever else it is wanted.
// - Times appear only when the desk sent an estimate for some task. Without one
//   the critical path is still chosen, by the longest chain of work.

import { stateOf } from "./Node.js";
import { isLoud } from "./Badge.js";

const FINISHED = new Set(["done", "cancelled"]);

/** isFinished says a task needs nothing more of anyone. */
export const isFinished = (t) => FINISHED.has(t.state);

/** The id of the root of the tree: what the team set out to do. */
export const GOAL = "GOAL";

/** depsOf indexes a list of tasks (the team push's) by id and reads the `depends` of each, leaving out a dependency on a task that does not exist and on itself. */
export function depsOf(tasks) {
  const byId = new Map();
  for (const t of tasks || []) if (t && t.id && !byId.has(t.id)) byId.set(t.id, t);
  const dep = new Map();
  for (const t of byId.values()) {
    const seen = new Set();
    const out = [];
    for (const id of Array.isArray(t.depends) ? t.depends : []) {
      if (typeof id !== "string" || id === t.id || !byId.has(id) || seen.has(id)) continue;
      seen.add(id);
      out.push(id);
    }
    dep.set(t.id, out);
  }
  return { byId, dep };
}

/** openBlockers are the unfinished tasks one must wait for, in the order `depends` lists them. */
export function openBlockers(idx, id) {
  return (idx.dep.get(id) || []).map((b) => idx.byId.get(b)).filter((b) => b && !isFinished(b));
}

/** isReady says a task can be started now: nobody has it, it is not finished, and nothing open blocks it. */
export function isReady(idx, id) {
  const t = idx.byId.get(id);
  return !!t && t.state === "planned" && !t.ownerPane && !t.owner && openBlockers(idx, id).length === 0;
}

/** hasEstimates says the desk sent an estimate for at least one task, so times may be shown. */
export function hasEstimates(idx) {
  for (const t of idx.byId.values()) if (t.estimateMin > 0) return true;
  return false;
}

/**
 * finishOf is how long a task is from done, counting from now: its own estimate
 * (one unit when there is none, so the chain is still ordered by its length) on
 * top of the longest of its open blockers. A finished task is 0. It walks with a
 * stack and visits a task once, so a loop does not run away: a task met again on
 * its own way down counts as 0.
 */
export function finishOf(idx, estimates) {
  const memo = new Map();
  const unit = (t) => (estimates ? t.estimateMin || 0 : 1);
  for (const root of idx.byId.keys()) {
    if (memo.has(root)) continue;
    const stack = [{ id: root, next: 0, best: 0 }];
    const onPath = new Set([root]);
    while (stack.length > 0) {
      const top = stack[stack.length - 1];
      const t = idx.byId.get(top.id);
      const deps = isFinished(t) ? [] : idx.dep.get(top.id);
      if (top.next < deps.length) {
        const d = deps[top.next++];
        if (memo.has(d)) top.best = Math.max(top.best, memo.get(d));
        else if (!onPath.has(d)) {
          onPath.add(d);
          stack.push({ id: d, next: 0, best: 0 });
        }
        continue;
      }
      stack.pop();
      onPath.delete(top.id);
      const v = isFinished(t) ? 0 : top.best + unit(t);
      memo.set(top.id, v);
      if (stack.length > 0) {
        const up = stack[stack.length - 1];
        up.best = Math.max(up.best, v);
      }
    }
  }
  return memo;
}

/** sinksOf are the tasks nothing else waits on: the ends of the work, which the goal waits on. */
export function sinksOf(idx) {
  const wanted = new Set();
  for (const deps of idx.dep.values()) for (const d of deps) wanted.add(d);
  return [...idx.byId.keys()].filter((id) => !wanted.has(id));
}

/**
 * planOf is everything the view draws for a task list: the tree to draw (rows
 * top down), which tasks are on the critical path, which are ready, and how long
 * the whole thing has to go.
 *
 * A row is { key, id, depth, parent, dup, kids, folded }. The goal is the first row. The
 * kids of a task are its open blockers, the one that finishes last first. A task
 * that is already in the tree above or earlier is drawn again as a `dup` row with
 * no kids ("see above"). A finished task has no kids. folded is a set of row
 * keys the person folded away, whose kids are left out.
 */
export function planOf(tasks, folded = new Set()) {
  const idx = depsOf(tasks);
  const estimates = hasEstimates(idx);
  const fin = finishOf(idx, estimates);
  const finish = (id) => fin.get(id) || 0;
  const order = (ids) => ids.slice().sort((a, b) => finish(b) - finish(a));
  const goalKids = order(sinksOf(idx).filter((id) => !isFinished(idx.byId.get(id))));
  // A ring in the tasks has no end for the goal to wait on: hang the first task nothing above reaches.
  const reached = new Set();
  const reach = (from) => {
    const todo = [from];
    while (todo.length > 0) {
      const id = todo.pop();
      if (reached.has(id)) continue;
      reached.add(id);
      for (const b of openBlockers(idx, id)) todo.push(b.id);
    }
  };
  goalKids.forEach(reach);
  for (const [id, t] of idx.byId) {
    if (reached.has(id) || isFinished(t)) continue;
    goalKids.push(id);
    reach(id);
  }
  const total = goalKids.length > 0 ? finish(goalKids[0]) : 0;

  // The critical path: from the goal, the open blocker that finishes last, over and over.
  const critical = [];
  const seenCrit = new Set();
  for (let cur = goalKids[0]; cur && !seenCrit.has(cur); ) {
    seenCrit.add(cur);
    critical.push(cur);
    cur = order(openBlockers(idx, cur).map((b) => b.id))[0];
  }

  // Popping the stack is reading order, so the first row for a task is the real
  // one and any later one is drawn again as "see above", with nothing under it.
  const rows = [];
  const seen = new Set();
  const stack = [{ id: GOAL, depth: 0, parent: "" }];
  let again = 0;
  while (stack.length > 0) {
    const { id, depth, parent } = stack.pop();
    const isGoal = id === GOAL;
    const dup = !isGoal && seen.has(id);
    const key = dup ? id + "~" + ++again : id;
    seen.add(id);
    const kidIds = isGoal ? goalKids : dup || isFinished(idx.byId.get(id)) ? [] : order(openBlockers(idx, id).map((b) => b.id));
    const isFolded = kidIds.length > 0 && folded.has(key);
    rows.push({ key, id, depth, parent, dup, kids: kidIds.length, folded: isFolded });
    if (isFolded) continue;
    for (let i = kidIds.length - 1; i >= 0; i--) stack.push({ id: kidIds[i], depth: depth + 1, parent: key });
  }
  const out = rows;
  const ready = [...idx.byId.keys()].filter((id) => isReady(idx, id));
  return { idx, rows: out, critical, ready, total, estimates, finish };
}

/** minutesWords is a time as the view says it: "+1h 20m", "+45m", or "done" for none. */
export function minutesWords(m) {
  if (!(m > 0)) return "done";
  const h = Math.floor(m / 60);
  return "+" + (h > 0 ? h + "h " + (m % 60 ? (m % 60) + "m" : "") : m + "m").trim();
}

// A pane's wait on another pane, from the graph's own waitingOn: helpers with a ref.
const waitRef = (model, n) => (n.waitingOn && n.waitingOn.kind === "helpers" && n.waitingOn.ref && model.byPane.has(n.waitingOn.ref) && n.waitingOn.ref !== n.pane ? n.waitingOn.ref : "");

/**
 * loopsOf are the rings of panes that wait on each other, each as the panes in
 * order of who waits on whom. A pane waits on at most one other, so a ring is a
 * cycle of that chain; a pane that only waits on a ring is not in it.
 */
export function loopsOf(model) {
  const out = [];
  if (!model || !model.ok) return out;
  const state = new Map();
  for (const start of model.nodes) {
    if (state.has(start.pane)) continue;
    const path = [];
    let cur = start.pane;
    while (cur && !state.has(cur)) {
      state.set(cur, 1);
      path.push(cur);
      cur = waitRef(model, model.byPane.get(cur));
    }
    if (cur && state.get(cur) === 1) out.push(path.slice(path.indexOf(cur)));
    for (const p of path) state.set(p, 2);
  }
  return out;
}

/**
 * waitsOf are the waits between agents as rows: { key, kind, from, to, why }.
 * kind is "loop" (a ring), "you" (the pane asks the person), "overlap" (two panes
 * on the same file) or "agent" (a plain wait on another pane), in that order.
 */
export function waitsOf(model) {
  if (!model || !model.ok) return [];
  const ring = new Set();
  const rows = { loop: [], you: [], overlap: [], agent: [] };
  for (const r of loopsOf(model)) {
    for (let i = 0; i < r.length; i++) {
      ring.add(r[i]);
      const n = model.byPane.get(r[i]);
      rows.loop.push({ key: "w-" + r[i] + "-" + r[(i + 1) % r.length], kind: "loop", from: r[i], to: r[(i + 1) % r.length], why: n.waitingOn.text || "" });
    }
  }
  const seen = new Set();
  for (const n of model.nodes) {
    const mine = (model.needsByPane.get(n.pane) || []).filter((x) => x.pane === n.pane);
    if (isLoud(mine.filter((x) => x.kind !== "conflict")) || stateOf(n).key === "waiting") rows.you.push({ key: "w-" + n.pane + "-you", kind: "you", from: n.pane, to: "you", why: "" });
    for (const x of mine) {
      if (x.kind !== "conflict" || !x.other || seen.has(x.id)) continue;
      seen.add(x.id);
      rows.overlap.push({ key: "w-" + n.pane + "-" + x.other + "~o", kind: "overlap", from: n.pane, to: x.other, why: x.text || "" });
    }
    const to = waitRef(model, n);
    if (to && !ring.has(n.pane)) rows.agent.push({ key: "w-" + n.pane + "-" + to, kind: "agent", from: n.pane, to, why: n.waitingOn.text || "" });
  }
  return [...rows.loop, ...rows.you, ...rows.overlap, ...rows.agent];
}
