// The updates dialog: the release waiting to be installed, a check for a new one,
// the list of releases to move to or back to, and the switch for checking by
// itself. It is modal like the start dialog: the keyboard goes to the choice that
// stops nothing, Tab stays inside, Escape closes, and the keyboard goes back
// where it was.
//
// Installing a release stops the agents (the layout comes back, a running process
// does not), so "Restart now" is never the control the keyboard starts on. Release
// notes and version names come from a published release and are drawn as text.
// The dialog is not drawn in a window reached through the relay, which has
// RELAY_WORDS instead (Live decides).
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import {
  checkCommand, dateOf, installCommand, installLabel, notesBlocks, restartCommand, updatesCommand, versionsCommand, versionsOf, webUrl,
} from "../lib/updates.js";

/** focusables are the controls Tab moves between inside the dialog. */
const focusables = (root) => (root && root.querySelectorAll ? [...root.querySelectorAll("button, input, a, .upd-notes")].filter((n) => !n.disabled) : []);

function trapTab(e, root) {
  const list = focusables(root);
  if (list.length === 0) return;
  const at = list.indexOf(root.ownerDocument.activeElement);
  const to = e.shiftKey ? (at <= 0 ? list.length - 1 : at - 1) : (at < 0 || at === list.length - 1 ? 0 : at + 1);
  e.preventDefault();
  list[to].focus();
}

const runs = (rs) => rs.map((r, i) => (r.t === "strong" ? html`<strong key=${i}>${r.text}</strong>` : r.t === "code" ? html`<code key=${i}>${r.text}</code>` : r.text));

/** Notes draws a release note from its blocks. */
export function Notes({ text }) {
  return notesBlocks(text).map((b, i) => {
    if (b.kind === "heading") return html`<${"h" + b.level} key=${i} class="upd-h">${runs(b.runs)}<//>`;
    if (b.kind === "list") return html`<ul key=${i} class="upd-list">${b.items.map((it, j) => html`<li key=${j}>${runs(it)}</li>`)}</ul>`;
    return html`<p key=${i} class="upd-p">${runs(b.runs)}</p>`;
  });
}

/** Updates is the dialog. update is { version, notes, url } or null; versions is
 *  the store's listVersions answer; updatesOff is the preference; notice is the
 *  text of a notice that came since the dialog opened, or "". */
export function Updates({ update, versions: answer, updatesOff, notice = "", send, onClose }) {
  const rootRef = useRef(null);
  const first = useRef(null);
  const [checking, setChecking] = useState(false);
  const [listing, setListing] = useState(false);
  const [installing, setInstalling] = useState("");

  useEffect(() => {
    const el = rootRef.current;
    const doc = el && el.ownerDocument;
    const before = doc && doc.activeElement;
    if (first.current && first.current.focus) first.current.focus();
    return () => {
      if (before && before !== doc.body && before.focus) before.focus();
    };
  }, []);
  // A check is over when the desktop says something, or the update arrives.
  useEffect(() => {
    setChecking(false);
    setInstalling("");
  }, [notice, update && update.version]);

  const list = listing ? versionsOf(answer) : null;
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
  const check = () => {
    if (send(checkCommand()) !== false) setChecking(true);
  };
  const openList = () => {
    setListing(true);
    send(versionsCommand());
  };
  const install = (v) => {
    if (send(installCommand(v)) !== false) setInstalling(v);
  };
  const link = update ? webUrl(update.url) : "";

  return html`
    <div class="dialog-scrim" onMouseDown=${(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="upd-title" ref=${rootRef} onKeyDown=${onKeyDown}>
        <h2 id="upd-title" class="dialog-title">${update ? "Update to " + update.version : "Updates"}</h2>
        ${update
          ? html`<p class="dialog-note">This version has been downloaded and checked against its published checksum. Installing it saves and reopens your layout, but the agents running in panes are stopped.</p>
              ${update.notes
                ? html`<div class="upd-notes" tabindex="0" role="region" aria-label="Release notes"><${Notes} text=${update.notes} /></div>`
                : null}
              ${link ? html`<a href=${link} target="_blank" rel="noreferrer noopener">Release notes on GitHub</a>` : null}
              <div class="dialog-foot">
                <button type="button" class="dialog-btn dialog-btn--danger" onClick=${() => { send(restartCommand()); onClose(); }}>Restart now</button>
                <button type="button" class="dialog-btn" ref=${first} onClick=${onClose}>Later</button>
              </div>`
          : html`<p class="dialog-note">No update is waiting.</p>`}
        <div class="dialog-foot">
          <button type="button" class="dialog-btn" ref=${update ? null : first} disabled=${checking} onClick=${check}>${checking ? "Checking…" : "Check for updates now"}</button>
          <button type="button" class="dialog-btn" disabled=${listing && !list} onClick=${openList}>Install a specific version…</button>
        </div>
        ${notice ? html`<p class="dialog-note" role="status">${notice}</p>` : null}
        <label class="dialog-check">
          <input type="checkbox" checked=${!updatesOff} onChange=${(e) => send(updatesCommand(!e.target.checked))} />
          <span>Check for updates by itself</span>
        </label>
        ${listing ? html`<${Versions} list=${list} installing=${installing} onInstall=${install} />` : null}
        ${update ? null : html`<div class="dialog-foot"><button type="button" class="dialog-btn" onClick=${onClose}>Close</button></div>`}
      </div>
    </div>
  `;
}

/** Versions is the list of recent releases to install one of. A release is
 *  downloaded and checked first; restarting onto it is a separate step. */
function Versions({ list, installing, onInstall }) {
  if (!list) return html`<p class="dialog-note" role="status">Loading…</p>`;
  if (list.error) return html`<p class="dialog-note dialog-note--error" role="alert">${list.error}</p>`;
  if (list.items.length === 0) return html`<p class="dialog-note">No recent releases could be listed.</p>`;
  return html`
    <p class="dialog-note">Downloads and checks a release against its signed checksum, as an ordinary update does. Restarting to put it in place is a separate step.</p>
    <ul class="upd-versions" aria-label="Releases">
      ${list.items.map((v) => {
        const when = dateOf(v.published);
        const first = v.notes.split("\n")[0];
        return html`<li key=${v.version} class="upd-version">
          <span class="upd-name">${v.version}${v.relation === "current" ? " (running now)" : ""}</span>
          ${when || first ? html`<span class="dialog-note">${[when, first].filter(Boolean).join(" - ")}</span>` : null}
          <button type="button" class="dialog-btn" disabled=${!!installing} onClick=${() => onInstall(v.version)}>
            ${installing === v.version ? "Downloading…" : installLabel(v.relation)}
          </button>
        </li>`;
      })}
    </ul>
  `;
}
