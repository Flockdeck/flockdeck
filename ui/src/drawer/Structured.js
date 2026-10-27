// The drawer's view of a structured pane, which has no terminal. It draws the
// latest structured push of the pane being watched (store.structured): the
// activity line, the context used of its size and the cost when the agent
// reported one, then the pane's conversation, and a prompt box with a Stop button.
//
// - The activity line is the tool still running, with an icon for its kind and
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
//   models and configOptions) are a row of selectors above the conversation
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
// Everything on a row came from an agent, a tool or a person, and none of it is
// put into the page as markup.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { outcomeOf, sendTo } from "../graph/ManagerBar.js";
import { Conversation } from "./Conversation.js";
import { Icon, toolKind } from "./conv/rows.js";
import { menuOf, flatten, insertText, keyOf, selectedIndex, OWN_COMMANDS } from "./slashModel.js";
import { controlsOf, controlFor, setMessage, ownCommandsFor, parseSetting } from "./settingsModel.js";

/** SEND_NOT_SENT_WORDS is what the prompt box says when the store did not send the text; the draft stays. */
export const SEND_NOT_SENT_WORDS = "Not connected to Flockdeck, so the text was not sent.";
/** STOP_NOT_SENT_WORDS is what Stop says when the store did not send it: the socket is down or the hello is not in. */
export const STOP_NOT_SENT_WORDS = "Not connected to Flockdeck, so Stop was not sent.";
// Each Stop carries its own req, so the store can tell which pane a refusal is for.
let cancelSeq = 0;
let settingSeq = 0;
/** SET_NOT_SENT_WORDS is what the selectors say when the store did not send the change. */
export const SET_NOT_SENT_WORDS = "Not connected to Flockdeck, so the change was not sent.";

export const IDLE_WORDS = "Idle";

const finite = (n) => typeof n === "number" && Number.isFinite(n) && n > 0;

/** contextText is "12,400 of 200,000 tokens used", or an empty string when the
 *  agent reported no size. */
export function contextText(usage) {
  if (!usage || !finite(usage.size)) return "";
  const used = finite(usage.used) ? usage.used : 0;
  return used.toLocaleString("en-US") + " of " + usage.size.toLocaleString("en-US") + " tokens used";
}

/** costText is the cost the agent reported, or an empty string. */
export function costText(usage) {
  if (!usage || !finite(usage.cost)) return "";
  const cur = typeof usage.currency === "string" ? usage.currency.replace(/[^A-Za-z$€£¥]/g, "").slice(0, 8) : "";
  return usage.cost.toFixed(2) + (cur ? " " + cur : "");
}

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
export function Structured({ pane, snap, st, error = null, cancelError = null, settingError = null, desk = true, store, conv = null, onOlder = null, details = null, onDetail = null, pending = null, search = null, onSearch = null, onClearSearch = null }) {
  const [draft, setDraft] = useState("");
  // The pane a Stop that was not sent was pressed for, so the words do not follow the drawer to another pane.
  const [stopFailed, setStopFailed] = useState("");
  const [sendFailed, setSendFailed] = useState("");
  // What the last change of a setting got that the desk did not say: { pane, text }.
  const [settingNote, setSettingNote] = useState(null);
  const settingsEl = useRef(null);
  // A change to a value that reads as running without asking, waiting for the person's yes: { pane, key, value, label }.
  const [confirming, setConfirming] = useState(null);
  // The send in flight: the mark outcomeOf follows. Set at once, so a second Send before the page has redrawn does not send again.
  const [mark, setMark] = useState(null);
  const busy = useRef(false);
  const box = useRef(null);
  const sendBtn = useRef(null);
  // The menu's chosen row, and the draft whose menu was closed with Escape.
  const [selKey, setSelKey] = useState(null);
  const [closedFor, setClosedFor] = useState(null);
  const outcome = mark ? outcomeOf(snap || {}, mark) : null;
  if (!outcome || outcome.kind !== "sending") busy.current = false;
  const taken = !!outcome && outcome.kind === "sent";
  // The text goes once the desk has taken it; a refused one stays to be changed or sent again.
  useEffect(() => {
    if (!taken) return;
    setDraft("");
    setMark(null);
  }, [taken, mark]);
  // Send is off while the answer is awaited, which takes the keyboard from it; a refusal gives it back to the text,
  // unless the person has moved on to something else in the meantime.
  const refusedNow = !!outcome && outcome.kind === "refused";
  useEffect(() => {
    const el = box.current;
    if (!refusedNow || !el || !el.focus) return;
    const doc = el.ownerDocument;
    const held = doc && doc.activeElement;
    // The drawer's own panel holding the keyboard is not something the person moved to.
    if (held && held !== doc.body && held !== sendBtn.current && !(held.contains && held.contains(el))) return;
    el.focus();
  }, [refusedNow, mark]);
  if (error) return html`<p class="drawer-stub conv-refused" role="status">${clip(String(error.error || ""), 300)}</p>`;
  if (!st || st.id !== pane) return html`<p class="drawer-stub">Loading the agent.</p>`;
  const act = activityOf(st, conv && conv.entries ? conv.entries : []);
  const ctx = contextText(st.usage);
  const cost = costText(st.usage);
  const submit = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    const text = draft.trim();
    if (!text || busy.current) return;
    // A leading space sends the text to the agent as it is, so an agent's own /mode command can still be reached.
    const asked = /^\s/.test(draft) ? null : parseSetting(text, controls);
    if (asked) {
      if (asked.error) {
        setSettingNote({ pane, text: asked.error });
      } else if (change(asked.control, asked.value)) {
        setDraft("");
      }
      return;
    }
    const next = sendTo((m) => store.send(m), snap, pane, text);
    if (next) {
      busy.current = true;
      setMark(next);
      setSendFailed("");
    } else {
      setMark(null);
      setSendFailed(pane);
    }
  };
  const stop = () => {
    setStopFailed(store.send({ cmd: "structuredCancel", id: pane, req: "c" + ++cancelSeq }) ? "" : pane);
  };
  const sending = !!outcome && outcome.kind === "sending";
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
  const menu = desk && !sending && closedFor !== draft ? menuOf(draft, st.commands, ownCommandsFor(OWN_COMMANDS, controls)) : null;
  const items = flatten(menu);
  // The chosen row is found by its command, so a list that changes under the person does not move it; -1 is none.
  const at = menu ? selectedIndex(items, selKey, menu.query) : -1;
  const choose = (it) => {
    if (it.group === "own") {
      setDraft("");
      if (it.id === "stop") stop();
      if (it.id === "mode" || it.id === "model") {
        const c = controlFor(controls, it.id);
        const el = c && settingsEl.current && settingsEl.current.querySelector ? settingsEl.current.querySelector('[data-setting="' + c.key + '"]') : null;
        if (el && el.focus) {
          el.focus();
          if (el.showPicker) {
            try { el.showPicker(); } catch (_) { /* a picker may refuse outside a gesture */ }
          }
        }
        setSelKey(null);
        setSendFailed("");
        return;
      }
    } else {
      setDraft(insertText(it));
    }
    setSelKey(null);
    setSendFailed("");
    if (box.current && box.current.focus) box.current.focus();
  };
  const onKey = (e) => {
    if (!menu) return;
    // A key that belongs to an input method's composition is the method's, not the menu's.
    if (e.isComposing || e.keyCode === 229) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const down = e.key === "ArrowDown";
      setSelKey(keyOf(items[at < 0 ? (down ? 0 : items.length - 1) : (at + (down ? 1 : items.length - 1)) % items.length]));
    } else if (((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") && at >= 0) {
      e.preventDefault();
      choose(items[at]);
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      setClosedFor(draft);
    }
  };
  const optId = (i) => "slash-" + pane + "-" + i;
  const row = (it, i) => html`<div id=${optId(i)} role="option" class=${"slash-item" + (i === at ? " slash-item--on" : "")} aria-selected=${i === at ? "true" : "false"} data-slash=${it.name}
    onMouseDown=${(e) => e.preventDefault()} onMouseEnter=${() => setSelKey(keyOf(it))} onClick=${() => choose(it)}>
    <span class="slash-name">/${it.name}</span>${it.hint ? html`<span class="slash-hint">${it.hint}</span>` : null}${it.description ? html`<span class="slash-desc">${it.description}</span>` : null}
  </div>`;
  return html`
    <div class="structured" data-structured-pane=${pane}>
      <div class="structured-facts">
        <p class="structured-activity" data-activity=${act ? "on" : "off"}>
          ${act
            ? html`<${Icon} kind=${act.kind} /><span class="structured-title">${clip(act.title, 160)}</span>${act.file ? html`<span class="structured-file">${clip(act.file, 160)}</span>` : null}`
            : html`<span class="structured-title">${IDLE_WORDS}</span>`}
        </p>
        ${ctx || cost
          ? html`<p class="structured-usage">${ctx ? html`<span class="structured-context">${ctx}</span>` : null}${cost ? html`<span class="structured-cost">${cost}</span>` : null}</p>`
          : null}
      </div>
      ${controls.length
        ? html`<div class="structured-settings" ref=${settingsEl} role="group" aria-label="Agent settings">
            ${controls.map((c) => html`<label class="structured-setting"><span class="structured-setting-name">${c.label}</span>
              <select class="structured-select" data-setting=${c.key} value=${c.current} onChange=${(e) => change(c, e.target.value)}>
                ${c.choices.some((o) => o.value === c.current) ? null : html`<option value="" disabled>Not set</option>`}
                ${c.choices.map((o) => html`<option value=${o.value} title=${o.description || undefined}>${(o.group ? o.group + ": " : "") + o.label}</option>`)}
              </select></label>`)}
          </div>`
        : null}
      <${Conversation} conv=${conv} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending}
        search=${search} onSearch=${onSearch} onClearSearch=${onClearSearch} className="structured-log" />
      ${desk
        ? html`<form class="structured-prompt" onSubmit=${submit}>
            ${menu
              ? html`<div class="slash-menu" role="listbox" aria-label="Commands" id=${"slash-" + pane}>
                  ${menu.own.length ? html`<div role="group" aria-label="Flockdeck"><p class="slash-group" aria-hidden="true">Flockdeck</p>${menu.own.map((c, i) => row({ ...c, group: "own" }, i))}</div>` : null}
                  ${menu.agent.length ? html`<div role="group" aria-label="From the agent"><p class="slash-group" aria-hidden="true">From the agent</p>${menu.agent.map((c, i) => row({ ...c, group: "agent" }, menu.own.length + i))}</div>` : null}
                </div>
                <p class="sr-only" role="status">${items.length === 1 ? "1 command" : items.length + " commands"}</p>`
              : null}
            <textarea ref=${box} class="structured-input" aria-label="Message to the agent" rows="2" value=${draft}
              role=${menu ? "combobox" : undefined} aria-expanded=${menu ? "true" : undefined} aria-autocomplete=${menu ? "list" : undefined} aria-controls=${menu ? "slash-" + pane : undefined} aria-activedescendant=${menu && at >= 0 ? optId(at) : undefined} onKeyDown=${onKey} readOnly=${outcome && outcome.kind === "sending"} aria-busy=${outcome && outcome.kind === "sending" ? "true" : undefined} onInput=${(e) => { setDraft(e.target.value); setSendFailed(""); if (outcome && outcome.kind === "refused") setMark(null); }}></textarea>
            <button ref=${sendBtn} type="submit" class="drawer-btn structured-send" disabled=${draft.trim() === "" || (outcome && outcome.kind === "sending")}>Send</button>
            <button type="button" class="drawer-btn structured-stop" onClick=${stop}>Stop</button>
            ${outcome && outcome.kind === "refused" ? html`<p class="structured-note structured-note--unsent structured-note--send" role="alert">${showControls(clip(outcome.text, 300))}</p>` : null}
            ${sendFailed === pane ? html`<p class="structured-note structured-note--unsent structured-note--send" role="alert">${SEND_NOT_SENT_WORDS}</p>` : null}
            ${stopFailed === pane ? html`<p class="structured-note structured-note--unsent" role="status">${STOP_NOT_SENT_WORDS}</p>` : null}
            ${confirming && confirming.pane === pane
              ? html`<div class="structured-note structured-confirm" role="alert" data-confirm=${confirming.key}>
                  <span>Change ${confirming.label}? The agent may then run commands and edit files without asking.</span>
                  <button type="button" class="drawer-btn structured-confirm-yes" onClick=${() => {
                    const c = controls.find((x) => x.key === confirming.key);
                    if (c) change(c, confirming.value, true);
                    else { setConfirming(null); setSettingNote({ pane, text: "That is not on the agent's list any more." }); }
                  }}>Change it</button>
                  <button type="button" class="drawer-btn structured-confirm-no" onClick=${() => setConfirming(null)}>Cancel</button>
                </div>`
              : null}
            ${settingNote && settingNote.pane === pane ? html`<p class="structured-note structured-note--unsent" role="status">${showControls(clip(settingNote.text, 300))}</p>` : null}
            ${settingError && settingError.id === pane ? html`<p class="structured-note" role="status">${showControls(clip(String(settingError.error || ""), 300))}</p>` : null}
            ${cancelError && cancelError.id === pane ? html`<p class="structured-note" role="status">${showControls(clip(String(cancelError.error || ""), 300))}</p>` : null}
          </form>`
        : null}
    </div>
  `;
}
