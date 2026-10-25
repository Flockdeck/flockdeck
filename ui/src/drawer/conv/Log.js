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
// - Runs of two or more tool rows are one "N steps" row (steps.js): closed to its
//   header before the last reply, open after it, and the reader's choice stays.
// - j and k move between rows, and Enter on a tool, image or steps row opens it.
// - jump ({ id, seq }) takes the list to an entry (a search result): the entry is
//   drawn, its group opened, and it takes the keyboard.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { Row, StepsRow } from "./rows.js";
import { groupEntries, defaultOpen } from "./steps.js";
import { pendingAfter } from "./Tool.js";
import {
  startIndex, earlierStart, atBottom, anchored, moveTo, unseenIds, MAX_ROWS, NEAR_TOP,
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
export function Log({ entries, atStart = true, older = "idle", onOlder = null, details = null, onDetail = null, pending = null, jump = null, label = "Conversation", className = "", children = null }) {
  const listRef = useRef(null);
  const following = useRef(true);
  const top = useRef("");
  const prevFirst = useRef("");
  const was = useRef({ height: 0, top: "", last: "", ids: null });
  const [, redraw] = useState(0);
  const [opened, setOpened] = useState({});
  const [unseen, setUnseen] = useState(0);
  const [groupOpen, setGroupOpen] = useState({});
  const jumped = useRef(0);
  const focusId = useRef("");

  const ids = entries.map((e) => e.id);
  const first = ids.length ? ids[0] : "";
  if (jump && jump.seq !== jumped.current && ids.includes(jump.id)) {
    jumped.current = jump.seq;
    following.current = false;
    top.current = ids[Math.max(0, ids.indexOf(jump.id) - 3)];
    focusId.current = jump.id;
  }
  let start = startIndex(ids, top.current, following.current);
  // Entries came in above all that was drawn: they are what the reader asked for.
  if (prevFirst.current && first !== prevFirst.current && top.current === prevFirst.current && ids.indexOf(prevFirst.current) > 0) {
    start = Math.max(0, ids.length - MAX_ROWS);
  }
  prevFirst.current = first;
  top.current = ids.length ? ids[start] : "";
  const shown = entries.slice(start);
  const lastId = ids.length ? ids[ids.length - 1] : "";

  // A jump to a step inside a closed group opens the group.
  useEffect(() => {
    if (!jump) return;
    const g = groupEntries(entries).find((it) => it.steps && it.steps.some((x) => x.id === jump.id));
    if (g) setGroupOpen((o) => (o[g.id] ? o : { ...o, [g.id]: true }));
  }, [jump ? jump.seq : 0]);

  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    if (focusId.current) {
      const row = rowsOf(el).find((r) => r.getAttribute("data-entry") === focusId.current);
      if (row) {
        focusId.current = "";
        if (row.scrollIntoView) row.scrollIntoView({ block: "center" });
        if (row.focus) row.focus();
        was.current = { height: el.scrollHeight, top: top.current, last: lastId, ids: new Set(ids) };
        return;
      }
    }
    const w = was.current;
    if (following.current) {
      el.scrollTop = el.scrollHeight;
      if (unseen) setUnseen(0);
    } else {
      if (w.top && top.current !== w.top) el.scrollTop = anchored(el.scrollTop, w.height, el.scrollHeight);
      const fresh = w.ids ? unseenIds(ids, w.ids, w.top) : 0;
      if (w.last && fresh) setUnseen((n) => n + fresh);
    }
    was.current = { height: el.scrollHeight, top: top.current, last: lastId, ids: new Set(ids) };
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
  const toggleGroup = (id, now) => setGroupOpen((o) => ({ ...o, [id]: !(id in o ? o[id] : now) }));
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
      const id = target.getAttribute("data-entry");
      if (kind === "tool" || kind === "subagent" || kind === "image") {
        e.preventDefault();
        toggle(id);
      } else if (kind === "steps") {
        e.preventDefault();
        const g = groupEntries(shown).find((it) => it.id === id);
        if (g) toggleGroup(id, defaultOpen(g, shown));
      }
    }
  };

  const pendingLi = (key) => html`<li key=${key} class="conv-row conv-pending" data-pending="">${pending.node}</li>`;
  const rowOf = (e) => html`<${Row} key=${e.id} entry=${e} open=${!!opened[e.id]} onToggle=${toggle} detail=${details ? details[e.id] : null} />`;
  const items = [];
  for (const it of groupEntries(shown)) {
    if (!it.steps) {
      items.push(rowOf(it.entry));
      if (pending && pendingAt === it.entry.id) items.push(pendingLi("pending:" + it.entry.id));
      continue;
    }
    const open = it.id in groupOpen ? groupOpen[it.id] : defaultOpen(it, shown);
    items.push(html`<${StepsRow} key=${it.id} id=${it.id} steps=${it.steps} open=${open} onToggle=${(id) => toggleGroup(id, defaultOpen(it, shown))} />`);
    // A card for a step in a closed group is drawn under the group's header.
    const inside = pending && it.steps.some((x) => x.id === pendingAt);
    if (open) {
      for (const e of it.steps) {
        items.push(rowOf(e));
        if (pending && pendingAt === e.id) items.push(pendingLi("pending:" + e.id));
      }
    } else if (inside) {
      items.push(pendingLi("pending:" + pendingAt));
    }
  }
  if (pending && pendingAt === "" && shown.length > 0) items.push(pendingLi("pending"));

  return html`
    <div class=${"conv" + (className ? " " + className : "")} ref=${listRef} onScroll=${onScroll} onKeyDown=${onKeyDown} tabindex="0" role="log" aria-label=${label}>
      ${atLimit
        ? html`<p class="conv-limit">${OLDER_LIMIT_WORDS}</p>`
        : canEarlier
          ? html`<button type="button" class="conv-older" disabled=${older === "loading"} onClick=${earlier}>${older === "loading" ? "Loading…" : "Older"}</button>`
          : null}
      ${shown.length === 0
        ? html`${children}${pending ? html`<ol class="conv-list"><li class="conv-row conv-pending" data-pending="">${pending.node}</li></ol>` : null}`
        : html`<ol class="conv-list">${items}</ol>`}
      ${unseen > 0 ? html`<button type="button" class="conv-new" onClick=${toBottom}>${unseen === 1 ? "1 new below" : unseen + " new below"}</button>` : null}
    </div>
  `;
}
