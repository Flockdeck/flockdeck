// The manager bar: one box on the home screen that sends the person's words to the
// manager of the watched project, as written, with paneSend (docs/team-graph.md
// section 6). Nothing is wrapped around the text, and allowShell is never sent.
//
// The desktop answers an accepted send with a paneSend message and a refusal with
// an error notice; the store keeps both (snap.sent, snap.notice). The bar reads
// the answer to its own send as the latest paneSend for the manager's pane, or the
// latest error notice, since it sent. Only one send is open at a time.
//
// The box is one line. The desk types a text as a prompt, and in a pane that has
// not asked for bracketed paste each line break types an Enter, so a text with
// line breaks would be several prompts. A line break typed or pasted becomes a space.
//
// - An empty text, or one over 16 KiB, is stopped here in the desk's words.
// - A project with no manager says so and the bar is off.
// - A window reached through the relay gets no bar: the desk refuses paneSend
//   there, and the graph is not shown there either.
//
// Text from the desktop (a refusal) and the manager's name are drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "./Node.js";
import { composing } from "../shell/keys.js";

/** SENT_SHOWN is how long, in ms, the line about an accepted send stays: long enough to read, then the bar is quiet again. A refusal stays until the text changes. */
export const SENT_SHOWN = 6000;

/** MAX_BYTES is the longest text the desk takes (workspace.MaxPaneSendBytes). */
export const MAX_BYTES = 16 * 1024;

/** The desk's own words for the two texts it refuses before anything else
 *  (internal/workspace/panesend.go, ErrSendEmpty and ErrSendTooLong). A Go test
 *  pins both. */
export const EMPTY_WORDS = "There was no text to send";
export const TOO_LONG_WORDS = "That text is over 16 KB, so it was not sent";

export const NO_MANAGER_WORDS = "This project has no manager. Its agents work on their own.";
export const NOT_CONNECTED_WORDS = "Not connected to Flockdeck, so the text was not sent.";

/** LOST_WORDS is said when the connection a text was sent on has been replaced before the desk answered: it may or may not have arrived. */
export const LOST_WORDS = "The connection dropped before Flockdeck answered. Check the pane, then send again if the text is not there.";

// Control characters other than tab, carriage return and line feed, which the
// desk removes before it looks at the text.
// eslint-disable-next-line no-control-regex
const CONTROLS = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/g;

const ONE_LINE = /[\r\n]/;
const ONE_LINE_ALL = /\r\n|[\r\n]/g;

const encoder = typeof TextEncoder === "function" ? new TextEncoder() : null;
const byteLength = (s) => (encoder ? encoder.encode(s).length : unescape(encodeURIComponent(s)).length);

/** checkText is what the desk would say about a text before sending it, or ""
 *  when it would take it. The length is counted before the control characters
 *  are removed, as the desk counts it. */
export function checkText(text) {
  const s = typeof text === "string" ? text : "";
  if (byteLength(s) > MAX_BYTES) return TOO_LONG_WORDS;
  if (s.replace(CONTROLS, "").trim() === "") return EMPTY_WORDS;
  return "";
}

/** managerOf is the manager of the watched project, as { pane, name }, or null
 *  when no team has one that is a node of the graph. With several teams it is
 *  the first team's. */
export function managerOf(model) {
  if (!model || !model.ok) return null;
  for (const t of model.teams) {
    const n = t.manager ? model.byPane.get(t.manager) : null;
    if (n) return { pane: n.pane, name: n.name || n.pane };
  }
  return null;
}

/** sentWords says what an accepted send did, from the paneSend answer. */
export function sentWords(sent) {
  if (sent.routed) return "Sent. The manager was restarted on another model to take it.";
  if (sent.waiting) return "Sent. The manager was waiting on a question, so it may read this as the answer.";
  if (sent.queued) return "Sent. The manager reads it when its turn ends.";
  return "Sent.";
}

/** outcomeOf is where the open send has got to, from the snapshot: sending,
 *  sent (with the words) or refused (with the desk's notice). */
export function outcomeOf(snap, pending) {
  if (!pending) return null;
  const s = snap.sent;
  if (s && s.seq > pending.sent && s.id === pending.pane) return { kind: "sent", text: sentWords(s) };
  const n = snap.notice;
  // An approval notice is the answer to something else.
  if (n && n.seq > pending.notice && n.error && n.kind !== "approval") return { kind: "refused", text: String(n.text || "The text was not sent.") };
  if (snap.connection !== "open") return { kind: "refused", text: NOT_CONNECTED_WORDS };
  // The answer belongs to the connection the text went on; after a reconnect none is coming.
  if (pending.hello && snap.hello !== pending.hello) return { kind: "refused", text: LOST_WORDS };
  return { kind: "sending", text: "Sending…" };
}

/**
 * ManagerBar draws the box. snap is the store's snapshot, manager is
 * managerOf(model) for the project on screen, and send is the store's send.
 * inputRef, if given, holds the text box for a view that moves the keyboard to
 * it. The keyboard goes to the box once on its own when nothing else holds it
 * (autoFocus, on by default). shownFor is how long, in ms, the line about an
 * accepted send stays. verb starts the box's hint: "Tell" the manager, and
 * "Answer" an agent that asked a question.
 */
export function ManagerBar({ snap, manager, send, inputRef, autoFocus = true, verb = "Tell", shownFor = SENT_SHOWN, lead = false }) {
  const own = useRef(null);
  const box = inputRef || own;
  const [draft, setDraft] = useState("");
  const [local, setLocal] = useState("");
  const [pending, setPending] = useState(null);
  const focused = useRef(false);
  // Set at once on a send, so a second Enter before the page has redrawn does not send again.
  const busy = useRef(false);

  const enabled = !!manager && !snap.remote;
  useEffect(() => {
    if (!autoFocus || !enabled || focused.current) return;
    const el = box.current;
    const doc = el && el.ownerDocument;
    if (!el || !el.focus) return;
    focused.current = true;
    // Do not take the keyboard from something the person is already using.
    if (doc && doc.activeElement && doc.activeElement !== doc.body) return;
    el.focus();
  }, [autoFocus, enabled]);

  const open = pending && manager && pending.pane === manager.pane ? pending : null;
  const outcome = outcomeOf(snap, open);
  if (!outcome || outcome.kind !== "sending") busy.current = false;
  const done = outcome && outcome.kind === "sent";
  // The text goes once the desk has taken it; a refused one stays to be changed.
  useEffect(() => {
    if (done) setDraft("");
  }, [done, open]);
  // What became of an accepted send is said briefly, then the line goes.
  useEffect(() => {
    if (!done) return undefined;
    const t = setTimeout(() => setPending(null), shownFor);
    return () => clearTimeout(t);
  }, [done, open]);

  if (snap.remote) return null;
  if (!manager) {
    return html`<div class="mbar mbar--off" data-state="no-manager">
      <input class="mbar-input" type="text" disabled aria-label="Message the manager" placeholder="No manager" />
      <button class="mbar-send" type="button" disabled>Send</button>
      <span class="mbar-status" role="status">${NO_MANAGER_WORDS}</span>
    </div>`;
  }

  const sending = !!outcome && outcome.kind === "sending";
  const submit = () => {
    if (busy.current) return;
    const why = checkText(draft);
    if (why) {
      setLocal(why);
      return;
    }
    setLocal("");
    const mark = { pane: manager.pane, notice: snap.notice ? snap.notice.seq : 0, sent: snap.sent ? snap.sent.seq : 0, hello: snap.hello || null };
    // The text as written; allowShell is never sent.
    if (send({ cmd: "paneSend", id: manager.pane, text: draft })) {
      busy.current = true;
      setPending(mark);
    } else {
      setPending(null);
      setLocal(NOT_CONNECTED_WORDS);
    }
  };
  const onKeyDown = (e) => {
    if (composing(e) || e.key !== "Enter" || e.shiftKey) return;
    e.preventDefault();
    submit();
  };

  const say = local ? { kind: "refused", text: local } : outcome;
  const state = say ? say.kind : "idle";
  return html`<div class="mbar" data-state=${state}>
    ${lead ? html`<span class="mbar-to">To <b>${clip(manager.name, 24)}</b></span>` : null}
    <input
      ref=${box}
      class="mbar-input"
      type="text"
      autocomplete="off"
      aria-label=${"Message " + clip(manager.name, 40)}
      placeholder=${verb + " " + clip(manager.name, 40) + "… (Enter sends)"}
      title="One line. Enter sends it to the agent, who passes it to the team."
      readOnly=${sending}
      aria-busy=${sending ? "true" : undefined}
      value=${draft}
      onInput=${(e) => {
        const v = e.target.value;
        setDraft(ONE_LINE.test(v) ? v.replace(ONE_LINE_ALL, " ") : v);
        if (local) setLocal("");
      }}
      onKeyDown=${onKeyDown}
    />
    <button class="mbar-send" type="button" disabled=${sending} onClick=${submit}>Send</button>
    <span class=${"mbar-status" + (state === "refused" ? " mbar-status--error" : "")} role=${state === "refused" ? "alert" : "status"}>${say ? say.text : ""}</span>
  </div>`;
}
