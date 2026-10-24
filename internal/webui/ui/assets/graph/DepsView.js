// The Dependencies view: the work of a team as a tree of what blocks what. Work at
// the top is finished last, and the chain that sets that time is marked. Under it,
// the agents that wait on each other, with a ring of them called out.
//
// Everything is derived in deps.js from the task list (depends, state, estimateMin)
// and the graph's waits. Times are drawn only when the desk sent an estimate. The
// view reads the team push the Board reads (watchTeam), so a window reached through
// the relay, which gets no tasks, says so and draws nothing else.
//
// Rows follow the tree pattern of the outline (Tree.js): one tab stop, arrows,
// Home and End, Right and Left to unfold, fold and move to the parent, Enter or
// Space opens the owner's drawer. Every text here is an agent's or a person's and
// is drawn as text.
import { useRef, useState } from "../vendor/hooks.mjs";
import { Fragment } from "../vendor/preact.mjs";
import { html } from "../html.js";
import { composing } from "../shell/keys.js";
import { focusNode } from "../drawer/state.js";
import { teamOf, TEAM_RELAY_WORDS, isFinished as stateFinished } from "../lib/team.js";
import { clip, stateOf } from "./Node.js";
import { managerOf } from "./ManagerBar.js";
import { labelOf, taskOfTeam, taskLabelOf } from "./model.js";
import { GOAL, IDLE, scopeTasks, teamOfTask, isReady, openBlockers, planOf, loopsOf, waitsOf, minutesWords } from "./deps.js";
import { stepOf, foldOf, ageOf } from "./Tree.js";

export const NO_TASKS_WORDS = "No tasks have been planned yet. The manager adds them as it plans.";
export const READING_WORDS = "Reading the tasks…";

/** glyphOf is the mark of a task by its state: done, in the works, not started, or stuck on someone. */
export function glyphOf(task) {
  if (stateFinished(task.state)) return "done";
  if (task.state === "blocked") return "waiting";
  if (task.state === "planned") return "idle";
  return "working";
}

const nameOf = labelOf;

/** startText is the line the manager is sent to start a task nobody has. */
export function startText(task) {
  return "Please start " + task.id + " (" + task.title + "): it has no open blockers and nobody on it.";
}

/** breakText is the line the manager is sent to break a ring of agents that wait on each other. */
export function breakText(names) {
  return "These agents are waiting on each other and none can move: " + names.join(", ") + ". Please break the loop.";
}

function Chip({ c, onLink }) {
  return c.to
    ? html`<button type="button" class=${"tree-chip tree-chip--" + c.kind + " is-link"} tabindex="-1" onClick=${(e) => { e.stopPropagation(); onLink(c.to); }}>${c.text}</button>`
    : html`<span class=${"tree-chip tree-chip--" + c.kind}>${c.text}</span>`;
}

/** Deps draws the view. team is the store's team push, model the graph model (or null), remote says the window is reached through the relay. */
export function Deps({ team: teamMsg, model = null, whole = null, remote = false, onOpen = null, send = null }) {
  const [folded, setFolded] = useState(() => new Set([IDLE]));
  const [focus, setFocus] = useState("");
  const [asked, setAsked] = useState({});
  const ref = useRef(null);
  if (remote) return html`<div class="view view--deps"><p class="view-note">${TEAM_RELAY_WORDS}</p></div>`;
  const team = teamOf(teamMsg);
  if (!team) return html`<div class="view view--deps"><p class="view-note">${READING_WORDS}</p></div>`;
  const scope = model && model.ok && model.teams.length === 1 ? model.teams[0] : null;
  const plan = planOf(scopeTasks(team.tasks, scope ? scope.manager : "", whole || model), folded);
  const waits = waitsOf(model);
  const rings = loopsOf(model);
  const manager = model && model.ok ? managerOf(model) : null;
  const goal = (model && model.ok && model.teams[0] && model.teams[0].goal) || team.project || "The team's goal";

  // The rows as the keyboard sees them: the tree, then the group of waits under its own heading.
  const keyRows = plan.rows.map((r) => ({ pane: r.key, depth: r.depth, parent: r.parent, kids: r.kids, collapsed: r.folded }));
  if (waits.length > 0) {
    keyRows.push({ pane: "W", depth: 0, parent: "", kids: waits.length, collapsed: folded.has("W") });
    if (!folded.has("W")) for (const w of waits) keyRows.push({ pane: w.key, depth: 1, parent: "W", kids: 0, collapsed: false });
  }
  const stop = keyRows.some((r) => r.pane === focus) ? focus : keyRows.length > 0 ? keyRows[0].pane : "";
  const toggle = (key) => {
    setFolded((f) => {
      const next = new Set(f);
      if (!next.delete(key)) next.add(key);
      return next;
    });
    setFocus(key);
  };
  const go = (key) => {
    setFocus(key);
    focusNode(ref.current, key);
  };
  // An ask is remembered with what it was about (a task's state, a ring's panes); when that changes the button is ready again.
  const sentOf = (id, stamp) => (asked[id] && asked[id].stamp === stamp ? (asked[id].ok ? "sent" : "failed") : "");
  const askManager = (id, stamp, text) => {
    if (!manager || !send) return;
    setAsked((a) => ({ ...a, [id]: { stamp, ok: send({ cmd: "paneSend", id: manager.pane, text }) } }));
  };
  const onKeyDown = (e) => {
    if (e.defaultPrevented || composing(e) || e.ctrlKey || e.altKey || e.shiftKey || e.metaKey) return;
    let el = null;
    for (let n = e.target; n && n !== e.currentTarget; n = n.parentNode) {
      if (n.getAttribute && n.getAttribute("role") === "treeitem") {
        el = n;
        break;
      }
    }
    if (!el) return;
    const key = el.getAttribute("data-pane");
    if (e.key === "Enter" || e.key === " ") {
      if (el !== e.target) return;
      const open = el.getAttribute("data-open");
      if (open && onOpen) {
        e.preventDefault();
        onOpen(open);
      }
      return;
    }
    if (foldOf(keyRows, key, e.key)) {
      e.preventDefault();
      toggle(key);
      return;
    }
    const to = stepOf(keyRows, key, e.key);
    if (to === "") {
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) e.preventDefault();
      return;
    }
    e.preventDefault();
    go(to);
  };

  const critical = new Set(plan.critical.length >= 2 ? plan.critical : []);
  const path = plan.critical.slice().reverse();
  const taskRow = (r) => {
    if (r.id === GOAL) {
      return html`<${DepRow} key=${r.key} r=${r} stop=${stop === r.key} onFocus=${() => setFocus(r.key)} onToggle=${toggle} glyph="idle" id="Goal" title=${goal} fin=${plan.estimates ? minutesWords(plan.total) : ""} />`;
    }
    if (r.id === IDLE) {
      return html`<${DepRow} key=${r.key} r=${r} stop=${stop === r.key} onFocus=${() => setFocus(r.key)} onToggle=${toggle} glyph="idle" id="" title=${plan.idle.length + (plan.idle.length === 1 ? " task" : " tasks") + " not started, no dependencies"} />`;
    }
    const t = plan.idx.byId.get(r.id);
    const blockers = openBlockers(plan.idx, r.id);
    const ready = isReady(plan.idx, r.id);
    // Where a team runs the task, its manager is the owner; else the pane the task is given to.
    const runner = teamOfTask(t, whole || model);
    const home = whole || model;
    const owner = runner && home && home.byPane.get(runner) ? home.byPane.get(runner) : t.ownerPane && home && home.byPane.get(t.ownerPane) ? home.byPane.get(t.ownerPane) : null;
    const ownerWaits = !!owner && stateOf(owner).key === "waiting";
    const ownerDown = !!owner && (stateOf(owner).key === "exited" || stateOf(owner).key === "failed");
    const chips = [];
    if (r.dup) chips.push({ kind: "dup", text: "see above", to: r.id });
    else {
      if (critical.has(r.id)) chips.push({ kind: "crit", text: "critical", to: "" });
      if (ready) chips.push({ kind: "ready", text: "ready", to: "" });
      for (const b of blockers.slice(0, 2)) chips.push({ kind: "blocked", text: "blocked by " + b.id, to: b.id });
      if (ownerWaits) chips.push({ kind: "ask", text: nameOf(home, owner.pane) + " waits on you", to: "" });
    }
    const cls = [blockers.length > 0 && !stateFinished(t.state) ? "is-blocked" : "", ready ? "is-ready" : "", critical.has(r.id) && !r.dup ? "is-crit" : "", r.dup ? "is-dup" : "", ownerWaits ? "is-waiting" : "", ownerDown ? "is-down" : ""].filter(Boolean).join(" ");
    const start = ready && !r.dup && r.parent !== IDLE
      ? html`<div class="deps-start" style=${"--depth:" + Math.min(r.depth, 12)} role="group" aria-label=${"Ready to start " + t.id}>
          <p class="deps-start-kind">Ready to start <span>all blockers merged</span></p>
          <p><b>${t.id}</b> ${clip(t.title, 120)} has no open blockers and nobody on it.</p>
          <button type="button" class="deps-start-btn deps-start-btn--main" disabled=${!manager || !send || sentOf(r.key, t.state) === "sent"} onClick=${() => askManager(r.key, t.state, startText(t))}>${sentOf(r.key, t.state) === "sent" ? "Manager asked" : manager ? "Ask " + manager.name + " to start it" : "No manager to ask"}</button>
          ${sentOf(r.key, t.state) === "failed" ? html`<span class="view-note" role="status">Not connected, so the manager was not asked.</span>` : null}
        </div>`
      : null;
    // A task another team runs is marked with that team's own task, not the raw owner.
    const otherTeam = runner && scope && runner !== scope.manager ? home && home.ok && home.teams.find((x) => x.manager === runner) : null;
    const otherTask = otherTeam ? taskOfTeam(home, otherTeam, team.tasks) : null;
    const ownerView = owner ? { name: otherTask ? taskLabelOf(otherTask) : nameOf(home, owner.pane), state: stateOf(owner).key, other: !!otherTask } : null;
    const ownerWords = owner || stateFinished(t.state) ? "" : t.state === "planned" ? "no one yet" : t.owner || "";
    return html`<${Fragment} key=${r.key}>
      <${DepRow} r=${r} stop=${stop === r.key} cls=${cls} onFocus=${() => setFocus(r.key)} onToggle=${toggle} onLink=${go}
        glyph=${glyphOf(t)} id=${t.id} title=${t.title} owner=${ownerView} ownerWords=${ownerWords} chips=${chips} openPane=${owner ? owner.pane : ""} onOpen=${onOpen && owner ? () => onOpen(owner.pane) : null}
        est=${plan.estimates && !stateFinished(t.state) && t.estimateMin > 0 ? t.estimateMin + "m" : ""} fin=${plan.estimates ? minutesWords(plan.finish(r.id)) : ""} />
      ${start}
    <//>`;
  };

  const waitHead = waits.length > 0
    ? html`<${DepRow} key="W" r=${{ key: "W", depth: 0, kids: waits.length, folded: folded.has("W") }} stop=${stop === "W"} onFocus=${() => setFocus("W")} onToggle=${toggle} glyph="idle" id="" title="Who waits on whom" sub="agents, not tasks" />`
    : null;
  const now = Date.now();
  const ringNames = (ring) => ring.map((p) => nameOf(model, p));
  return html`<div class="view view--deps">
    <div class="view-head">
      <div>
        <h2 class="view-title">Dependencies</h2>
        <p class="view-note deps-note">Work by what blocks it. The marked chain sets the finish.</p>
      </div>
    </div>
    ${plan.critical.length >= 2
      ? html`<div class="deps-path" role="group" aria-label="Critical path">
          <span class="deps-path-label">Critical path</span>
          ${path.map((id, i) => html`<${Fragment} key=${id}>${i > 0 ? html`<span class="deps-arrow" aria-hidden="true">→</span>` : null}<button type="button" class="tree-chip tree-chip--crit is-link" onClick=${() => go(id)}>${id}</button><//>`)}
          ${plan.estimates ? html`<span class="deps-path-note">about <b>${minutesWords(plan.total).replace("+", "")}</b> left, if nothing waits longer than its estimate</span>` : null}
          ${rings.map((ring, i) => html`<span class="tree-chip tree-chip--down" key=${"ring" + i}>${ring.length}-way loop</span>`)}
          ${plan.ready.length > 0 ? html`<span class="tree-chip tree-chip--ready">${plan.ready.length} ready</span>` : null}
        </div>`
      : null}
    ${rings.map((ring, i) => html`<div class="deps-deadlock" role="group" aria-label="Deadlock" key=${"dl" + i}>
      <p class="deps-deadlock-kind">Deadlock</p>
      <p>${ringNames(ring).map((n) => n + " waits on ").join("")}${ringNames(ring)[0]}. None of them can move.</p>
      <div class="deps-deadlock-acts">
        <button type="button" class="deps-start-btn deps-start-btn--main" disabled=${!manager || !send || sentOf("ring" + i, ring.join()) === "sent"} onClick=${() => askManager("ring" + i, ring.join(), breakText(ringNames(ring)))}>${sentOf("ring" + i, ring.join()) === "sent" ? "Manager asked" : manager ? "Ask " + manager.name + " to break the loop" : "No manager to ask"}</button>
        ${onOpen ? html`<button type="button" class="deps-open" onClick=${() => onOpen(ring[0])}>Open ${nameOf(model, ring[0])}</button>` : null}
      </div>
    </div>`)}
    ${team.boardError ? html`<p class="view-note" role="alert">${team.boardError}</p>` : null}
    ${team.tasks.length === 0 && !team.boardError ? html`<p class="view-note deps-empty">${NO_TASKS_WORDS}</p>` : null}
    <div class="tree deps">
      <div class="tree-head deps-head" aria-hidden="true"><span class="tree-lead"></span><span class="tree-c deps-tk">Task</span><span class="tree-c deps-ttl">Work, by what blocks it</span><span class="tree-c deps-own">Owner</span>${plan.estimates ? html`<span class="tree-c deps-est">Est</span><span class="tree-c deps-fin">Done in</span>` : null}</div>
      <div class="tree-rows" role="tree" aria-label="Dependencies" ref=${ref} onKeyDown=${onKeyDown}>
        ${plan.rows.map(taskRow)}
        ${waitHead}
        ${folded.has("W") ? null : waits.map((w) => html`<${WaitRow} key=${w.key} now=${now} w=${w} model=${model} stop=${stop === w.key} onFocus=${() => setFocus(w.key)} onOpen=${onOpen} onLink=${go} />`)}
      </div>
    </div>
  </div>`;
}

function DepRow({ r, stop, cls = "", onFocus, onToggle, onLink = null, glyph, id, title, sub = "", owner = null, ownerWords = "", chips = [], openPane = "", onOpen = null, est = "", fin = "" }) {
  const label = [id, title, owner ? owner.name : ownerWords, ...chips.map((c) => c.text), fin].filter(Boolean).join(", ");
  return html`<div class=${"tree-row deps-row " + cls} role="treeitem" data-pane=${r.key} data-open=${openPane || undefined}
    aria-level=${r.depth + 1} aria-expanded=${r.kids > 0 ? (r.folded ? "false" : "true") : undefined} aria-label=${label}
    tabindex=${stop ? "0" : "-1"} style=${"--depth:" + Math.min(r.depth, 12)} onFocus=${onFocus} onClick=${onOpen || undefined}>
    ${Array.from({ length: Math.min(r.depth, 12) }, (_, i) => html`<span class="tree-indent" key=${i} aria-hidden="true"></span>`)}
    ${r.kids > 0
      ? html`<button type="button" class="tree-toggle" tabindex="-1" aria-label=${(r.folded ? "Unfold " : "Fold ") + (id || title)} onClick=${(e) => { e.stopPropagation(); onToggle(r.key); }}>${r.folded ? "▸" : "▾"}</button>`
      : html`<span class="tree-toggle" aria-hidden="true"></span>`}
    <span class=${"tree-glyph tree-glyph--" + glyph} aria-hidden="true"></span>
    <span class="tree-c deps-tk">${id}</span>
    <span class="tree-c deps-ttl" title=${title}>${clip(title, 160)}${sub ? html` <span class="deps-sub">${sub}</span>` : null}</span>
    <span class="deps-more">
      <span class="tree-c deps-own" title=${owner ? owner.name : undefined}>${owner ? html`<span class=${"tree-glyph tree-glyph--" + owner.state} aria-hidden="true"></span> ${clip(owner.name, 14)}` : ownerWords ? html`<span class="deps-none">${ownerWords}</span>` : null}</span>
      ${chips.length > 0 ? html`<span class="tree-chips">${chips.map((c) => html`<${Chip} key=${c.kind + c.text} c=${c} onLink=${onLink} />`)}</span>` : null}
    </span>
    ${est !== "" || fin !== "" ? html`<span class="tree-c deps-est">${est}</span><span class="tree-c deps-fin">${fin}</span>` : null}
  </div>`;
}

function WaitRow({ w, now, model, stop, onFocus, onOpen, onLink }) {
  const from = nameOf(model, w.from);
  const to = w.to === "you" ? "you" : nameOf(model, w.to);
  const word = w.kind === "overlap" ? "and" : "waits on";
  const chips = [];
  if (w.kind === "loop") {
    chips.push({ kind: "down", text: "loop", to: "" });
    chips.push({ kind: "wait", text: "the other half", to: "w-" + w.to + "-" + w.from });
  }
  if (w.kind === "you") chips.push({ kind: "ask", text: "needs you", to: "" });
  const tone = w.kind === "loop" ? "tree-row--down" : w.kind === "you" ? "tree-row--ask" : "";
  const label = [from, word, to, w.why ? "for " + w.why : "", ...chips.map((c) => c.text)].filter(Boolean).join(" ");
  return html`<div class=${"tree-row deps-row " + tone} role="treeitem" data-pane=${w.key} data-open=${w.from} aria-level="2" aria-label=${label}
    tabindex=${stop ? "0" : "-1"} style="--depth:1" onFocus=${onFocus} onClick=${onOpen ? () => onOpen(w.from) : undefined}>
    <span class="tree-indent" aria-hidden="true"></span>
    <span class="tree-toggle" aria-hidden="true"></span>
    <span class=${"tree-glyph tree-glyph--" + (w.kind === "loop" ? "exited" : w.kind === "you" ? "waiting" : "idle")} aria-hidden="true"></span>
    <span class="tree-c deps-tk">${w.kind === "you" ? "you" : w.kind === "overlap" ? "files" : "wait"}</span>
    <span class="tree-c deps-ttl"><b>${clip(from, 24)}</b> <span class="deps-sub">${word}</span> <b>${clip(to, 24)}</b>${w.why ? html` <span class="deps-sub">for ${clip(w.why, 120)}</span>` : null}</span>
    <span class="deps-more">${chips.length > 0 ? html`<span class="tree-chips">${chips.map((c) => html`<${Chip} key=${c.kind + c.text} c=${c} onLink=${onLink} />`)}</span>` : null}</span>
    <span class="tree-c deps-age">${ageOf(w.since, now)}</span>
  </div>`;
}
