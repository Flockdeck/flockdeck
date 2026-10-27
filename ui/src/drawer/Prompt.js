// The card at the top of the drawer for an agent that is waiting on the person:
// a permission prompt, or a question.
//
// - A terminal pane's permission shows the real tool, command or file and the
//   diff, as the state push's permission view has them, and a button for each
//   choice the prompt offers. A button types the choice into the terminal over
//   the terminal socket (Yes is Enter, the prompt's default, and No is Esc), which
//   is sealed in a window reached through the relay. That is the one way a phone
//   answers a permission, and only for a terminal pane.
// - A structured pane's permission is answered with permissionAnswer, with the
//   card's number, the agent's request id and the option's id exactly as the card
//   carried them, and only in a window on the desk. A remote window shows the
//   request with no buttons and says to answer it on the desktop.
// - A question shows its text and options, and the box below it sends the answer
//   with paneSend by the pane's id, which the desk allows for a question.
//
// Nothing here picks a choice on its own: no button has the keyboard, no key
// answers, and an option that remembers its choice (allow_always) is a button
// like the others. A choice is sent once for each card, so a second click, or a
// key held down, cannot answer the prompt that comes next. A card that the desk
// refuses an answer to is shown with its error and can be answered again.
//
// The text on a card is what an agent, a tool or a file said. It is drawn as
// text and never as markup.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { isViewer, VIEWER_WORDS } from "../lib/role.js";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { shownName } from "../graph/model.js";
import { ManagerBar } from "../graph/ManagerBar.js";
import { QuestionForm } from "./questions.js";

/** KEYS is what each terminal choice types: Yes is Enter, the default of Claude
 *  Code's prompt, and No is Esc, its own "No, and tell Claude what to do
 *  differently" (docs/status-matrix.md, B4 and B7). An option with any other id
 *  has no keys and gets no button on a terminal pane. */
export const KEYS = Object.freeze({ yes: "\r", no: "\x1b" });

export const DESK_WORDS = "Answer this on the desktop.";
export const NOT_CONNECTED_WORDS = "The terminal is not connected, so nothing was sent.";
export const NOT_DELIVERED_WORDS = "The choice did not reach the terminal. Try again.";

/** ANSWER_WAIT_MS is how long the buttons stay locked after the keys were written. An agent that ignores them leaves the card up, so the buttons come back. */
export const ANSWER_WAIT_MS = 5000;
export const NOT_TAKEN_WORDS = "The agent has not taken the choice yet. You can choose again.";
export const NOT_ANSWERED_WORDS = "Not connected to Flockdeck, so the answer was not sent.";
export const IN_TERMINAL_WORDS = "The choices are in the terminal below.";
export const DETAILS_ON_DESKTOP_WORDS = "The details are on the desktop.";

/** MAX_DIFF_LINES is how many lines of a diff the card draws. The rest is said, not drawn. */
export const MAX_DIFF_LINES = 200;

/** diffRows splits a diff into the rows the card draws: each line with its kind
 *  (added, removed or context, from its first character), and how many lines were
 *  left out past MAX_DIFF_LINES. */
export function diffRows(diff) {
  const all = String(diff || "").split("\n");
  if (all.length && all[all.length - 1] === "") all.pop();
  const rows = all.slice(0, MAX_DIFF_LINES).map((text) => ({
    text,
    kind: text.startsWith("+") ? "add" : text.startsWith("-") ? "del" : "ctx",
  }));
  return { rows, more: Math.max(0, all.length - MAX_DIFF_LINES) };
}

/** isWaiting says the drawer's pane is waiting on the person, from what the state
 *  push says of it and what the graph says of its node. */
export function waitingOn(node, pane) {
  if (pane && pane.permission) return "permission";
  if (pane && pane.ask && Array.isArray(pane.ask.questions) && pane.ask.questions.length > 0) return "ask";
  if (node && node.waitingOn && node.waitingOn.kind === "user") return "text";
  return null;
}

const idOf = (...parts) => parts.map((p) => String(p == null ? "" : p)).join("\u0001");

function Details({ permission }) {
  const diff = permission.diff ? diffRows(permission.diff) : null;
  return html`
    ${permission.summary ? html`<p class="prompt-summary">${permission.summary}</p>` : null}
    ${permission.command ? html`<pre class="prompt-command" aria-label="Command">${permission.command}</pre>` : null}
    ${permission.file ? html`<p class="prompt-file"><span class="prompt-label">File</span> <code>${permission.file}</code></p>` : null}
    ${permission.description ? html`<p class="prompt-desc">${permission.description}</p>` : null}
    ${permission.body
      ? html`<p class="prompt-label">${permission.kind === "switch_mode" ? "Plan, from the agent" : "From the agent"}</p>
          <pre class="prompt-body" aria-label=${permission.kind === "switch_mode" ? "Plan" : "Text from the agent"}>${permission.body}</pre>`
      : null}
    ${diff
      ? html`<pre class="prompt-diff" aria-label="Changes">${diff.rows.map((r) => html`<span class=${"prompt-diff-" + r.kind}>${r.text}</span>`)}</pre>
          ${diff.more > 0 ? html`<p class="prompt-more">${diff.more} more lines are on the desktop.</p>` : null}`
      : null}
  `;
}

// What a card shows of a permission: its details, or for one made fit for the relay
// (redacted) only its kind and that the rest is on the desktop. The details of a
// redacted card are never drawn, whatever it still carries, on any path.
function PermissionBody({ permission }) {
  if (permission.redacted) {
    return html`<p class="prompt-summary">${permission.summary || permission.tool}</p>
      <p class="prompt-more">${DETAILS_ON_DESKTOP_WORDS}</p>`;
  }
  return html`<${Details} permission=${permission} />`;
}

// A terminal pane's permission card. A choice is "sending" from the click until
// the keys are written to the socket, and "sent" after; keys lost on the way (a
// sealed frame that could not be sealed, a socket that closed) put the buttons
// back and say so.
function TerminalPermission({ permission, sendKeys, waitMs = ANSWER_WAIT_MS, viewer = false }) {
  const [sent, setSent] = useState(null); // { name, written }
  const [note, setNote] = useState("");
  // Set at the click, not at the render after it: three clicks before the card
  // is drawn again would otherwise each find it unanswered.
  const busy = useRef(null); // the token of the choice in flight, or null
  const options = (permission.options || []).filter((o) => o && Object.prototype.hasOwnProperty.call(KEYS, o.id));
  // The card goes when the agent takes the choice. If it is still here after a while the keys were ignored.
  const written = !!(sent && sent.written);
  useEffect(() => {
    if (!written) return undefined;
    const token = sent.token;
    const t = setTimeout(() => {
      if (busy.current !== token) return;
      busy.current = null;
      setSent(null);
      setNote(NOT_TAKEN_WORDS);
    }, waitMs);
    return () => clearTimeout(t);
  }, [written, waitMs]);
  const choose = (opt) => {
    if (busy.current || viewer) return;
    const token = {};
    busy.current = token;
    const name = opt.name || opt.id;
    setSent({ name, written: false, token });
    setNote("");
    const done = (ok) => {
      if (ok) {
        setSent((s) => (s && s.token === token ? { ...s, written: true } : s));
      } else {
        if (busy.current !== token) return;
        busy.current = null;
        setSent(null);
        setNote(NOT_DELIVERED_WORDS);
      }
    };
    if (!(sendKeys && sendKeys(KEYS[opt.id], done))) {
      setSent(null);
      if (busy.current === token) busy.current = null;
      setNote(NOT_CONNECTED_WORDS);
    }
  };
  return html`
    <${PermissionBody} permission=${permission} />
    ${options.length > 0
      ? html`<div class="prompt-choices" role="group" aria-label="Choices">
          ${options.map((o) => html`<button type="button" class=${"prompt-choice prompt-choice--" + String(o.kind || "")} data-option=${o.id} disabled=${!!sent || viewer} onClick=${() => choose(o)}>${clip(o.name || o.id, 80)}</button>`)}
        </div>`
      : html`<p class="prompt-more">${IN_TERMINAL_WORDS}</p>`}
    <p key="status" class="prompt-status" role="status">${viewer ? VIEWER_WORDS : sent ? (sent.written ? "Sent “" + clip(sent.name, 80) + "”. Waiting for the agent." : "Sending “" + clip(sent.name, 80) + "”…") : ""}</p>
    ${!viewer && !sent && note ? html`<p key="refused" class="prompt-status prompt-status--error" role="alert">${note}</p>` : null}
  `;
}

/** PermissionCard is a structured pane's permission card, answered with
 *  permissionAnswer in a window on the desk. The strip's card and the one drawn in
 *  the conversation are this same component. What was answered is in the store's
 *  asking, which every copy of the card reads and the store keeps to one answer
 *  for each card, so the card is answered once whichever copy is pressed, or
 *  pressed twice. */
export function PermissionCard({ node, card, snap, store, tail = null, tabIndex = undefined }) {
  const [note, setNote] = useState("");
  const remote = !!snap.remote;
  const a = snap.answer;
  const k = snap.asking;
  const asked = k && k.id === node.pane && k.cardSeq === card.seq && k.requestId === card.requestId ? k : null;
  const mine = a && a.id === node.pane && a.cardSeq === card.seq && a.requestId === card.requestId ? a : null;
  const reply = mine && (!asked || mine.seq > asked.after) ? mine : null;
  const refused = reply && !reply.ok && reply.error ? showControls(clip(String(reply.error), 300)) : "";
  const done = !!asked && !refused;
  const all = Array.isArray(card.options) ? card.options.filter((o) => o && typeof o.id === "string") : [];
  const asksQuestions = Array.isArray(card.questions) && card.questions.length > 0;
  // A question card is allowed by the one option of kind allow_once, with answers. One without
  // questions, or an option of any other kind (allow_always), is never an Allow on it.
  const allows = (o) => o.kind === "allow_once";
  const isQuestion = card.kind === "question" || asksQuestions;
  const options = isQuestion ? all.filter((o) => !String(o.kind || "").startsWith("allow") || (asksQuestions && allows(o))) : all;
  const answer = (opt, answers) => {
    if (remote || done) return;
    const cmd = { cmd: "permissionAnswer", id: node.pane, cardSeq: card.seq, requestId: card.requestId, optionId: opt.id };
    if (answers) cmd.answers = answers;
    const sent = store.send(cmd);
    if (sent) {
      setNote("");
    } else {
      setNote(NOT_ANSWERED_WORDS);
    }
  };
  const says = refused || note || (done ? (reply && reply.ok ? "Answered." : "Sent. Waiting for the agent.") : remote ? DESK_WORDS : "");
  return html`
    <${PermissionBody} permission=${card} />
    ${remote
      ? null
      : asksQuestions
      ? html`<${QuestionForm} key=${card.seq + ":" + card.requestId} card=${card} done=${done} tabIndex=${tabIndex} allow=${options.find(allows)} refuse=${options.find((o) => !allows(o))}
          onSend=${(opt, a) => { if (a) answer(opt, a); }} onSkip=${(opt) => answer(opt)} />${tail}`
      : html`<div class="prompt-choices" role="group" aria-label="Choices">
          ${options.map((o) => html`<button type="button" class=${"prompt-choice prompt-choice--" + String(o.kind || "")} data-option=${o.id} data-kind=${o.kind || ""} tabindex=${tabIndex} disabled=${done} onClick=${() => answer(o)}>${clip(o.name || o.id, 80)}</button>`)}${tail}
        </div>`}
    <p key="status" class="prompt-status" role="status">${refused || note ? "" : says}</p>
    ${refused || note ? html`<p key="refused" class="prompt-status prompt-status--error" role="alert">${refused || note}</p>` : null}
  `;
}

/** StreamPermission is the card drawn in the conversation where the permission
 *  was asked: the strip's card, the same component, under its own heading. */
export function StreamPermission({ node, card, snap, store }) {
  return html`<section class="prompt prompt--stream" role="region" aria-label="Permission asked here" data-prompt="permission">
    <h3 class="prompt-title">${(Array.isArray(card.questions) && card.questions.length > 0 ? "Question: " : "Permission: ") + clip(card.tool || "tool", 80)}</h3>
    <${PermissionCard} node=${node} card=${card} snap=${snap} store=${store} />
  </section>`;
}

// A question: its text and options, and the box that sends the answer.
export function Question({ node, ask, snap, store }) {
  return html`
    ${ask.questions.map((q) => html`<div class="prompt-question">
      ${q.header ? html`<p class="prompt-label">${q.header}</p>` : null}
      <p class="prompt-text">${q.question}</p>
      ${Array.isArray(q.options) && q.options.length > 0
        ? html`<ul class="prompt-options">${q.options.map((o) => html`<li><span class="prompt-option-label">${o.label}</span>${o.description ? html` <span class="prompt-option-desc">${o.description}</span>` : null}</li>`)}</ul>`
        : null}
      ${q.multiSelect ? html`<p class="prompt-more">More than one can be chosen.</p>` : null}
    </div>`)}
    ${snap.remote
      ? html`<p class="prompt-more">${IN_TERMINAL_WORDS}</p>`
      : html`<${ManagerBar} snap=${snap} manager=${{ pane: node.pane, name: shownName(node) }} send=${(c) => store.send(c)} autoFocus=${false} verb="Answer" />`}
  `;
}

/**
 * Prompt is the card, or nothing when the pane is not waiting on the person.
 * node and pane are the graph's and the state push's entries for it; snap is the
 * store's snapshot, store the store, and sendKeys types into the pane's terminal
 * (the terminal's sendKeys). terminal says whether the pane has one.
 */
export function Prompt({ node, pane = null, snap, store, sendKeys, terminal = true, waitMs = ANSWER_WAIT_MS }) {
  const kind = waitingOn(node, pane);
  if (!kind) return null;
  let body = null;
  let id = "";
  let title = "Waiting on you";
  if (!terminal) {
    // A structured pane's card, with the number and request id the answer needs.
    const s = snap.structured && snap.structured.id === node.pane ? snap.structured : null;
    const card = s && s.card ? s.card : null;
    if (card) {
      id = idOf("s", card.seq, card.requestId);
      title = (Array.isArray(card.questions) && card.questions.length > 0 ? "Question: " : "Permission: ") + (card.tool || "tool");
      body = html`<${PermissionCard} key=${id} node=${node} card=${card} snap=${snap} store=${store} />`;
    } else if (pane && pane.permission) {
      id = idOf("sp", pane.permission.tool, pane.permission.command, pane.permission.file);
      title = "Permission: " + (pane.permission.tool || "tool");
      body = html`<${PermissionBody} permission=${pane.permission} />
        <p class="prompt-status" role="status">${snap.remote ? DESK_WORDS : snap.structuredError ? "The request could not be loaded: " + showControls(clip(snap.structuredError.error, 200)) : "Loading the request…"}</p>`;
    }
  } else if (kind === "permission") {
    const p = pane.permission;
    id = idOf("p", p.tool, p.command, p.file, p.summary, pane.statusSince);
    title = "Permission: " + (p.tool || "tool");
    body = html`<${TerminalPermission} key=${id} permission=${p} sendKeys=${sendKeys} waitMs=${waitMs} viewer=${isViewer(snap)} />`;
  } else if (kind === "ask") {
    id = idOf("a", pane.statusSince, pane.ask.questions.map((q) => q.question).join("\u0002"));
    title = "Question";
    body = html`<${Question} key=${id} node=${node} ask=${pane.ask} snap=${snap} store=${store} />`;
  }
  if (!body) {
    // The graph says the pane waits on the person, and the state push has not said
    // on what yet (or left the details for the desktop).
    const text = node.waitingOn && node.waitingOn.text;
    body = html`${text ? html`<p class="prompt-text">${clip(text, 200)}</p>` : null}
      <p class="prompt-more">${terminal ? IN_TERMINAL_WORDS : DESK_WORDS}</p>`;
  }
  return html`<section class="prompt" role="region" aria-label="Needs you" data-prompt=${kind}>
    <h3 class="prompt-title">${title}</h3>
    ${body}
  </section>`;
}
