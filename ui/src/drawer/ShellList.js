// The commands the person ran with "!" in a structured pane, as a list under the
// conversation: the command, where it is (running, done, stopped), its output as
// text, and Stop, Send to agent and Remove. They are the person's, not the agent's:
// nothing here passes through the agent until the person presses Send to agent.
// Everything on a row is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { shellMessage, shellStateWords, SHELL_NOT_SENT_WORDS } from "./shellModel.js";

let shellSeq = 0;

/** ShellList is the list. rows is shellOf(push); result is the store's latest answer to a shell command,
 *  { id, req, ok, error }. */
export function ShellList({ rows, pane, store, result = null }) {
  const [note, setNote] = useState("");
  const [sent, setSent] = useState("");
  const lastReq = useRef("");
  const ids = rows.map((r) => r.id + r.state).join(",");
  // A refusal belongs to the press that caused it and goes when the list moves on.
  useEffect(() => { setNote(""); }, [ids]);
  if (!rows.length) return null;

  const run = (op, id) => {
    const req = "h" + ++shellSeq;
    const msg = shellMessage(pane, rows, op, id, req);
    if (!msg) return;
    setNote("");
    setSent("");
    if (!store.send(msg)) {
      setNote(SHELL_NOT_SENT_WORDS);
      return;
    }
    lastReq.current = req;
    if (op === "send") setSent(req);
  };
  const mine = result && result.id === pane && result.req === lastReq.current ? result : null;
  const refused = mine && !mine.ok ? String(mine.error || "") : "";
  const queued = mine && mine.ok && sent === mine.req;
  const item = (r) => html`<li key=${r.id} class="structured-shell-item" data-shell-item=${r.id} data-state=${r.state}>
    <p class="shell-head">
      <span class="shell-cmd" dir="auto"><span aria-hidden="true">$ </span>${showControls(clip(r.cmd, 300))}</span>
      <span class="shell-state">${shellStateWords(r)}</span>
      <span class="queue-actions">
        ${r.running ? html`<button type="button" class="drawer-btn shell-stop" aria-label=${"Stop " + clip(r.cmd, 60)} onClick=${() => run("stop", r.id)}>Stop</button>` : null}
        <button type="button" class="drawer-btn shell-send" aria-label=${"Send the output of " + clip(r.cmd, 60) + " to the agent"} title="Sends the command and its output to the agent as a prompt" onClick=${() => run("send", r.id)}>Send to agent</button>
        ${r.running ? null : html`<button type="button" class="drawer-btn shell-remove" aria-label=${"Remove " + clip(r.cmd, 60)} onClick=${() => run("remove", r.id)}>Remove</button>`}
      </span>
    </p>
    ${r.text !== "" ? html`<pre class="shell-out" tabindex="0" role="region" aria-label=${"Output of " + clip(r.cmd, 60)} dir="auto">${r.cut ? "…\n" : ""}${r.text}</pre>` : null}
  </li>`;
  return html`<section class="structured-shell" aria-label="Your commands" data-shell=${rows.length}>
    <p class="queue-head"><span>Your commands</span><span class="shell-note">Run here by you, not by the agent</span></p>
    <ol class="queue-list">${rows.map(item)}</ol>
    ${note ? html`<p class="structured-note structured-note--unsent" role="status">${note}</p>` : null}
    ${refused ? html`<p class="structured-note" role="status">${showControls(clip(refused, 300))}</p>` : null}
    ${queued ? html`<p class="structured-note" role="status" data-shell-sent="on">Sent to the agent.</p>` : null}
  </section>`;
}
