// The drawer's conversation tab: what the agent and the person said, for any
// pane kind, drawn by one set of components (conv/): rows for each kind of
// entry (conv/rows.js), reply text as sanitised Markdown (conv/markdown.js), and
// a scrolling list that pages upward, follows the bottom only while the reader
// is there and says how many entries came in below (conv/Log.js).
//
// This file is the tab's states: loading, refused, and the list. The data path
// is the store's conversation (conversationOpen, Older, appends).
//
// Everything on a row is what an agent, a tool or a person wrote. It is drawn as
// text or as sanitised Markdown, never as markup.
import { html } from "../html.js";
import { Log } from "./conv/Log.js";

export { Row, Icon, toolKind, MAX_TEXT, MAX_REPLY } from "./conv/rows.js";
export const NO_ENTRIES_WORDS = "Nothing has been said yet.";
export const LOADING_WORDS = "Loading the conversation.";

/** Conversation is the tab's panel. conv is the store's conversation, onOlder asks
 *  for the page before. */
export function Conversation({ conv, onOlder }) {
  if (!conv) return html`<p class="drawer-stub">${LOADING_WORDS}</p>`;
  if (conv.refused) return html`<p class="drawer-stub conv-refused" role="status">${conv.refused}</p>`;
  if (conv.supported !== true) return html`<p class="drawer-stub">${LOADING_WORDS}</p>`;
  return html`<${Log} entries=${conv.entries} atStart=${conv.atStart} older=${conv.older} onOlder=${onOlder}>
    <p class="conv-empty">${NO_ENTRIES_WORDS}</p>
  <//>`;
}
