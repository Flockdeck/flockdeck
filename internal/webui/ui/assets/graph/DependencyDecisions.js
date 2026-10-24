// The two decisions of the Dependencies view, drawn as decision blocks: a
// deadlock (two or more agents each waiting on the next, so none can move) and a
// task that is ready to start (no open blocker and nobody on it). They are the
// frame and buttons of DecisionBlock.js; the answers are buttons that send the
// manager, or the agent, a line with paneSend, once.
//
// loopOf finds the deadlock from the graph's waits: a pane that waits on a helper
// (waitingOn kind "helpers" with a ref) is an edge, and a path of edges that
// returns to where it began is a loop. The view gives the ready task.
//
// Every name, reason and title is from an agent and is drawn as text.
import { html } from "../html.js";
import { clip } from "./Node.js";
import { Icon } from "./Badge.js";
import { Buttons, useAsk, tabOf, NO_MANAGER_WORDS } from "./DecisionBlock.js";
import { NOT_CONNECTED_WORDS, DESK_WORDS } from "../drawer/Prompt.js";
import { labelOf } from "./model.js";

const nameOf = labelOf;

/** loopOf is the first loop of waits in the model, as [{ pane, name, on, onName, why }] in the order they wait on each other, or null. A pane waiting on itself or on a pane that is not a node is no edge. */
export function loopOf(model) {
  const next = new Map();
  for (const n of model.nodes) {
    const w = n.waitingOn;
    if (w && w.kind === "helpers" && w.ref && w.ref !== n.pane && model.byPane.has(w.ref)) next.set(n.pane, { on: w.ref, why: w.text || "" });
  }
  for (const start of next.keys()) {
    const path = [];
    const at = new Map();
    let cur = start;
    while (cur && next.has(cur) && !at.has(cur)) {
      at.set(cur, path.length);
      path.push(cur);
      cur = next.get(cur).on;
    }
    if (cur && at.has(cur)) {
      return path.slice(at.get(cur)).map((pane) => {
        const e = next.get(pane);
        return { pane, name: nameOf(model, pane), on: e.on, onName: nameOf(model, e.on), why: e.why };
      });
    }
  }
  return null;
}

/** loopText is the sentence of the deadlock block, as parts: names are drawn bold. */
export function loopParts(loop) {
  return loop.map((s) => ({ who: s.name, on: s.onName, why: s.why }));
}

/** Deadlock draws the block for a loop from loopOf. ctx is { store, manager, onOpen, age }. */
export function Deadlock({ model, loop, ctx = {} }) {
  const [state, send] = useAsk(ctx);
  const [stubbed, sendStub] = useAsk(ctx);
  const manager = ctx.manager || null;
  const last = loop[loop.length - 1];
  const parts = loopParts(loop);
  const desk = !ctx.store;
  const note = state === "failed" || stubbed === "failed" ? html`<p class="dec-status dec-status--error" role="alert">${NOT_CONNECTED_WORDS}</p>` : null;
  return html`<section class="ask dec dec--down" role="group" aria-label="Deadlock" data-decision="deadlock">
    <header class="dec-head">
      <svg class="dec-icon" width="16" height="16" viewBox="-10 -10 20 20" aria-hidden="true"><${Icon} kind="down" x=${0} y=${0} /></svg>
      <span class="dec-kind">Deadlock</span>
      ${ctx.age ? html`<span class="dec-age">${ctx.age}</span>` : null}
    </header>
    <p class="dec-line">${parts.map((p, i) => html`<b>${p.who}</b> waits on <b>${p.on}</b>${p.why ? " for " + clip(p.why, 80) : ""}${i < parts.length - 1 ? ", and " : ". "}`)}${loop.length === 2 ? "Neither can move." : "None of them can move."}</p>
    <${Buttons}>
      ${desk ? null : html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--primary" disabled=${!manager || state === "sent"} title=${manager ? "" : NO_MANAGER_WORDS}
        onClick=${() => manager && send(manager.pane, "These agents wait on each other and none can move: " + loop.map((s) => s.name).join(", ") + ". Please break the loop.")}>${state === "sent" ? "Manager asked" : "Ask mgr to break the loop"}</button>`}
      ${desk ? null : html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn" disabled=${stubbed === "sent"}
        onClick=${() => sendStub(last.pane, "You and " + last.onName + " wait on each other. Use a stub for what you need of " + last.onName + " so you can move.")}>${stubbed === "sent" ? clip(last.name, 20) + " told" : "Tell " + clip(last.name, 20) + " to use a stub"}</button>`}
      ${ctx.onOpen ? html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--ghost" onClick=${() => ctx.onOpen(last.pane)}>Open ${clip(last.name, 20)}</button>` : null}
    <//>
    ${desk ? html`<p class="dec-more">${DESK_WORDS}</p>` : null}
    ${note}
  </section>`;
}

/** ReadyToStart draws the block for task = { id, title }. ctx is { store, manager }. */
export function ReadyToStart({ task, ctx = {} }) {
  const [state, send] = useAsk(ctx);
  const manager = ctx.manager || null;
  return html`<section class="ask dec dec--ok" role="group" aria-label=${"Ready to start " + task.id} data-decision="ready" data-task=${task.id}>
    <header class="dec-head">
      <svg class="dec-icon" width="16" height="16" viewBox="-10 -10 20 20" aria-hidden="true"><path d="M-6 0.5 L-2 4.5 L6 -4" fill="none" stroke="currentColor" stroke-width="1.8" /></svg>
      <span class="dec-kind">Ready to start</span>
      <span class="dec-age">all blockers merged</span>
    </header>
    <p class="dec-line"><b>${task.id}</b> ${clip(task.title, 80)} has no open blockers and nobody on it.</p>
    ${ctx.store ? null : html`<p class="dec-more">${DESK_WORDS}</p>`}
    ${ctx.store ? html`<${Buttons}>
      <button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--primary" disabled=${!manager || state === "sent"} title=${manager ? "" : NO_MANAGER_WORDS}
        onClick=${() => manager && send(manager.pane, "Please start " + task.id + ": " + task.title)}>${state === "sent" ? "Manager asked" : "Ask mgr to start it"}</button>
    <//>` : null}
    ${state === "failed" ? html`<p class="dec-status dec-status--error" role="alert">${NOT_CONNECTED_WORDS}</p>` : null}
  </section>`;
}
