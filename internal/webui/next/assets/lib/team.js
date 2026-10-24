// The Team panel's data: a project's task board and messages, as the desktop
// sends them, and the commands that change them. The roster is the graph, so it
// is not drawn here beyond naming who a task or a message can go to.
//
// Everything is read defensively and every string in it came from an agent or a
// person, so the views draw it as text. The panel is for the machine Flockdeck
// runs on: the desktop refuses every one of these commands to a window reached
// through the relay, and the store does not send them from one.

export const TEAM_RELAY_WORDS = "The Team panel is used on the machine Flockdeck runs on, not from a window reached through the relay.";

/** STATES are the states of a task, open ones first. */
export const STATES = [
  ["planned", "Planned"], ["assigned", "Assigned"], ["in-progress", "In progress"], ["in-review", "In review"],
  ["blocked", "Blocked"], ["done", "Done"], ["cancelled", "Cancelled"],
];
const FINISHED = new Set(["done", "cancelled"]);

export const stateName = (s) => (STATES.find(([v]) => v === s) || [s, s || "Unknown"])[1];
export const isFinished = (s) => FINISHED.has(s);

const str = (v) => (typeof v === "string" ? v : "");
const num = (v) => (Number.isInteger(v) && v > 0 ? v : 0);
const list = (v) => (Array.isArray(v) ? v : []);
const obj = (v) => (v && typeof v === "object" ? v : {});

function actorOf(a) {
  const x = obj(a);
  return { name: str(x.name), role: str(x.role), pane: str(x.pane), user: x.user === true };
}

/** actorWords says who did something: "you", or "api (worker)". */
export function actorWords(a) {
  if (a.user) return "you";
  return a.role && a.role !== a.name ? (a.name || a.pane || "an agent") + " (" + a.role + ")" : a.name || a.pane || "an agent";
}

/** taskOf reads one task of a push or an answer. */
export function taskOf(t) {
  const x = obj(t);
  return {
    id: str(x.id), title: str(x.title), state: str(x.state), owner: str(x.owner), ownerRole: str(x.ownerRole), ownerPane: str(x.ownerPane),
    ownerOpen: x.ownerOpen === true, branch: str(x.branch), notes: num(x.notes), creator: actorOf(x.creator), updated: str(x.updated),
    moves: list(x.moves).filter((m) => m && typeof m.to === "string").map((m) => ({ to: m.to, needNote: m.needNote === true })),
    canAssign: x.canAssign === true, canEdit: x.canEdit === true,
    body: str(x.body), baton: str(x.baton), created: str(x.created),
    noteList: list(x.noteList).map((n) => ({ author: actorOf(obj(n).author), time: str(obj(n).time), text: str(obj(n).text) })),
    historyList: list(x.historyList).map((e) => ({ actor: actorOf(obj(e).actor), time: str(obj(e).time), what: str(obj(e).what) })),
  };
}

/** messageOf reads one message. to is the pane it was delivered to. */
export function messageOf(m) {
  const x = obj(m);
  const from = actorOf(x.from);
  return {
    id: num(x.id), from, system: x.system === true, to: str(x.to), toName: str(x.toName), toRole: str(x.toRole), time: str(x.time),
    text: str(x.text), cut: x.cut === true, read: x.read === true, seen: x.seen === true,
  };
}

/** teamOf reads the desktop's picture of a project, or null before it has sent one. */
export function teamOf(msg) {
  if (!msg || typeof msg !== "object") return null;
  const c = obj(msg.counts);
  return {
    root: str(msg.root), project: str(msg.project), hasManager: msg.hasManager === true,
    roster: list(msg.roster).filter((r) => r && typeof r.pane === "string").map((r) => ({
      pane: r.pane, name: str(r.name), role: str(r.role), status: str(r.status), unread: num(r.unread), open: num(r.open),
    })),
    counts: { open: num(c.open), messages: num(c.messages), unread: num(c.unread), unseen: num(c.unseen), tasks: obj(c.tasks) },
    tasks: list(msg.tasks).map(taskOf).filter((t) => t.id),
    openHidden: num(msg.openHidden), finishedHidden: num(msg.finishedHidden), boardError: str(msg.boardError),
    messages: list(msg.messages).map(messageOf).filter((m) => m.id),
    messagesHidden: num(msg.messagesHidden),
  };
}

/** boardOf groups tasks by state, in the order of STATES, leaving out empty groups. */
export function boardOf(tasks) {
  return STATES.map(([state, name]) => ({ state, name, tasks: tasks.filter((t) => t.state === state) })).filter((g) => g.tasks.length);
}

/** mergeTasks lays the finished tasks of a full list over a push's, by id. */
export function mergeTasks(pushed, all) {
  const seen = new Map(pushed.map((t) => [t.id, t]));
  for (const t of all) if (!seen.has(t.id)) seen.set(t.id, t);
  return [...seen.values()].sort((a, b) => taskNumber(a.id) - taskNumber(b.id));
}
export const taskNumber = (id) => Number(String(id).replace(/^\D+/, "")) || 0;

/** resultOf reads the answer to a command that changes something. */
export function resultOf(msg) {
  const x = obj(msg);
  return {
    req: str(x.req), op: str(x.op), ok: x.ok === true, error: str(x.error), warning: str(x.warning),
    told: num(x.told), pruned: num(x.pruned), sent: num(x.sent), task: x.task ? taskOf(x.task) : null,
  };
}

/** resultWords is what an answer says in a sentence, or "" when it needs nothing said. */
export function resultWords(r) {
  if (!r.ok) return r.error || "That could not be done.";
  const said = [];
  if (r.op === "msgSend") said.push(r.sent === 1 ? "Sent to 1 pane." : "Sent to " + r.sent + " panes.");
  if (r.op === "taskPrune") said.push(r.pruned === 1 ? "Removed 1 finished task." : "Removed " + r.pruned + " finished tasks.");
  if (r.told && r.op !== "msgSend") said.push(r.told === 1 ? "1 pane was told." : r.told + " panes were told.");
  if (r.warning) said.push(r.warning);
  return said.join(" ");
}

// --- commands. A command carries the project it is for and a request id the
// answer repeats. One that would be refused for what it holds is null. Each is
// written out with its name, which is also how the parity scan finds it.
export const taskGetCommand = (root, id, req) => (id ? { cmd: "taskGet", root, req, id } : null);
export const listAllCommand = (root, req) => ({ cmd: "taskList", root, req, state: "all" });
export const fullMessageCommand = (root, id, req) => (id ? { cmd: "msgList", root, req, msgId: id } : null);

/** addTaskCommand makes a task. The title is required; the others may be empty. */
export function addTaskCommand(root, form, req) {
  const title = str(form.title).trim();
  if (!title) return null;
  const c = { cmd: "taskAdd", root, req, title };
  if (str(form.body).trim()) c.spec = str(form.body).trim();
  if (str(form.role).trim()) c.role = str(form.role).trim();
  if (str(form.branch).trim()) c.branch = str(form.branch).trim();
  return c;
}

/** statusCommand moves a task. A move that needs a note is null without one. */
export function statusCommand(root, task, to, note, req) {
  const move = task.moves.find((m) => m.to === to);
  const text = str(note).trim();
  if (!move || (move.needNote && !text)) return null;
  const c = { cmd: "taskStatus", root, req, id: task.id, state: to };
  if (text) c.text = text;
  return c;
}

export function noteCommand(root, id, text, req) {
  const t = str(text).trim();
  return id && t ? { cmd: "taskNote", root, req, id, text: t } : null;
}

/** assignCommand gives a task to a role, a name or a pane id. */
export function assignCommand(root, id, target, req) {
  const t = str(target).trim();
  return id && t ? { cmd: "taskAssign", root, req, id, target: t } : null;
}

export function branchCommand(root, id, branch, req) {
  return id ? { cmd: "taskBranch", root, req, id, branch: str(branch).trim() } : null;
}

/** pruneCommand removes finished tasks not changed for days (a whole number of 1 or more). */
export function pruneCommand(root, days, req) {
  const n = Number(days);
  return Number.isInteger(n) && n >= 1 && n <= 3650 ? { cmd: "taskPrune", root, req, size: n } : null;
}

export function sendMessageCommand(root, target, text, req) {
  const to = str(target).trim();
  const t = str(text).trim();
  return to && t ? { cmd: "msgSend", root, req, target: to, text: t } : null;
}

/** seenCommand marks a message seen; id is the pane whose inbox it is in. */
export const seenCommand = (root, m, req) => (m.id && m.to ? { cmd: "msgSeen", root, req, id: m.to, msgId: m.id } : null);
