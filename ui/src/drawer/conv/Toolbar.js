// The conversation's strip: search and a more menu (Copy, Copy last reply, Export),
// with room in front for a lead (the structured pane puts its activity, usage and
// pickers there) and extra menu items (its Agent info).
//
// - The search field is behind a search button; it opens by itself while a search
//   is showing. The match list stays under the strip.
//
// - Search sends the query to the desktop (conversationSearch) when the form is
//   submitted, not on every key, and lists the matches newest first with the
//   hit marked. A match in an entry that is loaded takes the list to it; one
//   earlier than what is loaded says so. The desktop searches prompts, replies
//   and the label and summary of tools, and says when there were more matches
//   than it sent.
// - Copy puts the loaded entries on the clipboard as plain text and says how
//   many.
// - Export is the desktop's own export of the stored transcript, for panes
//   that have one; for the others the item is left out.
//
// Everything shown from a match is what an agent or a person wrote, drawn as text.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { copyText } from "./markdown.js";
import { conversationText } from "./text.js";
import { lastReply } from "./CopyReply.js";

export const NO_MATCH_WORDS = "No matches.";
export const SEARCH_LOADING_WORDS = "Searching…";
export const SEARCH_FAILED_WORDS = "The search did not finish. Try again.";
export const NOT_LOADED_WORDS = "Earlier than what is loaded. Use Older to load more.";
export const MORE_MATCHES_WORDS = "There are more matches than are listed. Narrow the search.";
export const NOT_SENT_WORDS = "Not connected to Flockdeck, so the search was not sent.";

/** snippetParts splits a snippet at the hit: [before, hit, after], offset and
 *  length counted in characters (not UTF-16 units). */
export function snippetParts(snippet, offset, length) {
  const chars = Array.from(String(snippet || ""));
  const at = Math.min(Math.max(0, offset | 0), chars.length);
  const end = Math.min(chars.length, at + Math.max(0, length | 0));
  return [chars.slice(0, at).join(""), chars.slice(at, end).join(""), chars.slice(end).join("")];
}

/** Toolbar draws the row. entries are the loaded entries; search is the store's
 *  search; onSearch(q) asks (and returns false when it was not sent); onClear
 *  drops it; onJump(entryId) takes the list to an entry. canExport says the pane
 *  has an exporter and onExport starts it. */
export function Toolbar({ entries, search = null, onSearch, onClear, onJump, canExport = false, onExport = null, copy = copyText, lead = null, items = [] }) {
  const [q, setQ] = useState("");
  const [note, setNote] = useState("");
  const [copied, setCopied] = useState("");
  const [searchOpen, setSearchOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const field = useRef(null);
  const menuKey = useRef(false);
  const moreBtn = useRef(null);
  const menuEl = useRef(null);
  const wasOpen = useRef(false);
  const searching = searchOpen || !!search || q !== "";
  // Opening the search puts the caret in its field.
  useEffect(() => {
    if (searchOpen && field.current && field.current.focus) field.current.focus();
  }, [searchOpen]);
  // Opening the menu puts the keyboard on its first item. Closing it with Escape or by
  // choosing an item gives the keyboard back to its button, since the focused item is gone.
  // Closing it with the mouse or by tabbing away leaves the focus where the person put it.
  useEffect(() => {
    if (menuOpen) {
      const first = menuItems()[0];
      if (first && first.focus) first.focus();
    } else if (wasOpen.current && menuKey.current && moreBtn.current && moreBtn.current.focus) {
      moreBtn.current.focus();
    }
    wasOpen.current = menuOpen;
    menuKey.current = false;
  }, [menuOpen]);
  // The items that can take the keyboard, in order.
  const menuItems = () => (menuEl.current && menuEl.current.querySelectorAll ? Array.from(menuEl.current.querySelectorAll('[role="menuitem"]')).filter((b) => !b.disabled) : []);
  // closeMenu closes it; back says the keyboard goes to the More button.
  const closeMenu = (back) => {
    menuKey.current = !!back;
    setMenuOpen(false);
  };
  const held = new Set(entries.map((e) => e.id));
  const submit = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    if (q.trim() === "") {
      onClear();
      setNote("");
      return;
    }
    setNote(onSearch(q) ? "" : NOT_SENT_WORDS);
  };
  const clear = () => {
    setQ("");
    setNote("");
    setSearchOpen(false);
    onClear();
  };
  const doCopy = () => {
    closeMenu(true);
    const out = conversationText(entries);
    copy(out).then((ok) => {
      setCopied(ok ? "Copied " + entries.length + (entries.length === 1 ? " entry" : " entries") + " that are loaded." : "Couldn't copy.");
    });
  };
  const doCopyLast = () => {
    closeMenu(true);
    copy(lastReply(entries)).then((ok) => setCopied(ok ? "Copied the last reply." : "Couldn't copy."));
  };
  const needle = search ? Array.from(search.q.trim()).length : 0;
  const lastText = lastReply(entries);
  const exportOn = canExport && onExport;
  // The menu's keys, only while it is open: Escape closes it and is not passed on, so it does
  // not also stop the running turn; the arrows, Home and End move between items. With the menu
  // closed every key, Escape included, goes on to whoever wants it (ArrowDown on the button opens it).
  const onMenuKey = (e) => {
    if (!menuOpen) {
      if ((e.key === "ArrowDown" || e.key === "ArrowUp") && e.target === moreBtn.current) {
        e.preventDefault();
        setMenuOpen(true);
      }
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMenu(true);
      return;
    }
    const moves = { ArrowDown: 1, ArrowUp: -1, Home: "first", End: "last" };
    if (!(e.key in moves) || e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
    const list = menuItems();
    if (list.length === 0) return;
    e.preventDefault();
    const at = list.indexOf(e.target);
    const m = moves[e.key];
    const next = m === "first" ? 0 : m === "last" ? list.length - 1 : at < 0 ? (m > 0 ? 0 : list.length - 1) : (at + m + list.length) % list.length;
    if (list[next].focus) list[next].focus();
  };
  const onMenuBlur = (e) => {
    const next = e.relatedTarget;
    if (next && e.currentTarget && e.currentTarget.contains && e.currentTarget.contains(next)) return;
    setMenuOpen(false);
  };
  const item = (cls, label, onClick, props = {}) => html`<button type="button" role="menuitem" class=${"conv-menu-item " + cls} onClick=${onClick} ...${props}>${label}</button>`;
  return html`<div class="conv-toolbar" role="group" aria-label="Conversation">
    <div class="conv-strip">
      ${lead}
      ${onSearch ? html`<button type="button" class="conv-icon-btn conv-search-toggle" aria-label="Search this conversation" title="Search" aria-expanded=${searching ? "true" : "false"} aria-controls="conv-search-form"
        onClick=${() => { if (searching && !search && q === "") setSearchOpen(false); else if (!searching) setSearchOpen(true); else if (field.current && field.current.focus) field.current.focus(); }}>
        <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false"><circle cx="6.5" cy="6.5" r="4.5" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M10 10l4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg></button>` : null}
      <div class="conv-more" onKeyDown=${onMenuKey} onFocusOut=${onMenuBlur}>
        <button type="button" ref=${moreBtn} class="conv-icon-btn conv-more-btn" aria-label="More" title="More" aria-haspopup="menu" aria-expanded=${menuOpen ? "true" : "false"} aria-controls="conv-menu" onClick=${() => setMenuOpen(!menuOpen)}>
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false"><circle cx="3" cy="8" r="1.4" fill="currentColor"/><circle cx="8" cy="8" r="1.4" fill="currentColor"/><circle cx="13" cy="8" r="1.4" fill="currentColor"/></svg></button>
        ${menuOpen
          ? html`<div id="conv-menu" ref=${menuEl} class="conv-menu" role="menu" aria-label="More">
              ${items.map((it) => item("conv-menu-item--extra " + (it.className || ""), it.label, () => { closeMenu(true); it.onClick(); }, { disabled: !!it.disabled, title: it.title, "aria-expanded": it.expanded === undefined ? undefined : it.expanded ? "true" : "false" }))}
              ${item("conv-copy", "Copy", doCopy, { disabled: entries.length === 0 })}
              ${item("conv-copy-last", "Copy last reply", doCopyLast, { disabled: lastText === "" })}
              ${exportOn ? item("conv-export", "Export", () => { closeMenu(true); onExport(); }) : null}
            </div>`
          : null}
      </div>
    </div>
    ${onSearch && searching
      ? html`<form id="conv-search-form" class="conv-search" role="search" onSubmit=${submit}>
          <input ref=${field} type="search" class="conv-search-input" aria-label="Search this conversation" placeholder="Search" value=${q} maxlength="200" onInput=${(e) => setQ(e.target.value)} />
          <button type="submit" class="drawer-btn conv-search-go">Search</button>
          <button type="button" class="drawer-btn conv-search-clear" onClick=${clear}>${search || q ? "Clear" : "Close"}</button>
        </form>`
      : null}
    <span class="conv-toolbar-note conv-copied" role="status">${copied}</span>
    <span class="conv-toolbar-note conv-search-note" role="status">${note}</span>
    <p class="conv-results-note" role="status">${search ? (search.state === "loading" ? SEARCH_LOADING_WORDS : search.failed ? SEARCH_FAILED_WORDS : search.matches.length === 0 ? NO_MATCH_WORDS : "") : ""}</p>
    ${search && search.state !== "loading" && search.matches.length > 0
          ? html`<ul class="conv-results" aria-label="Matches">
              ${search.matches.map((m) => {
                const [before, hit, after] = snippetParts(m.snippet, m.offset, needle);
                const here = held.has(m.entryId);
                return html`<li key=${m.entryId}>
                  <button type="button" class="conv-result" data-entry-id=${m.entryId} disabled=${!here} onClick=${() => { if (here) onJump(m.entryId); }}>
                    <span class="conv-result-kind">${m.kind}</span>
                    <span class="conv-result-text">${before}<mark>${hit}</mark>${after}</span>
                    ${here ? null : html`<span class="conv-result-note">${NOT_LOADED_WORDS}</span>`}
                  </button>
                </li>`;
              })}
            </ul>
            ${search.more ? html`<p class="conv-results-note">${MORE_MATCHES_WORDS}</p>` : null}`
      : null}
  </div>`;
}
