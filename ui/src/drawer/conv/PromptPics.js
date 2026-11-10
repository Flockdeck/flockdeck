// The pictures a prompt carried, drawn under its words as thumbnails.
//
// The entry holds only metadata for each (type, bytes, size). The bytes are asked for
// with conversationDetail under the key "<entry id>#<n>", and the desktop reads them from
// the pane's uploads folder. A picture swept after a week or gone says so, in the words
// ImageBody has for it. Only rows near the viewport ask, and at most MAX_INFLIGHT pictures are
// being asked for at once across the window, so the store's budget is not outrun. A click enlarges one, as for any image row. With no way to ask (a
// window through the relay, whose store refuses), the prompt says how many pictures it had.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { ImageBody } from "./Image.js";

/** MAX_PICS is how many thumbnails one prompt draws: the most a prompt sends. */
export const MAX_PICS = 4;
/** MAX_ASKS is how many times one picture is asked for again after the store lost it or failed it. */
export const MAX_ASKS = 3;
/** MAX_INFLIGHT is how many pictures are being asked for at once, over every row. */
export const MAX_INFLIGHT = 4;
/** MAX_KEPT_CHARS bounds the base64 one row keeps of answers the store may drop. */
export const MAX_KEPT_CHARS = 2 << 20;
/** NEAR is how far outside the viewport a row still counts as near. */
const NEAR = "300px";

export const picsWords = (n) => (n === 1 ? "1 picture" : n + " pictures");
/** picKey is the detail key of picture n of the prompt entry id. */
export const picKey = (id, n) => id + "#" + n;

// The pictures being asked for, and the rows waiting for a place. A place is freed when the key answers, is lost or its row goes.
const inflight = new Set();
const waiters = new Set();
const release = (k) => {
  if (inflight.delete(k)) for (const w of [...waiters]) w();
};

const answered = (a) => !!a && (a.state === "ok" || a.state === "gone");
const size = (a) => (a && a.detail && typeof a.detail.data === "string" ? a.detail.data.length : 0);

export function PromptPics({ entry: e, details = null, onDetail = null }) {
  const images = Array.isArray(e.images) ? e.images.filter((x) => x && typeof x === "object").slice(0, MAX_PICS) : [];
  const [kept, setKept] = useState({});
  const [refused, setRefused] = useState(false);
  const [near, setNear] = useState(typeof IntersectionObserver === "undefined");
  const [tick, setTick] = useState(0);
  const box = useRef(null);
  const asks = useRef({});
  const sent = useRef({});
  const seen = useRef({});
  const n = images.length;
  const keys = images.map((_, i) => picKey(e.id, i));
  const answers = keys.map((k) => (details && details[k]) || null);
  const states = answers.map((a) => (a ? a.state + ":" + a.n : "")).join(",");
  // Without an observer every row counts as near; with one, a row asks once it is within NEAR of the viewport.
  useEffect(() => {
    if (near || !box.current || typeof IntersectionObserver === "undefined") return undefined;
    const io = new IntersectionObserver((list) => { if (list.some((x) => x.isIntersecting)) setNear(true); }, { rootMargin: NEAR });
    io.observe(box.current);
    return () => io.disconnect();
  }, [near, n > 0 && !!onDetail]);
  // An answer is kept here, so the store dropping it to make room does not blank a picture drawn. Bounded per row.
  useEffect(() => {
    let next = null;
    let used = keys.reduce((t, k) => t + size(kept[k]), 0);
    keys.forEach((k, i) => {
      const a = answers[i];
      if (answered(a) && kept[k] !== a && used + size(a) - size(kept[k]) <= MAX_KEPT_CHARS) {
        used += size(a) - size(kept[k]);
        (next = next || { ...kept })[k] = a;
      }
    });
    if (next) setKept(next);
  }, [states]);
  useEffect(() => {
    const wake = () => setTick((t) => t + 1);
    waiters.add(wake);
    return () => {
      waiters.delete(wake);
      keys.forEach(release);
    };
  }, []);
  useEffect(() => {
    if (!onDetail || !near) return;
    keys.forEach((k, i) => {
      const a = answers[i];
      if (a && a.state === "loading") seen.current[k] = true;
      // Asked for and now absent means the store dropped it; an answer that is not "loading" has landed. Either frees the place.
      const dropped = !a && sent.current[k] !== undefined && (seen.current[k] || sent.current[k] !== states);
      if (inflight.has(k) && sent.current[k] !== undefined && (dropped || (a && a.state !== "loading"))) release(k);
      if (answered(a) || kept[k] || (a && a.state === "loading")) return;
      if (!a && sent.current[k] !== undefined && !dropped) return;
      if (asks.current[k] >= MAX_ASKS) return;
      if (inflight.size >= MAX_INFLIGHT && !inflight.has(k)) return;
      inflight.add(k);
      asks.current[k] = (asks.current[k] || 0) + 1;
      sent.current[k] = states;
      seen.current[k] = false;
      if (onDetail(k) === false) {
        release(k);
        setRefused(true);
      }
    });
  }, [e.id, states, near, tick]);
  if (n === 0) return null;
  if (!onDetail || refused) return html`<p class="conv-pics">${picsWords(n)}</p>`;
  return html`<div ref=${box} class="conv-pics conv-thumbs" role="group" aria-label=${picsWords(n)}>
    ${images.map((im, i) => {
      const k = keys[i];
      let a = kept[k] || answers[i] || { state: "loading" };
      // Asked for as often as allowed and still nothing: the store has lost it, and the row says so instead of loading for ever.
      if (!kept[k] && !answers[i] && asks.current[k] >= MAX_ASKS) a = { state: "gone", n: 0 };
      return html`<div key=${k} class="conv-thumb"><${ImageBody} entry=${im} detail=${a} /></div>`;
    })}
  </div>`;
}
