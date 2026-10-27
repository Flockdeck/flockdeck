// The prompts waiting behind the running turn of a structured pane, as a list
// under the conversation: each with its place, its text, and Edit, Remove and
// Send now; Clear for the person's own. A message from another agent is in the
// same list and marked as mail, with its sender beside the text and not in it. A
// prompt Flockdeck sent itself (the opening prompt) is marked "From Flockdeck".
// Neither can be edited or removed, and Clear leaves them; Send now moves them up.
//
// A prompt is named to the desk by its id. One the agent was sent while the
// person was looking at it is refused there with fixed words and shown here; an
// edit that was refused stays open with its text. An edit starts from the text
// the push carried, exactly as the person typed it. Everything on a row is drawn
// as text.
//
// Focus: when a row goes (Remove, or the prompt being sent) it moves to the next
// row, or to the prompt box when none is left; when an edit closes it returns to
// that row's Edit button.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { clip } from "../graph/Node.js";
import { showControls } from "./diff/diffModel.js";
import { queueMessage, hasUserItems, QUEUE_GONE_WORDS } from "./queueModel.js";

export const QUEUE_NOT_SENT_WORDS = "Not connected to Flockdeck, so that was not sent.";
let queueSeq = 0;

/** QueueList is the list. rows is queueOf(push); result is the store's latest answer to a queue command,
 *  { id, req, ok, error }; working says a turn is running, so Send now ends it; focusBox puts the focus in the
 *  prompt box. */
export function QueueList({ rows, pane, store, result = null, working = false, focusBox = null }) {
  // The prompt being edited: { id, text, req } where req is the Save in flight, or "".
  const [edit, setEdit] = useState(null);
  const [note, setNote] = useState("");
  // The request whose refusal was last shown and then dismissed by the list changing.
  const [dismissed, setDismissed] = useState("");
  const lastReq = useRef("");
  const boxRef = useRef(null);
  const root = useRef(null);
  // Where the focus goes after the next draw: { edit: id } for an edit's row, { near: index, id } for a row that went.
  const refocus = useRef(null);
  const ids = rows.map((r) => r.id).join(",");
  // The list changed: an edit open for a prompt that is gone is closed and says so, and any other note is stale.
  useEffect(() => {
    if (edit && !rows.some((r) => r.id === edit.id)) {
      setEdit(null);
      setNote(QUEUE_GONE_WORDS);
    } else {
      setNote("");
    }
    setDismissed(lastReq.current);
  }, [ids]);
  // The answer to the Save in flight closes the edit when it worked; when it did not, the edit stays with its text.
  useEffect(() => {
    if (!result || !edit || !edit.req || result.req !== edit.req) return;
    if (result.ok) {
      refocus.current = { edit: edit.id };
      setEdit(null);
    } else {
      setEdit({ ...edit, req: "" });
    }
  }, [result]);
  useEffect(() => {
    if (edit && boxRef.current && boxRef.current.focus) boxRef.current.focus();
  }, [edit && edit.id]);
  // After a draw: put the focus where the person's last action leaves it.
  useEffect(() => {
    const to = refocus.current;
    if (!to) return;
    refocus.current = null;
    const row = to.edit || to.now ? rows.find((r) => r.id === (to.edit || to.now)) : rows[Math.min(to.near, rows.length - 1)];
    const sel = to.edit ? ".queue-edit-btn" : to.now ? ".queue-send-now" : "button";
    const li = row && root.current && root.current.querySelectorAll ? Array.from(root.current.querySelectorAll("[data-queue-item]")).find((n) => n.getAttribute("data-queue-item") === String(row.id)) : null;
    const btn = li ? li.querySelector(sel) : null;
    if (btn && btn.focus) btn.focus();
    else if (focusBox) focusBox();
  });
  if (!rows.length) return null;

  const run = (op, id, text) => {
    const req = "q" + ++queueSeq;
    const msg = queueMessage(pane, rows, op, id, text, req);
    if (!msg) return "";
    setNote("");
    if (!store.send(msg)) {
      setNote(QUEUE_NOT_SENT_WORDS);
      return "";
    }
    lastReq.current = req;
    setDismissed("");
    // The row the press was on goes (Remove), or moves to the front (Send now): the focus follows it or the row after.
    if (op === "remove") refocus.current = { near: rows.findIndex((r) => r.id === id) };
    else if (op === "sendNow") refocus.current = { now: id };
    else if (op === "clear") refocus.current = { near: 0 };
    return req;
  };
  const closeEdit = (id) => {
    refocus.current = { edit: id };
    setEdit(null);
  };
  const save = () => {
    if (!edit || edit.req) return;
    const req = run("edit", edit.id, edit.text);
    if (req) setEdit({ ...edit, req });
  };
  const onKey = (e) => {
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeEdit(edit.id);
    } else if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      save();
    }
  };
  const refused = result && result.id === pane && !result.ok && result.req === lastReq.current && result.req !== dismissed ? String(result.error || "") : "";
  const item = (r) => html`<li key=${r.id} class="structured-queue-item" data-queue-item=${r.id} data-kind=${r.mail ? "mail" : r.system ? "flockdeck" : "user"}>
    <span class="queue-pos" aria-hidden="true">${r.pos}</span>
    ${r.mail ? html`<span class="queue-mail">Mail from <span class="queue-peer" dir="auto">${showControls(r.peer || "another agent")}</span></span>` : null}
    ${r.system ? html`<span class="queue-system">From Flockdeck</span>` : null}
    ${edit && edit.id === r.id
      ? html`<textarea ref=${boxRef} class="queue-edit" aria-label=${"Edit waiting prompt " + r.pos} rows="3" value=${edit.text} dir="auto"
            readOnly=${!!edit.req} onKeyDown=${onKey} onInput=${(e) => setEdit({ ...edit, text: e.target.value })}></textarea>
          <span class="queue-actions">
            <button type="button" class="drawer-btn queue-save" disabled=${!!edit.req || edit.text.trim() === ""} onClick=${save}>Save</button>
            <button type="button" class="drawer-btn queue-cancel" disabled=${!!edit.req} onClick=${() => closeEdit(r.id)}>Cancel</button>
          </span>`
      : html`<span class="queue-text" dir="auto">${showControls(clip(r.text, 300))}</span>
          <span class="queue-actions">
            ${r.editable ? html`<button type="button" class="drawer-btn queue-edit-btn" aria-label=${"Edit waiting prompt " + r.pos} onClick=${() => { setNote(""); setEdit({ id: r.id, text: r.raw, req: "" }); }}>Edit</button>` : null}
            ${r.removable ? html`<button type="button" class="drawer-btn queue-remove" aria-label=${"Remove waiting prompt " + r.pos} onClick=${() => run("remove", r.id)}>Remove</button>` : null}
            <button type="button" class="drawer-btn queue-send-now" aria-label=${"Send waiting prompt " + r.pos + " now"}
              title=${working ? "Ends the current turn and sends this next" : "Sends this next"} onClick=${() => run("sendNow", r.id)}>Send now</button>
          </span>`}
  </li>`;
  return html`<section ref=${root} class="structured-queue" aria-label="Waiting prompts" data-queue=${rows.length}>
    <p class="queue-head"><span>${rows.length === 1 ? "1 prompt waiting" : rows.length + " prompts waiting"}</span>
      ${hasUserItems(rows)
        ? html`<button type="button" class="drawer-btn queue-clear" title=${rows.some((r) => r.mail || r.system) ? "Removes yours; messages from other agents and Flockdeck's own prompt stay" : "Removes every waiting prompt"} onClick=${() => run("clear")}>Clear</button>`
        : null}</p>
    <ol class="queue-list">${rows.map(item)}</ol>
    ${note ? html`<p class="structured-note structured-note--unsent" role="status">${note}</p>` : null}
    ${refused ? html`<p class="structured-note" role="status">${showControls(clip(refused, 300))}</p>` : null}
  </section>`;
}
