// An image entry: a line saying what it is, and on request the picture.
//
// The bytes are not in the entry; they come with conversationDetail as base64 in
// the entry's own media type. The picture is made from a data: address and
// nothing is fetched from the network. Only the formats a browser draws without
// running anything are drawn (PNG, JPEG, GIF, WebP), the base64 must be only
// base64 characters, and over MAX_IMAGE_BYTES the picture is not drawn and the
// row says so. A click on the picture toggles its size.
import { useState } from "../../vendor/hooks.mjs";
import { html } from "../../html.js";

/** MAX_IMAGE_BYTES is the most an image drawn here is, in decoded bytes. */
export const MAX_IMAGE_BYTES = 8 << 20;
export const IMAGE_TYPES = Object.freeze(["image/png", "image/jpeg", "image/gif", "image/webp"]);
export const NOT_DRAWN_WORDS = "This image is not drawn here.";
export const TOO_BIG_WORDS = "This image is too large to draw here.";
const BASE64 = /^[A-Za-z0-9+/]*={0,2}$/;

/** imageSrc is the data: address for an image, or "" when it is not one to draw. */
export function imageSrc(mediaType, data) {
  const type = String(mediaType || "").toLowerCase();
  if (!IMAGE_TYPES.includes(type) || typeof data !== "string" || data === "" || !BASE64.test(data)) return "";
  return "data:" + type + ";base64," + data;
}

/** decodedBytes is how many bytes a base64 string decodes to. */
export const decodedBytes = (data) => Math.floor((data.length * 3) / 4) - (data.endsWith("==") ? 2 : data.endsWith("=") ? 1 : 0);

/** sizeWords words a byte count: "12.3 KB". */
export function sizeWords(n) {
  if (typeof n !== "number" || !(n >= 0)) return "";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / (1024 * 1024)).toFixed(1) + " MB";
}

/** imageWords is the one line for an image entry. */
export function imageWords(e) {
  const parts = [String(e.mediaType || "unknown type")];
  if (e.width && e.height) parts.push(e.width + " by " + e.height);
  const s = sizeWords(e.bytes);
  if (s) parts.push(s);
  return "Image (" + parts.join(", ") + ")";
}

/** ImageBody is what an opened image row shows: the picture, or why not. */
export function ImageBody({ entry: e, detail }) {
  const [big, setBig] = useState(false);
  if (!detail) return null;
  const data = detail.detail && typeof detail.detail.data === "string" ? detail.detail.data : "";
  const src = detail.state === "loading" || detail.state === "gone" ? "" : imageSrc(e.mediaType, data);
  const tooBig = !!src && decodedBytes(data) > MAX_IMAGE_BYTES;
  // The words stay in one element, empty when the picture is drawn, so a screen reader announces a change in it.
  const say = detail.state === "loading" ? "Loading the image…" : detail.state === "gone" ? "No longer available." : !src ? NOT_DRAWN_WORDS : tooBig ? TOO_BIG_WORDS : "";
  return html`<p class="conv-detail-state" data-detail=${detail.state === "loading" || detail.state === "gone" ? detail.state : undefined} role="status">${say}</p>
    ${say ? null : html`<button type="button" class=${"conv-image" + (big ? " is-big" : "")} aria-label=${imageWords(e) + (big ? ", press to make it small" : ", press to make it larger")} aria-pressed=${big ? "true" : "false"} onClick=${() => setBig(!big)}>
      <img src=${src} alt=${imageWords(e)} />
    </button>`}`;
}
