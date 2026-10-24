// What the helper apps dialog reads and sends, without a DOM. A helper is a
// small program Flockdeck downloads, checks, starts and stops. The rows and the
// plan come from the desktop; every field is read as text (or a list of text)
// and a field of another kind is dropped, so the view can put each one in an
// element as a child and never as markup.

/** RELAY_WORDS is what a window reached through the relay says in place of the
 *  dialog: helpers install a program on, and open a browser on, the machine
 *  Flockdeck runs on. */
export const RELAY_WORDS = "Helper apps are managed on the machine Flockdeck runs on, not from a window reached through the relay.";

/** SAFETY_WORDS is said once, over the list and under a plan: what a helper is
 *  not protected from. It is the old dialog's own sentence. */
export const SAFETY_WORDS =
  "Any program running as you can read and write a helper's files and reach it on this computer, and any account on this computer can reach it with no sign-in. It is not sandboxed.";

/** INTRO_WORDS opens the list. */
export const INTRO_WORDS = "Helpers are small programs Flockdeck downloads, checks, starts and stops for you. Each one is opened in your browser.";

/** SIGNATURE_WORDS says how a release was checked. */
export const SIGNATURE_WORDS = "manifest.json is signed with the Flockdeck release key; the archive is checked against it when it is downloaded";

/** STATES are the words and marks for every state the desktop reports. A state
 *  not in the table is shown as its own text with the plain mark. */
const STATES = {
  notinstalled: ["○", "Not installed"],
  installing: ["◐", "Installing"],
  stopped: ["○", "Stopped"],
  starting: ["◐", "Starting"],
  running: ["●", "Running"],
  stopping: ["◐", "Stopping"],
  unresponsive: ["▲", "Unresponsive"],
  failed: ["▲", "Failed"],
};

/** stateWords is the mark and the word for a state. */
export function stateWords(state) {
  return STATES[state] || ["○", String(state || "")];
}

const text = (v) => (typeof v === "string" ? v : "");
const list = (v) => (Array.isArray(v) ? v.filter((x) => typeof x === "string") : []);

/** rowsOf reads the desktop's helpers message into a list of rows, or null
 *  before one has come. A row without an id is dropped. */
export function rowsOf(msg) {
  if (!msg || !Array.isArray(msg.rows)) return null;
  return msg.rows
    .filter((r) => r && typeof r === "object" && typeof r.id === "string" && r.id)
    .map((r) => ({
      id: r.id,
      name: text(r.name) || r.id,
      summary: text(r.summary),
      installed: text(r.installed),
      state: text(r.state) || "notinstalled",
      url: text(r.url),
      error: text(r.error),
      log: list(r.log),
      owner: text(r.owner),
      update: text(r.update),
      allows: list(r.allows),
      dataDir: text(r.dataDir),
      hasData: r.hasData === true,
      needsRepair: r.needsRepair === true,
    }));
}

/** planOf reads a helperPlan message. It is an error, a refusal with no way
 *  round when fatal is set, or a plan with everything the person is shown before
 *  anything is downloaded. */
export function planOf(msg) {
  if (!msg || typeof msg.id !== "string") return null;
  return {
    id: msg.id,
    name: text(msg.name),
    summary: text(msg.summary),
    version: text(msg.version),
    url: text(msg.url),
    sha256: text(msg.sha256),
    installed: text(msg.installed),
    repair: msg.repair === true,
    allows: list(msg.allows),
    error: text(msg.error),
    fatal: msg.fatal === true,
  };
}

/** buttonsFor is the verbs a row offers, in order, for its state. Each is
 *  { verb, label, primary }. A helper that is installing or stopping offers none
 *  until it settles. */
export function buttonsFor(row) {
  const out = [];
  switch (row.state) {
    case "notinstalled":
      out.push({ verb: "install", label: "Install…", primary: true });
      if (row.hasData) out.push({ verb: "delete", label: "Delete data…" });
      break;
    case "stopped":
    case "failed":
      out.push({ verb: "start", label: row.state === "failed" ? "Try again" : "Start", primary: true });
      if (row.update) out.push({ verb: "update", label: "Update…" });
      if (row.needsRepair) out.push({ verb: "repair", label: "Repair…", primary: true });
      out.push({ verb: "remove", label: "Remove…" });
      break;
    case "starting":
    case "unresponsive":
      out.push({ verb: "stop", label: "Stop" });
      break;
    case "running":
      out.push({ verb: "open", label: "Open", primary: true });
      out.push({ verb: "stop", label: "Stop" });
      break;
    default:
      break;
  }
  return out;
}

/** announcement is what a screen reader is told when a helper changes state:
 *  nothing for the first time a helper is seen or for no change. heard is a Map
 *  from id to the last word said, and is updated. */
export function announcement(rows, heard) {
  const said = [];
  for (const r of rows) {
    const word = stateWords(r.state)[1].toLowerCase();
    const before = heard.get(r.id);
    heard.set(r.id, word);
    if (before === undefined || before === word) continue;
    said.push(r.name + " is " + word + (r.state === "failed" && r.error ? ": " + r.error : ""));
  }
  return said.join(". ");
}

export const listCommand = (force) => (force ? { cmd: "helpers", force: true } : { cmd: "helpers" });
export const planCommand = (id, version = "") => ({ cmd: "helperPlan", id, text: version });
/** installCommand carries what the person was shown: the version and the hash of
 *  its archive. The desktop looks the release up again and refuses to install it
 *  if either is different. */
export const installCommand = (plan) => ({ cmd: "helperInstall", id: plan.id, text: plan.version, sha256: plan.sha256, confirmed: true });
export const startCommand = (id) => ({ cmd: "helperStart", id });
export const stopCommand = (id) => ({ cmd: "helperStop", id });
export const openCommand = (id) => ({ cmd: "helperOpen", id });
export const uninstallCommand = (id, purge) => ({ cmd: "helperUninstall", id, purge: !!purge });
