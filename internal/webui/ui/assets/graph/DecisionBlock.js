// A decision block: what a row of the outline puts under itself when its agent
// needs the person. Four kinds, from the graph's needs-you items: a permission, a
// question, two agents changing the same file (conflict) and an agent that is
// down. The block says who and what, and carries the buttons that answer it.
//
// Answers are buttons and nothing else; there is no key that answers from the
// outline. A structured pane's permission is answered with the same card the
// drawer draws (PermissionCard), so it is answered once whichever copy is
// pressed. A terminal pane's permission is answered in the drawer, where its
// terminal is, and the block says so. A question's options are sent to the
// pane as text, as the manager bar sends a line.
//
// The row passes ctx: { snap, store, panes, onOpen, onSee, onRestart, manager,
// now }. Each is optional; a button whose action is missing is not drawn.
// snap and store are the store's; panes maps a pane id to its entry in the state
// push; onOpen(pane) opens the drawer; onSee(pane) opens Changes; onRestart(pane)
// asks to restart (the drawer's confirm); manager is { pane, name } or null.
//
// Every text here is from an agent and is drawn as text.
import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "./Node.js";
import { Icon, firstLine } from "./Badge.js";
import { ageOf } from "./NeedsYou.js";
import { askText } from "./OverlapBar.js";
import { PermissionCard, NOT_CONNECTED_WORDS, DETAILS_ON_DESKTOP_WORDS } from "../drawer/Prompt.js";

export const KINDS = ["permission", "ask", "conflict", "down"];
export const NO_MANAGER_WORDS = "This team has no manager to ask.";
const HEADS = { permission: "Permission", ask: "Question", conflict: "Files overlap", down: "Down" };
const MAX_OPTIONS = 6;
const TERMINAL_WORDS = "Answer in its drawer, where the terminal is.";

const nameOf = (model, id) => {
  const n = model.byPane.get(id);
  return n && n.name ? n.name : id;
};

/** isDecision says whether the item is one of the four kinds a block draws. */
export function isDecision(need) {
  return !!need && KINDS.includes(need.kind);
}

/** decisionsOf is the items of a pane that draw a block, in the graph's order. A conflict is drawn under the first pane of the item only; the other row carries the overlap chip. */
export function decisionsOf(model, paneId) {
  return (model.needsByPane.get(paneId) || []).filter((n) => isDecision(n) && (n.kind !== "conflict" || n.pane === paneId));
}

/** questionOptions are the labels a question offers, from the state push's ask: the first question's, empty when it has none or is more than one question. */
export function questionOptions(pane) {
  const ask = pane && pane.ask;
  const qs = ask && Array.isArray(ask.questions) ? ask.questions : [];
  if (qs.length !== 1 || qs[0].multiSelect) return [];
  const opts = Array.isArray(qs[0].options) ? qs[0].options : [];
  return opts.map((o) => (o && typeof o.label === "string" ? o.label : "")).filter((l) => l !== "").slice(0, MAX_OPTIONS);
}

/**
 * decisionOf is what a block says for one item: { id, kind, head, who, task, age, line, command, tone }.
 * line is the sentence, command the text that goes in the inset (a permission's
 * command), both "" when there is nothing to say. A text that was replaced
 * before it left the desk is not shown, and the block says it is on the desktop.
 */
export function decisionOf(model, need, now = Date.now(), card = null) {
  const who = nameOf(model, need.pane);
  const other = need.other ? nameOf(model, need.other) : "";
  const text = need.textHidden ? "" : String(need.text || "");
  const out = {
    id: need.id,
    kind: need.kind,
    head: HEADS[need.kind] || need.kind,
    who,
    task: need.task || "",
    age: ageOf(need.time, now),
    line: "",
    command: "",
    tone: need.kind === "down" ? "down" : "waiting",
  };
  if (need.kind === "permission") {
    // A structured card can be a file edit with no command: say "run" only for a command.
    const command = card ? String(card.command || "") : text;
    out.command = command;
    out.line = command ? "asks to run" : "asks permission"; // the card body says what it is
  } else if (need.kind === "ask") {
    out.line = firstLine(text) || "asks a question";
  } else if (need.kind === "conflict") {
    out.who2 = other;
    out.line = " both change " + (firstLine(text) || "the same files") + ". They are on separate worktrees, so the clash shows up at merge.";
  } else if (need.kind === "down") {
    out.line = firstLine(text) || "is down";
  }
  if (need.textHidden) out.line = need.kind === "conflict" ? out.line : DETAILS_ON_DESKTOP_WORDS;
  return out;
}

/** useAsk is a one-shot send to a pane: state is "", "sent" or "failed", and a second call before the redraw does nothing. */
export function useAsk(ctx) {
  const [state, setState] = useState("");
  const done = useRef(false);
  const send = (pane, text) => {
    if (done.current) return;
    if (ctx.store && ctx.store.send({ cmd: "paneSend", id: pane, text })) {
      done.current = true;
      setState("sent");
    } else {
      setState("failed");
    }
  };
  return [state, send];
}

export function Buttons({ children }) {
  return html`<div class="dec-actions" role="group" aria-label="Choices">${children}</div>`;
}

function Permission({ model, need, ctx, tail }) {
  const s = ctx.snap && ctx.snap.structured && ctx.snap.structured.id === need.pane ? ctx.snap.structured : null;
  const card = s && s.card ? s.card : null;
  const node = model.byPane.get(need.pane);
  if (card && node && ctx.store) {
    return html`<${PermissionCard} node=${node} card=${card} snap=${ctx.snap} store=${ctx.store} tail=${tail} />`;
  }
  return null;
}

function Question({ need, ctx, options, tail }) {
  const [sent, setSent] = useState("");
  const [note, setNote] = useState("");
  // Set at the click: a second click before the card is drawn again finds it answered.
  const done = useRef(false);
  const pick = (label) => {
    if (done.current) return;
    if (ctx.store && ctx.store.send({ cmd: "paneSend", id: need.pane, text: label })) {
      done.current = true;
      setSent(label);
      setNote("");
    } else {
      setNote(NOT_CONNECTED_WORDS);
    }
  };
  if (options.length === 0) return null;
  return html`<${Buttons}>
      ${options.map((label, i) => html`<button type="button" class=${"dec-btn" + (i === 0 ? " dec-btn--primary" : "")} data-option=${label} disabled=${!!sent} onClick=${() => pick(label)}>${clip(label, 60)}</button>`)}${tail}
    <//>
    <p class=${"dec-status" + (note ? " dec-status--error" : "")} role=${note ? "alert" : "status"}>${sent ? "Sent “" + clip(sent, 60) + "”. Waiting for the agent." : note}</p>`;
}

function Conflict({ model, need, ctx }) {
  const [state, send] = useAsk(ctx);
  const manager = ctx.manager || null;
  const ask = () => {
    if (!manager) return;
    send(manager.pane, askText({ who: nameOf(model, need.pane) + " and " + nameOf(model, need.other), files: String(need.text || "") }));
  };
  return html`<${Buttons}>
      <button type="button" class="dec-btn dec-btn--primary" disabled=${!manager || state === "sent"} title=${manager ? "" : NO_MANAGER_WORDS} onClick=${ask}>${state === "sent" ? "Manager asked" : "Ask mgr to sort it out"}</button>
      ${ctx.onSee ? html`<button type="button" class="dec-btn" onClick=${() => ctx.onSee(need.pane)}>See in Review</button>` : null}
    <//>
    ${state === "failed" ? html`<p class="dec-status dec-status--error" role="alert">${NOT_CONNECTED_WORDS}</p>` : null}`;
}

function Down({ need, who, ctx }) {
  const [state, send] = useAsk(ctx);
  const manager = ctx.manager || null;
  const reassign = manager && need.task
    ? html`<button type="button" class="dec-btn" disabled=${state === "sent"} onClick=${() => send(manager.pane, "Please reassign " + need.task + ": " + who + " is down.")}>${state === "sent" ? "Manager asked" : "Ask mgr to reassign " + need.task}</button>`
    : null;
  const terminal = ctx.onOpen ? html`<button type="button" class="dec-btn dec-btn--ghost" onClick=${() => ctx.onOpen(need.pane)}>See terminal</button>` : null;
  return html`<${Buttons}>
      ${ctx.onRestart ? html`<button type="button" class="dec-btn dec-btn--primary" onClick=${() => ctx.onRestart(need.pane)}>Restart</button>` : null}
      ${reassign}${terminal}
    <//>
    ${state === "failed" ? html`<p class="dec-status dec-status--error" role="alert">${NOT_CONNECTED_WORDS}</p>` : null}`;
}

/**
 * DecisionBlock draws one item. model is the graph model, need an item of it
 * (see decisionsOf), ctx as in the head of this file.
 */
export function DecisionBlock({ model, need, ctx = {} }) {
  const structuredCard = need.kind === "permission" && ctx.snap && ctx.snap.structured && ctx.snap.structured.id === need.pane && ctx.snap.structured.card;
  const d = decisionOf(model, need, ctx.now || Date.now(), structuredCard || null);
  const open = ctx.onOpen && need.kind !== "conflict" && need.kind !== "down" ? html`<button type="button" class="dec-btn dec-btn--ghost" onClick=${() => ctx.onOpen(need.pane)}>Open ${clip(d.who, 20)}</button>` : null;
  const hasOptions = questionOptions(ctx.panes && ctx.panes.get(need.pane)).length > 0;
  let body = null;
  if (need.kind === "permission") body = html`<${Permission} model=${model} need=${need} ctx=${ctx} tail=${open} />`;
  else if (need.kind === "ask") body = html`<${Question} need=${need} ctx=${ctx} options=${questionOptions(ctx.panes && ctx.panes.get(need.pane))} tail=${open} />`;
  else if (need.kind === "conflict") body = html`<${Conflict} model=${model} need=${need} ctx=${ctx} />`;
  else if (need.kind === "down") body = html`<${Down} need=${need} who=${d.who} ctx=${ctx} />`;
  const age = need.kind === "down" ? d.age + " ago" : need.kind === "conflict" ? d.age : "waiting " + d.age;
  return html`<section class=${"ask dec dec--" + d.tone} role="group" aria-label=${d.head + " for " + d.who} data-decision=${need.kind} data-pane=${need.pane}>
    <header class="dec-head">
      <svg class="dec-icon" width="16" height="16" viewBox="-10 -10 20 20" aria-hidden="true"><${Icon} kind=${need.kind} x=${0} y=${0} /></svg>
      <span class="dec-kind">${d.head}</span>
      ${d.age ? html`<span class="dec-age">${age}</span>` : null}
    </header>
    <p class="dec-line">${need.kind === "conflict"
      ? html`<b>${d.who}</b>${d.who2 ? html` and <b>${d.who2}</b>` : null}`
      : html`<span class="dec-who">${d.who}</span>${d.task ? html` <span class="dec-task">${d.task}</span>` : null} `}${d.line}</p>
    ${d.command && !structuredCard ? html`<pre class="dec-command" aria-label="Command">${clip(d.command, 600)}</pre>` : null}
    ${need.kind === "permission" && !structuredCard ? html`<p class="dec-more">${TERMINAL_WORDS}</p>` : null}
    ${body}
    ${((need.kind === "permission" && !structuredCard) || (need.kind === "ask" && !hasOptions)) && open ? html`<${Buttons}>${open}<//>` : null}
  </section>`;
}

/** Decisions draws every block a pane has, in the graph's order: the slot a row puts under itself. skip names a kind not to draw (the drawer shows the row's own permission, question and down when it is open). */
export function Decisions({ model, pane, ctx = {}, skip = [] }) {
  const items = decisionsOf(model, pane).filter((n) => !skip.includes(n.kind));
  if (items.length === 0) return null;
  return html`<div class="decs-box"><div class="decs">${items.map((n) => html`<${DecisionBlock} key=${n.id || n.kind + n.pane} model=${model} need=${n} ctx=${ctx} />`)}</div></div>`;
}
