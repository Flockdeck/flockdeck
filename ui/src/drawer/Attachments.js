// The pictures of a prompt, kept beside the text and not in it. A picture pasted, dropped or chosen becomes
// a chip: a thumbnail made from an object URL, the name, the size and dimensions it will be sent at, a
// remove button and a progress bar. The chips are prepared (scaled to SEND_MAX or less, imageAttach.js)
// and sent to the desktop with attachImage one at a time, each with a req of its own that the desk echoes
// on attachImageResult, so a result is matched to its chip and the desk's attach rate limit (10 a 10 s)
// is not hit by a batch of four. A chip that fails says so by itself; the others carry on. When the prompt
// is sent the composer passes the ready chips' paths as paneSend's images; nothing is written into the text.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { prepareImage, imageName, ImageError } from "./imageAttach.js";

/** MAX_IMAGES is the most pictures one prompt carries; the desk refuses more. */
export const MAX_IMAGES = 4;
/** ATTACH_WAIT_MS is how long the desk's answer to one picture is waited for. */
export const ATTACH_WAIT_MS = 15000;
/** ATTACH_LIMIT and ATTACH_WINDOW_MS are the desk's attachImage rate limit, less one call of slack. */
export const ATTACH_LIMIT = 9;
export const ATTACH_WINDOW_MS = 10000;
export const IMAGE_NOT_SENT_WORDS = "Not connected to Flockdeck, so the picture was not sent.";
export const IMAGE_FAILED_WORDS = "That picture could not be read.";
export const IMAGE_WAITING_WORDS = "Wait for the pictures to attach.";
export const IMAGE_REMOVE_FAILED_WORDS = "Remove the pictures that did not attach, then send.";
export const IMAGE_TOO_MANY_WORDS = "A prompt takes at most 4 pictures.";
export const IMAGE_NO_ANSWER_WORDS = "Flockdeck did not answer, so the picture was not attached.";

/** sizeWords is a byte count as a person reads it: "212 KB", "1.4 MB". */
export function sizeWords(bytes) {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return Math.max(1, Math.round(bytes / 1024)) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

/** infoWords is what a chip says about its picture once it is prepared: "1600 × 900 · 212 KB". */
export function infoWords(c) {
  const parts = [];
  if (c.width && c.height) parts.push(c.width + " × " + c.height);
  const bytes = c.bytes || c.origSize;
  if (bytes) parts.push(sizeWords(bytes));
  return parts.join(" · ");
}

/** STAGE is how far along a chip is, for its bar, 0 to 3. */
const STAGE = { queued: 0, preparing: 1, uploading: 2, ready: 3, error: 3 };

/** blocksSend is the words that stop a send while pictures are not all attached, or "" when it can go. */
export function blocksSend(chips) {
  if (chips.some((c) => c.status === "queued" || c.status === "preparing" || c.status === "uploading")) return IMAGE_WAITING_WORDS;
  if (chips.some((c) => c.status === "error")) return IMAGE_REMOVE_FAILED_WORDS;
  return "";
}

/** imageRefs is paneSend's images for the ready chips, in the order they were added. */
export const imageRefs = (chips) => chips.filter((c) => c.status === "ready").map((c) => ({ path: c.path }));

// The times of the last attachImage calls of this window, across panes, for pacing.
const sentAt = [];
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
async function pace() {
  for (;;) {
    const now = Date.now();
    while (sentAt.length && now - sentAt[0] >= ATTACH_WINDOW_MS) sentAt.shift();
    if (sentAt.length < ATTACH_LIMIT) break;
    await wait(ATTACH_WINDOW_MS - (now - sentAt[0]) + 20);
  }
  sentAt.push(Date.now());
}

/** resetPacing forgets the calls counted for pacing; a test that sends many pictures in a second needs it. */
export function resetPacing() { sentAt.length = 0; }

const objectUrls = () => (typeof URL !== "undefined" && URL.createObjectURL ? URL : null);
let chipSeq = 0;
let reqSeq = 0;

/**
 * useAttachments keeps the chips of one pane's box. Pass the pane's id, the store, the store's snapshot
 * (whose attached is the latest attachImageResult), and onNote({ text, level }) for the box's status line.
 * It returns { chips, add(files), remove(key), clear(), room } where add takes the pictures (File objects)
 * and says nothing about files that are not pictures: the caller picks them first.
 */
export function useAttachments({ pane, store, snap, onNote }) {
  const [chips, setChips] = useState([]);
  const chipsRef = useRef([]);
  const live = useRef({ pane, store, onNote });
  live.current = { pane, store, onNote };
  const running = useRef(false);
  const waiting = useRef(null);
  const seen = useRef(snap && snap.attached ? snap.attached.seq : 0);
  const gone = useRef(false);
  const commit = (next) => { chipsRef.current = next; setChips(next); };
  const patch = (key, p) => { if (chipsRef.current.some((c) => c.key === key)) commit(chipsRef.current.map((c) => (c.key === key ? { ...c, ...p } : c))); };
  const revoke = (c) => {
    const u = objectUrls();
    if (c.url && u && u.revokeObjectURL) { try { u.revokeObjectURL(c.url); } catch (e) { /* a URL already gone is fine */ } }
  };

  const attachedNow = snap ? snap.attached : null;
  useEffect(() => {
    const a = attachedNow;
    if (!a || !(a.seq > seen.current)) return;
    seen.current = a.seq;
    const w = waiting.current;
    if (!w || a.id !== live.current.pane || !a.req || a.req !== w.req) return;
    waiting.current = null;
    clearTimeout(w.timer);
    w.resolve(a);
  }, [attachedNow]);

  const answer = (req) => new Promise((resolve) => {
    const timer = setTimeout(() => { waiting.current = null; resolve({ error: IMAGE_NO_ANSWER_WORDS }); }, ATTACH_WAIT_MS);
    waiting.current = { req, resolve, timer };
  });

  const pump = async () => {
    if (running.current) return;
    running.current = true;
    try {
      for (;;) {
        const c = chipsRef.current.find((x) => x.status === "queued");
        if (!c || gone.current) break;
        patch(c.key, { status: "preparing" });
        try {
          const img = await prepareImage(c.file);
          if (!chipsRef.current.some((x) => x.key === c.key)) continue;
          await pace();
          if (!chipsRef.current.some((x) => x.key === c.key)) continue;
          const req = "ai" + ++reqSeq;
          patch(c.key, { status: "uploading", req, bytes: img.bytes, width: img.width, height: img.height, mediaType: img.mediaType });
          const pending = answer(req);
          if (!live.current.store.send({ cmd: "attachImage", id: live.current.pane, req, name: img.name, mediaType: img.mediaType, data: img.data })) {
            clearTimeout(waiting.current && waiting.current.timer);
            waiting.current = null;
            throw new ImageError(IMAGE_NOT_SENT_WORDS);
          }
          const res = await pending;
          if (res.error || !res.path) throw new ImageError(clip(String(res.error || IMAGE_FAILED_WORDS), 200));
          patch(c.key, { status: "ready", path: res.path, file: null });
        } catch (err) {
          const text = err instanceof ImageError ? err.message : IMAGE_FAILED_WORDS;
          patch(c.key, { status: "error", error: text, file: null });
          if (live.current.onNote) live.current.onNote({ text: (c.name ? c.name + ": " : "") + text, level: "error" });
        }
      }
    } finally {
      running.current = false;
    }
  };

  const add = (files) => {
    const list = Array.from(files || []).filter(Boolean);
    if (!list.length) return;
    const room = Math.max(0, MAX_IMAGES - chipsRef.current.length);
    const take = list.slice(0, room);
    if (list.length > take.length && live.current.onNote) live.current.onNote({ text: IMAGE_TOO_MANY_WORDS, level: "warning" });
    if (!take.length) return;
    const u = objectUrls();
    const made = take.map((file) => {
      let url = "";
      try { url = u ? u.createObjectURL(file) : ""; } catch (e) { url = ""; }
      return { key: "c" + ++chipSeq, name: imageName(file.name), origSize: file.size, url, status: "queued", file };
    });
    commit([...chipsRef.current, ...made]);
    pump();
  };
  const remove = (key) => {
    const c = chipsRef.current.find((x) => x.key === key);
    if (!c) return;
    revoke(c);
    commit(chipsRef.current.filter((x) => x.key !== key));
  };
  const clear = () => {
    chipsRef.current.forEach(revoke);
    commit([]);
  };
  useEffect(() => () => {
    gone.current = true;
    if (waiting.current) { clearTimeout(waiting.current.timer); waiting.current = null; }
    chipsRef.current.forEach(revoke);
  }, []);
  return { chips, add, remove, clear, room: Math.max(0, MAX_IMAGES - chips.length) };
}

/** Attachments draws the chips in the composer's images slot. */
export function Attachments({ chips, remove }) {
  if (!chips.length) return null;
  return html`<ul class="attach-list" aria-label="Pictures to send">
    ${chips.map((c) => html`<li key=${c.key} class="attach-chip" data-state=${c.status}>
      ${c.url ? html`<img class="attach-thumb" src=${c.url} alt="" />` : html`<span class="attach-thumb attach-thumb--none" aria-hidden="true"></span>`}
      <span class="attach-meta">
        <span class="attach-name" title=${c.name}>${c.name}</span>
        <span class="attach-info">${c.status === "error" ? c.error : infoWords(c)}</span>
        ${c.status === "ready" || c.status === "error"
          ? null
          : html`<span class="attach-bar" role="progressbar" aria-label=${"Attaching " + c.name} aria-valuemin="0" aria-valuemax="3" aria-valuenow=${STAGE[c.status]} data-stage=${STAGE[c.status]}><span class="attach-fill"></span></span>`}
      </span>
      <button type="button" class="attach-remove" aria-label=${"Remove " + c.name} title="Remove" onClick=${() => remove(c.key)}>
        <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false"><path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg>
      </button>
    </li>`)}
  </ul>`;
}
