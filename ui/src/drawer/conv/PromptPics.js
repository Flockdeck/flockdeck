// The pictures a prompt carried, drawn under its words as thumbnails.
//
// The entry holds only metadata for each (type, bytes, size). The bytes are asked for
// with conversationDetail under the key "<entry id>#<n>", and the desktop reads them from
// the pane's uploads folder. A picture swept after a week or gone says so, in the words
// ImageBody has for it. A click enlarges one, as for any image row. With no way to ask (a
// window through the relay, whose store refuses), the prompt says how many pictures it had.
import { useEffect, useRef, useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";
import { ImageBody } from "./Image.js";

/** MAX_PICS is how many thumbnails one prompt draws: the most a prompt sends. */
export const MAX_PICS = 4;
/** MAX_ASKS is how many times one row asks again for a picture the store lost before it answered. */
const MAX_ASKS = 3;

export const picsWords = (n) => (n === 1 ? "1 picture" : n + " pictures");
/** picKey is the detail key of picture n of the prompt entry id. */
export const picKey = (id, n) => id + "#" + n;

export function PromptPics({ entry: e, details = null, onDetail = null }) {
  const images = Array.isArray(e.images) ? e.images.filter((x) => x && typeof x === "object").slice(0, MAX_PICS) : [];
  const [kept, setKept] = useState({});
  const [refused, setRefused] = useState(false);
  const asks = useRef(0);
  const n = images.length;
  const keys = images.map((_, i) => picKey(e.id, i));
  const answers = keys.map((k) => (details && details[k]) || null);
  // An answer is kept here, so the store dropping it to make room does not blank a picture drawn.
  useEffect(() => {
    let next = null;
    keys.forEach((k, i) => {
      const a = answers[i];
      if (a && (a.state === "ok" || a.state === "gone") && (!kept[k] || kept[k] !== a)) (next = next || { ...kept })[k] = a;
    });
    if (next) setKept(next);
  }, [answers.map((a) => (a ? a.state + ":" + a.n : "")).join(",")]);
  const missing = keys.map((k, i) => !answers[i] && !kept[k]).join(",");
  useEffect(() => {
    if (!onDetail) return;
    keys.forEach((k, i) => {
      if (!answers[i] && !kept[k] && asks.current < MAX_ASKS * Math.max(1, n)) {
        asks.current++;
        if (onDetail(k) === false) setRefused(true);
      }
    });
  }, [e.id, missing]);
  if (n === 0) return null;
  if (!onDetail || refused) return html`<p class="conv-pics">${picsWords(n)}</p>`;
  return html`<div class="conv-pics conv-thumbs" role="group" aria-label=${picsWords(n)}>
    ${images.map((im, i) => {
      const a = kept[keys[i]] || answers[i] || { state: "loading" };
      return html`<div key=${keys[i]} class="conv-thumb"><${ImageBody} entry=${im} detail=${a} /></div>`;
    })}
  </div>`;
}
