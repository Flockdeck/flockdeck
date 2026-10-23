// The tab strip: the desk's tabs for the project on screen, as the state push
// lists them. A tab is chosen, renamed, moved left or right, or closed here, and
// a new agent tab or shell tab is opened from the buttons at its end. The desk
// decides everything: this sends a command and draws what the next state says,
// so a refusal (a tab with a locked pane cannot be closed) arrives as a notice.
//
// The strip is for the project the desk has active. The tabs of the state push
// belong to it, so for any other project the window shows a line and no tabs.
//
// A tab's title can come from an agent (an automatic title is made from the
// first words of its task), so it is drawn as text and is never markup.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Confirm } from "./Confirm.js";
import { moveTabCommand } from "../lib/lifecycle.js";

/** TITLE_MAX is the most characters of a title the desk keeps. */
export const TITLE_MAX = 80;

/** OTHER_PROJECT_WORDS is what the strip says for a project that is not the
 *  desk's active one. */
export const OTHER_PROJECT_WORDS = "Tabs are shown for the project the desk has open.";

/** CLOSE_WAITING_WORDS is the sentence before closing a tab that is waiting on
 *  the person. */
export const CLOSE_WAITING_WORDS = "An agent in this tab is waiting for your answer. Closing the tab ends it and cancels the request.";

/** Tabs draws the strip. tabs are from tabsOf; send is the store's; onChoose
 *  opens the start dialog for a tab on a chosen agent. With show false (the
 *  window is on a project that is not the desk's) there is only the note. */
export function Tabs({ tabs, send, onChoose, show = true }) {
  const [renaming, setRenaming] = useState("");
  const [closing, setClosing] = useState("");
  if (!show) return html`<div class="tabs tabs--off"><p class="tabs-note">${OTHER_PROJECT_WORDS}</p></div>`;
  const current = tabs.find((t) => t.active) || null;
  const move = (dir) => {
    const cmd = current ? moveTabCommand(tabs, current.id, dir) : null;
    if (cmd) send(cmd);
  };
  return html`
    <div class="tabs" role="toolbar" aria-label="Tabs">
      <ul class="tabs-list" role="tablist" aria-label="Open tabs">
        ${tabs.map((t) =>
          renaming === t.id
            ? html`<${RenameBox} key=${t.id} tab=${t} onDone=${(text) => { if (text !== null && text !== t.title) send({ cmd: "renameTab", id: t.id, text }); setRenaming(""); }} />`
            : html`<${TabItem} key=${t.id} tab=${t} onSelect=${() => send({ cmd: "selectTab", id: t.id })}
                onRename=${() => setRenaming(t.id)}
                onClose=${() => (t.activity === "waiting" ? setClosing(t.id) : send({ cmd: "closeTab", id: t.id }))} />`)}
      </ul>
      <div class="tabs-actions">
        <button type="button" class="tabs-btn" onClick=${() => send({ cmd: "newTab", kind: "agent" })}>New agent tab</button>
        <button type="button" class="tabs-btn" onClick=${() => onChoose && onChoose()}>Choose agent…</button>
        <button type="button" class="tabs-btn" onClick=${() => send({ cmd: "newTab", kind: "shell" })}>New shell tab</button>
        ${current
          ? html`<button type="button" class="tabs-btn" onClick=${() => setRenaming(current.id)}>Rename</button>
              <button type="button" class="tabs-btn" onClick=${() => move(-1)} disabled=${!moveTabCommand(tabs, current.id, -1)}>Move left</button>
              <button type="button" class="tabs-btn" onClick=${() => move(1)} disabled=${!moveTabCommand(tabs, current.id, 1)}>Move right</button>`
          : null}
      </div>
      ${closing
        ? html`<${Confirm} title="Close this tab?" words=${[CLOSE_WAITING_WORDS]} label="Close tab"
            onConfirm=${() => { send({ cmd: "closeTab", id: closing }); setClosing(""); }} onCancel=${() => setClosing("")} />`
        : null}
    </div>
  `;
}

function TabItem({ tab, onSelect, onRename, onClose }) {
  const cls = "tab" + (tab.active ? " is-active" : "") + (tab.activity ? " tab--" + tab.activity : "");
  const label = (tab.title || "Untitled") + (tab.activity ? ", " + tab.activity : "");
  return html`
    <li class=${cls} role="presentation" data-tab=${tab.id}>
      <button type="button" class="tab-pick" role="tab" aria-selected=${tab.active ? "true" : "false"} aria-label=${label} title=${tab.title}
        onClick=${onSelect} onDblClick=${onRename}>
        <span class="tab-title">${tab.title || "Untitled"}</span>
        ${tab.activity ? html`<span class=${"badge badge--" + tab.activity} aria-hidden="true">${tab.activity}</span>` : null}
      </button>
      <button type="button" class="tab-close" aria-label=${"Close tab " + (tab.title || "Untitled")} onClick=${onClose}>×</button>
    </li>
  `;
}

/** RenameBox is the field a tab's title is edited in. Enter sends it, Escape
 *  leaves the title alone, and leaving the field empty gives the tab its
 *  automatic title back. */
function RenameBox({ tab, onDone }) {
  const [value, setValue] = useState(tab.title);
  const input = useRef(null);
  useEffect(() => {
    if (input.current && input.current.focus) input.current.focus();
  }, []);
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      e.stopPropagation();
      onDone(value.trim());
    } else if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      onDone(null);
    }
  };
  return html`
    <li class="tab tab--renaming" data-tab=${tab.id}>
      <input ref=${input} class="tab-rename" type="text" maxlength=${TITLE_MAX} value=${value} aria-label="Tab title"
        placeholder="Leave empty for the automatic title" onInput=${(e) => setValue(e.target.value)} onKeyDown=${onKeyDown} onBlur=${() => onDone(null)} />
    </li>
  `;
}
