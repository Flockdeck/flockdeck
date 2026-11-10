// The drawer's conversation tab: what the agent and the person said, for any
// pane kind, drawn by one set of components (conv/): rows for each kind of
// entry (conv/rows.js), reply text as sanitised Markdown (conv/markdown.js), and
// a scrolling list that pages upward, follows the bottom only while the reader
// is there and says how many entries came in below (conv/Log.js).
//
// This file is the tab's states: loading, refused, and the strip (search and a
// more menu with Copy and Export: conv/Toolbar.js) over the list. lead and items
// let a pane put its own header in that strip; the list fills the height left. The data path is the store's
// conversation (conversationOpen, Older, appends) and its search.
//
// Everything on a row is what an agent, a tool or a person wrote. It is drawn as
// text or as sanitised Markdown, never as markup.
import { useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Log } from "./conv/Log.js";
import { Toolbar } from "./conv/Toolbar.js";

export { Row, Icon, toolKind, MAX_TEXT, MAX_REPLY } from "./conv/rows.js";
export const NO_ENTRIES_WORDS = "Nothing has been said yet.";
export const LOADING_WORDS = "Loading the conversation.";

/** Conversation is the tab's panel. conv is the store's conversation, onOlder asks
 *  for the page before, details and onDetail are the store's entry details and the
 *  ask for one, and pending is a card drawn where it was asked (Log.js). search,
 *  onSearch and onClearSearch are the store's search; canExport and onExport are
 *  the pane's exporter, when it has one. toolbar false leaves the toolbar out. */
export function Conversation({ conv, onOlder, details = null, onDetail = null, pending = null, search = null, onSearch = null, onClearSearch = null, canExport = false, onExport = null, toolbar = true, className = "", lead = null, items = [] }) {
  // Before the list can be drawn the lead still is, so the pane's pickers and activity do not vanish.
  const strip = lead && toolbar ? html`<${Toolbar} entries=${[]} onClear=${() => {}} onJump=${() => {}} lead=${lead} items=${items} />` : null;
  if (!conv) return html`${strip}<p class="drawer-stub">${LOADING_WORDS}</p>`;
  if (conv.refused) return html`${strip}<p class="drawer-stub conv-refused" role="status">${conv.refused}</p>`;
  if (conv.supported !== true) return html`${strip}<p class="drawer-stub">${LOADING_WORDS}</p>`;
  return html`<${ConversationBody} conv=${conv} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending}
    search=${search} onSearch=${onSearch} onClearSearch=${onClearSearch} canExport=${canExport} onExport=${onExport} toolbar=${toolbar} className=${className} lead=${lead} items=${items} />`;
}

function ConversationBody({ conv, onOlder, details, onDetail, pending, search, onSearch, onClearSearch, canExport, onExport, toolbar, className, lead, items }) {
  const [jump, setJump] = useState(null);
  return html`
    ${toolbar && (onSearch || lead)
      ? html`<${Toolbar} entries=${conv.entries} search=${search} onSearch=${onSearch} onClear=${onClearSearch || (() => {})}
          onJump=${(id) => setJump({ id, seq: (jump ? jump.seq : 0) + 1 })} canExport=${canExport} onExport=${onExport} lead=${lead} items=${items} />`
      : null}
    <${Log} entries=${conv.entries} atStart=${conv.atStart} older=${conv.older} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending} jump=${jump} className=${className}>
      <p class="conv-empty">${NO_ENTRIES_WORDS}</p>
    <//>`;
}
