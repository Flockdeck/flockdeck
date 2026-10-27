// The card for a question, drawn in the conversation where the agent asked it (the
// manager's "decision for you"): how long it has waited, what it asks, and one
// button for each choice, the first as the main one. The choices are sent to the
// pane as text, as the manager bar sends a line, and a button is pressed once.
// A question the buttons cannot answer (several questions, more than one
// choice allowed, or no choices) keeps the drawer card's answer box.
//
// The question and its choices are the agent's and are drawn as text.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { shownName } from "../graph/model.js";
import { ageOf } from "../graph/NeedsYou.js";
import { questionOptions } from "../graph/DecisionBlock.js";
import { usePaneSend } from "../graph/ManagerBar.js";
import { showControls } from "./diff/diffModel.js";
import { Question, IN_TERMINAL_WORDS } from "./Prompt.js";

/** StreamDecision draws the card. node and pane are the graph's and the state push's entries for the pane, snap the store's snapshot and store the store. */
export function StreamDecision({ node, pane, snap, store, now = Date.now() }) {
  const [picked, setPicked] = useState("");
  // One send at a time: a second click before the card is drawn again finds it answered, and a refusal leaves the choices to be pressed again.
  const [out, go] = usePaneSend(snap, (m) => store.send(m));
  const busy = !!out && out.kind !== "refused";
  const ask = pane && pane.ask;
  const questions = ask && Array.isArray(ask.questions) ? ask.questions : [];
  const options = questionOptions(pane);
  const age = ageOf(pane && pane.statusSince, now);
  const name = shownName(node);
  const pick = (label) => {
    if (busy || snap.remote) return;
    // go says whether this click started the send: a second click in the same tick must not change the choice named as sent.
    if (go(node.pane, label)) setPicked(label);
  };
  const note = out && out.kind === "refused" ? showControls(clip(out.text, 160)) : "";
  const status = out && out.kind === "sent" ? "Sent “" + clip(picked, 80) + "”. Waiting for the agent." : out && out.kind === "sending" ? "Sending…" : note;
  return html`<section class="prompt prompt--stream prompt--decision" role="region" aria-label=${clip(name, 40) + ", decision for you"} data-prompt="ask">
    <p class="prompt-label">${clip(name, 40)}, decision for you</p>
    <h3 class="prompt-title">${age ? "Waiting " + age : "Waiting on you"}</h3>
    ${options.length > 0 && !snap.remote
      ? html`${questions.map((q) => html`<p class="prompt-text">${q.question}</p>`)}
          <div class="prompt-choices" role="group" aria-label="Choices">
            ${options.map((label, i) => html`<button type="button" class=${"prompt-choice" + (i === 0 ? " prompt-choice--primary" : "")} data-option=${label} aria-disabled=${busy ? "true" : undefined} onClick=${() => pick(label)}>${clip(label, 80)}</button>`)}
          </div>
          ${note
            ? html`<p key="refused" class="prompt-status prompt-status--error" role="alert">${note}</p>`
            : html`<p key="status" class="prompt-status" role="status">${status}</p>`}`
      : questions.length > 0
        ? html`<${Question} node=${node} ask=${ask} snap=${snap} store=${store} />`
        : html`<p class="prompt-more">${IN_TERMINAL_WORDS}</p>`}
  </section>`;
}
