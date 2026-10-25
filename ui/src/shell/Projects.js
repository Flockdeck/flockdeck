// The projects dialog: the open projects (switch to one, rename, archive, close,
// add a directory to, group several into one), the recent and archived ones the
// desktop remembers (open, rename, move, archive, remove from the list), and a
// folder browser for opening one that is in neither.
//
// It is modal like the start dialog: the keyboard goes to the first control when
// it opens and back to where it was when it closes, Tab stays inside, Escape
// closes (or, in a name being edited, only ends the edit). Closing a project
// stops every agent in it, so it asks first when anything runs there. Removing a
// project from a list, or archiving it, touches nothing on disk, and the dialog
// says so. Names and paths come from the machine and are drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { trapTab } from "./focus.js";
import { Confirm } from "./Confirm.js";
import * as P from "../lib/projects.js";

const NOTHING_ON_DISK = "Nothing on disk is touched.";

/** Projects is the dialog. open is the open projects (openOf), recents and
 *  browse the store's answers (or null before they come), root the project the
 *  desk has active, and send the store's send. */
export function Projects({ open, recents: recentsMsg, browse: browseMsg, root, send, onClose }) {
  const rootRef = useRef(null);
  const first = useRef(null);
  const recents = P.recentsOf(recentsMsg);
  const browse = P.browseOf(browseMsg);
  // Which name is being edited ({ root, open, draft }), which close is being
  // asked about, which groups are expanded, and the group being made.
  const [renaming, setRenaming] = useState(null);
  const [closing, setClosing] = useState("");
  const [expanded, setExpanded] = useState(() => new Set());
  const [grouping, setGrouping] = useState(null);
  const [adding, setAdding] = useState(null);
  const [allRecent, setAllRecent] = useState(false);
  const [showArchived, setShowArchived] = useState(false);
  const [path, setPath] = useState(null);
  const [problem, setProblem] = useState("");

  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    send(P.recentsCommand());
    send(P.browseCommand(root));
    if (first.current && first.current.focus) first.current.focus();
    else if (el && el.focus) el.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);

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
  const go = (cmd) => {
    send(cmd);
    onClose();
  };
  const toggle = (r) => {
    const next = new Set(expanded);
    if (next.has(r)) next.delete(r);
    else next.add(r);
    setExpanded(next);
  };
  const saveName = () => {
    if (!renaming) return;
    const current = (renaming.open ? open.find((p) => p.root === renaming.root) : (recents ? [...recents.recent, ...recents.archived].find((p) => p.root === renaming.root) : null));
    if (!current || renaming.draft.trim() !== current.name) send(P.renameCommand(renaming.root, renaming.draft, renaming.open));
    setRenaming(null);
  };
  const startClose = (p) => {
    if (P.closeWords(p).length === 0) send(P.closeCommand(p.root));
    else setClosing(p.root);
  };
  const target = path !== null ? path : browse ? browse.path : "";
  const pickFolder = (typed) => {
    // What is sent is the path as it was checked: no quotes round it, no spaces at its ends.
    const folder = P.cleanPath(typed);
    if (!folder) return;
    if (!P.isAbsolute(folder)) {
      setProblem("A project has to be named by its full path");
      return;
    }
    setProblem("");
    if (adding) {
      send(P.addRepoCommand(adding.root, folder));
      setExpanded(new Set(expanded).add(adding.root));
      setAdding(null);
    } else {
      go(P.openCommand(folder));
    }
  };
  const makeGroup = () => {
    send(P.groupCommand(grouping.picked, grouping.name));
    setExpanded(new Set([...expanded, ...grouping.picked]));
    setGrouping(null);
  };
  const toggleGroupPick = (r, on) => setGrouping({ ...grouping, picked: on ? [...grouping.picked, r] : grouping.picked.filter((x) => x !== r) });

  const nameEditor = (r) =>
    html`<span class="proj-edit">
      <input type="text" class="dialog-field proj-name-input" aria-label=${"New name for " + r.name} placeholder="Leave empty to use the folder's name" value=${renaming.draft}
        ref=${(el) => el && el.focus && el.focus()}
        onInput=${(e) => setRenaming({ ...renaming, draft: e.target.value })}
        onKeyDown=${(e) => {
          if (e.key === LIST.accept) { e.preventDefault(); saveName(); }
          else if (e.key === LIST.cancel) { e.preventDefault(); e.stopPropagation(); setRenaming(null); }
        }} />
      <button type="button" class="dialog-btn proj-save" onClick=${saveName}>Save</button>
      <button type="button" class="dialog-btn" onClick=${() => setRenaming(null)}>Cancel</button>
    </span>`;

  const rename = (r, isOpen) => html`<button type="button" class="dialog-btn" aria-label=${"Rename " + r.name} onClick=${() => setRenaming({ root: r.root, open: isOpen, draft: r.name })}>Rename</button>`;
  const archive = (r) => html`<button type="button" class="dialog-btn" aria-label=${(r.archived ? "Unarchive " : "Archive ") + r.name} title=${r.archived ? "Bring this project back among your other projects." : "Keep this project out of the way. " + NOTHING_ON_DISK + " An open project stays open."}
    onClick=${() => send(P.archiveCommand(r.root, !r.archived))}>${r.archived ? "Unarchive" : "Archive"}</button>`;

  const openRow = (p, i) => {
    const multi = p.members.length > 1;
    const picking = grouping !== null;
    const editing = renaming && renaming.open && renaming.root === p.root;
    return html`
      <li key=${p.root} class=${"proj-row" + (p.active ? " is-active" : "") + (p.archived ? " is-archived" : "")} data-root=${p.root}>
        ${picking
          ? html`<label class="proj-pick"><input type="checkbox" id=${"group-pick-" + i} checked=${grouping.picked.includes(p.root)} onChange=${(e) => toggleGroupPick(p.root, e.target.checked)} />
              <span class="proj-name">${p.name}</span><span class="proj-path">${p.root}</span></label>`
          : editing
            ? nameEditor(p)
            : html`<button type="button" class="proj-go" id=${"open-" + i} ref=${i === 0 ? first : null} onClick=${() => go(P.selectCommand(p.root))}>
                <span class="proj-name">${p.name}</span>
                <span class="proj-path">${multi ? p.members.length + " directories" : p.root}</span>
                <span class="proj-badges">
                  ${p.waiting ? html`<span class="proj-badge proj-badge--waiting">${p.waiting} waiting</span>` : null}
                  ${p.working ? html`<span class="proj-badge proj-badge--working">${p.working} working</span>` : null}
                  <span class="proj-badge">${p.tabs} ${p.tabs === 1 ? "tab" : "tabs"}</span>
                </span>
              </button>`}
        ${picking || editing
          ? null
          : html`<span class="proj-actions">
              ${rename(p, true)}
              ${archive(p)}
              <button type="button" class="dialog-btn" aria-label=${"Add a directory to " + p.name} title="Add another directory to this project. Each agent is told about the other directories and the agents working in them."
                onClick=${() => { setAdding({ root: p.root, name: p.name }); send(P.browseCommand(root)); setPath(null); }}>Add directory…</button>
              ${multi ? html`<button type="button" class="dialog-btn" aria-expanded=${expanded.has(p.root) ? "true" : "false"} aria-label=${"Directories of " + p.name} onClick=${() => toggle(p.root)}>${expanded.has(p.root) ? "Hide directories" : "Directories"}</button>` : null}
              ${open.length > 1 ? html`<button type="button" class="dialog-btn dialog-btn--danger" aria-label=${"Close " + p.name} title="Close this project. The agents running in it stop." onClick=${() => startClose(p)}>Close</button>` : null}
            </span>`}
        ${closing === p.root
          ? html`<${Confirm} title=${"Close " + p.name + "?"} words=${P.closeWords(p)} label="Close project" onConfirm=${() => { setClosing(""); send(P.closeCommand(p.root)); }} onCancel=${() => setClosing("")} />`
          : null}
        ${multi && expanded.has(p.root)
          ? html`<ul class="proj-members" aria-label=${"Directories of " + p.name}>
              ${p.members.map((m) => html`<li class="proj-member" key=${m.root}>
                <button type="button" class="proj-member-go" onClick=${() => go(P.selectRepoCommand(m.root))}>${m.name}</button>
                <span class="proj-path">${m.root}</span>
                <button type="button" class="dialog-btn" aria-label=${"Remove " + m.name + " out of " + p.name} title=${"It stays open on its own. " + NOTHING_ON_DISK} onClick=${() => send(P.removeRepoCommand(m.root))}>Remove</button>
              </li>`)}
            </ul>`
          : null}
      </li>`;
  };

  const listRow = (r, list, i, prefix) => {
    const editing = renaming && !renaming.open && renaming.root === r.root;
    const move = (d) => {
      const c = P.moveCommand(list, r.root, d);
      if (c) send(c);
    };
    return html`
      <li key=${r.root} class=${"proj-row" + (r.exists ? "" : " is-missing")} data-root=${r.root}>
        ${editing
          ? nameEditor(r)
          : r.exists
            ? html`<button type="button" class="proj-go" id=${prefix + "-" + i} onClick=${() => go(P.openCommand(r.root))}>
                <span class="proj-name">${r.name}</span><span class="proj-path">${r.root}</span></button>`
            : html`<span class="proj-go proj-go--off"><span class="proj-name">${r.name}</span><span class="proj-path">${r.root} (missing)</span></span>`}
        ${editing
          ? null
          : html`<span class="proj-actions">
              ${rename(r, false)}
              <button type="button" class="dialog-btn" aria-label=${"Move " + r.name + " up"} disabled=${i <= 0} onClick=${() => move(-1)}>Up</button>
              <button type="button" class="dialog-btn" aria-label=${"Move " + r.name + " down"} disabled=${i >= list.length - 1} onClick=${() => move(1)}>Down</button>
              ${archive(r)}
              <button type="button" class="dialog-btn" aria-label=${"Remove " + r.name + " from the list"} title=${"Remove this project from the list. " + NOTHING_ON_DISK} onClick=${() => send(P.removeCommand(r.root))}>Remove</button>
            </span>`}
      </li>`;
  };

  const recentShown = recents ? (allRecent ? recents.recent : recents.recent.slice(0, P.SHOWN_RECENT)) : [];
  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog dialog--wide" role="dialog" aria-modal="true" aria-labelledby="projects-title" tabindex="-1" ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id="projects-title" class="dialog-title">Projects</h2>

        <section class="proj-section" aria-labelledby="proj-open-h">
          <h3 id="proj-open-h" class="proj-h">Open</h3>
          ${open.length > 1
            ? grouping
              ? html`<div class="proj-bar">
                  <input type="text" class="dialog-field" aria-label="Name for the grouped project" placeholder="Name this project" value=${grouping.name}
                    onInput=${(e) => setGrouping({ ...grouping, name: e.target.value })} />
                  <button type="button" id="group-make" class="dialog-btn dialog-btn--primary" disabled=${grouping.picked.length < 2} onClick=${makeGroup}>
                    ${grouping.picked.length ? "Group " + grouping.picked.length + " selected" : "Group…"}</button>
                  <button type="button" class="dialog-btn" onClick=${() => setGrouping(null)}>Cancel</button>
                </div>`
              : html`<div class="proj-bar"><button type="button" id="group-start" class="dialog-btn" title="Pick two or more of the projects below to treat as one. Each agent is told about the other directories and the agents working in them."
                  onClick=${() => setGrouping({ picked: [], name: "" })}>Group open projects…</button></div>`
            : null}
          ${open.length === 0 ? html`<p class="dialog-note">No project is open.</p>` : null}
          <ul class="proj-list">${open.map((p, i) => openRow(p, i))}</ul>
        </section>

        ${recents && recents.recent.length
          ? html`<section class="proj-section" aria-labelledby="proj-recent-h">
              <h3 id="proj-recent-h" class="proj-h">Recent</h3>
              <ul class="proj-list">${recentShown.map((r, i) => listRow(r, recents.recent, i, "recent"))}</ul>
              ${recentShown.length < recents.recent.length
                ? html`<button type="button" id="recent-more" class="dialog-btn" onClick=${() => setAllRecent(true)}>Show ${recents.recent.length - recentShown.length} more</button>`
                : null}
            </section>`
          : null}

        ${recents && recents.archived.length
          ? html`<section class="proj-section" aria-labelledby="proj-archived-h">
              <h3 id="proj-archived-h" class="proj-h">Archived</h3>
              ${showArchived
                ? html`<ul class="proj-list">${recents.archived.map((r, i) => listRow(r, recents.archived, i, "archived"))}</ul>`
                : html`<button type="button" id="archived-show" class="dialog-btn" onClick=${() => setShowArchived(true)}>Show ${recents.archived.length} archived ${recents.archived.length === 1 ? "project" : "projects"}</button>`}
            </section>`
          : null}

        <section class="proj-section" aria-labelledby="proj-browse-h">
          <h3 id="proj-browse-h" class="proj-h">${adding ? "Add a directory to " + adding.name : "Open a folder"}</h3>
          ${adding ? html`<button type="button" class="dialog-btn" onClick=${() => setAdding(null)}>Cancel</button>` : null}
          <div class="proj-bar">
            <button type="button" class="dialog-btn" disabled=${!browse || !browse.parent} onClick=${() => { setPath(null); send(P.browseCommand(browse.parent)); }}>Up</button>
            <input id="browse-path" type="text" class="dialog-field" aria-label="Folder" placeholder="Type or paste a path, then press Enter" value=${target}
              onInput=${(e) => setPath(e.target.value)}
              onKeyDown=${(e) => { if (e.key === LIST.accept) { e.preventDefault(); setPath(null); send(P.browseCommand(P.cleanPath(e.target.value))); } }} />
            <button type="button" id="browse-open" class="dialog-btn dialog-btn--primary" disabled=${!target} onClick=${() => pickFolder(target)}>${adding ? "Add this folder" : "Open this folder"}</button>
          </div>
          ${problem ? html`<p class="dialog-note dialog-note--error" role="alert">${problem}</p>` : null}
          ${browse && browse.places.length
            ? html`<div class="proj-places" role="group" aria-label="Places">
                ${browse.places.map((pl) => html`<button type="button" class="dialog-btn" key=${pl.path} onClick=${() => { setPath(null); send(P.browseCommand(pl.path)); }}>${pl.name}</button>`)}
              </div>`
            : null}
          ${browse && browse.error
            ? html`<p class="dialog-note dialog-note--error" role="status">${browse.error}</p>`
            : !browse
              ? html`<p class="dialog-note" role="status">Loading…</p>`
              : browse.entries.length === 0
                ? html`<p class="dialog-note">No sub-folders here.</p>`
                : html`<ul class="proj-dirs" aria-label="Folders">
                    ${browse.entries.map((d) => html`<li class="proj-dir" key=${d.path}>
                      <button type="button" class="proj-dir-into" onClick=${() => { setPath(null); send(P.browseCommand(d.path)); }}>
                        <span class="proj-name">${d.name}</span>${d.isRepo ? html`<span class="proj-badge">git</span>` : null}
                      </button>
                      <button type="button" class="dialog-btn" aria-label=${(adding ? "Add " : "Open ") + d.name} onClick=${() => pickFolder(d.path)}>${adding ? "Add" : "Open"}</button>
                    </li>`)}
                  </ul>`}
        </section>

        <div class="dialog-foot"><button type="button" class="dialog-btn" onClick=${onClose}>Close</button></div>
      </div>
    </div>
  `;
}
