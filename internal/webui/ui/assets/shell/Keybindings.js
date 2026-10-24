// The keyboard section of the settings: every action of the key table with its
// binding. Pressing a binding records the next key as its new one; Escape or Tab
// leaves it alone, Backspace or Delete takes the binding away (the action is
// then in the command palette only), and each changed action has its own Reset.
// The desktop keeps the bindings and sends the whole table again, which every
// window follows.
//
// While a key is being recorded the shell's own key handling is off (Live tells
// it through onRecording), so the key pressed is the new binding and does not
// run the action it was.
import { useEffect, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Row } from "./Controls.js";
import { Confirm } from "./Confirm.js";
import { bindingsFrom, isBareKey, recorded, signatureOf } from "./keys.js";
import { RISKY_WORDS, anyChanged, resetAllCommand, resetCommand, rowsOf, setCommand } from "../lib/keybindings.js";

/** Keybindings is the section. keys is the key table, send the store's send,
 *  onRecording(on) is told when a key is being recorded and when it stops, and
 *  page is the window the keys arrive on. */
export function Keybindings({ keys, send, page = null, onRecording = () => {} }) {
  const [editing, setEditing] = useState("");
  const [problem, setProblem] = useState(null);
  const [asking, setAsking] = useState(false);
  const sections = rowsOf(keys);

  useEffect(() => {
    onRecording(!!editing);
    return () => onRecording(false);
  }, [!!editing]);

  // The next key is the new binding. It is read before the shell sees it, and
  // nothing else runs on it.
  useEffect(() => {
    if (!editing || !page) return undefined;
    const id = editing;
    const onKey = (e) => {
      const r = recorded(e);
      if (r.kind === "wait") return;
      if (!r.pass) {
        e.preventDefault();
        e.stopPropagation();
      }
      setEditing("");
      if (r.kind === "cancel") return;
      if (r.kind === "chord") {
        if (isBareKey(r.chord)) {
          setProblem({ id, text: r.chord + " " + RISKY_WORDS });
          return;
        }
        const other = bindingsFrom(keys).get(signatureOf(r.chord));
        if (other && other !== id) {
          const k = keys.find((x) => x.id === other);
          setProblem({ id, text: r.chord + " is already " + (k && k.label ? k.label : other) });
          return;
        }
      }
      setProblem(null);
      send(setCommand(id, r.kind === "clear" ? "" : r.chord));
    };
    page.addEventListener("keydown", onKey, true);
    return () => page.removeEventListener("keydown", onKey, true);
  }, [editing, keys, page]);

  if (!Array.isArray(keys) || keys.length === 0) {
    return html`<h3 class="set-head">Keyboard</h3><p class="set-intro" role="status">The desktop has not sent the key table yet.</p>`;
  }
  return html`
    <h3 class="set-head">Keyboard</h3>
    <p class="set-intro">Shortcuts for this window's own controls: agents, tabs, dialogs and the rest. A terminal keeps its own shortcuts for what you type in a pane; this does not touch those.</p>
    <${Row} id="set-keybind-all" label="Every shortcut" about="Undoes every change made here, for every action at once.">
      <button type="button" class="dialog-btn" id="set-keybind-reset-all" disabled=${!anyChanged(keys)} onClick=${() => setAsking(true)}>Reset every shortcut to its default…</button>
    <//>
    ${asking
      ? html`<${Confirm} title="Put every shortcut back to its default?" words=${["Every change made to a shortcut is undone, in every window."]} label="Reset every shortcut"
          onConfirm=${() => { setAsking(false); setProblem(null); send(resetAllCommand()); }} onCancel=${() => setAsking(false)} />`
      : null}
    ${sections.map((s) => html`
      <section key=${s.name} aria-label=${s.name}>
        <h4 class="set-sub">${s.name}</h4>
        ${s.rows.map((k) => html`
          <div class="keybind-row" key=${k.id} data-action=${k.id}>
            <div class="keybind-label" id=${"set-keybind-label-" + k.id}>${k.label}</div>
            <div class="keybind-controls">
              ${k.range
                ? html`<span class="keybind-chord empty">${k.keys}</span>`
                : html`<button type="button" id=${"set-keybind-" + k.id}
                    class=${"keybind-chord" + (k.keys ? "" : " empty") + (k.overridden ? " changed" : "") + (editing === k.id ? " recording" : "")}
                    aria-labelledby=${"set-keybind-label-" + k.id} aria-describedby=${"set-keybind-hint-" + k.id}
                    onClick=${() => { setProblem(null); setEditing(k.id); }}>${editing === k.id ? "Press a key…" : k.keys || "No binding"}</button>
                  <span class="sr-only" id=${"set-keybind-hint-" + k.id}>${k.keys ? "Bound to " + k.keys + ". " : "No binding. "}Press to record a new one.</span>
                  <button type="button" id=${"set-keybind-reset-" + k.id} class="keybind-reset" aria-label=${"Reset " + k.label + " to its default"}
                    disabled=${!k.overridden} onClick=${() => { setProblem(null); send(resetCommand(k.id)); }}>Reset</button>`}
            </div>
            ${problem && problem.id === k.id ? html`<div class="keybind-error" role="alert">${problem.text}</div>` : null}
          </div>`)}
      </section>`)}
  `;
}
