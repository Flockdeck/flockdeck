// The activity feed beside the team home: what just happened, newest first, each
// entry one line with how long ago. The entries are made in the page from the
// graph pushes (FeedDiff.js) and kept in memory, so a reload starts it empty.
// Nothing here is sent anywhere.
//
// An entry is a button: it puts the keyboard on the row of its pane, and it does
// not open the drawer. The feed is not drawn while the drawer is open (the caller
// leaves it out), and it comes back when the drawer closes.
import { html } from "../html.js";
import { useEffect, useState } from "../vendor/hooks.mjs";
import { agoOf } from "./FeedDiff.js";

/** TICK_MS is how often the times are drawn again. */
export const TICK_MS = 30000;

/** EMPTY_WORDS is what the feed says before anything has happened. */
export const EMPTY_WORDS = "Nothing has happened yet. Events appear here as the team works.";

// The mark of each kind, in the shapes the rows use for the same states: a
// diamond for something that waits on the person, a cross for an end, a check
// for finished work and a ring for the rest.
function Mark({ kind }) {
  const cls = "feed-mark feed-mark--" + kind;
  switch (kind) {
    case "need":
    case "warn":
      return html`<svg class=${cls} viewBox="0 0 14 14" aria-hidden="true"><polygon points="7,1.5 12.5,7 7,12.5 1.5,7" /></svg>`;
    case "down":
      return html`<svg class=${cls} viewBox="0 0 14 14" aria-hidden="true"><path d="M3 3 L11 11 M11 3 L3 11" /></svg>`;
    case "ship":
      return html`<svg class=${cls} viewBox="0 0 14 14" aria-hidden="true"><path d="M2.5 7.5 L5.8 10.5 L11.5 3.5" /></svg>`;
    default:
      return html`<svg class=${cls} viewBox="0 0 14 14" aria-hidden="true"><circle cx="7" cy="7" r="4.5" /></svg>`;
  }
}

/** KIND_WORDS is what a screen reader hears before an entry. */
const KIND_WORDS = { need: "Needs you", warn: "Warning", down: "Ended", ship: "Finished", info: "" };

/**
 * Feed draws the entries (feed.entries, newest first). onFocus is called with
 * the pane of the entry that was chosen. now is the clock, for a test; by
 * default it is read each time the feed is drawn and drawn again every
 * TICK_MS.
 */
export function Feed({ feed, onFocus = null, now = null }) {
  const [, tick] = useState(0);
  useEffect(() => {
    if (now !== null) return undefined;
    const id = setInterval(() => tick((n) => n + 1), TICK_MS);
    return () => clearInterval(id);
  }, [now]);
  const clock = now !== null ? now : Date.now();
  const entries = feed && feed.entries ? feed.entries : [];
  return html`<aside class="feed" aria-label="Activity">
    <h2 class="feed-head">Activity</h2>
    ${entries.length === 0
      ? html`<p class="feed-empty">${EMPTY_WORDS}</p>`
      : html`<ul class="feed-list">
          ${entries.map((e) => html`<li key=${e.id} class=${"feed-entry feed-entry--" + e.kind}>
            <button type="button" class="feed-btn" data-feed-pane=${e.pane} onClick=${() => onFocus && e.pane && onFocus(e.pane)}>
              <span class="feed-time">${agoOf(e.at, clock)}</span>
              <${Mark} kind=${e.kind} />
              <span class="feed-text">${KIND_WORDS[e.kind] ? html`<span class="sr-only">${KIND_WORDS[e.kind]}: </span>` : null}${e.text}</span>
            </button>
          </li>`)}
        </ul>`}
  </aside>`;
}
