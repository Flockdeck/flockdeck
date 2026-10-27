// Resume: the stored conversations of the project on screen, to pick one up
// again in a tab of its own. The desktop reads them (it takes as long as the
// project is old), the dialog narrows the list to the words typed, and choosing a
// row sends resumeConversation.
//
// A conversation that already has a pane is listed and cannot be chosen. A
// structured agent keeps no conversation to resume, and the dialog says so.
//
// The dialog shows only an answer that came after it asked, so the listing of
// another project, read before, is never drawn under this project's name. Every
// string in a row came from an agent or a person and is drawn as text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Dialog } from "./Dialog.js";
import { STRUCTURED_RESUME_WORDS, conversationsCommand, filterConversations, listingOf, resumeCommand, statsOf } from "../lib/history.js";

/** History is the dialog. history is the store's latest conversations answer (or
 *  null), root the project to read, send the store's send, onClose closes it. */
export function History({ history, root, send, onClose, nav = null }) {
  const [floor, setFloor] = useState(history ? history.seq : 0);
  const [query, setQuery] = useState("");
  const [failed, setFailed] = useState(false);
  const listRef = useRef(null);
  const fieldRef = useRef(null);
  const ask = () => {
    setFloor(history ? history.seq : floor);
    setFailed(!send(conversationsCommand(root)));
  };
  useEffect(() => {
    setFailed(!send(conversationsCommand(root)));
  }, []);
  const listing = history && history.seq > floor ? listingOf(history) : null;
  const rows = listing ? filterConversations(listing.items, query) : [];
  // The field is not there until there is a list. When it arrives the keyboard
  // moves to it from where the dialog first put it (a button, or nowhere), but
  // not from a row the person has already moved to.
  const hasList = !!listing && listing.items.length > 0;
  useEffect(() => {
    const f = fieldRef.current;
    if (!hasList || !f) return;
    const at = f.ownerDocument.activeElement;
    const cls = (at && at.getAttribute && at.getAttribute("class")) || "";
    if (!at || at === f.ownerDocument.body || /(^|\s)dialog-btn(\s|$)/.test(cls)) f.focus();
  }, [hasList]);

  const buttons = () => (listRef.current && listRef.current.querySelectorAll ? [...listRef.current.querySelectorAll("button")].filter((b) => !b.disabled) : []);
  const onFieldKey = (e) => {
    if (composing(e)) return;
    if (e.key === LIST.cancel && query) {
      // A field with words in it is emptied first, as search fields are.
      e.preventDefault();
      e.stopPropagation();
      setQuery("");
    } else if (e.key === LIST.down) {
      const first = buttons()[0];
      if (first) {
        e.preventDefault();
        first.focus();
      }
    }
  };
  const onRowKey = (e, i) => {
    if (composing(e)) return;
    const all = buttons();
    if (e.key === LIST.down && all[i + 1]) {
      e.preventDefault();
      all[i + 1].focus();
    } else if (e.key === LIST.up) {
      e.preventDefault();
      (all[i - 1] || fieldRef.current || { focus() {} }).focus();
    }
  };
  const resume = (c) => {
    const cmd = resumeCommand(listing, c);
    if (!cmd) return;
    send(cmd);
    onClose();
  };
  // The right-click menu of a row runs these, through the page's nav block.
  useEffect(() => {
    if (!nav) return undefined;
    nav.history = { find: (id) => (listing ? listing.items.find((c) => c.id === id) : null), resume };
    return () => { nav.history = null; };
  });

  let enabled = -1;
  return html`
    <${Dialog} id="history" title="Resume a conversation" onClose=${onClose} focus="#history-filter" wide=${true}>
      <p class="dialog-note" id="history-about">${STRUCTURED_RESUME_WORDS}</p>
      ${failed ? html`<p class="dialog-note dialog-note--error" role="alert">The request could not be sent. Check the connection and press Refresh.</p>` : null}
      ${!listing && !failed ? html`<p class="dialog-note" role="status">Reading conversations…</p>` : null}
      ${listing && listing.error ? html`<p class="dialog-note dialog-note--error" role="alert">${listing.error}</p>` : null}
      ${listing && !listing.error && listing.items.length === 0 ? html`<p class="dialog-note" role="status">No stored conversations for this project yet.</p>` : null}
      ${listing && listing.items.length > 0
        ? html`<input id="history-filter" class="dialog-field" type="text" autocomplete="off" spellcheck="false" ref=${fieldRef}
            placeholder="Type to narrow the list…" aria-label="Find a conversation" aria-describedby="history-about"
            value=${query} onInput=${(e) => setQuery(e.target.value)} onKeyDown=${onFieldKey} />
          <ul class="hist-list" ref=${listRef} aria-label="Stored conversations">
            ${rows.map((c) => {
              if (!c.open) enabled++;
              const at = enabled;
              return html`<li key=${c.id} data-ctx="history" data-conv=${c.id}>
                <button type="button" class=${"hist-row" + (c.open ? " is-open" : "")} data-conversation=${c.id} disabled=${c.open}
                  onClick=${() => resume(c)} onKeyDown=${(e) => onRowKey(e, at)}>
                  <span class="hist-summary">${c.summary}</span>
                  <span class="hist-meta">${statsOf(c).join(" · ")}${c.agent ? " · " + c.agent : ""}</span>
                </button>
              </li>`;
            })}
          </ul>
          ${rows.length === 0 ? html`<p class="dialog-note" role="status" id="history-none">No conversation matches that.</p>` : null}`
        : null}
      <div class="dialog-foot">
        <button type="button" class="dialog-btn" onClick=${ask}>Refresh</button>
        <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
        ${listing && listing.cwd ? html`<span class="dialog-hint">${listing.cwd}</span>` : null}
      </div>
    <//>
  `;
}
