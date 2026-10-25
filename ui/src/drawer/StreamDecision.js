// The card for a question, drawn in the conversation where the agent asked it (the
// manager's "decision for you"): how long it has waited, what it asks, and one
// button for each choice, the first as the main one. The choices are sent to the
// pane as text, as the manager bar sends a line, and a button is pressed once.
// A question the buttons cannot answer (several questions, more than one
// choice allowed, or no choices) keeps the drawer card's answer box.
//
// The question and its choices are the agent's and are drawn as text.
import { useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { shownName } from "../graph/model.js";
import { ageOf } from "../graph/NeedsYou.js";
import { questionOptions } from "../graph/DecisionBlock.js";
import { Question, NOT_ANSWERED_WORDS, IN_TERMINAL_WORDS } from "./Prompt.js";

/** StreamDecision draws the card. node and pane are the graph's and the state push's entries for the pane, snap the store's snapshot and store the store. */
export function StreamDecision({ node, pane, snap, store, now = Date.now() }) {
  const [sent, setSent] = useState("");
  const [note, setNote] = useState("");
  // Set at the click: a second click before the card is drawn again finds it answered.
  const done = useRef(false);
  const ask = pane && pane.ask;
  const questions = ask && Array.isArray(ask.questions) ? ask.questions : [];
  const options = questionOptions(pane);
  const age = ageOf(pane && pane.statusSince, now);
  const name = shownName(node);
  const pick = (label) => {
    if (done.current || snap.remote) return;
    if (store.send({ cmd: "paneSend", id: node.pane, text: label })) {
      done.current = true;
      setSent(label);
      setNote("");
    } else {
      setNote(NOT_ANSWERED_WORDS);
    }
  };
  return html`<section class="prompt prompt--stream prompt--decision" role="region" aria-label=${clip(name, 40) + ", decision for you"} data-prompt="ask">
    <p class="prompt-label">${clip(name, 40)}, decision for you</p>
    <h3 class="prompt-title">${age ? "Waiting " + age : "Waiting on you"}</h3>
    ${options.length > 0 && !snap.remote
      ? html`${questions.map((q) => html`<p class="prompt-text">${q.question}</p>`)}
          <div class="prompt-choices" role="group" aria-label="Choices">
            ${options.map((label, i) => html`<button type="button" class=${"prompt-choice" + (i === 0 ? " prompt-choice--primary" : "")} data-option=${label} disabled=${!!sent} onClick=${() => pick(label)}>${clip(label, 80)}</button>`)}
          </div>
          <p class=${"prompt-status" + (note ? " prompt-status--error" : "")} role=${note ? "alert" : "status"}>${sent ? "Sent “" + clip(sent, 80) + "”. Waiting for the agent." : note}</p>`
      : questions.length > 0
        ? html`<${Question} node=${node} ask=${ask} snap=${snap} store=${store} />`
        : html`<p class="prompt-more">${IN_TERMINAL_WORDS}</p>`}
  </section>`;
}
