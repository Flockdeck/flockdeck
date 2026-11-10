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
/** attachTiming holds the waits as numbers a test can shorten: how long an answer is waited for, and the pacing. */
export const attachTiming = { waitMs: ATTACH_WAIT_MS, limit: ATTACH_LIMIT, windowMs: ATTACH_WINDOW_MS };
export const IMAGE_NOT_SENT_WORDS = "Not connected to Flockdeck, so the picture was not sent.";
export const IMAGE_FAILED_WORDS = "That picture could not be read.";
export const IMAGE_WAITING_WORDS = "Wait for the pictures to attach.";
export const IMAGE_REMOVE_FAILED_WORDS = "Remove the pictures that did not attach, then send.";
export const IMAGE_TOO_MANY_WORDS = "A prompt takes at most 4 pictures.";
export const IMAGE_NO_ANSWER_WORDS = "Flockdeck did not answer, so the picture was not attached.";
export const IMAGE_DROPPED_WORDS = "The connection to Flockdeck dropped, so the picture was not attached.";
/** notKeptWords is what a pane's box says when it comes back and some pictures did not survive the visit. */
export const notKeptWords = (n) => (n === 1 ? "A picture was not kept when you left this pane. Add it again." : n + " pictures were not kept when you left this pane. Add them again.");
/** leftOutWords is what a drop or paste says about the files in it that are not pictures. */
export const leftOutWords = (n) => (n === 1 ? "One file that is not a picture was left out." : n + " files that are not pictures were left out.");

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
  if (c.note) parts.push(c.note);
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
    while (sentAt.length && now - sentAt[0] >= attachTiming.windowMs) sentAt.shift();
    if (sentAt.length < attachTiming.limit) break;
    await wait(attachTiming.windowMs - (now - sentAt[0]) + 20);
  }
  sentAt.push(Date.now());
}

/** resetPacing forgets the calls counted for pacing; a test that sends many pictures in a second needs it. */
export function resetPacing() { sentAt.length = 0; }

const objectUrls = () => (typeof URL !== "undefined" && URL.createObjectURL ? URL : null);
let chipSeq = 0;
let reqSeq = 0;

const revokeUrl = (c) => {
  const u = objectUrls();
  if (c && c.url && u && u.revokeObjectURL) { try { u.revokeObjectURL(c.url); } catch (e) { /* a URL already gone is fine */ } }
};
// The pictures of a pane whose box is closed, as the text draft is kept (composerModel.js): the chips that
// were attached, and how many were lost (not yet attached, or failed). Their thumbnails stay alive until the
// pictures are sent, removed, or the pane is gone.
const kept = new Map();
const takeKept = (pane) => {
  const k = kept.get(pane);
  kept.delete(pane);
  return k || { chips: [], lost: 0 };
};
/** pruneAttachments drops what is kept for panes that are no longer open. */
export function pruneAttachments(open) {
  const live = new Set(open);
  for (const [pane, k] of [...kept]) if (!live.has(pane)) { k.chips.forEach(revokeUrl); kept.delete(pane); }
}
/** forgetAttachments drops everything kept; a test starts from it. */
export function forgetAttachments() {
  for (const k of kept.values()) k.chips.forEach(revokeUrl);
  kept.clear();
}

/**
 * useAttachments keeps the chips of one pane's box. Pass the pane's id, the store, the store's snapshot
 * (whose attached is the latest attachImageResult), and onNote({ text, level }) for the box's status line.
 * It returns { chips, add(files, { left }), remove(key), removeMany(keys), clear(), room, announce }: add takes the
 * pictures (File objects), picked by the caller, and left is how many files of the drop were not pictures, which
 * it says so with the rest; announce is the words for a polite live region. The chips that are attached are kept
 * for the pane when the box goes away, unless sending() says a send is in flight (those pictures went with it).
 */
export function useAttachments({ pane, store, snap, onNote, sending = null }) {
  const first = useRef(null);
  if (first.current === null) first.current = takeKept(pane);
  const [chips, setChips] = useState(first.current.chips);
  const chipsRef = useRef(first.current.chips);
  const [announce, setAnnounce] = useState("");
  const live = useRef({ pane, store, onNote, sending });
  live.current = { pane, store, onNote, sending };
  const connected = !snap || !snap.connection || snap.connection === "open";
  const running = useRef(false);
  const waiting = useRef(null);
  const seen = useRef(snap && snap.attached ? snap.attached.seq : 0);
  const gone = useRef(false);
  const commit = (next) => { chipsRef.current = next; setChips(next); };
  const patch = (key, p) => { if (chipsRef.current.some((c) => c.key === key)) commit(chipsRef.current.map((c) => (c.key === key ? { ...c, ...p } : c))); };
  const has = (key) => chipsRef.current.some((c) => c.key === key);
  const revoke = revokeUrl;
  useEffect(() => {
    if (first.current.lost > 0 && live.current.onNote) live.current.onNote({ text: notKeptWords(first.current.lost), level: "warning" });
  }, []);

  const attachedNow = snap ? snap.attached : null;
  useEffect(() => {
    const a = attachedNow;
    if (!a || !(a.seq > seen.current)) return;
    seen.current = a.seq;
    const w = waiting.current;
    if (!w || a.id !== live.current.pane || !a.req || a.req !== w.req) return;
    settleWait(a);
  }, [attachedNow]);

  // The wait for one picture's answer ends with the desk's result, or with { error } (no answer, the socket
  // dropped) or { cancelled } (the chip was removed, the box went away), whichever comes first.
  const settleWait = (res) => {
    const w = waiting.current;
    if (!w) return;
    waiting.current = null;
    clearTimeout(w.timer);
    w.resolve(res);
  };
  const answer = (req, key) => new Promise((resolve) => {
    const timer = setTimeout(() => settleWait({ error: IMAGE_NO_ANSWER_WORDS }), attachTiming.waitMs);
    waiting.current = { req, key, resolve, timer };
  });
  // A socket that drops while a picture is out will not answer for it: fail it now, not after the wait.
  useEffect(() => {
    if (!connected) settleWait({ error: IMAGE_DROPPED_WORDS });
  }, [connected]);

  const pump = async () => {
    if (running.current) return;
    running.current = true;
    try {
      for (;;) {
        if (gone.current) break;
        const c = chipsRef.current.find((x) => x.status === "queued");
        if (!c) break;
        patch(c.key, { status: "preparing" });
        try {
          const img = await prepareImage(c.file);
          // A box that went away, or a chip that was removed, uploads nothing.
          if (gone.current) break;
          if (!has(c.key)) continue;
          await pace();
          if (gone.current) break;
          if (!has(c.key)) continue;
          const req = "ai" + ++reqSeq;
          patch(c.key, { status: "uploading", req, bytes: img.bytes, width: img.width, height: img.height, mediaType: img.mediaType, note: img.note });
          setAnnounce("Attaching " + c.name);
          const pending = answer(req, c.key);
          if (!live.current.store.send({ cmd: "attachImage", id: live.current.pane, req, name: img.name, mediaType: img.mediaType, data: img.data })) {
            settleWait({ cancelled: true });
            throw new ImageError(IMAGE_NOT_SENT_WORDS);
          }
          const res = await pending;
          if (gone.current) break;
          // Removed while it was out: nothing to say about it, and the queue goes on.
          if (res.cancelled || !has(c.key)) continue;
          if (res.error || !res.path) throw new ImageError(clip(String(res.error || IMAGE_FAILED_WORDS), 200));
          patch(c.key, { status: "ready", path: res.path, file: null });
          setAnnounce(c.name + " is attached" + (img.note ? ", " + img.note : ""));
        } catch (err) {
          if (gone.current) break;
          if (!has(c.key)) continue;
          const text = err instanceof ImageError ? err.message : IMAGE_FAILED_WORDS;
          patch(c.key, { status: "error", error: text, file: null });
          if (live.current.onNote) live.current.onNote({ text: (c.name ? c.name + ": " : "") + text, level: "error" });
        }
      }
    } finally {
      running.current = false;
    }
  };

  const add = (files, { left = 0 } = {}) => {
    const list = Array.from(files || []).filter(Boolean);
    const room = Math.max(0, MAX_IMAGES - chipsRef.current.length);
    const take = list.slice(0, room);
    const words = [];
    if (left > 0) words.push(leftOutWords(left));
    if (list.length > take.length) words.push(IMAGE_TOO_MANY_WORDS);
    if (words.length && live.current.onNote) live.current.onNote({ text: words.join(" "), level: "warning" });
    if (!take.length) return;
    const u = objectUrls();
    const made = take.map((file) => {
      let url = "";
      try { url = u ? u.createObjectURL(file) : ""; } catch (e) { url = ""; }
      return { key: "c" + ++chipSeq, name: imageName(file.name), origSize: file.size, url, status: "queued", file };
    });
    commit([...chipsRef.current, ...made]);
    setAnnounce(made.length === 1 ? "Added " + made[0].name : "Added " + made.length + " pictures");
    pump();
  };
  const remove = (key) => {
    const c = chipsRef.current.find((x) => x.key === key);
    if (!c) return;
    revoke(c);
    commit(chipsRef.current.filter((x) => x.key !== key));
    setAnnounce("Removed " + c.name);
    // The one being waited on is let go of, so the next picture does not wait for it.
    if (waiting.current && waiting.current.key === key) settleWait({ cancelled: true });
  };
  // The chips that were sent go, and any added while the send was out stay.
  const removeMany = (keys) => {
    const sent = new Set(keys);
    chipsRef.current.filter((c) => sent.has(c.key)).forEach(revoke);
    commit(chipsRef.current.filter((c) => !sent.has(c.key)));
  };
  const clear = () => {
    chipsRef.current.forEach(revoke);
    commit([]);
  };
  useEffect(() => () => {
    gone.current = true;
    settleWait({ cancelled: true });
    const held = chipsRef.current;
    const sent = !!live.current.sending && live.current.sending();
    const keep = sent ? [] : held.filter((c) => c.status === "ready" && c.path);
    held.filter((c) => !keep.includes(c)).forEach(revoke);
    if (!sent && held.length) kept.set(live.current.pane, { chips: keep, lost: held.length - keep.length });
  }, []);
  return { chips, add, remove, removeMany, clear, announce, room: Math.max(0, MAX_IMAGES - chips.length) };
}

/** Attachments draws the chips in the composer's images slot. */
export function Attachments({ chips, remove, buttons = null }) {
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
      <button type="button" class="attach-remove" ref=${(el) => { if (buttons) { if (el) buttons.current[c.key] = el; else delete buttons.current[c.key]; } }} aria-label=${"Remove " + c.name} title="Remove" onClick=${() => remove(c.key)}>
        <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" focusable="false"><path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg>
      </button>
    </li>`)}
  </ul>`;
}
