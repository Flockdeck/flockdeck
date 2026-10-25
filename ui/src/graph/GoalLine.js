// The goal line above the graph: for each team, its goal and what it has cost and
// taken so far, in one row (the mockup's "GOAL ... 2h 14m $18.40 Working: 4
// 8 of 8 helpers, at the cap 3 need you").
//
// Everything is from the graph push: the team's goal, started time, spend
// (spendOf, with its "partial" mark), helper caps and needs, and each member's
// status. Nothing here asks the desktop for anything. What the push does not have
// is not drawn: there is no "merged" word, because nothing says a team is done.
//
// The elapsed time is worked out from the started time and the clock, and the row
// is drawn again every half minute so that it moves. Every text is untrusted and
// is drawn as text.
import { useEffect, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "./Node.js";
import { taskOfTeam, taskLabelOf } from "./model.js";

/** TICK_MS is how often the elapsed time is looked at again. */
export const TICK_MS = 30000;

/** elapsedOf is how long since a time, as "2h 14m", "14m" or "under 1m", or
 *  "" when the time is not known (the graph writes year 1 for that) or is ahead of
 *  now. */
export function elapsedOf(started, now) {
  const t = Date.parse(started);
  if (!Number.isFinite(t) || t < Date.UTC(2000, 0, 1)) return "";
  const m = Math.floor((now - t) / 60000);
  if (m < 0) return "";
  if (m < 1) return "under 1m";
  if (m < 60) return m + "m";
  const h = Math.floor(m / 60);
  if (h < 48) return h + "h " + (m % 60) + "m";
  return Math.floor(h / 24) + "d " + (h % 24) + "h";
}

/** helpersOf is the helper cap of a team as a sentence, or "" when no cap is
 *  known: "6 of 8 helpers", with ", at the cap" when it is reached. */
export function helpersOf(team) {
  const { used, max } = capOf(team);
  if (max <= 0) return "";
  return used + " of " + max + " helpers" + (used > max ? ", over the cap" : used >= max ? ", at the cap" : "");
}

/** capOf is how many helpers a team has and the most it may have, over its caps. */
export function capOf(team) {
  let used = 0;
  let max = 0;
  for (const c of team.caps) {
    used += c.used;
    max += c.max;
  }
  return { used, max };
}

/** summaryOf is the numbers of one team's line. now is the clock. */
/** goalOf is what the line says the team is for: its task (id and title) when it was started for one and the title is known, else the goal text, else the bare task id. */
function goalOf(model, team) {
  const task = taskOfTeam(model, team);
  return task && task.title ? taskLabelOf(task) : team.goal || taskLabelOf(task) || "Team";
}

/** managerOf is the name of the pane that runs the team, "" when the team has none, or null when it has one the graph no longer holds. */
function managerOf(model, team) {
  if (!team.manager) return "";
  const n = model.byPane.get(team.manager);
  return n ? n.name || team.manager : null;
}

export function summaryOf(model, team, now) {
  let working = 0;
  for (const id of team.members) {
    const n = model.byPane.get(id);
    if (n && n.status === "working") working++;
  }
  return {
    goal: goalOf(model, team),
    manager: managerOf(model, team),
    over: capOf(team).used > capOf(team).max && capOf(team).max > 0,
    elapsed: elapsedOf(team.started, now),
    working,
    helpers: helpersOf(team),
    capped: capOf(team).max > 0 && capOf(team).used >= capOf(team).max,
    needs: team.needs.length + team.needsHidden,
  };
}

/** GoalLine draws one row for each team that has a goal. now is the clock, for a
 *  test; by default it is read each time the row is drawn. */
export function GoalLine({ model, now = null, shownAs = "" }) {
  const [, tick] = useState(0);
  useEffect(() => {
    if (now !== null) return undefined;
    const id = setInterval(() => tick((n) => n + 1), TICK_MS);
    return () => clearInterval(id);
  }, [now]);
  const clock = now !== null ? now : Date.now();
  const rows = model.teams;
  if (rows.length === 0) return null;
  return html`<div class="goalline-box">
    ${rows.map((team) => {
      const s = summaryOf(model, team, clock);
      return html`<p key=${team.id} class="goalline" data-team=${team.id}>
        <span class="goalline-head">
          ${s.goal.trim() === String(shownAs).trim() ? null : html`<span class="goalline-label">Goal</span>
          <span class="goalline-goal">${clip(s.goal, 120)}</span>`}
          ${s.manager === null ? html`<span class="goalline-manager">manager gone</span>` : s.manager ? html`<span class="goalline-manager">run by ${clip(s.manager, 40)}</span>` : null}
        </span>
        <span class="goalline-facts">
          ${s.elapsed ? html`<span class="goalline-fact goalline-time">${s.elapsed}</span>` : null}
          <span class="goalline-fact goalline-working">Working: ${s.working}</span>
          ${s.helpers ? html`<span class=${"goalline-fact goalline-helpers" + (s.capped ? " is-capped" : "") + (s.over ? " is-over" : "")}>${s.helpers.replace(", at the cap", "")}</span>` : null}
          ${s.needs > 0
            ? html`<span class="goalline-fact goalline-needs">${s.needs} ${s.needs === 1 ? "needs" : "need"} you</span>`
            : html`<span class="goalline-fact goalline-none">Nothing waits on you</span>`}
        </span>
      </p>`;
    })}
  </div>`;
}
