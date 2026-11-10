// The drawer's view of a structured pane, which has no terminal. It draws the
// latest structured push of the pane being watched (store.structured): one strip
// (activity, the context meter, the mode and model pickers, search and a More menu
// with Agent info), then the pane's conversation, which fills the height left, and
// a prompt box with a Stop button.
//
// - The activity is the tool still running, with an icon for its kind and
//   the first file its summary names; with none running it is the push's own
//   activity text, and with that empty it says the agent is idle.
// - The conversation is the one a terminal agent's drawer has (Conversation.js):
//   the same rows, paging, search, Copy and steps. The push carries no entries
//   of its own. The activity line reads the tool still running from the same
//   conversation.
// - The prompt box sends paneSend and Stop sends structuredCancel. Both are
//   desk-only commands, so a window reached through the relay draws neither.
//   The draft goes when the desk says it took the text, not when the socket
//   did, so a text the desk refuses (the agent is gone, over 16 KiB) is said
//   and stays in the box.
// - Typing "/" at the start of the box lists the agent's commands (the push's
//   commands) and Flockdeck's own in a group of their own (slashModel.js).
//   Arrow keys move, Enter or Tab choose, Escape closes. An agent command is
//   written into the box as text and sent like any other; Flockdeck's runs here.
//   The first agent row is chosen by default; one of Flockdeck's only when its
//   name is exactly what was typed, so Enter on "/p" cannot run /stop. Keys that
//   belong to an input method's composition are left alone.
//
// - The mode, the model and the other settings the agent offers (the push's modes,
//   models and configOptions) are small selectors in the strip above the conversation
//   (settingsModel.js). Choosing a value sends structuredSet; the selector shows
//   the value the agent reports, so a change the agent refused puts the old one
//   back and the desk's refusal is said under the box. /mode and /model are
//   Flockdeck's commands for the two the agent offers: choosing one moves to its
//   selector, and "/mode plan" sets it from the box. Only a value from the
//   current list is sent. A value that reads as running without asking (looksUnasked)
//   is confirmed first, whichever way it was chosen; the pane's unasked warning then
//   follows the mode from the desk's side. Sending a text that starts with a space
//   skips /mode and /model and goes to the agent as typed.
//
// - The box takes several lines. Enter (or Ctrl or Command with Enter) sends and Shift+Enter
//   adds a line; the box grows to ten lines (composerModel.js). Up on the first line steps back
//   through the prompts this pane was sent, Down forward to the draft that was being written. The
//   draft and the last 50 prompts are kept per pane in this window's storage, so they survive a
//   switch of tab. A prompt over 8 KiB is sent but not kept.
// - "@" after white space opens a list of file names under the pane's directory (structuredFiles,
//   which reads names only and honours .gitignore). The list is shown only for the word as it stands
//   (the reply to the ask made for it). Tab takes the highlighted name; Enter takes one only after an
//   arrow key or the mouse has chosen it, and otherwise sends. Choosing one writes "@path" into the
//   text; the file is not read here. A window reached through the relay has no list.
// - When the agent takes images (capabilities.image of the push), a picture pasted, dropped or chosen
//   with Attach image is saved by the desktop (attachImage) and its path goes into the text as
//   "[image: path]", for the agent to read. One picture at a time is in flight.
//
// - Escape stops a running turn, and so does Ctrl+C when no text is selected (interruptModel.js): both
//   send what Stop sends, keeping the waiting prompts (the person did not ask to lose them; Stop drops them).
//   Escape in the box is the box's own until it is empty, and with a permission request open Escape is the
//   card's (only Ctrl+C stops the turn then); a held key and a second press before the redraw send once;
//   and a menu, the settings dialogs, a select or the
//   search field keep their Escape.
// - The agent's plan (the push's plan) is a collapsible checklist above the conversation (TodoList.js).
//
// Everything on a row came from an agent, a tool or a person, and none of it is
// put into the page as markup.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { Conversation } from "./Conversation.js";
import { Icon, toolKind } from "./conv/rows.js";
import { controlsOf, controlFor, setMessage, parseSetting } from "./settingsModel.js";
import { Composer } from "./Composer.js";
import { QueueList } from "./QueueList.js";
import { UsageMeter } from "./UsageMeter.js";
import { ExportPanel } from "./ExportPanel.js";
import { InfoPanel } from "./InfoPanel.js";
import { needsSignIn } from "./infoModel.js";
import { RewindPanel } from "./RewindPanel.js";
import { queueOf, stopMessage } from "./queueModel.js";
import { ShellList } from "./ShellList.js";
import { TodoList } from "./TodoList.js";
import { interruptKey, selectedText } from "./interruptModel.js";
import { shellOf } from "./shellModel.js";

export { SEND_NOT_SENT_WORDS, STOP_NOT_SENT_WORDS, FILES_DELAY_MS, ATTACH_WAIT_MS, SAVE_DELAY_MS, IMAGE_NOT_SENT_WORDS, IMAGE_WAITING_WORDS, IMAGE_FAILED_WORDS, IMAGE_UNSUPPORTED_WORDS } from "./Composer.js";
// Each Stop carries its own req, so the store can tell which pane a refusal is for.
let cancelSeq = 0;
let settingSeq = 0;
/** SET_NOT_SENT_WORDS is what the selectors say when the store did not send the change. */
export const SET_NOT_SENT_WORDS = "Not connected to Flockdeck, so the change was not sent.";


export const IDLE_WORDS = "Idle";

export { contextText, costText } from "./usageModel.js";

/** fileOf is the first word of a tool summary that looks like a file: it has a
 *  path separator or a short extension. */
export function fileOf(summary) {
  if (typeof summary !== "string") return "";
  for (const w of summary.split(/\s+/)) {
    if (w && (/[\/]/.test(w) || /\.[A-Za-z0-9]{1,6}$/.test(w))) return w;
  }
  return "";
}

/** activityOf is what the agent is doing now: { title, kind, file }. entries are
 *  the conversation's, for the tool still running. */
export function activityOf(st, entries = []) {
  let running = null;
  for (let i = entries.length - 1; i >= 0; i--) {
    if (entries[i] && entries[i].kind === "tool" && entries[i].status === "running") {
      running = entries[i];
      break;
    }
  }
  const title = (st && typeof st.activity === "string" && st.activity) || (running && (running.label || running.name)) || "";
  if (!title) return null;
  return { title, kind: running ? toolKind(running.name) : "other", file: running ? fileOf(running.summary) : "" };
}

/** Structured is the panel. st is the store's structured push, or null before the
 *  first one; error is the store's structuredError; desk is false in a window
 *  reached through the relay. snap is the store's snapshot, which carries the
 *  desk's answer to a send. */
export function Structured({ pane, snap, st, error = null, cancelError = null, settingError = null, queueResult = null, shellResult = null, exportResult = null, infoResult = null, signInResult = null, rewindResult = null, rulesResult = null, desk = true, store, conv = null, onOlder = null, details = null, onDetail = null, pending = null, search = null, onSearch = null, onClearSearch = null }) {
  // The pane a Stop that was not sent was pressed for, so the words do not follow the drawer to another pane.
  const [stopFailed, setStopFailed] = useState("");
  // The pane whose Stop was sent while a turn ran; it clears when the push says the turn is over.
  const [stopping, setStopping] = useState("");
  // The pane a key-sent Stop is out for, set at once so a second press before the page redraws sends nothing;
  // it goes when the turn is no longer running or waiting.
  const keyStop = useRef("");
  useEffect(() => {
    if (!st || (st.status !== "working" && st.status !== "waiting")) keyStop.current = "";
  }, [st && st.status]);
  useEffect(() => {
    if (stopping && (!st || st.status !== "working")) setStopping("");
  }, [stopping, st && st.status]);
  // The pane whose export panel is open.
  const [exporting, setExporting] = useState("");
  // The pane whose agent info is open.
  const [infoFor, setInfoFor] = useState("");
  // The pane whose rewind and fork panel is open.
  const [rewinding, setRewinding] = useState("");
  // What the last change of a setting got that the desk did not say: { pane, text }.
  const [settingNote, setSettingNote] = useState(null);
  const settingsEl = useRef(null);
  // A change to a value that reads as running without asking, waiting for the person's yes: { pane, key, value, label }.
  const [confirming, setConfirming] = useState(null);
  // What the composer reports and offers: the draft and whether a list is open, for the Escape handler; and focus and restore.
  const live = useRef({ draft: "", menuOpen: false });
  const composer = useRef({ focus: () => {}, restore: () => {} });
  if (error) return html`<p class="drawer-stub conv-refused" role="status">${clip(String(error.error || ""), 300)}</p>`;
  if (!st || st.id !== pane) return html`<p class="drawer-stub">Loading the agent.</p>`;
  const act = activityOf(st, conv && conv.entries ? conv.entries : []);
  const stop = (keepQueue = false) => {
    const ok = store.send(stopMessage(pane, keepQueue === true, "c" + ++cancelSeq));
    setStopFailed(ok ? "" : pane);
    // The turn ends when the agent says so, which may be a moment after the press.
    setStopping(ok && st.status === "working" ? pane : "");
    return ok;
  };
  const queue = queueOf(st);
  const controls = desk ? controlsOf(st) : [];
  // Every route to a change (a selector, "/mode yolo") comes through here, so a confirm cannot be skipped by one of them.
  const change = (control, value, confirmed = false) => {
    const msg = setMessage(pane, control, value, "s" + ++settingSeq);
    if (!msg) {
      setSettingNote({ pane, text: "That is not on the agent's list any more." });
      return false;
    }
    const choice = control.choices.find((o) => o.value === value);
    if (!confirmed && choice && choice.unasked) {
      setSettingNote(null);
      setConfirming({ pane, key: control.key, value, label: control.label + " to " + choice.label });
      return true;
    }
    setConfirming(null);
    const ok = store.send(msg);
    setSettingNote(ok ? null : { pane, text: SET_NOT_SENT_WORDS });
    return ok;
  };
  // The composer asks for these: a settings request typed into the box, and Flockdeck's own commands that are not the composer's.
  const applySetting = (text) => {
    const asked = parseSetting(text, controls);
    if (!asked) return "none";
    if (asked.error) {
      setSettingNote({ pane, text: asked.error });
      return "kept";
    }
    return change(asked.control, asked.value) ? "done" : "kept";
  };
  const onOwn = (id) => {
    if (id === "info") setInfoFor(pane);
    if (id === "rewind" || id === "fork") setRewinding(pane);
    if (id === "mode" || id === "model") {
      const c = controlFor(controls, id);
      const el = c && settingsEl.current && settingsEl.current.querySelector ? settingsEl.current.querySelector('[data-setting="' + c.key + '"]') : null;
      if (el && el.focus) {
        el.focus();
        if (el.showPicker) {
          try { el.showPicker(); } catch (_) { /* a picker may refuse outside a gesture */ }
        }
      }
    }
  };
  // Escape, or Ctrl+C over no selection, stops a running turn from the box or from anywhere in the drawer. The
  // keys of a menu are taken in onKey, which stops them here; interruptKey leaves the rest to their owners.
  const onRootKey = (e) => {
    const el = e && e.target;
    const win = el && el.ownerDocument ? el.ownerDocument.defaultView : null;
    const ctx = {
      desk, working: st.status === "working" || st.status === "waiting", waiting: st.status === "waiting", draft: live.current.draft, stopping: stopping === pane || keyStop.current === pane,
      menuOpen: live.current.menuOpen, dialogOpen: (!!confirming && confirming.pane === pane) || exporting === pane || rewinding === pane,
      selected: selectedText(win, el),
    };
    if (!interruptKey(e, ctx)) return;
    e.preventDefault();
    if (stop(queue.length > 0)) keyStop.current = pane;
  };
  const lead = html`<span class="structured-dot" data-status=${st.status} aria-hidden="true"></span>
    <p class="structured-activity" data-activity=${act ? "on" : "off"}>
      ${act
        ? html`<${Icon} kind=${act.kind} /><span class="structured-title">${clip(act.title, 160)}</span>${act.file ? html`<span class="structured-file">${clip(act.file, 160)}</span>` : null}`
        : html`<span class="structured-title">${IDLE_WORDS}</span>`}
    </p>
    <${UsageMeter} usage=${st.usage} />
    ${controls.length
      ? html`<div class="structured-settings" ref=${settingsEl} role="group" aria-label="Agent settings">
          ${controls.map((c) => html`<label class="structured-setting"><span class="structured-setting-name">${c.label}</span>
            <select class="structured-select" data-setting=${c.key} title=${c.label} value=${c.current} onChange=${(e) => change(c, e.target.value)}>
              ${c.choices.some((o) => o.value === c.current) ? null : html`<option value="" disabled>Not set</option>`}
              ${c.choices.map((o) => html`<option value=${o.value} title=${o.description || undefined}>${(o.group ? o.group + ": " : "") + o.label}</option>`)}
            </select></label>`)}
        </div>`
      : null}`;
  const stripItems = desk
    ? [{ label: "Agent info", className: "structured-info-btn", expanded: infoFor === pane || needsSignIn(st), disabled: needsSignIn(st), title: needsSignIn(st) ? "Open until the agent is signed in" : undefined, onClick: () => setInfoFor(infoFor === pane ? "" : pane) }]
    : [];
  const tail = html`${confirming && confirming.pane === pane
      ? html`<div class="structured-note structured-confirm" role="alert" data-confirm=${confirming.key}>
          <span>Change ${confirming.label}? The agent may then run commands and edit files without asking.</span>
          <button type="button" class="drawer-btn structured-confirm-yes" onClick=${() => {
            const c = controls.find((x) => x.key === confirming.key);
            if (c) change(c, confirming.value, true);
            else { setConfirming(null); setSettingNote({ pane, text: "That is not on the agent's list any more." }); }
          }}>Change it</button>
          <button type="button" class="drawer-btn structured-confirm-no" onClick=${() => setConfirming(null)}>Cancel</button>
        </div>`
      : null}`;
  return html`
    <div class="structured" data-structured-pane=${pane} onKeyDown=${onRootKey}>
      <${TodoList} st=${st} pane=${pane} />
      ${desk && (infoFor === pane || needsSignIn(st)) ? html`<${InfoPanel} pane=${pane} st=${st} store=${store} result=${infoResult} signInResult=${signInResult} rulesResult=${rulesResult} onClose=${() => setInfoFor("")} />` : null}
      ${desk && rewinding === pane ? html`<${RewindPanel} pane=${pane} store=${store} st=${st} entries=${conv && conv.entries ? conv.entries : []} result=${rewindResult} onText=${(t) => composer.current.restore(t)} onClose=${() => setRewinding("")} />` : null}
      ${desk && exporting === pane ? html`<${ExportPanel} pane=${pane} store=${store} result=${exportResult} onClose=${() => setExporting("")} />` : null}
      <${Conversation} lead=${lead} items=${stripItems} conv=${conv} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending}
        search=${search} onSearch=${onSearch} onClearSearch=${onClearSearch} canExport=${desk} onExport=${() => setExporting(pane)} className="structured-log" />
      ${desk ? html`<${ShellList} rows=${shellOf(st)} pane=${pane} store=${store} result=${shellResult} />` : null}
      ${desk ? html`<${QueueList} rows=${queue} pane=${pane} store=${store} result=${queueResult} working=${st.status === "working"} onStopKeep=${() => stop(true)} focusBox=${() => composer.current.focus()} />` : null}
      ${desk
        ? html`<${Composer} pane=${pane} snap=${snap} st=${st} store=${store} controls=${controls} applySetting=${applySetting} onOwn=${onOwn} stop=${stop}
            stopping=${stopping === pane} stopFailed=${stopFailed === pane} settingText=${settingNote && settingNote.pane === pane ? clip(settingNote.text, 300) : ""} settingError=${settingError && settingError.id === pane ? clip(String(settingError.error || ""), 300) : ""} cancelError=${cancelError && cancelError.id === pane ? clip(String(cancelError.error || ""), 300) : ""} shellResult=${shellResult} tail=${tail} live=${live} api=${composer} />`
        : null}
    </div>
  `;
}
