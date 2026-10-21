// The graph as a list, for a narrow window, and the roster of a window the graph
// is not sent to, drawn the same way.
//
// - GraphList: the strip of what needs the person first, in the graph's order
//   (the same NeedsYou the wide graph has, from the same model, so the order is
//   the same), then each team as indented rows: the manager, its helpers under it
//   and theirs under them, the members the tree does not reach, and the panes in
//   no team. A row opens the agent's drawer.
// - RosterList: the same, from the state push alone, for a window reached through
//   the relay. The push has no tree, so the agents are one level, in the push's
//   order, after the strip of those that wait on the person. The desktop line is
//   the caller's.
//
// Every text here came from an agent, a task or a person, and is drawn as text.
import { html } from "../html.js";
import { NeedsYou } from "./NeedsYou.js";
import { clip, stateOf } from "./Node.js";

/** MAX_INDENT is how deep a row is indented. A deeper one is drawn at this depth. */
export const MAX_INDENT = 6;

/** rowsOf lists a team's panes top down with their depth: the tree in the order
 *  of the model (each pane before its helpers), then the members it does not
 *  reach. It walks with a stack, so a chain of any length is listed. */
export function rowsOf(team) {
  const out = [];
  if (team.tree) {
    const stack = [{ f: team.tree, depth: 0 }];
    while (stack.length > 0) {
      const { f, depth } = stack.pop();
      out.push({ pane: f.pane, depth });
      for (let i = f.children.length - 1; i >= 0; i--) stack.push({ f: f.children[i], depth: depth + 1 });
    }
  }
  for (const n of team.unlinked) out.push({ pane: n.pane, depth: 0, loose: true });
  return out;
}

function Row({ node, depth, onOpen, open }) {
  const state = stateOf(node);
  const who = [node.role || (node.shell ? "shell" : ""), node.agent].filter(Boolean).join(" · ");
  const body = html`
    <span class=${"list-mark list-mark--" + state.key} aria-hidden="true"></span>
    <span class="list-name">${clip(node.name || node.pane, 40)}</span>
    <span class="list-state">${state.word}</span>
    ${who ? html`<span class="list-who">${clip(who, 40)}</span>` : null}
    ${node.live ? html`<span class="list-live">${clip(node.live, 60)}</span>` : null}
  `;
  return html`<li class=${"list-row list-row--d" + Math.min(depth, MAX_INDENT)} data-pane=${node.pane} key=${node.pane}>
    ${onOpen
      ? html`<button type="button" class=${"list-pick" + (open === node.pane ? " is-open" : "")} aria-expanded=${open === node.pane ? "true" : "false"} onClick=${() => onOpen(node.pane)}>${body}</button>`
      : html`<div class="list-pick">${body}</div>`}
  </li>`;
}

/** GraphList draws a graph model as a list. onOpen and open are the graph's. */
export function GraphList({ model, onOpen = null, open = "" }) {
  const teams = model.teams.map((t) => ({ t, rows: rowsOf(t).filter((r) => model.byPane.has(r.pane)) })).filter((x) => x.rows.length > 0);
  const solo = model.solo.filter((n) => model.byPane.has(n.pane));
  const inTeams = new Set(teams.flatMap((x) => x.rows.map((r) => r.pane)));
  // A node no team and no solo list holds is still a row, in the last group.
  const stray = model.nodes.filter((n) => !inTeams.has(n.pane) && !solo.some((s) => s.pane === n.pane));
  const last = solo.concat(stray);
  return html`
    <div class="list">
      <${NeedsYou} model=${model} onSelect=${onOpen ? (pane) => onOpen(pane, "strip") : null} />
      ${teams.map(({ t, rows }) => html`<section class="list-team" data-team=${t.id} key=${t.id}>
        <h3 class="list-head">${clip(t.goal || "Team", 80)}</h3>
        <ul class="list-rows">${rows.map((r) => html`<${Row} node=${model.byPane.get(r.pane)} depth=${r.depth} onOpen=${onOpen} open=${open} />`)}</ul>
      </section>`)}
      ${last.length > 0
        ? html`<section class="list-team list-team--solo">
            <h3 class="list-head">Not in a team</h3>
            <ul class="list-rows">${last.map((n) => html`<${Row} node=${model.byPane.get(n.pane)} depth=${0} onOpen=${onOpen} open=${open} />`)}</ul>
          </section>`
        : null}
    </div>
  `;
}

/** needsOfRoster turns the state push's panes that wait on the person into the
 *  model NeedsYou draws: a permission or a question, in the push's order. The
 *  push a relay window gets is already made fit for it, so the summary and the
 *  question are what the row shows. */
export function needsOfRoster(state) {
  const panes = state && state.panes && typeof state.panes === "object" ? Object.values(state.panes) : [];
  const byPane = new Map();
  const needs = [];
  for (const p of panes) {
    if (!p || typeof p !== "object" || !p.id) continue;
    const id = String(p.id);
    byPane.set(id, { name: String(p.name || id) });
    const q = p.ask && Array.isArray(p.ask.questions) && p.ask.questions[0];
    if (p.permission && typeof p.permission === "object") {
      needs.push({ id: "p:" + id, pane: id, kind: "permission", text: String(p.permission.summary || p.permission.tool || "") });
    } else if (q && typeof q === "object") {
      needs.push({ id: "a:" + id, pane: id, kind: "ask", text: String(q.question || "") });
    }
  }
  return { byPane, needs, teams: [], soloNeedsHidden: 0 };
}

/** RosterList draws the agents of a state push: state is the push, rows are Home's rosterOf rows of it. */
export function RosterList({ state, rows, onOpen = null, open = "" }) {
  const model = needsOfRoster(state);
  return html`
    <div class="list list--roster">
      <${NeedsYou} model=${model} onSelect=${onOpen ? (pane) => onOpen(pane, "strip") : null} />
      <ul class="home-roster" aria-label="Agents in this project">
        ${rows.map((r) => {
          const body = html`
            <span class="home-agent-name">${clip(r.name, 60)}</span>
            ${r.role ? html`<span class="home-agent-role">${clip(r.role, 30)}</span>` : null}
            <span class="home-agent-doing">${clip(r.doing, 20)}</span>
          `;
          return html`<li class="home-agent" data-pane=${r.id} key=${r.id}>
            ${onOpen
              ? html`<button type="button" class=${"list-pick" + (open === r.id ? " is-open" : "")} aria-expanded=${open === r.id ? "true" : "false"} onClick=${() => onOpen(r.id)}>${body}</button>`
              : body}
          </li>`;
        })}
      </ul>
    </div>
  `;
}
