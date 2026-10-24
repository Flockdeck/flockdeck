// The Team dialog: a project's task board and messages. The roster is the graph,
// so it appears here only as the names a task can be given to and a message sent
// to.
//
// The person acts as themselves: a task they add or move, a note, a message, are
// theirs, and the desktop applies the same rules it applies to the manager pane
// (the moves offered on a task are the desktop's list). The desktop pushes the
// whole picture whenever the board or the messages change, so the dialog only
// sends and draws what comes back. Every string in it came from an agent or a
// person and is drawn as text.
//
// The dialog is for the machine Flockdeck runs on; Live does not draw it in a
// window reached through the relay.
import { useEffect, useRef, useState } from "../vendor/hooks.mjs";
import { html } from "../html.js";
import { composing, LIST } from "./keys.js";
import { Dialog } from "./Dialog.js";
import { Confirm } from "./Confirm.js";
import { ago } from "../lib/remote.js";
import {
  actorWords, addTaskCommand, assignCommand, boardOf, branchCommand, fullMessageCommand, listAllCommand, mergeTasks, messageOf, noteCommand, pruneCommand,
  resultOf, resultWords, seenCommand, sendMessageCommand, stateName, statusCommand, taskGetCommand, taskOf, teamOf,
} from "../lib/team.js";

const NEW_TASK = { title: "", body: "", role: "", branch: "" };

/** Team is the dialog. team is the store's team push, tasks its latest taskList
 *  answer, messages its latest msgList answer and result its latest answer to a
 *  command that changed something. gone is the reason the project can no longer be
 *  read, root the project asked for, send the store's send. */
export function Team({ team: teamMsg, tasks: tasksMsg, messages: messagesMsg, result: resultMsg, gone, root, send, onClose, clock = () => Date.now() }) {
  const team = teamOf(teamMsg);
  const [tab, setTab] = useState("tasks");
  const [open, setOpen] = useState("");
  const [form, setForm] = useState(NEW_TASK);
  const [note, setNote] = useState("");
  const [assign, setAssign] = useState("");
  const [branch, setBranch] = useState("");
  const [text, setText] = useState("");
  const [target, setTarget] = useState("");
  const [days, setDays] = useState("30");
  const [asking, setAsking] = useState(false);
  const [problem, setProblem] = useState("");
  const [said, setSaid] = useState("");
  const [detail, setDetail] = useState(null);
  const [full, setFull] = useState({});
  const [finishedShown, setFinishedShown] = useState(false);
  const reqs = useRef(0);
  const pending = useRef(new Map());
  const floor = useRef(resultMsg ? resultMsg.seq : 0);
  const tasksFloor = useRef(tasksMsg ? tasksMsg.seq : 0);
  const msgFloor = useRef(messagesMsg ? messagesMsg.seq : 0);
  const at = team ? team.root : root;

  const go = (c, kind) => {
    if (!c) return false;
    const ok = send(c) !== false;
    setProblem(ok ? "" : "The request could not be sent. Check the connection.");
    if (ok) pending.current.set(c.req, kind);
    return ok;
  };
  const next = () => "u" + ++reqs.current;

  // The first push is asked for by the page that opened the dialog.
  // An answer to something sent from here: say it, and empty the form it came from.
  useEffect(() => {
    if (!resultMsg || resultMsg.seq <= floor.current) return;
    floor.current = resultMsg.seq;
    const r = resultOf(resultMsg);
    const kind = pending.current.get(r.req);
    if (kind === undefined) return;
    pending.current.delete(r.req);
    setSaid(r.ok ? resultWords(r) : "");
    setProblem(r.ok ? "" : resultWords(r));
    if (!r.ok) return;
    if (r.task && r.task.id === open) setDetail(r.task);
    if (kind === "add") setForm(NEW_TASK);
    if (kind === "note") setNote("");
    if (kind === "assign") setAssign("");
    if (kind === "send") setText("");
    if (kind === "status") setNote("");
  }, [resultMsg && resultMsg.seq]);
  // The whole list of tasks, for Show finished.
  useEffect(() => {
    if (tasksMsg && tasksMsg.seq > tasksFloor.current) {
      tasksFloor.current = tasksMsg.seq;
      setFinishedShown(true);
    }
  }, [tasksMsg && tasksMsg.seq]);
  // The whole text of a message that was cut.
  useEffect(() => {
    if (!messagesMsg || messagesMsg.seq <= msgFloor.current) return;
    msgFloor.current = messagesMsg.seq;
    const ms = (Array.isArray(messagesMsg.messages) ? messagesMsg.messages : []).map(messageOf);
    if (ms.length === 1 && ms[0].id && !ms[0].cut) setFull((f) => ({ ...f, [ms[0].id]: ms[0].text }));
  }, [messagesMsg && messagesMsg.seq]);

  const allTasks = team ? (finishedShown && tasksMsg ? mergeTasks(team.tasks, (Array.isArray(tasksMsg.tasks) ? tasksMsg.tasks : []).map(taskOf)) : team.tasks) : [];
  const toggle = (t) => {
    if (open === t.id) {
      setOpen("");
      setDetail(null);
      return;
    }
    setOpen(t.id);
    setDetail(null);
    setNote("");
    setAssign("");
    setBranch(t.branch);
    go(taskGetCommand(at, t.id, next()), "get");
  };
  const targets = team ? [...new Set(team.roster.flatMap((r) => [r.name, r.role, r.pane]).filter(Boolean))] : [];
  const key = (fn) => (e) => {
    if (composing(e)) return;
    if (e.key === LIST.accept && !e.shiftKey) {
      e.preventDefault();
      fn();
    }
  };

  const taskDetail = (t) => {
    const d = detail && detail.id === t.id ? detail : null;
    const live = d || t;
    const needNote = live.moves.some((m) => m.needNote);
    return html`<div class="team-detail" id="team-detail">
      ${d && d.body ? html`<p class="team-body">${d.body}</p>` : null}
      ${d && d.baton ? html`<p class="dialog-note">Baton: ${d.baton}</p>` : null}
      ${d && d.noteList.length
        ? html`<ul class="team-notes" aria-label="Notes">${d.noteList.map((n, i) => html`<li key=${i}><span class="team-who">${actorWords(n.author)}, ${ago(n.time, clock())}</span> ${n.text}</li>`)}</ul>`
        : null}
      ${d && d.historyList.length
        ? html`<ul class="team-history" aria-label="History">${d.historyList.map((e, i) => html`<li key=${i}>${e.what} <span class="dialog-note">${actorWords(e.actor)}, ${ago(e.time, clock())}</span></li>`)}</ul>`
        : null}
      <div class="team-row">
        <input type="text" class="dialog-field" id="team-note" aria-label=${needNote ? "Note (needed for some moves)" : "Note"} placeholder=${needNote ? "Note (needed for some moves)" : "Note"}
          value=${note} autocomplete="off" onInput=${(e) => setNote(e.target.value)} onKeyDown=${key(() => go(noteCommand(at, t.id, note, next()), "note"))} />
        <button type="button" class="dialog-btn" id="team-note-add" disabled=${!note.trim()} onClick=${() => go(noteCommand(at, t.id, note, next()), "note")}>Add note</button>
      </div>
      ${live.moves.length
        ? html`<div class="team-row" role="group" aria-label="Move this task">
            ${live.moves.map((m) => html`<button type="button" class="dialog-btn team-move" data-to=${m.to} key=${m.to}
              title=${m.needNote ? "Needs a note" : undefined} disabled=${m.needNote && !note.trim()}
              onClick=${() => go(statusCommand(at, live, m.to, note, next()), "status")}>${"Move to " + stateName(m.to).toLowerCase()}</button>`)}
          </div>`
        : null}
      ${live.canAssign
        ? html`<div class="team-row">
            <input type="text" class="dialog-field" id="team-assign" list="team-targets" aria-label="Give to a role, a name or a pane" placeholder="Give to a role, a name or a pane"
              value=${assign} autocomplete="off" onInput=${(e) => setAssign(e.target.value)} onKeyDown=${key(() => go(assignCommand(at, t.id, assign, next()), "assign"))} />
            <button type="button" class="dialog-btn" id="team-assign-go" disabled=${!assign.trim()} onClick=${() => go(assignCommand(at, t.id, assign, next()), "assign")}>Give</button>
          </div>`
        : null}
      ${live.canEdit
        ? html`<div class="team-row">
            <input type="text" class="dialog-field" id="team-branch" aria-label="Branch" placeholder="Branch" value=${branch} autocomplete="off" spellcheck="false"
              onInput=${(e) => setBranch(e.target.value)} onKeyDown=${key(() => go(branchCommand(at, t.id, branch, next()), "branch"))} />
            <button type="button" class="dialog-btn" id="team-branch-go" disabled=${branch.trim() === t.branch} onClick=${() => go(branchCommand(at, t.id, branch, next()), "branch")}>Set branch</button>
          </div>`
        : null}
    </div>`;
  };

  const taskRow = (t) => html`<li key=${t.id} class="team-task" data-task=${t.id}>
    <button type="button" class="team-task-head" aria-expanded=${open === t.id ? "true" : "false"} onClick=${() => toggle(t)}>
      <span class="team-id">${t.id}</span>
      <span class="team-title">${t.title}</span>
      <span class="team-meta">${t.owner ? t.owner : "no owner"}${t.branch ? " · " + t.branch : ""}${t.notes ? " · " + t.notes + (t.notes === 1 ? " note" : " notes") : ""}</span>
    </button>
    ${open === t.id ? taskDetail(t) : null}
  </li>`;

  const tasksTab = () => html`
    ${team.boardError ? html`<p class="dialog-note dialog-note--error" role="alert">${team.boardError}</p>` : null}
    ${allTasks.length === 0 && !team.boardError ? html`<p class="dialog-note" id="team-no-tasks">No tasks yet.</p>` : null}
    <div class="team-board" id="team-board">
      ${boardOf(allTasks).map((g) => html`<section key=${g.state} class="team-group" data-state=${g.state} aria-label=${g.name}>
        <h4 class="team-group-head">${g.name} <span class="dialog-note">${g.tasks.length}</span></h4>
        <ul class="team-list">${g.tasks.map(taskRow)}</ul>
      </section>`)}
    </div>
    ${team.openHidden ? html`<p class="dialog-note">${team.openHidden} older open tasks are not listed.</p>` : null}
    <div class="team-row">
      ${team.finishedHidden && !finishedShown
        ? html`<button type="button" class="dialog-btn" id="team-show-finished" onClick=${() => go(listAllCommand(at, next()), "list")}>Show ${team.finishedHidden} more finished</button>`
        : null}
      <input type="number" class="dialog-field team-days" id="team-prune-days" min="1" max="3650" aria-label="Days" value=${days} onInput=${(e) => setDays(e.target.value)} />
      <button type="button" class="dialog-btn dialog-btn--danger" id="team-prune" disabled=${!pruneCommand(at, days, "x")} onClick=${() => setAsking(true)}>Remove old finished tasks…</button>
    </div>
    ${asking
      ? html`<${Confirm} title=${"Remove finished tasks not changed for " + days + " days?"}
          words=${["Done and cancelled tasks are deleted from the board. This cannot be undone."]} label="Remove them"
          onConfirm=${() => { setAsking(false); go(pruneCommand(at, days, next()), "prune"); }} onCancel=${() => setAsking(false)} />`
      : null}
    <h4 class="team-group-head">Add a task</h4>
    <div class="team-form">
      <input type="text" class="dialog-field" id="team-new-title" aria-label="Title" placeholder="Title" value=${form.title} autocomplete="off"
        onInput=${(e) => setForm({ ...form, title: e.target.value })} />
      <textarea class="dialog-field" id="team-new-body" aria-label="Brief" placeholder="Brief (optional)" rows="3" value=${form.body}
        onInput=${(e) => setForm({ ...form, body: e.target.value })}></textarea>
      <input type="text" class="dialog-field" id="team-new-role" aria-label="Role" placeholder="Role it is for (optional)" value=${form.role} autocomplete="off"
        onInput=${(e) => setForm({ ...form, role: e.target.value })} />
      <input type="text" class="dialog-field" id="team-new-branch" aria-label="Branch" placeholder="Branch (optional)" value=${form.branch} autocomplete="off" spellcheck="false"
        onInput=${(e) => setForm({ ...form, branch: e.target.value })} />
      <button type="button" class="dialog-btn dialog-btn--primary" id="team-add" disabled=${!form.title.trim()} onClick=${() => go(addTaskCommand(at, form, next()), "add")}>Add task</button>
    </div>`;

  const messagesTab = () => html`
    <div class="team-form">
      <input type="text" class="dialog-field" id="team-to" list="team-targets" aria-label="To a role, a name or a pane" placeholder="To a role, a name or a pane" value=${target}
        autocomplete="off" onInput=${(e) => setTarget(e.target.value)} />
      <textarea class="dialog-field" id="team-text" aria-label="Message" placeholder="Message" rows="3" value=${text} onInput=${(e) => setText(e.target.value)}></textarea>
      <button type="button" class="dialog-btn dialog-btn--primary" id="team-send" disabled=${!target.trim() || !text.trim()}
        onClick=${() => go(sendMessageCommand(at, target, text, next()), "send")}>Send message</button>
    </div>
    ${team.messages.length === 0 ? html`<p class="dialog-note" id="team-no-messages">No messages yet.</p>` : null}
    <ul class="team-messages" id="team-messages">
      ${team.messages.map((m) => html`<li key=${m.id + ":" + m.to} class=${"team-message" + (m.seen || m.from.user ? "" : " is-unseen")} data-message=${m.id}>
        <div class="team-who">${m.system ? "Flockdeck" : actorWords(m.from)} to ${m.toName || m.to}${m.toRole ? " (" + m.toRole + ")" : ""}, ${ago(m.time, clock())}${m.read ? ", read" : ""}</div>
        <p class="team-body">${full[m.id] !== undefined ? full[m.id] : m.text}${m.cut && full[m.id] === undefined ? "…" : ""}</p>
        <div class="team-row">
          ${m.cut && full[m.id] === undefined
            ? html`<button type="button" class="dialog-btn team-whole" onClick=${() => go(fullMessageCommand(at, m.id, next()), "whole")}>Show all</button>`
            : null}
          ${!m.seen && !m.from.user
            ? html`<button type="button" class="dialog-btn team-seen" onClick=${() => go(seenCommand(at, m, next()), "seen")}>Mark seen</button>`
            : null}
        </div>
      </li>`)}
    </ul>
    ${team.messagesHidden ? html`<p class="dialog-note">${team.messagesHidden} older messages are not listed.</p>` : null}`;

  const unseen = team ? team.counts.unseen : 0;
  return html`
    <${Dialog} id="team" title=${"Team" + (team && team.project ? ", " + team.project : "")} onClose=${onClose} focus="#team-tab-tasks" wide>
      <div class="team-tabs" role="tablist" aria-label="Team">
        <button type="button" role="tab" class=${"team-tab" + (tab === "tasks" ? " is-active" : "")} id="team-tab-tasks" aria-selected=${tab === "tasks" ? "true" : "false"} onClick=${() => setTab("tasks")}>
          Tasks${team && team.counts.open ? " (" + team.counts.open + " open)" : ""}</button>
        <button type="button" role="tab" class=${"team-tab" + (tab === "messages" ? " is-active" : "")} id="team-tab-messages" aria-selected=${tab === "messages" ? "true" : "false"} onClick=${() => setTab("messages")}>
          Messages${unseen ? " (" + unseen + " new)" : ""}</button>
      </div>
      ${problem ? html`<p class="dialog-note dialog-note--error" id="team-problem" role="alert">${problem}</p>` : null}
      ${said ? html`<p class="dialog-note" id="team-said" role="status">${said}</p>` : null}
      ${gone ? html`<p class="dialog-note dialog-note--error" id="team-gone" role="alert">${gone}</p>` : null}
      ${!team && !gone ? html`<p class="dialog-note" id="team-loading" role="status">Reading the team…</p>` : null}
      ${team && tab === "tasks" ? tasksTab() : null}
      ${team && tab === "messages" ? messagesTab() : null}
      <datalist id="team-targets">${targets.map((t) => html`<option key=${t} value=${t}></option>`)}</datalist>
      <div class="dialog-foot">
        <button type="button" class="dialog-btn" onClick=${onClose}>Close</button>
      </div>
    <//>
  `;
}
