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
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "./Node.js";
import { Icon, firstLine } from "./Badge.js";
import { ageOf } from "./NeedsYou.js";
import { PermissionCard, DETAILS_ON_DESKTOP_WORDS, DESK_WORDS } from "../drawer/Prompt.js";
import { labelOf } from "./model.js";
import { usePaneSend } from "./ManagerBar.js";
import { showControls } from "../drawer/diff/diffModel.js";

export const KINDS = ["permission", "ask", "conflict", "down"];
export const NO_MANAGER_WORDS = "This team has no manager to ask.";
const HEADS = { permission: "Permission", ask: "Question", conflict: "Files overlap", down: "Down" };
const MAX_OPTIONS = 6;
/** tabOf is the tabindex of a block's button: not a tab stop unless the row above it is (ctx.tab false), as the chips on a row. */
export const tabOf = (ctx) => (ctx && ctx.tab === false ? -1 : 0);

const TERMINAL_WORDS = "Answer in its drawer, where the terminal is.";

const nameOf = labelOf;

/** askText is the line the manager is sent about two agents that changed the same files; o is { who, files }. */
export function askText(o) {
  return "Two agents have changed the same files: " + o.who + (o.files ? " (" + o.files + ")" : "") + ". Please decide now who keeps which changes, and tell them both.";
}

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

/** storeSendOf is the send of the context's store, or null when it has none. */
const storeSendOf = (ctx) => (ctx.store ? (m) => ctx.store.send(m) : null);

/** useAsk is a one-shot send to a pane: state is "", "sending", "sent" or "failed" (the socket or the desk refused it; words says why), and a second call before the redraw does nothing. */
export function useAsk(ctx) {
  const [outcome, go] = usePaneSend(ctx.snap, storeSendOf(ctx));
  const state = !outcome ? "" : outcome.kind === "refused" ? "failed" : outcome.kind;
  const send = (pane, text) => {
    if (!ctx.store) return;
    go(pane, text);
  };
  return [state, send, state === "failed" ? outcome.text : ""];
}

export function Buttons({ children }) {
  return html`<div class="dec-actions" role="group" aria-label="Choices">${children}</div>`;
}

function Permission({ model, need, ctx, tail }) {
  const s = ctx.snap && ctx.snap.structured && ctx.snap.structured.id === need.pane ? ctx.snap.structured : null;
  const card = s && s.card ? s.card : null;
  const node = model.byPane.get(need.pane);
  if (card && node && ctx.store) {
    return html`<${PermissionCard} node=${node} card=${card} snap=${ctx.snap} store=${ctx.store} tail=${tail} tabIndex=${tabOf(ctx)} />`;
  }
  return null;
}

function Question({ need, ctx, options, tail }) {
  const [picked, setPicked] = useState("");
  // One send at a time: a second click before the card is drawn again finds it answered, and a refusal leaves the choices to be pressed again.
  const [out, go] = usePaneSend(ctx.snap, storeSendOf(ctx));
  const busy = !!out && out.kind !== "refused";
  const pick = (label) => {
    if (busy || !ctx.store) return;
    // go says whether this click started the send: a second click in the same tick must not change the choice named as sent.
    if (go(need.pane, label)) setPicked(label);
  };
  if (options.length === 0) return null;
  if (!ctx.store) return html`<${Buttons}>${tail}<//><p class="dec-more">${DESK_WORDS}</p>`;
  const note = out && out.kind === "refused" ? showControls(clip(out.text, 160)) : "";
  const status = out && out.kind === "sent" ? "Sent “" + clip(picked, 60) + "”. Waiting for the agent." : out && out.kind === "sending" ? "Sending…" : note;
  return html`<${Buttons}>
      ${options.map((label, i) => html`<button type="button" tabindex=${tabOf(ctx)} class=${"dec-btn" + (i === 0 ? " dec-btn--primary" : "")} data-option=${label} aria-disabled=${busy ? "true" : undefined} onClick=${() => pick(label)}>${clip(label, 60)}</button>`)}${tail}
    <//>
    ${note
      ? html`<p key="refused" class="dec-status dec-status--error" role="alert">${note}</p>`
      : html`<p key="status" class="dec-status" role="status">${status}</p>`}`;
}

function Conflict({ model, need, ctx }) {
  const [state, send, why] = useAsk(ctx);
  const manager = ctx.manager || null;
  const ask = () => {
    if (!manager) return;
    send(manager.pane, askText({ who: nameOf(model, need.pane) + " and " + nameOf(model, need.other), files: String(need.text || "") }));
  };
  const see = ctx.onSee ? html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn" onClick=${() => ctx.onSee(need.pane)}>See in Review</button>` : null;
  if (!ctx.store) return html`<${Buttons}>${see}<//><p class="dec-more">${DESK_WORDS}</p>`;
  return html`<${Buttons}>
      <button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--primary" disabled=${!manager} aria-disabled=${state === "sent" || state === "sending" ? "true" : undefined} title=${manager ? "" : NO_MANAGER_WORDS} onClick=${ask}>${state === "sent" ? "Manager asked" : "Ask mgr to sort it out"}</button>
      ${see}
    <//>
    ${state === "failed" ? html`<p class="dec-status dec-status--error" role="alert">${showControls(clip(why, 160))}</p>` : null}`;
}

function Down({ need, who, ctx }) {
  const [state, send, why] = useAsk(ctx);
  const manager = ctx.manager || null;
  const reassign = manager && need.task && ctx.store
    ? html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn" aria-disabled=${state === "sent" || state === "sending" ? "true" : undefined} onClick=${() => send(manager.pane, "Please reassign " + need.task + ": " + who + " is down.")}>${state === "sent" ? "Manager asked" : "Ask mgr to reassign " + need.task}</button>`
    : null;
  const terminal = ctx.onOpen ? html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--ghost" onClick=${() => ctx.onOpen(need.pane)}>See terminal</button>` : null;
  return html`<${Buttons}>
      ${ctx.onRestart && ctx.store ? html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--primary" onClick=${() => ctx.onRestart(need.pane)}>Restart</button>` : null}
      ${reassign}${terminal}
    <//>
    ${ctx.store ? null : html`<p class="dec-more">${DESK_WORDS}</p>`}
    ${state === "failed" ? html`<p class="dec-status dec-status--error" role="alert">${showControls(clip(why, 160))}</p>` : null}`;
}

/**
 * DecisionBlock draws one item. model is the graph model, need an item of it
 * (see decisionsOf), ctx as in the head of this file.
 */
export function DecisionBlock({ model, need, ctx = {} }) {
  const structuredCard = need.kind === "permission" && ctx.store && ctx.snap && ctx.snap.structured && ctx.snap.structured.id === need.pane && ctx.snap.structured.card;
  const d = decisionOf(model, need, ctx.now || Date.now(), structuredCard || null);
  const open = ctx.onOpen && need.kind !== "conflict" && need.kind !== "down" ? html`<button type="button" tabindex=${tabOf(ctx)} class="dec-btn dec-btn--ghost" onClick=${() => ctx.onOpen(need.pane)}>Open ${clip(d.who, 20)}</button>` : null;
  // When a button of the block is pressed and the block goes (the answer came), the row it was under takes the keyboard.
  const pressed = useRef(false);
  const latest = useRef(ctx);
  latest.current = ctx;
  useEffect(() => () => { if (pressed.current && latest.current.onGone) latest.current.onGone(need.pane); }, []);
  const hasOptions = questionOptions(ctx.panes && ctx.panes.get(need.pane)).length > 0;
  let body = null;
  if (need.kind === "permission") body = html`<${Permission} model=${model} need=${need} ctx=${ctx} tail=${open} />`;
  else if (need.kind === "ask") body = html`<${Question} need=${need} ctx=${ctx} options=${questionOptions(ctx.panes && ctx.panes.get(need.pane))} tail=${open} />`;
  else if (need.kind === "conflict") body = html`<${Conflict} model=${model} need=${need} ctx=${ctx} />`;
  else if (need.kind === "down") body = html`<${Down} need=${need} who=${d.who} ctx=${ctx} />`;
  const age = need.kind === "down" ? d.age + " ago" : need.kind === "conflict" ? d.age : "waiting " + d.age;
  return html`<section class=${"ask dec dec--" + d.tone} role="group" aria-label=${d.head + " for " + d.who} data-decision=${need.kind} data-pane=${need.pane} onClick=${() => { pressed.current = true; }}>
    <header class="dec-head">
      <svg class="dec-icon" width="16" height="16" viewBox="-10 -10 20 20" aria-hidden="true"><${Icon} kind=${need.kind} x=${0} y=${0} /></svg>
      <span class="dec-kind">${d.head}</span>
      ${d.age ? html`<span class="dec-age">${age}</span>` : null}
    </header>
    <p class="dec-line">${need.kind === "conflict"
      ? html`<b>${d.who}</b>${d.who2 ? html` and <b>${d.who2}</b>` : null}`
      : html`<span class="dec-who">${d.who}</span>${d.task ? html` <span class="dec-task">${d.task}</span>` : null} `}${d.line}</p>
    ${d.command && !structuredCard ? html`<pre class="dec-command" aria-label="Command">${clip(d.command, 600)}</pre>` : null}
    ${need.kind === "permission" && !structuredCard ? html`<p class="dec-more">${ctx.store ? TERMINAL_WORDS : DESK_WORDS}</p>` : null}
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
