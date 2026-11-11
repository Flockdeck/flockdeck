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
import { useState, useEffect } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { Log } from "./conv/Log.js";
import { Toolbar } from "./conv/Toolbar.js";

export { Row, Icon, toolKind, MAX_TEXT, MAX_REPLY } from "./conv/rows.js";
export const NO_ENTRIES_WORDS = "Nothing has been said yet.";
export const LOADING_WORDS = "Loading the conversation.";
export const REFUSED_TITLE = "Could not open the conversation";

/** useFilled is false on the first draw and true from the next tick. A live region that mounts already
 *  holding its words is not announced by many screen readers: the region has to be in the page empty
 *  first and then be filled. */
function useFilled() {
  const [filled, setFilled] = useState(false);
  useEffect(() => {
    const id = setTimeout(() => setFilled(true), 0);
    return () => clearTimeout(id);
  }, []);
  return filled;
}

/** Skeleton is the loading block: grey lines where rows will be, and the words for a screen reader.
 *  The words are put in the live region on the tick after it is drawn (useFilled). */
export function Skeleton({ words = LOADING_WORDS }) {
  const filled = useFilled();
  return html`<div class="drawer-stub conv-skeleton" role="status" aria-busy="true">
    ${filled ? html`<span class="conv-sr">${words}</span>` : null}
    <span class="conv-skel conv-skel--end" aria-hidden="true"></span>
    <span class="conv-skel conv-skel--wide" aria-hidden="true"></span>
    <span class="conv-skel conv-skel--mid" aria-hidden="true"></span>
    <span class="conv-skel conv-skel--end conv-skel--short" aria-hidden="true"></span>
    <span class="conv-skel conv-skel--wide" aria-hidden="true"></span>
  </div>`;
}

/** ErrorBlock is a refusal or a failure in the Changes tab's state block, with Retry when there is something to retry.
 *  It is role alert, which a screen reader announces when it appears, filled or not. */
export function ErrorBlock({ title, words, onRetry = null }) {
  return html`<div class="drawer-stub chg-state chg-error conv-refused" role="alert">
    <strong>${title}</strong>
    <span>${words}</span>
    ${onRetry ? html`<button type="button" class="drawer-btn conv-retry-open" onClick=${onRetry}>Retry</button>` : null}
  </div>`;
}

/** EmptyState is what an empty conversation says. tips are the ways to start that the pane's box has
 *  (a list of { key, words }); a pane without a box of its own passes none. */
export function EmptyState({ tips = null }) {
  const filled = useFilled();
  return html`<div class="chg-state conv-empty" role="status">
    ${filled
      ? html`<strong>${NO_ENTRIES_WORDS}</strong>
        ${tips && tips.length
          ? html`<span>Type a message below. Besides plain text, the box takes:</span>
            <ul class="conv-tips">${tips.map((t) => html`<li key=${t.key}><kbd>${t.key}</kbd> ${t.words}</li>`)}</ul>`
          : null}`
      : null}
  </div>`;
}

/** Conversation is the tab's panel. conv is the store's conversation, onOlder asks
 *  for the page before, details and onDetail are the store's entry details and the
 *  ask for one, and pending is a card drawn where it was asked (Log.js). search,
 *  onSearch and onClearSearch are the store's search; canExport and onExport are
 *  the pane's exporter, when it has one. toolbar false leaves the toolbar out. tips are the empty
 *  state's ways to start (EmptyState), onRetry asks again after a refusal and onRetryPrompt sends
 *  the newest prompt again. */
export function Conversation({ conv, onOlder, details = null, onDetail = null, pending = null, search = null, onSearch = null, onClearSearch = null, canExport = false, onExport = null, toolbar = true, className = "", lead = null, items = [], tips = null, onRetry = null, onRetryPrompt = null }) {
  // Before the list can be drawn the lead still is, so the pane's pickers and activity do not vanish.
  const strip = lead && toolbar ? html`<${Toolbar} entries=${[]} onClear=${() => {}} onJump=${() => {}} lead=${lead} items=${items} />` : null;
  if (!conv) return html`${strip}<${Skeleton} />`;
  if (conv.refused) return html`${strip}<${ErrorBlock} title=${REFUSED_TITLE} words=${conv.refused} onRetry=${onRetry} />`;
  if (conv.supported !== true) return html`${strip}<${Skeleton} />`;
  return html`<${ConversationBody} conv=${conv} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending}
    search=${search} onSearch=${onSearch} onClearSearch=${onClearSearch} canExport=${canExport} onExport=${onExport} toolbar=${toolbar} className=${className} lead=${lead} items=${items} tips=${tips} onRetryPrompt=${onRetryPrompt} />`;
}

function ConversationBody({ conv, onOlder, details, onDetail, pending, search, onSearch, onClearSearch, canExport, onExport, toolbar, className, lead, items, tips, onRetryPrompt }) {
  const [jump, setJump] = useState(null);
  return html`
    ${toolbar && (onSearch || lead)
      ? html`<${Toolbar} entries=${conv.entries} search=${search} onSearch=${onSearch} onClear=${onClearSearch || (() => {})}
          onJump=${(id) => setJump({ id, seq: (jump ? jump.seq : 0) + 1 })} canExport=${canExport} onExport=${onExport} lead=${lead} items=${items} />`
      : null}
    <${Log} entries=${conv.entries} atStart=${conv.atStart} older=${conv.older} onOlder=${onOlder} details=${details} onDetail=${onDetail} pending=${pending} jump=${jump} className=${className} onRetry=${onRetryPrompt}>
      <${EmptyState} tips=${tips} />
    <//>`;
}
