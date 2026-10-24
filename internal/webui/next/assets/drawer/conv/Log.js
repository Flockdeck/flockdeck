// Log is the scrolling list of a conversation, the same for a terminal agent's
// conversation and a structured pane's.
//
// - Rows are drawn from the entry drawn first (START_ROWS from the end at the
//   start). Older steps up PAGE_ROWS at a time from what is held, and asks the
//   desktop for the page before only when all that is held is drawn. At most
//   MAX_ROWS rows are drawn, so a long conversation keeps a bounded number of
//   elements.
// - Scrolling to the top asks for earlier rows too, so reading back needs no
//   button. Rows added above what is being read do not move it.
// - The list follows new entries only while it is at the bottom. Away from the
//   bottom, new entries are counted on a chip, "N new below", that goes to the
//   bottom.
// - j and k move between rows, and Enter on a tool row opens it.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { Row } from "./rows.js";
import { pendingAfter } from "./Tool.js";
import {
  startIndex, earlierStart, atBottom, anchored, moveTo, newBelow, MAX_ROWS, NEAR_TOP,
} from "./scroll.js";

export const OLDER_LIMIT_WORDS = "Earlier entries are not drawn: the list shows at most " + MAX_ROWS.toLocaleString("en-US") + " rows.";

/** rowsOf are the row elements under a list element, in order. */
function rowsOf(el) {
  return el && el.querySelectorAll ? Array.from(el.querySelectorAll("[data-entry]")) : [];
}

/** Log draws entries. details is the store's answers for opened rows and
 *  onDetail asks for one (by entry id). pending is { node, card }: a card drawn
 *  after the running tool it was asked for (pendingAfter), or at the end, such as
 *  a permission waiting where it was asked. atStart says there is nothing before the first one;
 *  older is "loading" while the page before is asked for; onOlder asks for it.
 *  A log with atStart set has no Older button once all it holds is drawn. */
export function Log({ entries, atStart = true, older = "idle", onOlder = null, details = null, onDetail = null, pending = null, label = "Conversation", className = "", children = null }) {
  const listRef = useRef(null);
  const following = useRef(true);
  const top = useRef("");
  const prevFirst = useRef("");
  const was = useRef({ height: 0, top: "", last: "" });
  const [, redraw] = useState(0);
  const [opened, setOpened] = useState({});
  const [unseen, setUnseen] = useState(0);

  const ids = entries.map((e) => e.id);
  const first = ids.length ? ids[0] : "";
  let start = startIndex(ids, top.current, following.current);
  // Entries came in above all that was drawn: they are what the reader asked for.
  if (prevFirst.current && first !== prevFirst.current && top.current === prevFirst.current && ids.indexOf(prevFirst.current) > 0) {
    start = Math.max(0, ids.length - MAX_ROWS);
  }
  prevFirst.current = first;
  top.current = ids.length ? ids[start] : "";
  const shown = entries.slice(start);
  const lastId = ids.length ? ids[ids.length - 1] : "";

  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    const w = was.current;
    if (following.current) {
      el.scrollTop = el.scrollHeight;
      if (unseen) setUnseen(0);
    } else {
      if (w.top && top.current !== w.top) el.scrollTop = anchored(el.scrollTop, w.height, el.scrollHeight);
      if (w.last && lastId !== w.last) setUnseen((n) => n + newBelow(ids, w.last));
    }
    was.current = { height: el.scrollHeight, top: top.current, last: lastId };
  });

  const canEarlier = start > 0 || !atStart;
  const atLimit = start === 0 && shown.length >= MAX_ROWS && !atStart;
  const earlier = () => {
    if (start > 0) {
      top.current = ids[earlierStart(start, ids.length)];
      redraw((n) => n + 1);
    } else if (!atStart && onOlder && older !== "loading" && shown.length < MAX_ROWS) {
      onOlder();
    }
  };

  const onScroll = () => {
    const el = listRef.current;
    if (!el) return;
    following.current = atBottom(el.scrollHeight, el.scrollTop, el.clientHeight);
    if (following.current && unseen) setUnseen(0);
    if (el.scrollTop < NEAR_TOP && canEarlier && !atLimit && older !== "loading") earlier();
  };

  // The card goes after its entry when that is drawn, and at the end otherwise.
  const after = pending ? pendingAfter(entries, pending.card) : "";
  const pendingAt = pending ? (after && shown.some((e) => e.id === after) ? after : "") : null;
  const toggle = (id) => {
    const e = entries.find((x) => x.id === id);
    // Opening a row whose full body was not sent asks for it.
    if (!opened[id] && e && e.hasDetail && onDetail && (e.kind === "tool" || e.kind === "subagent" || e.kind === "image")) onDetail(id);
    setOpened((o) => ({ ...o, [id]: !o[id] }));
  };
  const toBottom = () => {
    const el = listRef.current;
    following.current = true;
    if (el) el.scrollTop = el.scrollHeight;
    setUnseen(0);
  };

  const onKeyDown = (e) => {
    if (e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return;
    const el = listRef.current;
    const target = e.target;
    // Only from the list itself or one of its rows, not from a button or a link in one.
    const onRow = target && target.getAttribute && target.getAttribute("data-entry") !== null;
    if (target !== el && !onRow) return;
    if (e.key === "j" || e.key === "k") {
      const rows = rowsOf(el);
      const at = onRow ? rows.indexOf(target) : -1;
      const next = moveTo(rows.length, at, e.key === "j" ? 1 : -1);
      if (next >= 0 && rows[next].focus) {
        e.preventDefault();
        rows[next].focus();
      }
    } else if (e.key === "Enter" && onRow) {
      const kind = target.getAttribute("data-kind");
      if (kind === "tool" || kind === "subagent") {
        e.preventDefault();
        toggle(target.getAttribute("data-entry"));
      }
    }
  };

  return html`
    <div class=${"conv" + (className ? " " + className : "")} ref=${listRef} onScroll=${onScroll} onKeyDown=${onKeyDown} tabindex="0" role="log" aria-label=${label}>
      ${atLimit
        ? html`<p class="conv-limit">${OLDER_LIMIT_WORDS}</p>`
        : canEarlier
          ? html`<button type="button" class="conv-older" disabled=${older === "loading"} onClick=${earlier}>${older === "loading" ? "Loading…" : "Older"}</button>`
          : null}
      ${shown.length === 0
        ? html`${children}${pending ? html`<ol class="conv-list"><li class="conv-row conv-pending" data-pending="">${pending.node}</li></ol>` : null}`
        : html`<ol class="conv-list">${shown.flatMap((e) => [
            html`<${Row} key=${e.id} entry=${e} open=${!!opened[e.id]} onToggle=${toggle} detail=${details ? details[e.id] : null} />`,
            pending && pendingAt === e.id ? html`<li key=${"pending:" + e.id} class="conv-row conv-pending" data-pending="">${pending.node}</li>` : null,
          ])}${pending && pendingAt === "" ? html`<li key="pending" class="conv-row conv-pending" data-pending="">${pending.node}</li>` : null}</ol>`}
      ${unseen > 0 ? html`<button type="button" class="conv-new" onClick=${toBottom}>${unseen === 1 ? "1 new below" : unseen + " new below"}</button>` : null}
    </div>
  `;
}
