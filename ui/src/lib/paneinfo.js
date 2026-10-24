// The pure parts of pane info, find pane and the transcript questions: reading
// the desktop's answers defensively, and the sentences and commands for recording
// and exporting.
//
// Pane info carries ids, process ids and file paths, so the desktop answers it to
// a window on its own machine only. Every value is drawn as text; some came from
// an agent (a pane name, a peer name).

/** DESK_FILES_WORDS is what a window reached through the relay says for the
 *  things that show a file or an id of the machine Flockdeck runs on. The page
 *  sends nothing for them. */
export const DESK_FILES_WORDS = "This is shown on the machine Flockdeck runs on, not from a window reached through the relay.";

/** NOT_SET is what a value a pane does not have yet reads as. */
export const NOT_SET = "not set";

/** REVEAL_NONE_WORDS says why a pane has no transcript file to show. */
export const REVEAL_NONE_WORDS = "Nothing to show yet. This pane has no recording or export. Start recording or Export transcript makes one.";

/** STRUCTURED_RECORD_WORDS says why a structured pane cannot be recorded or
 *  exported from here. */
export const STRUCTURED_RECORD_WORDS = "A structured pane has no terminal transcript to record or export.";

const text = (v) => (typeof v === "string" ? v : "");

/** infoOf reads a paneInfo message into the pane's name, the labelled values in
 *  the order sent, and the reason the transcript file cannot be made, if given. */
export function infoOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  const fields = [];
  for (const f of Array.isArray(m.fields) ? m.fields : []) {
    if (!f || typeof f !== "object" || typeof f.label !== "string" || !f.label) continue;
    fields.push({ label: f.label, value: text(f.value), note: text(f.note) });
  }
  return { id: text(m.id), name: text(m.name), fields, transcriptOff: text(m.transcriptOff) };
}

/** infoText is the whole list as plain text, one "Label: value" line each, with
 *  "not set" where a pane has no value yet. */
export function infoText(fields) {
  return fields.map((f) => f.label + ": " + (f.value || NOT_SET)).join("\n");
}

/** matchesOf reads a paneMatches message. A row without a pane id is left out. */
export function matchesOf(msg) {
  const m = msg && typeof msg === "object" ? msg : {};
  const items = [];
  for (const r of Array.isArray(m.items) ? m.items : []) {
    if (!r || typeof r !== "object" || typeof r.paneId !== "string" || !r.paneId) continue;
    const matched = [];
    for (const f of Array.isArray(r.matched) ? r.matched : []) {
      if (f && typeof f === "object" && typeof f.label === "string") matched.push({ label: f.label, value: text(f.value) });
    }
    items.push({ paneId: r.paneId, tabId: text(r.tabId), root: text(r.root), name: text(r.name), project: text(r.project), tab: text(r.tab), matched });
  }
  return { query: text(m.query), items };
}

/** whereOf is where a found pane is: its project and tab. */
export const whereOf = (r) => [r.project, r.tab].filter(Boolean).join(" · ");

/** whyOf says what of a found pane held the words typed. */
export const whyOf = (r) => (r.matched.length ? "Matched " + r.matched.map((f) => f.label.toLowerCase() + " " + f.value).join(", ") : "");

/** revealCommand is what choosing a found pane sends: the desktop brings the pane
 *  to the front, switching project and tab as it needs to. */
export const revealCommand = (r) => ({ cmd: "revealPane", root: r.root, node: r.tabId, id: r.paneId });

/** RECORD_WORDS is what the person is told before the first recording ever. */
export const RECORD_WORDS = [
  "Flockdeck will write a transcript of this pane's conversation to a file on this machine: your prompts, the agent's messages, and the tools it runs and what they print, from the start of the conversation and as it goes on, until you stop.",
  "It is made from the conversation the agent stores itself, so an agent that stores none has nothing to record.",
  "A transcript stops at 16 MiB, and a conversation already longer than that is cut there.",
  "It can contain secrets. Flockdeck removes common ones and clips very long output, but that is best effort and not a guarantee.",
  "Nothing is written into your project and nothing leaves this machine. Recordings older than 30 days are deleted, and the oldest go sooner once a project has more than 100 of them or 256 MiB.",
];

/** EXPORT_WORDS is what the person is told every time before an export. */
export const EXPORT_WORDS = [
  "Flockdeck will write the whole conversation this pane's agent has stored (your prompts, the agent's messages, and the tools it runs and what they print) to a file on this machine, in the same format as a recording. It works whether or not recording is on.",
  "It can contain secrets. Flockdeck removes common ones and clips very long output, but that is best effort and not a guarantee.",
  "Nothing is written into your project and nothing leaves this machine. Exports are kept until you delete them yourself.",
  "An agent that stores no conversation Flockdeck can read has nothing to export, and nothing is written. A conversation longer than 16 MiB is cut there.",
];

/** acknowledged says whether the person has already been told what recording
 *  keeps, from the preferences of the hello. */
export const acknowledged = (prefs) => !!(prefs && prefs.recordingAcknowledged === true);

/** askFor is the question before a recording or an export, or null when none is
 *  needed: stopping a recording, and starting one after the first, go at once. */
export function askFor(kind, ack) {
  if (kind === "export") return { title: "Export this pane's transcript?", words: EXPORT_WORDS, label: "Export transcript" };
  if (kind === "record" && !ack) return { title: "Record this pane's agent interaction?", words: RECORD_WORDS, label: "Start recording" };
  return null;
}

/** transcriptCommand is the command that carries out a request once it is answered
 *  (or needed no answer). Both sides check the confirmation, so a request that was
 *  asked about says so. */
export function transcriptCommand(kind, pane, { on = true, confirmed = false } = {}) {
  if (kind === "export") return { cmd: "exportTranscript", id: pane, confirmed: true };
  const cmd = { cmd: "recordPane", id: pane, recording: !!on };
  if (confirmed) cmd.confirmed = true;
  return cmd;
}

/** canTranscribe says whether a pane can be recorded or exported from this
 *  window: an agent with a terminal, not a shell and not a structured pane. */
export function canTranscribe(node, pane) {
  if (!node) return false;
  if (node.shell || (pane && pane.shell)) return false;
  return !(node.structured || node.kind === "structured" || (pane && pane.structured));
}
