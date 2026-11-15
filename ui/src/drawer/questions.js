// A question card: the questions an agent asks the person, and the form that
// answers them. The model is plain functions so it can be tested without a DOM.
//
// Each question is a group of options (radio buttons when one answer is allowed,
// checkboxes when several are), and "Other" is the last option: choosing it opens
// a text box, and typing in the box chooses it. Each question keeps its own state,
// the form shows what is chosen before anything is sent, and one button sends the
// whole set. The same form answers a structured pane (permissionAnswer) and a
// terminal pane (keys into claude's question screen, see tuiQuestion.js).
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
export const OTHER_EMPTY_WORDS = "Type your own answer, or choose an option.";
export const NOT_ANSWERED_YET_WORDS = "Not answered yet";
export const KEYS_WORDS = "Arrow keys move, Space chooses, 1 to 9 choose an option, Enter sends when every question is answered.";

const bytes = (s) => new TextEncoder().encode(String(s)).length;

/** blank is the state of a form before anything is chosen: for each question, the positions of the options chosen, the text typed and whether "Other" is chosen. */
export function blank(questions) {
  return (Array.isArray(questions) ? questions : []).map(() => ({ selected: [], other: "", otherOn: false }));
}

/** pick returns the state after the option at oi of question qi is pressed. A question that takes one answer holds one thing: choosing an option lets go of "Other", and an option pressed again is let go. The text typed is kept, in case "Other" is chosen again. */
export function pick(state, questions, qi, oi) {
  const q = questions[qi];
  if (!q || !state[qi]) return state;
  const cur = state[qi];
  const has = cur.selected.includes(oi);
  let next;
  if (q.multiSelect) {
    next = { ...cur, selected: has ? cur.selected.filter((x) => x !== oi) : [...cur.selected, oi].sort((a, b) => a - b) };
  } else {
    next = { ...cur, selected: has ? [] : [oi], otherOn: false };
  }
  return state.map((s, i) => (i === qi ? next : s));
}

/** pickOther returns the state after "Other" of question qi is pressed: chosen, or let go if it was. For a question that takes one answer, choosing it lets go of the option chosen. */
export function pickOther(state, questions, qi) {
  const q = questions[qi];
  if (!q || !state[qi]) return state;
  const cur = state[qi];
  const next = cur.otherOn ? { ...cur, otherOn: false } : { ...cur, otherOn: true, selected: q.multiSelect ? cur.selected : [] };
  return state.map((s, i) => (i === qi ? next : s));
}

/** type returns the state after the text of question qi changes. Typing chooses "Other", and for a question that takes one answer lets go of the option chosen. Emptying the box leaves "Other" as it was, so that the form says it needs text. */
export function type(state, questions, qi, text) {
  const q = questions[qi];
  if (!q || !state[qi]) return state;
  const cur = state[qi];
  const some = String(text).trim() !== "";
  const next = some ? { selected: q.multiSelect ? cur.selected : [], other: text, otherOn: true } : { ...cur, other: text };
  return state.map((s, i) => (i === qi ? next : s));
}

/** problem says why the form cannot be sent, or "" when it can. */
export function problem(questions, state) {
  if (!Array.isArray(questions) || questions.length === 0 || state.length !== questions.length) return INCOMPLETE_WORDS;
  for (let i = 0; i < questions.length; i++) {
    const s = state[i];
    const other = s.other.trim();
    if (s.otherOn && bytes(other) > MAX_OTHER_BYTES) return TOO_LONG_WORDS;
    if (s.otherOn && !other) return OTHER_EMPTY_WORDS;
    const n = s.selected.length + (s.otherOn && other ? 1 : 0);
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
    if (s.otherOn && other) a.other = other;
    return a;
  });
}

/** summaryOf is what the form says is chosen, for each question: { header, question, text, set }. text is the labels chosen and the typed text, joined by ", ". */
export function summaryOf(questions, state) {
  return (Array.isArray(questions) ? questions : []).map((q, i) => {
    const s = state[i] || { selected: [], other: "", otherOn: false };
    const opts = Array.isArray(q.options) ? q.options : [];
    const parts = s.selected.map((oi) => (opts[oi] ? opts[oi].label : "")).filter(Boolean);
    const other = s.other.trim();
    if (s.otherOn && other) parts.push(other);
    return { header: q.header || "", question: q.question, text: parts.join(", "), set: parts.length > 0 };
  });
}

const focusIn = (root, selector) => {
  const el = root && root.querySelector ? root.querySelector(selector) : null;
  if (el && el.focus) el.focus();
};

/**
 * QuestionForm draws the questions of a card, what is chosen, and the buttons. onSend gets the allowing option
 * and the answers; onSkip is called for the refusing option (a form with none has no Skip). done says an answer
 * is on its way, and the form is shut; locked says nothing may be sent from this window.
 */
export function QuestionForm({ card, allow, refuse, done, locked = false, tabIndex = undefined, onSend, onSkip }) {
  const questions = Array.isArray(card.questions) ? card.questions : [];
  const [state, setState] = useState(() => blank(questions));
  const why = problem(questions, state);
  const shut = done || locked;
  const touched = state.some((s) => s.selected.length || s.other || s.otherOn);
  const sendNow = () => {
    if (shut || why || !allow) return;
    onSend(allow, answers(questions, state));
  };
  // Number keys choose option n of the question the keyboard is in, and one past the last is "Other". In the text box a digit is text.
  const onKeys = (qi) => (e) => {
    if (shut || e.ctrlKey || e.metaKey || e.altKey) return;
    const target = e.target;
    const input = target && target.localName === "input";
    const inText = input && target.getAttribute("type") === "text";
    if (e.key === "Enter") {
      if (!input) return;
      e.preventDefault();
      sendNow();
      return;
    }
    if (inText || !/^[1-9]$/.test(e.key)) return;
    const n = Number(e.key);
    const q = questions[qi];
    const opts = Array.isArray(q.options) ? q.options : [];
    if (n > opts.length + 1) return;
    e.preventDefault();
    const box = e.currentTarget;
    if (n === opts.length + 1) {
      setState((st) => (st[qi] && st[qi].otherOn && !q.multiSelect ? st : pickOther(st, questions, qi)));
      focusIn(box, ".prompt-other-text");
    } else {
      setState((st) => pick(st, questions, qi, n - 1));
      focusIn(box, '[data-option="' + (n - 1) + '"]');
    }
  };
  const summary = summaryOf(questions, state);
  return html`<div class="prompt-questions">
    <div class="prompt-qlist">
    ${questions.map((q, qi) => {
      const s = state[qi] || { selected: [], other: "", otherOn: false };
      const kind = q.multiSelect ? "checkbox" : "radio";
      const opts = Array.isArray(q.options) ? q.options : [];
      return html`<fieldset class="prompt-question" data-question=${qi} role=${q.multiSelect ? "group" : "radiogroup"} onKeyDown=${onKeys(qi)}>
        <legend class="prompt-label">${questions.length > 1 ? "Question " + (qi + 1) + " of " + questions.length + " · " : ""}${q.header ? clip(q.header, 60) + " · " : ""}${q.multiSelect ? "choose any" : "choose one"} · from the agent</legend>
        <p class="prompt-text prompt-qtext">${q.question}</p>
        ${opts.map((o, oi) => html`<label class=${"prompt-opt" + (s.selected.includes(oi) ? " is-chosen" : "")}>
          <input type=${kind} name=${"q" + qi} data-option=${oi} checked=${s.selected.includes(oi)} disabled=${shut} tabindex=${tabIndex} onClick=${() => setState((st) => pick(st, questions, qi, oi))} />
          <span class="prompt-opt-text"><span class="prompt-option-label">${o.label}</span>${o.description ? html` <span class="prompt-option-desc">${o.description}</span>` : null}</span>
        </label>`)}
        <div class=${"prompt-opt prompt-other" + (s.otherOn ? " is-chosen" : "")}>
          <input type=${kind} name=${"q" + qi} class="prompt-other-pick" data-other-option=${qi} id=${"qo" + qi} checked=${s.otherOn} disabled=${shut} tabindex=${tabIndex}
            onClick=${(e) => {
              const box = e.currentTarget && e.currentTarget.closest ? e.currentTarget.closest("fieldset") : null;
              setState((st) => (s.otherOn && !q.multiSelect ? st : pickOther(st, questions, qi)));
              // A pointer press opens the text box; a key press (detail 0) stays on the option so the arrows go on moving.
              if (e.detail > 0 && !s.otherOn) focusIn(box, ".prompt-other-text");
            }} />
          <span class="prompt-opt-text prompt-other-body">
            <label class="prompt-option-label" for=${"qo" + qi}>${OTHER_WORDS}</label>
            <input type="text" class="prompt-other-text" data-other=${qi} value=${s.other} disabled=${shut} tabindex=${tabIndex} placeholder="Type your own answer" aria-label=${"Your own answer to question " + (qi + 1)} onInput=${(e) => setState((st) => type(st, questions, qi, e.target.value))} />
          </span>
        </div>
      </fieldset>`;
    })}
    </div>
    ${touched
      ? html`<ol class="prompt-chosen" aria-label="Your answers" aria-live="polite">
          ${summary.map((r, i) => html`<li class=${r.set ? "is-set" : "is-unset"} data-summary=${i}><span class="prompt-chosen-q">${r.header ? clip(r.header, 40) : "Question " + (i + 1)}</span> <span class="prompt-chosen-a">${r.set ? r.text : NOT_ANSWERED_YET_WORDS}</span></li>`)}
        </ol>`
      : null}
    <div class="prompt-choices prompt-qfoot" role="group" aria-label="Choices">
      ${allow ? html`<button type="button" class="prompt-choice prompt-choice--allow_once prompt-send" data-option=${allow.id} disabled=${shut || !!why} tabindex=${tabIndex} onClick=${sendNow}>${SEND_WORDS}</button>` : null}
      ${refuse ? html`<button type="button" class="prompt-choice prompt-choice--reject_once prompt-skip" data-option=${refuse.id} disabled=${shut} tabindex=${tabIndex} onClick=${() => onSkip(refuse)}>${SKIP_WORDS}</button>` : null}
    </div>
    ${!shut && why && touched ? html`<p class="prompt-more">${why}</p>` : null}
    ${!shut ? html`<p class="prompt-hint">${KEYS_WORDS}</p>` : null}
  </div>`;
}
