// The goal strip: one tab for each goal (team) in the graph, and a Solo tab for the
// panes in none. It replaces the strip of the desk's terminal tabs. What it shows
// comes from graph/GoalTabs.js; what the person does here (open, rename, move,
// archive, restore) is handed to the page, which keeps it in the interface's
// preferences. The switch between views (Team, Graph, Board, ...) is not in this
// strip: it is the ViewBar under it, so the two are never on one row.
//
// A goal's label can come from an agent (the goal text), so it is drawn as text and
// is never markup.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { viewsOf, firstViewOf } from "./Views.js";

/** TITLE_MAX is the most characters of a name the person may give a goal's tab. */
export const TITLE_MAX = 80;

function labelFor(t) {
  const n = t.needs;
  return t.label + (t.activity ? ", " + t.activity : "") + (n > 0 ? ", " + n + (n === 1 ? " needs" : " need") + " you" : "");
}

/** GoalStrip draws the tabs. tabs and archived are from goalTabsOf, active is the
 *  open tab's id. canStep(id, dir) says whether a goal can move that way. The
 *  other props are the page's: each is a function, and one left out (null) leaves
 *  its control out. */
export function GoalStrip({
  tabs, archived = [], active, onSelect, onRename, onArchive, onRestore, onMove, onStep, canStep,
  onStartTeam = null, onNewAgent = null, onChoose = null, onNewShell = null, onHistory = null, onFind = null,
}) {
  const [renaming, setRenaming] = useState("");
  const [stop, setStop] = useState("");
  const [dragging, setDragging] = useState("");
  const root = useRef(null);
  const stopId = tabs.some((t) => t.id === stop) ? stop : active;
  const goals = tabs.filter((t) => t.kind === "team");
  const current = tabs.find((t) => t.id === active) || null;
  // The open tab is kept in view when the row scrolls sideways.
  useEffect(() => {
    const li = root.current ? [...root.current.querySelectorAll("li")].find((n) => n.getAttribute("data-tab") === active) : null;
    if (li && li.scrollIntoView) li.scrollIntoView({ inline: "nearest", block: "nearest" });
  }, [active]);
  // The tab to give focus to once the strip has redrawn: after a rename ends at the keyboard, or an archive removes the focused tab.
  const refocus = useRef("");
  useEffect(() => {
    if (!refocus.current || renaming || !root.current) return;
    const li = [...root.current.querySelectorAll("li")].find((n) => n.getAttribute("data-tab") === refocus.current);
    const pick = li ? li.querySelector('[role="tab"]') : null;
    if (!pick) return;
    refocus.current = "";
    setStop(pick.closest("li").getAttribute("data-tab"));
    pick.focus();
  }, [renaming, tabs]);
  // An archive is only followed by a focus move once the strip has lost the tab: a refused one leaves it alone.
  const archiving = useRef(null);
  useEffect(() => {
    const a = archiving.current;
    if (!a || a.tabs === tabs) return;
    archiving.current = null;
    if (tabs.some((x) => x.id === a.id)) return;
    const li = root.current ? [...root.current.querySelectorAll("li")].find((n) => n.getAttribute("data-tab") === a.near) : null;
    const to = (li && li.querySelector('[role="tab"]')) || (root.current && root.current.querySelector(".tabs-plus"));
    if (li) setStop(a.near);
    if (to && to.focus) to.focus();
  }, [tabs]);
  const archive = (id) => {
    const at = tabs.findIndex((x) => x.id === id);
    const near = tabs[at + 1] || tabs[at - 1];
    archiving.current = { id, near: near ? near.id : "", tabs };
    onArchive(id);
  };
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
    if (e.key === LIST.remove) {
      // Delete archives the goal under the keyboard, as the close button does; Solo has no close button and keeps its tab.
      const t = tabs[at];
      if (t.kind === "team" && onArchive) {
        e.preventDefault();
        e.stopPropagation();
        archive(t.id);
      }
      return;
    }
    if (to < 0) return;
    e.preventDefault();
    e.stopPropagation();
    const next = [...root.current.querySelectorAll("li")].find((n) => n.getAttribute("data-tab") === tabs[to].id);
    const pick = next ? next.querySelector('[role="tab"]') : null;
    setStop(tabs[to].id);
    if (pick) pick.focus();
  };
  return html`
    <div class="goaltabs" role="group" aria-label="Goals" ref=${root}>
      <ul class="goaltabs-list" role="tablist" aria-label="Goals" onKeyDown=${onListKey}>
        ${tabs.map((t) => {
          if (renaming === t.id) {
            return html`<${RenameBox} key=${t.id} tab=${{ id: t.id, title: t.label }} onDone=${(text, by) => {
              if (text !== null && text !== t.label) onRename(t.id, text);
              if (by !== "blur") refocus.current = t.id;
              setRenaming("");
            }} />`;
          }
          const goal = t.kind === "team";
          const cls = "tab goaltab" + (t.id === active ? " is-active" : "") + (t.activity ? " tab--" + t.activity : "") + (dragging === t.id ? " is-dragging" : "");
          return html`<li key=${t.id} class=${cls} role="presentation" data-tab=${t.id} data-kind=${t.kind}
            draggable=${goal && onMove ? "true" : undefined}
            onDragStart=${goal && onMove ? (e) => { setDragging(t.id); if (e.dataTransfer) { e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", t.id); } } : undefined}
            onDragOver=${goal && dragging && dragging !== t.id ? (e) => e.preventDefault() : undefined}
            onDrop=${goal && dragging && dragging !== t.id
              ? (e) => { e.preventDefault(); onMove(dragging, goals.findIndex((g) => g.id === t.id)); setDragging(""); }
              : undefined}
            onDragEnd=${() => setDragging("")}>
            <button type="button" class="tab-pick" role="tab" aria-selected=${t.id === active ? "true" : "false"} aria-label=${labelFor(t)} title=${t.title}
              tabindex=${t.id === stopId ? "0" : "-1"} onFocus=${() => setStop(t.id)} onClick=${() => onSelect(t.id)}
              onDblClick=${goal && onRename ? () => setRenaming(t.id) : undefined}>
              <span class="tab-title">${t.label}</span>
              ${t.needs > 0 ? html`<span class="badge badge--waiting tab-needs" aria-hidden="true">${t.needs}</span>` : t.activity ? html`<span class=${"tab-dot tab-dot--" + t.activity} aria-hidden="true"></span>` : null}
            </button>
            ${goal && onArchive
              ? html`<button type="button" class="tab-close" aria-hidden="true" tabindex="-1" title="Archive this goal's tab" onClick=${() => archive(t.id)}>×</button>`
              : null}
          </li>`;
        })}
      </ul>
      <${PlusMenu} dot=${archived.some((t) => t.needs > 0)}>${(act) => html`
        ${onStartTeam ? html`<button type="button" role="menuitem" class="tabs-btn tabs-team" onClick=${act(onStartTeam)}>Start a team…</button>` : null}
        ${onNewAgent ? html`<button type="button" role="menuitem" class="tabs-btn" onClick=${act(onNewAgent)}>New agent tab</button>` : null}
        ${onChoose ? html`<button type="button" role="menuitem" class="tabs-btn" onClick=${act(onChoose)}>Choose agent…</button>` : null}
        ${onNewShell ? html`<button type="button" role="menuitem" class="tabs-btn" onClick=${act(onNewShell)}>New shell tab</button>` : null}
        ${onHistory ? html`<button type="button" role="menuitem" class="tabs-btn tabs-history" onClick=${act(onHistory)}>Resume…</button>` : null}
        ${onFind ? html`<button type="button" role="menuitem" class="tabs-btn tabs-find" onClick=${act(onFind)}>Find pane…</button>` : null}
        ${current && current.kind === "team"
          ? html`<hr class="tabs-sep" />
              ${onRename ? html`<button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => setRenaming(current.id))}>Rename this goal</button>` : null}
              ${onArchive ? html`<button type="button" role="menuitem" class="tabs-btn tabs-archive" onClick=${act(() => archive(current.id))}>Archive this goal</button>` : null}
              ${onStep ? html`<button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => onStep(current.id, -1))} disabled=${!canStep(current.id, -1)}>Move left</button>
              <button type="button" role="menuitem" class="tabs-btn" onClick=${act(() => onStep(current.id, 1))} disabled=${!canStep(current.id, 1)}>Move right</button>` : null}`
          : null}
        ${archived.length > 0 && onRestore
          ? html`<hr class="tabs-sep" /><div role="group" aria-label="Archived goals"><p class="tabs-menu-head" aria-hidden="true">Archived goals</p>
              ${archived.map((t) => html`<button key=${t.id} type="button" role="menuitem" class="tabs-btn tabs-restore" data-tab=${t.id} title=${t.title} onClick=${act(() => onRestore(t.id))}>Show ${t.label}</button>`)}</div>`
          : null}`}<//>
    </div>
  `;
}

/** ViewBar is the switch between the views of the page. It is a row of its own
 *  under the goal strip, drawn as a group of buttons and not as tabs. */
export function ViewBar({ view = firstViewOf(), views = null, onView }) {
  const bar = useRef(null);
  useEffect(() => {
    const on = bar.current ? [...bar.current.querySelectorAll("button")].find((b) => b.getAttribute("data-view") === view) : null;
    if (on && on.scrollIntoView) on.scrollIntoView({ inline: "nearest", block: "nearest" });
  }, [view]);
  return html`<div class="viewbar" role="group" aria-label="Views" ref=${bar}>
    ${(views || viewsOf()).map(([id, name]) => html`<button type="button" key=${id} class=${"tabs-view" + (view === id ? " is-on" : "")} data-view=${id} aria-pressed=${view === id ? "true" : "false"} onClick=${() => onView(id)}>${name}</button>`)}
  </div>`;
}

/** PlusMenu is the "+" at the end of the tabs and the menu it opens. Its child is a
 *  function that is given `act`, which wraps an item's action so that choosing the
 *  item gives the keyboard back to the "+" and closes the menu before the action
 *  runs (a dialog the action opens then takes the "+" as where to return to); Escape closes it and gives the keyboard back to the "+";
 *  Up and Down move along the items; a press outside closes it. */
export function PlusMenu({ children, dot = false }) {
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
    // Tab leaves the menu: it closes and the next stop is the one after the + button.
    if (e.key === LIST.tab) {
      close(true);
      return;
    }
    const down = e.key === LIST.down, up = e.key === LIST.up, home = e.key === LIST.home, end = e.key === LIST.end;
    if (!down && !up && !home && !end) return;
    e.preventDefault();
    e.stopPropagation();
    const list = items();
    const at = list.indexOf(e.target);
    const to = home ? 0 : end ? list.length - 1 : down ? (at + 1) % list.length : (at - 1 + list.length) % list.length;
    if (list[to]) list[to].focus();
  };
  return html`
    <div class="tabs-plus-box" ref=${box} onKeyDown=${onKeyDown}>
      <button type="button" class="tabs-plus" aria-label=${"New tab and tab actions" + (dot ? ", an archived goal needs you" : "")} title=${dot ? "An archived goal needs you" : "New tab and tab actions"} aria-haspopup="menu" aria-expanded=${open ? "true" : "false"}
        onClick=${() => setOpen(!open)}>+${dot ? html`<span class="tabs-plus-dot" aria-hidden="true"></span>` : null}</button>
      ${open ? html`<div class="tabs-menu" role="menu" aria-label="Tab actions">${children(act)}</div>` : null}
    </div>
  `;
}

/** RenameBox is the field a goal's name is edited in. Enter keeps it, Escape
 *  leaves the name alone, leaving the field (blur) keeps what was typed, and an
 *  empty field gives the tab the label made from the goal back. onDone(text, by)
 *  gets by = "blur" when focus left the field of its own accord. */
export function RenameBox({ tab, onDone }) {
  const [value, setValue] = useState(tab.title);
  const input = useRef(null);
  const done = useRef(false);
  const finish = (text, by) => {
    if (done.current) return;
    done.current = true;
    onDone(text, by);
  };
  useEffect(() => {
    if (input.current && input.current.focus) input.current.focus();
  }, []);
  const onKeyDown = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept) {
      e.preventDefault();
      e.stopPropagation();
      finish(value.trim(), "key");
    } else if (e.key === LIST.cancel) {
      e.preventDefault();
      e.stopPropagation();
      finish(null, "key");
    }
  };
  return html`
    <li class="tab tab--renaming" data-tab=${tab.id}>
      <input ref=${input} class="tab-rename" type="text" maxlength=${TITLE_MAX} value=${value} aria-label="Goal name"
        placeholder="Leave empty to use the goal's own words" onInput=${(e) => setValue(e.target.value)} onKeyDown=${onKeyDown} onBlur=${() => finish(value.trim(), "blur")} />
    </li>
  `;
}
