// The baton dialog, top to bottom: anything the draft could not be built from;
// the baton itself, as text to edit; what the scrubber removed, and that it is a
// filter and not a guarantee; where the work goes (a new agent, with its own
// choices, or this pane started over); and the buttons that do it.
//
// The text is what is sent back, whole: the desktop reads the baton out of it,
// scrubs it once more and keeps what it made of it. Nothing here edits it, and it
// is only ever put in the box as a value. The dialog is modal like the start
// dialog. While a command is out its buttons are off; if no answer comes within
// waitMs the dialog is given back with the edits kept, and the same command for
// the same pane stays refused until the desktop answers (Live holds that).
//
// A start that sends the baton to another company needs the box ticked here, and
// the desktop asks for it as well.
import { useEffect, useMemo, useRef, useState } from "../vendor/hooks.mjs";
import { useInert } from "./inert.js";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./focus.js";
import {
  DIRTY_WORDS, SCRUB_WORDS, WAIT_MS, choices, marksIn, marksWords, pickOf, providerCheck, providerWords, restartCommand, saveCommand, startCommand,
  startProblem, startValue,
} from "../lib/baton.js";

/** Baton is the dialog for a draft. draft is the read draft (draftOf), or null
 *  while it is being made; pane is the name of the pane it was asked for; reading
 *  is the words to show while there is no draft; run(command, cmd) sends a command
 *  with a fresh request id and returns it, or "" when it was refused; answer is
 *  { req, ok } for the latest answer to a command of this dialog's, or null. */
export function Baton({ draft, pane, reading = "", run, answer, onClose, waitMs = WAIT_MS }) {
  const rootRef = useRef(null);
  useInert(rootRef);
  const first = useRef(null);
  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);
  useEffect(() => {
    if (first.current && first.current.focus) first.current.focus();
    else if (rootRef.current && rootRef.current.focus) rootRef.current.focus();
  }, [!!draft]);
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onClose();
    } else if (e.key === LIST.tab) {
      trapTab(e, rootRef.current);
    }
  };
  const from = draft ? [draft.name, (draft.agents.find((a) => a.id === draft.agent) || {}).name || draft.agent].filter(Boolean).join(" · ") : pane;
  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog dialog--wide" role="dialog" aria-modal="true" aria-labelledby="baton-title" tabindex="-1" ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id="baton-title" class="dialog-title">Make baton</h2>
        ${from ? html`<p class="dialog-note">from ${from}</p>` : null}
        ${draft
          ? html`<${Form} draft=${draft} first=${first} run=${run} answer=${answer} waitMs=${waitMs} onClose=${onClose} />`
          : html`<p class="dialog-note" role="status">${reading || "Reading this pane's work…"}</p>
              <div class="dialog-foot"><button type="button" class="dialog-btn" ref=${first} onClick=${onClose}>Close</button></div>`}
      </div>
    </div>
  `;
}

function Form({ draft, first, run, answer, waitMs, onClose }) {
  const { options, choosable } = useMemo(() => choices(draft), [draft]);
  const [body, setBody] = useState(draft.text);
  const [task, setTask] = useState("");
  const [pick, setPick] = useState(() => startValue(draft));
  const [worktree, setWorktree] = useState(false);
  const [branch, setBranch] = useState("");
  const [beside, setBeside] = useState(false);
  const [ack, setAck] = useState(false);
  // The request of the command out, or "". Its answer ends the wait.
  const [busy, setBusy] = useState("");
  const [said, setSaid] = useState("");
  const timer = useRef(0);

  const chosen = pickOf(draft, pick, choosable);
  const check = providerCheck(draft, chosen.agent);
  useEffect(() => {
    if (!check.differs) setAck(false);
  }, [check.differs]);
  const marks = marksIn(body, draft.markKinds);
  const groups = [];
  for (const o of options) {
    let g = groups.find((x) => x.name === o.group);
    if (!g) groups.push((g = { name: o.group, items: [] }));
    g.items.push(o);
  }

  // The answer to the command out: done, so the dialog closes, or refused, so it
  // is given back with what was typed.
  useEffect(() => {
    if (!answer || !busy || answer.req !== busy) return;
    clearTimeout(timer.current);
    setBusy("");
    if (answer.ok) onClose();
  }, [answer]);
  useEffect(() => () => clearTimeout(timer.current), []);

  const out = (cmd) => {
    if (busy) return;
    const req = run(cmd);
    if (!req) return;
    setBusy(req);
    setSaid("");
    clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      setBusy("");
      setSaid("Flockdeck has not answered. The baton may still be saving, so the same command is refused until it answers; check flockdeck baton list.");
    }, waitMs);
  };
  const blocked = !!busy || !body.trim();
  const problem = startProblem({ body, worktree: worktree && draft.isRepo, branch, check, ack });
  const doStart = () => out((req) => startCommand(draft, req, body, task, {
    agent: chosen.agent, model: chosen.model, beside, branch: worktree && draft.isRepo ? branch : "", confirmed: ack,
  }));

  return html`
    ${draft.notes.map((n, i) => html`<p class="dialog-note" key=${i}>${n}</p>`)}
    <label class="dialog-label" for="baton-text">The baton, as text to edit</label>
    <textarea id="baton-text" class="dialog-field baton-text" rows="14" spellcheck="false" ref=${first} value=${body} onInput=${(e) => setBody(e.target.value)}></textarea>
    <p class="dialog-note baton-scrub"><strong>${marksWords(marks)}.</strong> ${SCRUB_WORDS}</p>

    <h3 class="baton-sub">Start from it</h3>
    <label class="dialog-label" for="baton-task">What the agent should do first</label>
    <input id="baton-task" class="dialog-field" type="text" placeholder="What should it do first? (optional)" value=${task} onInput=${(e) => setTask(e.target.value)} />
    ${choosable
      ? html`<label class="dialog-label" for="baton-agent">New pane runs</label>
          <select id="baton-agent" class="dialog-field" value=${pick} onChange=${(e) => setPick(e.target.value)}>
            ${groups.map((g) => html`<optgroup key=${g.name} label=${g.name}>
              ${g.items.map((o) => html`<option key=${o.value} value=${o.value} disabled=${o.disabled}>${o.label}</option>`)}
            </optgroup>`)}
          </select>`
      : null}
    ${check.differs
      ? html`<p class="dialog-note dialog-note--warn baton-provider" role="status">
          <strong>${providerWords(check)}</strong> It holds what this agent worked out, and the new agent's company receives it.
        </p>
        <label class="dialog-check">
          <input id="baton-ack" type="checkbox" checked=${ack} onChange=${(e) => setAck(e.target.checked)} />
          <span>Send it there</span>
        </label>`
      : null}
    <label class="dialog-check">
      <input id="baton-worktree" type="checkbox" checked=${worktree && draft.isRepo} disabled=${!draft.isRepo} onChange=${(e) => setWorktree(e.target.checked)} />
      <span>${draft.isRepo ? "In a new git worktree" : "Not a git repository, so no worktree"}</span>
    </label>
    ${worktree && draft.isRepo
      ? html`<input id="baton-branch" class="dialog-field" type="text" aria-label="Branch for the worktree" placeholder="Branch for the worktree, for example fix-auth"
          value=${branch} onInput=${(e) => setBranch(e.target.value)} />`
      : null}
    ${worktree && draft.isRepo && draft.dirty ? html`<p class="dialog-note dialog-note--warn">${DIRTY_WORDS}</p>` : null}
    <label class="dialog-check">
      <input id="baton-beside" type="checkbox" checked=${beside} onChange=${(e) => setBeside(e.target.checked)} />
      <span>Beside this pane, not in a new tab</span>
    </label>
    ${said ? html`<p class="dialog-note dialog-note--error" role="alert">${said}</p>` : null}
    <div class="dialog-foot">
      <span class="dialog-hint">${marksWords(marks)}</span>
      <button type="button" id="baton-cancel" class="dialog-btn" onClick=${onClose}>Cancel</button>
      <button type="button" id="baton-save" class="dialog-btn" disabled=${blocked} title="Keep it for later. An agent can start from it with spawn -baton and its id."
        onClick=${() => out((req) => saveCommand(draft, req, body, task))}>Save only</button>
      <button type="button" id="baton-restart" class="dialog-btn" disabled=${blocked}
        title="End this pane's conversation and start a new one here from the baton. The old one stays in the history."
        onClick=${() => out((req) => restartCommand(draft, req, body, task))}>Restart this pane</button>
      <button type="button" id="baton-start" class="dialog-btn dialog-btn--primary" disabled=${blocked || !!problem} title=${problem || "Start a new agent from the baton"}
        onClick=${doStart}>New pane</button>
    </div>
  `;
}
