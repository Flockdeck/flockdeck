// The tab strip: the desk's tabs for the project on screen, as the state push
// lists them. A tab is chosen, renamed, moved left or right, or closed here, and
// a new agent tab or shell tab is opened from the "+" menu at its end, which also
// holds Choose agent, Resume, Find pane, Rename and the two moves. The desk
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
import { VIEWS } from "./Views.js";

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
export function Tabs({ tabs, send, onChoose, onHistory = null, onFind = null, view = "graph", onView = null, warnFor, show = true }) {
  const [renaming, setRenaming] = useState("");
  const [closing, setClosing] = useState(null);
  const [stop, setStop] = useState("");
  const root = useRef(null);
  // The tab whose button takes the keyboard back after this render: one that
  // had it (a browser drops the keyboard from a button it moves in the list when
  // the tabs are reordered), or one whose rename field has just closed. The
  // keyboard is read here, before the page is changed.
  const refocus = useRef("");
  const had = root.current && root.current.ownerDocument ? root.current.ownerDocument.activeElement : null;
  if (had && had.getAttribute && had.getAttribute("role") === "tab" && root.current.contains(had)) {
    const li = had.closest("li");
    if (li) refocus.current = li.getAttribute("data-tab") || "";
  }
  useEffect(() => {
    const id = refocus.current;
    if (!id || !root.current) return;
    refocus.current = "";
    const li = [...root.current.querySelectorAll("li")].find((n) => n.getAttribute("data-tab") === id);
    const pick = li ? li.querySelector('[role="tab"]') : null;
    if (pick && pick.focus && pick.ownerDocument.activeElement !== pick) pick.focus();
  });
  if (!show) return html`<div class="tabs tabs--off"><p class="tabs-note">${OTHER_PROJECT_WORDS}</p></div>`;
  const current = tabs.find((t) => t.active) || null;
  const move = (dir) => {
    const cmd = current ? moveTabCommand(tabs, current.id, dir) : null;
    if (cmd) send(cmd);
  };
  // Roving tabindex: one tab button is in the Tab order, the one that last had the
  // keyboard, else the open tab, else the first. Arrow keys, Home and End move the
  // keyboard along the tabs and do not choose one; Enter or Space on a tab does.
  const stopId = tabs.some((t) => t.id === stop) ? stop : current ? current.id : tabs.length ? tabs[0].id : "";
  const onListKey = (e) => {
    if (composing(e) || e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
    const li = e.target && e.target.getAttribute && e.target.getAttribute("role") === "tab" ? e.target.closest("li") : null;
    const at = li ? tabs.findIndex((t) => t.id === li.getAttribute("data-tab")) : -1;
    if (at < 0) return;
    let to = -1;
    if (e.key === LIST.right) to = (at + 1) % tabs.length;
    else if (e.key === LIST.left) to = (at - 1 + tabs.length) % tabs.length;
    else if (e.key === LIST.home) to = 0;
    else if (e.key === LIST.end) to = tabs.length - 1;
    if (to < 0) return;
    e.preventDefault();
    e.stopPropagation();
    const next = [...root.current.querySelectorAll("li")].find((n) => n.getAttribute("data-tab") === tabs[to].id);
    const pick = next ? next.querySelector('[role="tab"]') : null;
    setStop(tabs[to].id);
    if (pick) pick.focus();
  };
  const askClose = (t) => {
    const words = warnFor ? warnFor(t) : [];
    if (words.length === 0 && t.activity === "waiting") words.push(CLOSE_WAITING_WORDS);
    if (words.length === 0) send({ cmd: "closeTab", id: t.id });
    else setClosing({ id: t.id, words });
  };
  return html`
    <div class="tabs" role="toolbar" aria-label="Tabs" ref=${root}>
      <ul class="tabs-list" role="tablist" aria-label="Open tabs" onKeyDown=${onListKey}>
        ${tabs.map((t) =>
          renaming === t.id
            ? html`<${RenameBox} key=${t.id} tab=${t} onDone=${(text) => {
                // An empty title on a tab that already has the automatic one changes nothing.
                if (text !== null && text !== t.title && !(text === "" && !t.named)) send({ cmd: "renameTab", id: t.id, text });
                refocus.current = t.id;
                setRenaming("");
              }} />`
            : html`<${TabItem} key=${t.id} tab=${t} stop=${t.id === stopId} onFocus=${() => setStop(t.id)} onSelect=${() => send({ cmd: "selectTab", id: t.id })}
                onRename=${() => setRenaming(t.id)}
                onClose=${() => askClose(t)} />`)}
      </ul>
      <${PlusMenu}>${(act) => html`
        <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => send({ cmd: "newTab", kind: "agent" }))}>New agent tab</button>
        <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => onChoose && onChoose())}>Choose agent…</button>
        <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => send({ cmd: "newTab", kind: "shell" }))}>New shell tab</button>
        ${onHistory ? html`<button type="button" role="menuitem" class="tabs-btn tabs-history" onClick=${act(onHistory)}>Resume…</button>` : null}
        ${onFind ? html`<button type="button" role="menuitem" class="tabs-btn tabs-find" onClick=${act(onFind)}>Find pane…</button>` : null}
        ${current
          ? html`<hr class="tabs-sep" />
              <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => setRenaming(current.id))}>Rename</button>
              <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => move(-1))} disabled=${!moveTabCommand(tabs, current.id, -1)}>Move left</button>
              <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => move(1))} disabled=${!moveTabCommand(tabs, current.id, 1)}>Move right</button>`
          : null}`}<//>
      ${onView
        ? html`<div class="tabs-views" role="group" aria-label="Views">
            ${VIEWS.map(([id, name]) => html`<button type="button" key=${id} class=${"tabs-view" + (view === id ? " is-on" : "")} data-view=${id} aria-pressed=${view === id ? "true" : "false"} onClick=${() => onView(id)}>${name}</button>`)}
          </div>`
        : null}
      ${closing
        ? html`<${Confirm} title="Close this tab?" words=${closing.words} label="Close tab"
            onConfirm=${() => { send({ cmd: "closeTab", id: closing.id }); setClosing(null); }} onCancel=${() => setClosing(null)} />`
        : null}
    </div>
  `;
}

/** PlusMenu is the "+" at the end of the tabs and the menu it opens. Its child is a
 *  function that is given `act`, which wraps an item's action so that choosing the
 *  item gives the keyboard back to the "+" and closes the menu before the action
 *  runs (a dialog the action opens then takes the "+" as where to return to); Escape closes it and gives the keyboard back to the "+";
 *  Up and Down move along the items; a press outside closes it. */
function PlusMenu({ children }) {
  const [open, setOpen] = useState(false);
  const box = useRef(null);
  const items = () => (box.current ? [...box.current.querySelectorAll('[role="menuitem"]')].filter((b) => !b.disabled) : []);
  useEffect(() => {
    if (!open) return undefined;
    const first = items()[0];
    if (first && first.focus) first.focus();
    const doc = box.current && box.current.ownerDocument;
    const away = (e) => { if (box.current && !box.current.contains(e.target)) setOpen(false); };
    if (doc && doc.addEventListener) doc.addEventListener("mousedown", away);
    return () => { if (doc && doc.removeEventListener) doc.removeEventListener("mousedown", away); };
  }, [open]);
  const close = (refocus) => {
    setOpen(false);
    if (refocus && box.current) {
      const plus = box.current.querySelector(".tabs-plus");
      if (plus && plus.focus) plus.focus();
    }
  };
  const act = (fn) => () => {
    close(true);
    if (fn) fn();
  };
  const onKeyDown = (e) => {
    if (!open || composing(e) || e.ctrlKey || e.altKey || e.metaKey) return;
    if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      close(true);
      return;
    }
    const down = e.key === LIST.down, up = e.key === LIST.up;
    if (!down && !up) return;
    e.preventDefault();
    e.stopPropagation();
    const list = items();
    const at = list.indexOf(e.target);
    const to = down ? (at + 1) % list.length : (at - 1 + list.length) % list.length;
    if (list[to]) list[to].focus();
  };
  return html`
    <div class="tabs-plus-box" ref=${box} onKeyDown=${onKeyDown}>
      <button type="button" class="tabs-plus" aria-label="New tab and tab actions" title="New tab and tab actions" aria-haspopup="menu" aria-expanded=${open ? "true" : "false"}
        onClick=${() => setOpen(!open)}>+</button>
      ${open ? html`<div class="tabs-menu" role="menu" aria-label="Tab actions">${children(act)}</div>` : null}
    </div>
  `;
}

function TabItem({ tab, stop, onFocus, onSelect, onRename, onClose }) {
  const cls = "tab" + (tab.active ? " is-active" : "") + (tab.activity ? " tab--" + tab.activity : "");
  const label = (tab.title || "Untitled") + (tab.activity ? ", " + tab.activity : "");
  return html`
    <li class=${cls} role="presentation" data-tab=${tab.id}>
      <button type="button" class="tab-pick" role="tab" aria-selected=${tab.active ? "true" : "false"} aria-label=${label} title=${tab.title}
        tabindex=${stop ? "0" : "-1"} onFocus=${onFocus} onClick=${onSelect} onDblClick=${onRename}>
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
