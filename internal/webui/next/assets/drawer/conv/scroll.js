// The arithmetic of the conversation list, apart from the component, so it can
// be tested on numbers: how many rows are drawn, which row a j or k moves to,
// whether the list is at the bottom, and how far to move the scroll position
// when rows are added above what is being read.

/** START_ROWS is how many of the newest entries are drawn when a conversation
 *  opens; PAGE_ROWS is how many more one step up reveals; MAX_ROWS is the most
 *  that are drawn at once, so a long conversation keeps a bounded number of
 *  elements. */
export const START_ROWS = 200;
export const PAGE_ROWS = 50;
export const MAX_ROWS = 1000;
/** NEAR is how close to an edge, in pixels, counts as at it. */
export const NEAR_BOTTOM = 40;
export const NEAR_TOP = 80;

/** startIndex is the index of the first entry to draw. topId is the id of the
 *  first entry drawn the last time, or "" for none. It stays on that entry when
 *  entries are added below, so the list does not slide under the reader; it is
 *  moved only when the entry is gone (a new conversation, or the store dropped
 *  its oldest), and, when the list is following the bottom, to keep at most
 *  MAX_ROWS drawn. */
export function startIndex(ids, topId, following) {
  let start = topId ? ids.indexOf(topId) : -1;
  if (start < 0) start = Math.max(0, ids.length - START_ROWS);
  if (following && ids.length - start > MAX_ROWS) start = ids.length - MAX_ROWS;
  return start;
}

/** earlierStart is where the drawn rows begin after one step up: PAGE_ROWS
 *  earlier, and never so early that more than MAX_ROWS are drawn. */
export function earlierStart(start, total) {
  return Math.max(0, start - PAGE_ROWS, total - MAX_ROWS);
}

/** atBottom says whether a scroll position is at the bottom. */
export function atBottom(scrollHeight, scrollTop, clientHeight) {
  return scrollHeight - scrollTop - clientHeight < NEAR_BOTTOM;
}

/** anchored is the scrollTop that keeps the same rows in view after the content
 *  grew from oldHeight to newHeight above them. */
export function anchored(scrollTop, oldHeight, newHeight) {
  return scrollTop + (newHeight - oldHeight);
}

/** moveTo is the index j (dir 1) or k (dir -1) goes to from index from, or 0
 *  for the first row (from -1) and the last row for k from nothing. It does not
 *  wrap. */
export function moveTo(count, from, dir) {
  if (count <= 0) return -1;
  if (from < 0) return dir > 0 ? 0 : count - 1;
  return Math.max(0, Math.min(count - 1, from + dir));
}

/** newBelow is how many entries came after the one that was last, among ids; an
 *  id that is gone counts them all. */
export function newBelow(ids, lastId) {
  if (!lastId) return 0;
  const at = ids.lastIndexOf(lastId);
  return at < 0 ? ids.length : ids.length - 1 - at;
}
