// The conversation's search, Copy and Export.
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
//   that have one; for the others the row says it is not available.
//
// Everything shown from a match is what an agent or a person wrote, drawn as text.
import { useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { copyText } from "./markdown.js";
import { conversationText } from "./text.js";

export const NO_MATCH_WORDS = "No matches.";
export const SEARCH_LOADING_WORDS = "Searching…";
export const NOT_LOADED_WORDS = "Earlier than what is loaded. Use Older to load more.";
export const MORE_MATCHES_WORDS = "There are more matches than are listed. Narrow the search.";
export const EXPORT_UNAVAILABLE_WORDS = "Export is not available for this pane.";
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
export function Toolbar({ entries, search = null, onSearch, onClear, onJump, canExport = false, onExport = null, copy = copyText }) {
  const [q, setQ] = useState("");
  const [note, setNote] = useState("");
  const [copied, setCopied] = useState("");
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
    onClear();
  };
  const doCopy = () => {
    const out = conversationText(entries);
    copy(out).then((ok) => {
      setCopied(ok ? "Copied " + entries.length + (entries.length === 1 ? " entry" : " entries") + " that are loaded." : "Couldn't copy.");
    });
  };
  const needle = search ? Array.from(search.q.trim()).length : 0;
  return html`<div class="conv-toolbar" role="group" aria-label="Search and copy the conversation">
    <form class="conv-search" role="search" onSubmit=${submit}>
      <input type="search" class="conv-search-input" aria-label="Search this conversation" placeholder="Search" value=${q} maxlength="200" onInput=${(e) => setQ(e.target.value)} />
      <button type="submit" class="drawer-btn conv-search-go">Search</button>
      ${search || q ? html`<button type="button" class="drawer-btn conv-search-clear" onClick=${clear}>Clear</button>` : null}
    </form>
    <button type="button" class="drawer-btn conv-copy" disabled=${entries.length === 0} onClick=${doCopy}>Copy</button>
    ${canExport && onExport ? html`<button type="button" class="drawer-btn conv-export" onClick=${onExport}>Export</button>` : html`<span class="conv-toolbar-note conv-export-note">${EXPORT_UNAVAILABLE_WORDS}</span>`}
    <span class="conv-toolbar-note conv-copied" role="status">${copied}</span>
    <span class="conv-toolbar-note conv-search-note" role="status">${note}</span>
    <p class="conv-results-note" role="status">${search ? (search.state === "loading" ? SEARCH_LOADING_WORDS : search.matches.length === 0 ? NO_MATCH_WORDS : "") : ""}</p>
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
