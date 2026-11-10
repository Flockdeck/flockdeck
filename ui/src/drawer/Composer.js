// The composer of a structured pane: the prompt box and everything that belongs to it. Moved out of
// Structured.js with no change in behaviour. It owns the draft (kept per pane, composerModel.js), the
// history and recall, the "/" menu, the "@" file list, picture attaching, the "!" shell path, the
// send-once guard and the notes under the box. Structured keeps the settings, Stop, the panels and the
// Escape handler (interruptModel.js), and reaches the box through two refs:
//   live: { draft, menuOpen } as of the last draw, which the Escape handler reads.
//   api:  { restore(text), focus() }, for the rewind panel and the queue list.
// The keys the box owns are described at the top of Structured.js.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { outcomeOf, sendTo } from "../graph/ManagerBar.js";
import { menuOf, flatten, insertText, keyOf, selectedIndex, OWN_COMMANDS } from "./slashModel.js";
import { ownCommandsFor } from "./settingsModel.js";
import { sendKey, rowsFor, pushHistory, recall, NO_RECALL, onFirstLine, onLastLine, mentionAt, insertMention, cleanFiles, loadComposer, saveComposer, pruneComposer } from "./composerModel.js";
import { restoredDraft } from "./rewindModel.js";
import { composerNotes, SEND_NOT_SENT_WORDS, STOP_NOT_SENT_WORDS } from "./composerNotes.js";
import { shellText, shellRunMessage, shellWait, SHELL_EMPTY_WORDS, SHELL_ONE_LINE_WORDS, SHELL_NOT_SENT_WORDS, SHELL_NO_ANSWER_WORDS } from "./shellModel.js";
import { prepareImage, pickImages, attachNote, ImageError, IMAGE_TYPES } from "./imageAttach.js";

export { SEND_NOT_SENT_WORDS, STOP_NOT_SENT_WORDS };
let filesSeq = 0;
let shellRunSeq = 0;
/** FILES_DELAY_MS is how long the box waits after the last keystroke of a mention before asking for names. */
export const FILES_DELAY_MS = 120;
/** ATTACH_WAIT_MS is how long a picture is waited for before the box takes another. */
export const ATTACH_WAIT_MS = 15000;
/** SAVE_DELAY_MS is how long the draft waits after the last change before it is written to storage. */
export const SAVE_DELAY_MS = 300;
export const IMAGE_NOT_SENT_WORDS = "Not connected to Flockdeck, so the picture was not sent.";
export const IMAGE_WAITING_WORDS = "Wait for the first picture to attach.";
export const IMAGE_FAILED_WORDS = "That picture could not be read.";
export const IMAGE_UNSUPPORTED_WORDS = "This agent does not take pictures.";
export const COMMANDS_NEED_EMPTY_WORDS = "Commands start an empty box. Clear the text first.";
/** STOP_GUARD_MS is how long after a send is accepted that Stop, which takes Send's place, ignores a press. */
export const STOP_GUARD_MS = 500;

/** Composer is the box. pane, snap, st and store are Structured's; controls are the agent's settings (for
 *  the "/" menu); applySetting(text) answers "none" when the text is not a settings request, "done" when it
 *  was made and "kept" when it was refused or waits for a yes; onOwn(id) runs Flockdeck's /info, /rewind,
 *  /fork, /mode and /model; stop(keep) sends Stop; stopping and stopFailed are Stop's state; settingText,
 *  settingError and cancelError are the words about a setting and a Stop, for the status line, and onExpire() is
 *  called when they are stale: the draft changed, the person sent or stopped again, or the turn ended;
 *  shellResult is the store's answer to a "!" command; tail is the settings confirm, drawn above the status line. */
export function Composer({ pane, snap, st, store, controls, applySetting, onOwn, stop, onExpire = null, stopping = false, stopFailed = false, settingText = "", settingError = "", cancelError = "", tail = null, shellResult = null, live, api }) {
  // The draft and the history are kept per pane (composerModel.js), so they outlive this component.
  const kept = useRef(null);
  if (kept.current === null) kept.current = loadComposer(pane);
  const [draft, setDraft] = useState(kept.current.draft);
  const history = useRef(kept.current.history);
  const recalling = useRef({ nav: NO_RECALL, text: null });
  const sentText = useRef("");
  // The caret as the box last reported it, or null for the end; and where to put it after the next draw.
  const [caret, setCaret] = useState(null);
  const putCaret = useRef(null);
  // The mention list: the key of the chosen name, the draft whose list was closed with Escape, and the names asked for.
  const [fileKey, setFileKey] = useState(null);
  const [filesClosedFor, setFilesClosedFor] = useState(null);
  const askedFiles = useRef({ req: "", q: null });
  // Pictures: the one in flight, what the last attempt said, and the newest result already handled.
  // imageNote is { text, level } for composerNotes, or null.
  const [imageNote, setImageNote] = useState(null);
  const attaching = useRef(false);
  const attachTimer = useRef(null);
  const seenAttach = useRef(snap && snap.attached ? snap.attached.seq : 0);
  const fileInput = useRef(null);
  const [sendFailed, setSendFailed] = useState("");
  // Words that are not about the text in the box go stale: when the draft changes, the person acts again, or the turn ends.
  const expireRef = useRef(onExpire);
  expireRef.current = onExpire;
  const expire = () => {
    if (expireRef.current) expireRef.current();
    setImageNote((n) => (n && n.level !== "hint" ? null : n));
  };
  const firstDraft = useRef(true);
  useEffect(() => {
    if (firstDraft.current) { firstDraft.current = false; return; }
    expire();
  }, [draft]);
  const wasRunning = useRef(st.status === "working" || st.status === "waiting");
  useEffect(() => {
    const now = st.status === "working" || st.status === "waiting";
    if (wasRunning.current && !now) expire();
    wasRunning.current = now;
  }, [st.status]);
  // A "!" command in flight: its req, and what the last one was refused with. The draft stays until the desk has taken it.
  const [shellReq, setShellReq] = useState("");
  const [shellNote, setShellNote] = useState("");
  useEffect(() => {
    if (!shellReq || !shellResult || shellResult.req !== shellReq) return;
    setShellReq("");
    if (shellResult.ok) {
      history.current = pushHistory(history.current, sentText.current);
      recalling.current = { nav: NO_RECALL, text: null };
      setDraft("");
      setShellNote("");
    } else {
      setShellNote(clip(String(shellResult.error || ""), 200));
    }
  }, [shellResult]);
  // No answer within shellWait.ms, or the socket went away, ends the wait, so "!" cannot stay stuck; the text stays.
  const connected = !snap || !snap.connection || snap.connection === "open";
  useEffect(() => {
    if (!shellReq) return;
    if (!connected) {
      setShellReq("");
      setShellNote(SHELL_NO_ANSWER_WORDS);
      return;
    }
    const t = setTimeout(() => { setShellReq(""); setShellNote(SHELL_NO_ANSWER_WORDS); }, shellWait.ms);
    return () => clearTimeout(t);
  }, [shellReq, connected]);
  // The send in flight: the mark outcomeOf follows. Set at once, so a second Send before the page has redrawn does not send again.
  const [mark, setMark] = useState(null);
  const busy = useRef(false);
  const inFlight = useRef(false);
  const box = useRef(null);
  const sendBtn = useRef(null);
  // The menu's chosen row, and the draft whose menu was closed with Escape.
  const [selKey, setSelKey] = useState(null);
  const [closedFor, setClosedFor] = useState(null);
  const outcome = mark ? outcomeOf(snap || {}, mark) : null;
  if (!outcome || outcome.kind !== "sending") busy.current = false;
  inFlight.current = !!outcome && outcome.kind === "sending";
  const taken = !!outcome && outcome.kind === "sent";
  // The text goes once the desk has taken it; a refused one stays to be changed or sent again.
  useEffect(() => {
    if (!taken) return;
    history.current = pushHistory(history.current, sentText.current);
    recalling.current = { nav: NO_RECALL, text: null };
    setDraft("");
    setMark(null);
  }, [taken, mark]);
  // The draft is written 300 ms after the last change, and at once when the box goes away.
  const unsaved = useRef(false);
  const saveTimer = useRef(null);
  const latest = useRef({ draft, pane });
  latest.current = { draft, pane };
  const flushSave = () => {
    clearTimeout(saveTimer.current);
    if (!unsaved.current) return;
    unsaved.current = false;
    saveComposer(latest.current.pane, { draft: latest.current.draft, history: history.current });
  };
  useEffect(() => {
    unsaved.current = true;
    clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(flushSave, SAVE_DELAY_MS);
    // A draft the person has edited after recalling a prompt is no longer that recall.
    if (recalling.current.nav.at >= 0 && draft !== recalling.current.text) recalling.current = { nav: NO_RECALL, text: null };
  }, [draft, pane, taken]);
  // A send still out when the box goes away (the pane's push went null, another pane was opened) cannot be
  // followed: the draft is not kept as unsent text, which would be sent twice if the desk took it, and the text
  // goes into the history instead, where Up brings it back if the desk refused it.
  useEffect(() => () => {
    if (inFlight.current && sentText.current) {
      history.current = pushHistory(history.current, sentText.current);
      latest.current = { ...latest.current, draft: "" };
      unsaved.current = true;
    }
    flushSave();
  }, []);
  // What is kept for a pane that is no longer open goes.
  useEffect(() => {
    const panes = snap && snap.state && snap.state.panes;
    if (panes && typeof panes === "object" && Object.keys(panes).length) pruneComposer(Object.keys(panes));
  }, []);
  const sendingNow = !!outcome && outcome.kind === "sending";
  const mention = !sendingNow && !draft.startsWith("!") ? mentionAt(draft, caret === null ? draft.length : caret) : null;
  const mentionQuery = mention ? mention.query : null;
  useEffect(() => {
    setFileKey(null);
  }, [mentionQuery]);
  useEffect(() => {
    if (mentionQuery === null) return;
    const t = setTimeout(() => {
      const req = "f" + ++filesSeq;
      askedFiles.current = { req, q: mentionQuery };
      store.send({ cmd: "structuredFiles", id: pane, q: mentionQuery, req });
    }, FILES_DELAY_MS);
    return () => clearTimeout(t);
  }, [mentionQuery, pane]);
  useEffect(() => {
    const el = box.current;
    const c = putCaret.current;
    putCaret.current = null;
    if (c !== null && el && el.setSelectionRange) el.setSelectionRange(c, c);
  });
  const attachedNow = snap ? snap.attached : null;
  useEffect(() => {
    const a = attachedNow;
    if (!a || !(a.seq > seenAttach.current)) return;
    seenAttach.current = a.seq;
    if (a.id !== pane || !attaching.current) return;
    attaching.current = false;
    clearTimeout(attachTimer.current);
    if (a.error) {
      setImageNote({ text: clip(String(a.error), 200), level: "error" });
      return;
    }
    setImageNote(null);
    setDraft((d) => d + (d === "" || /\s$/.test(d) ? "" : "\n") + attachNote(a.path) + " ");
    setCaret(null);
  }, [attachedNow, pane]);
  useEffect(() => () => clearTimeout(attachTimer.current), []);
  // Send is off while the answer is awaited, which takes the keyboard from it; a refusal gives it back to the text,
  // unless the person has moved on to something else in the meantime.
  const refusedNow = !!outcome && outcome.kind === "refused";
  useEffect(() => {
    const el = box.current;
    if (!refusedNow || !el || !el.focus) return;
    const doc = el.ownerDocument;
    const held = doc && doc.activeElement;
    // The drawer's own panel holding the keyboard is not something the person moved to.
    if (held && held !== doc.body && held !== sendBtn.current && !(held.contains && held.contains(el))) return;
    el.focus();
  }, [refusedNow, mark]);
  // After a send the keyboard goes back to the box: Send is off while the answer is awaited and may turn into Stop,
  // and Escape, the refusal's refocus and the next line all need the focus in the text.
  const focusBox = () => { if (box.current && box.current.focus) box.current.focus(); };
  // Stop takes Send's place in the same spot when a send is accepted; a second press of that spot is ignored for a moment.
  const stopGuard = useRef(0);
  const submit = (e) => {
    if (e && e.preventDefault) e.preventDefault();
    const text = draft.trim();
    if (!text || busy.current) return;
    expire();
    try { submitText(text); } finally { focusBox(); }
  };
  const submitText = (text) => {
    // A "!" first runs the rest as the person's own command, never the agent's. A leading space sends it to the agent instead.
    const bang = shellText(draft);
    if (bang !== null) {
      if (shellReq) return;
      const req = "hr" + ++shellRunSeq;
      const msg = shellRunMessage(pane, bang, req);
      if (!msg) setShellNote(bang === "" ? SHELL_EMPTY_WORDS : SHELL_ONE_LINE_WORDS);
      else if (!store.send(msg)) setShellNote(SHELL_NOT_SENT_WORDS);
      else {
        sentText.current = text;
        setShellReq(req);
        setShellNote("");
      }
      return;
    }
    // A leading space sends the text to the agent as it is, so an agent's own /mode command can still be reached.
    if (!/^\s/.test(draft)) {
      const r = applySetting(text);
      if (r !== "none") {
        if (r === "done") setDraft("");
        return;
      }
    }
    const next = sendTo((m) => store.send(m), snap, pane, text);
    if (next) {
      sentText.current = text;
      busy.current = true;
      stopGuard.current = Date.now() + STOP_GUARD_MS;
      setMark(next);
      setSendFailed("");
    } else {
      setMark(null);
      setSendFailed(pane);
    }
  };
  const sending = !!outcome && outcome.kind === "sending";
  const menu = !sending && !draft.startsWith("!") && closedFor !== draft ? menuOf(draft, st.commands, ownCommandsFor(OWN_COMMANDS, controls, !!st.capabilities && st.capabilities.rewind === true)) : null;
  const items = flatten(menu);
  // The chosen row is found by its command, so a list that changes under the person does not move it; -1 is none.
  const at = menu ? selectedIndex(items, selKey, menu.query) : -1;
  const choose = (it) => {
    if (it.group === "own") {
      setDraft("");
      if (it.id === "stop") { expire(); stop(); }
      if (it.id === "info" || it.id === "rewind" || it.id === "fork") onOwn(it.id);
      if (it.id === "mode" || it.id === "model") {
        onOwn(it.id);
        setSelKey(null);
        setSendFailed("");
        return;
      }
    } else {
      setDraft(insertText(it));
    }
    setSelKey(null);
    setSendFailed("");
    if (box.current && box.current.focus) box.current.focus();
  };
  // The names the mention list shows: those of the reply to the latest ask, while the @ word is still being typed.
  const reply = snap ? snap.files : null;
  // Fresh: the reply to the ask made for the word as it stands. In the wait after a keystroke the list is
  // the previous word's, so it is not shown and nothing from it can be chosen.
  const fresh = !!mention && !!reply && reply.id === pane && reply.req !== "" && reply.req === askedFiles.current.req && askedFiles.current.q === mentionQuery;
  const filesOpen = !!mention && filesClosedFor !== draft;
  const names = filesOpen && fresh ? cleanFiles(reply.files) : [];
  const nameAt = names.length ? Math.max(0, names.indexOf(fileKey)) : -1;
  const pickName = (name) => {
    const r = insertMention(draft, mention, name);
    putCaret.current = r.caret;
    setCaret(r.caret);
    setDraft(r.text);
    setFileKey(null);
    setSendFailed("");
    if (box.current && box.current.focus) box.current.focus();
  };
  const caretOf = (el) => {
    if (typeof el.selectionStart === "number") return [el.selectionStart, typeof el.selectionEnd === "number" ? el.selectionEnd : el.selectionStart];
    const end = typeof el.value === "string" ? el.value.length : draft.length;
    return [end, end];
  };
  const recallTo = (e, dir) => {
    const r = recall(history.current, recalling.current.nav, dir, draft);
    if (!r) return;
    e.preventDefault();
    recalling.current = { nav: r.nav, text: r.text };
    setCaret(null);
    setDraft(r.text);
    setSendFailed("");
  };
  const onKey = (e) => {
    // A key that belongs to an input method's composition is the method's, not the menu's.
    if (e.isComposing || e.keyCode === 229) return;
    if (menu) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const down = e.key === "ArrowDown";
        setSelKey(keyOf(items[at < 0 ? (down ? 0 : items.length - 1) : (at + (down ? 1 : items.length - 1)) % items.length]));
        return;
      } else if (((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") && at >= 0) {
        e.preventDefault();
        choose(items[at]);
        return;
      } else if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setClosedFor(draft);
        return;
      }
    } else if (names.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const down = e.key === "ArrowDown";
        setFileKey(names[(nameAt + (down ? 1 : names.length - 1)) % names.length]);
        return;
      } else if ((e.key === "Tab" && !e.shiftKey || e.key === "Enter" && !e.shiftKey && fileKey !== null) && nameAt >= 0) {
        // Tab takes the highlighted name; Enter takes one only after an arrow key or the mouse chose it,
        // so a prompt that ends in an @ word can be sent with Enter.
        e.preventDefault();
        pickName(names[nameAt]);
        return;
      } else if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setFilesClosedFor(draft);
        return;
      }
    }
    const [from, to] = caretOf(e.target || box.current);
    if (e.key === "ArrowUp" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && onFirstLine(draft, from, to)) {
      recallTo(e, -1);
    } else if (e.key === "ArrowDown" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && onLastLine(draft, from, to)) {
      recallTo(e, 1);
    } else if (sendKey(e) === "send") {
      e.preventDefault();
      submit();
    }
  };
  const attach = async (file) => {
    if (!file) return;
    if (attaching.current) {
      setImageNote({ text: IMAGE_WAITING_WORDS, level: "warning" });
      return;
    }
    attaching.current = true;
    setImageNote({ text: "Attaching the picture.", level: "hint" });
    clearTimeout(attachTimer.current);
    attachTimer.current = setTimeout(() => { attaching.current = false; setImageNote({ text: IMAGE_FAILED_WORDS, level: "error" }); }, ATTACH_WAIT_MS);
    try {
      const img = await prepareImage(file);
      if (!store.send({ cmd: "attachImage", id: pane, name: img.name, mediaType: img.mediaType, data: img.data })) throw new ImageError(IMAGE_NOT_SENT_WORDS);
    } catch (err) {
      attaching.current = false;
      clearTimeout(attachTimer.current);
      setImageNote({ text: err instanceof ImageError ? err.message : IMAGE_FAILED_WORDS, level: "error" });
    }
  };
  const imageOk = !!st.capabilities && st.capabilities.image === true;
  // A picture dropped or pasted: taken when the agent takes pictures, said no to otherwise. Text is left to the box.
  const onPicture = (data, e) => {
    const pics = pickImages(data);
    if (!pics.length) return;
    e.preventDefault();
    if (!imageOk) setImageNote({ text: IMAGE_UNSUPPORTED_WORDS, level: "warning" });
    else attach(pics[0]);
  };
  // What the rest of the drawer reads and calls.
  live.current = { draft, menuOpen: !!menu || names.length > 0 };
  api.current = {
    focus: () => { if (box.current && box.current.focus) box.current.focus(); },
    restore: (t) => { setDraft((d) => restoredDraft(d, t)); if (box.current && box.current.focus) box.current.focus(); },
  };
  const optId = (i) => "slash-" + pane + "-" + i;
  const row = (it, i) => html`<div id=${optId(i)} role="option" class=${"slash-item" + (i === at ? " slash-item--on" : "")} aria-selected=${i === at ? "true" : "false"} data-slash=${it.name}
    onMouseDown=${(e) => e.preventDefault()} onMouseEnter=${() => setSelKey(keyOf(it))} onClick=${() => choose(it)}>
    <span class="slash-name">/${it.name}</span>${it.hint ? html`<span class="slash-hint">${it.hint}</span>` : null}${it.description ? html`<span class="slash-desc">${it.description}</span>` : null}
  </div>`;
  const running = st.status === "working" || st.status === "waiting";
  const pressStop = () => {
    if (Date.now() < stopGuard.current) { focusBox(); return; }
    expire();
    stop();
    focusBox();
  };
  // While the agent works and nothing is typed the button is Stop; with text in the box it is Send, which queues the text.
  const showStop = running && draft.trim() === "";
  // The "@" button puts an @ at the caret, after a space when the word before it is not over, which opens the file list.
  const addMention = () => {
    if (sending) return;
    const el = box.current;
    const at = Math.min(el ? caretOf(el)[0] : draft.length, draft.length);
    const lead = at > 0 && !/\s/.test(draft[at - 1]) ? " " : "";
    const c = at + lead.length + 1;
    putCaret.current = c;
    setCaret(c);
    setDraft(draft.slice(0, at) + lead + "@" + draft.slice(at));
    setSendFailed("");
    if (el && el.focus) el.focus();
  };
  // The "/" button starts a command; commands only count at the start of an empty box.
  const addSlash = () => {
    if (sending) return;
    if (draft !== "") {
      setImageNote({ text: COMMANDS_NEED_EMPTY_WORDS, level: "warning" });
      return;
    }
    putCaret.current = 1;
    setCaret(1);
    setDraft("/");
    setSendFailed("");
    if (box.current && box.current.focus) box.current.focus();
  };
  const noted = composerNotes({
    refused: outcome && outcome.kind === "refused" ? clip(outcome.text, 300) : "", sendFailed: sendFailed === pane,
    shellOn: draft.startsWith("!"), shellNote, stopFailed, setting: settingText, settingError, cancelError,
    image: imageNote, stopping, status: st.status,
  });
  const statusAttrs = !noted ? null : {
    "shell-note": { "data-shell-note": "on" }, "shell-hint": { "data-shell-hint": "on" },
    "stopping": { "data-stopping": "on" },
    "working": st.status === "working" ? { "data-interrupt-hint": "on", "data-queue-hint": "on" } : { "data-interrupt-hint": "on" },
  }[noted.id] || {};
  const noteClass = (n) => "structured-note composer-status composer-status--" + n.level + (n.level === "error" ? " structured-note--unsent" : n.level === "hint" ? " structured-note--hint" : "") + (n.send ? " structured-note--send" : "");
  return html`<form class="structured-prompt composer" onSubmit=${submit}>
      ${menu
        ? html`<div class="slash-menu" role="listbox" aria-label="Commands" id=${"slash-" + pane}>
            ${menu.own.length ? html`<div role="group" aria-label="Flockdeck"><p class="slash-group" aria-hidden="true">Flockdeck</p>${menu.own.map((c, i) => row({ ...c, group: "own" }, i))}</div>` : null}
            ${menu.agent.length ? html`<div role="group" aria-label="From the agent"><p class="slash-group" aria-hidden="true">From the agent</p>${menu.agent.map((c, i) => row({ ...c, group: "agent" }, menu.own.length + i))}</div>` : null}
          </div>
          <p class="sr-only" role="status">${items.length === 1 ? "1 command" : items.length + " commands"}</p>`
        : null}
      ${names.length
        ? html`<div class="slash-menu mention-menu" role="listbox" aria-label="Files" id=${"mention-" + pane}>
            ${names.map((n, i) => html`<div id=${"mention-" + pane + "-" + i} role="option" class=${"slash-item" + (i === nameAt ? " slash-item--on" : "")} aria-selected=${i === nameAt ? "true" : "false"} data-mention=${n}
              onMouseDown=${(e) => e.preventDefault()} onMouseEnter=${() => setFileKey(n)} onClick=${() => pickName(n)}><span class="slash-name mention-path" dir="auto">${n}</span></div>`)}
          </div>
          <p class="sr-only" role="status">${names.length === 1 ? "1 file" : names.length + " files"}</p>`
        : null}
      ${filesOpen && fresh && reply.more ? html`<p class="slash-group mention-note">More files match. Type more of the name.</p>` : null}
      ${filesOpen && fresh && reply.error ? html`<p class="slash-group mention-note" role="status">${showControls(clip(String(reply.error), 200))}</p>` : null}
      <div class="composer-box">
        <div class="composer-images" data-composer-slot="images"></div>
        <textarea ref=${box} class="structured-input" aria-label="Message to the agent" placeholder="Enter sends, Shift+Enter adds a line" rows=${rowsFor(draft)} value=${draft}
          role=${menu || names.length ? "combobox" : undefined} aria-expanded=${menu || names.length ? "true" : undefined} aria-autocomplete=${menu || names.length ? "list" : undefined} aria-controls=${menu ? "slash-" + pane : names.length ? "mention-" + pane : undefined} aria-activedescendant=${menu && at >= 0 ? optId(at) : names.length ? "mention-" + pane + "-" + nameAt : undefined}
          onKeyDown=${onKey} onKeyUp=${(e) => setCaret(caretOf(e.target)[0])} onClick=${(e) => setCaret(caretOf(e.target)[0])}
          onPaste=${(e) => onPicture(e.clipboardData, e)} onDragOver=${(e) => { if (imageOk && e.dataTransfer && pickImages(e.dataTransfer).length) e.preventDefault(); }} onDrop=${(e) => onPicture(e.dataTransfer, e)}
          readOnly=${outcome && outcome.kind === "sending"} aria-busy=${outcome && outcome.kind === "sending" ? "true" : undefined} onInput=${(e) => { setDraft(e.target.value); setCaret(caretOf(e.target)[0]); setSendFailed(""); setShellNote(""); if (outcome && outcome.kind === "refused") setMark(null); }}></textarea>
        <div class="composer-tools">
          <button type="button" class="composer-tool structured-attach" aria-label="Attach image" title=${imageOk ? "Attach image" : IMAGE_UNSUPPORTED_WORDS}
            aria-disabled=${imageOk ? undefined : "true"} data-dim=${imageOk ? undefined : "on"} onClick=${() => { if (!imageOk) setImageNote({ text: IMAGE_UNSUPPORTED_WORDS, level: "warning" }); else if (fileInput.current) fileInput.current.click(); }}>
            <svg class="composer-glyph" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M13.5 7.5 8 13a3.2 3.2 0 0 1-4.5-4.5l5.6-5.6a2.1 2.1 0 0 1 3 3L6.4 11.3a1 1 0 0 1-1.4-1.4L10.5 4.4" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" /></svg>
          </button>
          ${imageOk
            ? html`<input ref=${fileInput} type="file" class="sr-only" tabindex="-1" aria-hidden="true" accept=${IMAGE_TYPES.join(",")}
                  onChange=${(e) => { const f = e.target.files && e.target.files[0]; e.target.value = ""; attach(f); }} />`
            : null}
          <button type="button" class="composer-tool composer-mention" aria-label="Mention a file" title="Mention a file" disabled=${draft.startsWith("!")} onClick=${addMention}>@</button>
          <button type="button" class="composer-tool composer-command" aria-label="Commands" title=${draft === "" ? "Commands" : COMMANDS_NEED_EMPTY_WORDS} aria-disabled=${draft !== "" ? "true" : undefined} data-dim=${draft !== "" ? "on" : undefined} onClick=${addSlash}>/</button>
          <span class="composer-gap"></span>
          ${showStop
            ? html`<button key="stop" type="button" class="drawer-btn composer-send composer-send--stop structured-stop" onClick=${pressStop}>Stop</button>`
            : html`<button key="send" ref=${sendBtn} type="submit" class="drawer-btn composer-send structured-send" disabled=${draft.trim() === "" || (outcome && outcome.kind === "sending")}
                aria-label=${running ? "Send (queues)" : undefined} title=${running ? "The agent is working, so this is queued. Stop returns when the box is empty." : undefined}>${running ? "Send (queues)" : "Send"}</button>`}
        </div>
      </div>
      ${tail}
      ${noted && noted.role === "status"
        ? html`<p class=${noteClass(noted)} role="status" data-note=${noted.id} ...${statusAttrs}>${showControls(noted.text)}</p>`
        : html`<p class="sr-only" role="status"></p>`}
      ${noted && noted.role === "alert"
        ? html`<p class=${noteClass(noted)} role="alert" data-note=${noted.id} ...${statusAttrs}>${showControls(noted.text)}</p>`
        : html`<p class="sr-only" role="alert"></p>`}
    </form>`;
}
