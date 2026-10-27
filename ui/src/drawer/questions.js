// A question card: the questions an agent asks the person, and the form that
// answers them. The model is plain functions so it can be tested without a DOM.
//
// Everything on the card is the agent's text. It is drawn as text, in a block
// that says whose it is, and never as markup or as a control of Flockdeck's own.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";

/** MAX_OTHER_BYTES is the most the typed answer of a question may hold, as the desk limits it. */
export const MAX_OTHER_BYTES = 2000;
export const SEND_WORDS = "Send answers";
export const SKIP_WORDS = "Skip";
export const OTHER_WORDS = "Other";
export const TOO_LONG_WORDS = "That answer is too long.";
export const INCOMPLETE_WORDS = "Answer every question to send.";

const bytes = (s) => new TextEncoder().encode(String(s)).length;

/** blank is the state of a form before anything is chosen: for each question, the positions of the options chosen and the text typed. */
export function blank(questions) {
  return (Array.isArray(questions) ? questions : []).map(() => ({ selected: [], other: "" }));
}

/** pick returns the state after the option at oi of question qi is pressed. A question that takes one answer holds one thing: choosing an option clears the text, and an option pressed again is let go. */
export function pick(state, questions, qi, oi) {
  const q = questions[qi];
  if (!q || !state[qi]) return state;
  const cur = state[qi];
  const has = cur.selected.includes(oi);
  let next;
  if (q.multiSelect) {
    next = { ...cur, selected: has ? cur.selected.filter((x) => x !== oi) : [...cur.selected, oi].sort((a, b) => a - b) };
  } else {
    next = { selected: has ? [] : [oi], other: has ? cur.other : "" };
  }
  return state.map((s, i) => (i === qi ? next : s));
}

/** type returns the state after the text of question qi changes. For a question that takes one answer, typing lets go of the option chosen. */
export function type(state, questions, qi, text) {
  const q = questions[qi];
  if (!q || !state[qi]) return state;
  const cur = state[qi];
  const next = q.multiSelect || String(text).trim() === "" ? { ...cur, other: text } : { selected: [], other: text };
  return state.map((s, i) => (i === qi ? next : s));
}

/** problem says why the form cannot be sent, or "" when it can. */
export function problem(questions, state) {
  if (!Array.isArray(questions) || questions.length === 0 || state.length !== questions.length) return INCOMPLETE_WORDS;
  for (let i = 0; i < questions.length; i++) {
    const s = state[i];
    const other = s.other.trim();
    if (bytes(other) > MAX_OTHER_BYTES) return TOO_LONG_WORDS;
    const n = s.selected.length + (other ? 1 : 0);
    if (n === 0 || (!questions[i].multiSelect && n > 1)) return INCOMPLETE_WORDS;
  }
  return "";
}

/** answers is what permissionAnswer carries: one entry for each question, by its position, or null when the form cannot be sent. */
export function answers(questions, state) {
  if (problem(questions, state)) return null;
  return state.map((s, i) => {
    const a = { question: i, selected: [...s.selected] };
    const other = s.other.trim();
    if (other) a.other = other;
    return a;
  });
}

/** QuestionForm draws the questions of a card and the two buttons. onSend gets the answers; onSkip is called for the refusing option. */
export function QuestionForm({ card, allow, refuse, done, tabIndex = undefined, onSend, onSkip }) {
  const questions = Array.isArray(card.questions) ? card.questions : [];
  const [state, setState] = useState(() => blank(questions));
  const why = problem(questions, state);
  return html`<div class="prompt-questions">
    ${questions.map((q, qi) => {
      const s = state[qi] || { selected: [], other: "" };
      const kind = q.multiSelect ? "checkbox" : "radio";
      return html`<fieldset class="prompt-question" data-question=${qi}>
        <legend class="prompt-label">${q.header ? clip(q.header, 60) + " · " : ""}${q.multiSelect ? "choose any" : "choose one"} · from the agent</legend>
        <p class="prompt-text prompt-qtext">${q.question}</p>
        ${(Array.isArray(q.options) ? q.options : []).map((o, oi) => html`<label class="prompt-opt">
          <input type=${kind} name=${"q" + qi} data-option=${oi} checked=${s.selected.includes(oi)} disabled=${done} tabindex=${tabIndex} onClick=${() => setState((st) => pick(st, questions, qi, oi))} />
          <span class="prompt-option-label">${o.label}</span>${o.description ? html` <span class="prompt-option-desc">${o.description}</span>` : null}
        </label>`)}
        <label class="prompt-opt prompt-other">
          <span class="prompt-option-label">${OTHER_WORDS}</span>
          <input type="text" class="prompt-other-text" data-other=${qi} value=${s.other} disabled=${done} tabindex=${tabIndex} aria-label=${"Your own answer to question " + (qi + 1)} onInput=${(e) => setState((st) => type(st, questions, qi, e.target.value))} />
        </label>
      </fieldset>`;
    })}
    <div class="prompt-choices" role="group" aria-label="Choices">
      ${allow ? html`<button type="button" class="prompt-choice prompt-choice--allow_once prompt-send" data-option=${allow.id} disabled=${done || !!why} tabindex=${tabIndex} onClick=${() => onSend(allow, answers(questions, state))}>${SEND_WORDS}</button>` : null}
      ${refuse ? html`<button type="button" class="prompt-choice prompt-choice--reject_once prompt-skip" data-option=${refuse.id} disabled=${done} tabindex=${tabIndex} onClick=${() => onSkip(refuse)}>${SKIP_WORDS}</button>` : null}
    </div>
    ${!done && why && state.some((s) => s.selected.length || s.other) ? html`<p class="prompt-more">${why}</p>` : null}
  </div>`;
}
