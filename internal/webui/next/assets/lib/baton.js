// What the baton dialog reads and sends, without a DOM. A baton is the notes one
// agent leaves for the next: the desktop drafts it from a pane's work, the person
// edits the text, and it is kept, started in a new pane, or used to start this
// pane over. The draft holds what an agent worked out, so it is drawn as text in
// a box and sent back whole; nothing here parses it as markup.
//
// The relay never carries a draft: the desktop refuses makeBaton to a window
// reached through the relay and the store does not send it there.
import { optionsOf, splitValue, valueOf } from "./lifecycle.js";

/** WAIT_MS is how long a command is waited for before the dialog is given back. */
export const WAIT_MS = 60000;

/** SCRUB_WORDS says what the scrubber is. It is a filter, and the dialog says so
 *  next to the count of what it removed. */
export const SCRUB_WORDS = "Scrubbing is a filter, not a guarantee: a secret it does not know and that does not look like one gets through. Read the whole baton before you use it.";

/** DIRTY_WORDS is said when a worktree is chosen for a pane with uncommitted work. */
export const DIRTY_WORDS = "This pane has uncommitted changes. A new worktree starts from the branch and does not have them. The baton lists them under Not in your checkout.";

/** RELAY_WORDS is what a window reached through the relay says in place of the
 *  dialog. */
export const RELAY_WORDS = "A baton is made on the machine Flockdeck runs on, not from a window reached through the relay.";

/** STILL_OUT_WORDS refuses a command that is already out for the same pane. */
export const STILL_OUT_WORDS = "That is still being done. Wait for Flockdeck's answer, then try again.";

/** The marks the scrubber makes when the draft does not say which kinds it uses. */
const DEFAULT_MARK = /\[REDACTED: [a-z0-9-]+\]/g;

const text = (v) => (typeof v === "string" ? v : "");
const escape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** draftOf reads a batonDraft message. Every field is text, a flag or a list of
 *  text; a field of any other kind is dropped. */
export function draftOf(msg) {
  if (!msg || typeof msg !== "object" || typeof msg.paneId !== "string") return null;
  const agents = (Array.isArray(msg.agents) ? msg.agents : [])
    .filter((a) => a && typeof a === "object" && typeof a.id === "string" && a.id)
    .map((a) => ({
      id: a.id,
      name: text(a.name) || a.id,
      provider: text(a.provider),
      models: (Array.isArray(a.models) ? a.models : []).filter((m) => m && typeof m.id === "string").map((m) => ({ id: m.id, name: text(m.name) })),
      defaultModel: text(a.default),
      unavailable: text(a.unavailable),
      install: text(a.install),
    }));
  return {
    paneId: msg.paneId,
    req: text(msg.req),
    name: text(msg.name),
    agent: text(msg.agent),
    model: text(msg.model),
    provider: text(msg.provider),
    agents,
    text: text(msg.text),
    notes: (Array.isArray(msg.notes) ? msg.notes : []).filter((n) => typeof n === "string"),
    dirty: msg.dirty === true,
    isRepo: msg.isRepo === true,
    markKinds: (Array.isArray(msg.markKinds) ? msg.markKinds : []).filter((k) => typeof k === "string" && k),
  };
}

/** catalogOf is the draft's agents in the shape the start dialog's picker reads,
 *  so the two choose an agent and a model the same way. */
export function catalogOf(draft) {
  return {
    items: draft.agents.map((a) => ({
      id: a.id, name: a.name, models: a.models, defaultModel: a.defaultModel, available: !a.unavailable, install: a.install,
    })),
    project: null,
    default: null,
  };
}

/** choices are the entries of the picker for the new pane, and whether there is
 *  anything to choose between: more than one agent or model. */
export function choices(draft) {
  const options = optionsOf(catalogOf(draft));
  return { options, choosable: options.length > 1 };
}

/** pickOf is the agent and model the new pane runs: the picker's entry when there
 *  is one to choose from, else the pane's own. */
export function pickOf(draft, value, choosable) {
  if (choosable && value) {
    const [agent, model] = splitValue(value);
    if (agent) return { agent, model };
  }
  return { agent: draft.agent, model: draft.model };
}

/** startValue is the entry the picker starts on: the pane's own agent and model. */
export const startValue = (draft) => valueOf(draft.agent, draft.model);

/** providerCheck says whether starting on an agent would send the baton to
 *  another company, and how to say so. A different agent whose company is not
 *  known is treated as another company: it is asked about, not let through. */
export function providerCheck(draft, agent) {
  if (!agent || agent === draft.agent) return { differs: false, unknown: false, there: "", from: draft.provider };
  const there = (draft.agents.find((a) => a.id === agent) || {}).provider || "";
  const unknown = !there || !draft.provider;
  return { differs: unknown || there !== draft.provider, unknown, there, from: draft.provider };
}

/** providerWords is the sentence for a provider check that differs. */
export function providerWords(check) {
  return check.unknown
    ? "This sends the baton to an agent whose company Flockdeck does not know."
    : "This sends the baton to " + check.there + ", not " + check.from + ".";
}

/** marksIn counts the removed values in the text, as the scrubber marks them,
 *  with the kinds the draft names when it names any. */
export function marksIn(body, kinds) {
  const re = kinds && kinds.length ? new RegExp("\\[REDACTED: (?:" + kinds.map(escape).join("|") + ")\\]", "g") : DEFAULT_MARK;
  return (String(body || "").match(re) || []).length;
}

/** marksWords is the count of removed values as words. */
export const marksWords = (n) => (n === 1 ? "1 secret removed" : n + " secrets removed");

/** problemWith says what stands in the way of a start, or "". */
export function startProblem({ body, worktree, branch, check, ack }) {
  if (!String(body || "").trim()) return "The baton is empty.";
  if (worktree && !String(branch || "").trim()) return "Name a branch for the worktree first";
  if (check.differs && !ack) return "Tick Send it there first";
  return "";
}

/** savedWords is the sentence for an answered command: what was done, the baton's
 *  id, and how many values the scrub removed. */
export function savedWords(msg) {
  const what = msg.cmd === "restartWithBaton" ? "Restarted this pane from baton " : msg.cmd === "startFromBaton" ? "Started a new pane from baton " : "Saved baton ";
  const n = Number(msg.scrubbed) || 0;
  return what + text(msg.id) + (n ? " (" + n + (n === 1 ? " secret" : " secrets") + " removed)" : "") + ".";
}

/** inflight is the commands sent and not answered, by request id. A command stays
 *  here after the dialog was given back or closed, until the desktop says it is
 *  done or failed (or the connection drops): a slow worktree can still be
 *  starting an agent, and sending the same command again would start another. */
export function createInflight() {
  const out = new Map();
  let count = 0;
  return {
    next: () => "b" + ++count,
    add: (req, cmd, paneId) => out.set(req, { cmd, paneId }),
    get: (req) => out.get(req) || null,
    delete: (req) => out.delete(req),
    clear: () => out.clear(),
    size: () => out.size,
    /** same says whether this command for this pane is still out. */
    same: (cmd, paneId) => [...out.values()].some((f) => f.cmd === cmd && f.paneId === paneId),
  };
}

export const makeCommand = (id, req) => ({ cmd: "makeBaton", id, req });
const rest = (d, req, body, task) => ({ id: d.paneId, req, text: body, task: String(task || "").trim() });
export const saveCommand = (d, req, body, task) => ({ cmd: "saveBaton", ...rest(d, req, body, task) });
export const restartCommand = (d, req, body, task) => ({ cmd: "restartWithBaton", ...rest(d, req, body, task) });
export const startCommand = (d, req, body, task, { agent, model, beside, branch, confirmed }) => ({
  cmd: "startFromBaton", ...rest(d, req, body, task), agent, model, split: !!beside, branch: String(branch || "").trim(), confirmed: !!confirmed,
});
