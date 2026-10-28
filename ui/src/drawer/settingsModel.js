// The mode, model and settings of a structured pane, without the drawing. The
// agent lists what it offers in the push (`modes`, `models`, `configOptions`);
// this turns that into the selectors in the pane's header and into the values
// the /mode and /model commands may take. Everything here came from the agent:
// it is cut to a length, counted and shown as text only, and only a value that
// is in the list the push carries can be sent. The desk checks the same list
// again, so a value the agent has since replaced is refused there too.

export const CONTROLS_MAX = 32;
export const CHOICES_MAX = 500;
export const LABEL_MAX = 80;
export const VALUE_MAX = 256;

// cut reads at most n+1 characters of s, so a huge text costs no more than a short one,
// and never leaves half of a surrogate pair.
const cut = (s, n) => {
  const t = typeof s === "string" ? s.slice(0, n + 1).replace(/[\p{C}\s]+/gu, " ").trim() : "";
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

// A value is sent as the agent wrote it, so one that would have to be cut or cleaned is not offered.
const plain = (v) => typeof v === "string" && v !== "" && v.length <= VALUE_MAX && !/[\p{C}\r\n]/u.test(v);

const ASKED_MODES = ["accept-edits", "acceptedits", "accept_edits", "accept edits"];
const UNASKED_WORDS = ["yolo", "bypass", "dangerous", "auto-approve", "auto approve", "autoapprove", "accept-all", "accept all", "acceptall",
  "full-access", "full access", "full_access", "fullaccess", "dontask", "dont-ask", "don't ask", "without asking", "without approval",
  "without permission", "without prompting", "never ask", "no approval", "skip permission"];

/** looksUnasked says a choice, by its id, name and description, reads as one that lets the agent run commands or
 *  edit files without asking. It mirrors LooksUnasked in internal/structured/settings.go, which marks the pane. A
 *  guess from words: a change that reads this way is confirmed before it is sent. */
export function looksUnasked(id, name, description) {
  const head = (String(id || "") + " " + String(name || "")).toLowerCase();
  if (ASKED_MODES.some((w) => head.includes(w))) return false;
  const all = head + " " + String(description || "").toLowerCase();
  return UNASKED_WORDS.some((w) => all.includes(w));
}

function choicesOf(list, valueKey) {
  const out = [];
  const seen = new Set();
  if (!Array.isArray(list)) return out;
  for (let i = 0; i < list.length && i < CHOICES_MAX; i++) {
    const c = list[i];
    if (!c || !plain(c[valueKey]) || seen.has(c[valueKey])) continue;
    seen.add(c[valueKey]);
    out.push({ value: c[valueKey], label: cut(c.name, LABEL_MAX) || cut(c[valueKey], LABEL_MAX), group: cut(c.group, LABEL_MAX), description: cut(c.description, 160),
      unasked: looksUnasked(c[valueKey], c.name, c.description) });
  }
  return out;
}

/** controlsOf is the selectors the push calls for: { key, kind, setting, label, category, current, choices }.
 *  kind is "mode" or "model" (the blocks Gemini and the Claude and Codex adapters send) or "config" (a
 *  config option, whose id is setting). An option that is not a select or a boolean, or has nothing to
 *  choose, is left out. */
export function controlsOf(st) {
  const out = [];
  if (!st || typeof st !== "object") return out;
  const block = (b, kind, label, key) => {
    if (!b || typeof b !== "object") return;
    const choices = choicesOf(b.available, key);
    if (choices.length) out.push({ key: kind, kind, setting: "", label, category: kind, current: typeof b.current === "string" ? b.current : "", choices });
  };
  block(st.modes, "mode", "Mode", "id");
  block(st.models, "model", "Model", "id");
  const opts = Array.isArray(st.configOptions) ? st.configOptions : [];
  for (let i = 0; i < opts.length && out.length < CONTROLS_MAX; i++) {
    const o = opts[i];
    if (!o || !plain(o.id)) continue;
    let choices;
    if (o.type === "boolean") {
      choices = [{ value: "true", label: "On", group: "", description: "", unasked: false }, { value: "false", label: "Off", group: "", description: "", unasked: false }];
    } else if (o.type === "select") {
      choices = choicesOf(o.values, "value");
    } else continue;
    if (!choices.length) continue;
    out.push({ key: "config:" + o.id, kind: "config", setting: o.id, label: cut(o.name, LABEL_MAX) || cut(o.id, LABEL_MAX), category: cut(o.category, 40), current: typeof o.current === "string" ? o.current : "", choices });
  }
  return out.slice(0, CONTROLS_MAX);
}

/** controlFor is the control a Flockdeck command (/mode, /model) works on: the block of that name, else the
 *  first config option of that category. null when the agent offers none. */
export function controlFor(controls, name) {
  return controls.find((c) => c.kind === name) || controls.find((c) => c.kind === "config" && c.category === name) || null;
}

/** offered says whether value is one of the control's choices. */
export const offered = (control, value) => !!control && typeof value === "string" && control.choices.some((c) => c.value === value);

/** setMessage is the command that gives control the value, or null when the value is not on offer. */
export function setMessage(pane, control, value, req) {
  if (!offered(control, value)) return null;
  return { cmd: "structuredSet", id: pane, kind: control.kind, setting: control.setting, value, req };
}

/** ownCommandsFor adds /mode and /model to Flockdeck's commands for the ones the agent offers. */
export function ownCommandsFor(own, controls, rewind = false) {
  const out = [...own];
  if (rewind) out.push({ name: "rewind", id: "rewind", description: "Go back to an earlier prompt" }, { name: "fork", id: "fork", description: "Start a new pane from an earlier point" });
  if (controlFor(controls, "mode")) out.push({ name: "mode", id: "mode", description: "Choose the agent's mode" });
  if (controlFor(controls, "model")) out.push({ name: "model", id: "model", description: "Choose the agent's model" });
  return out;
}

/** parseSetting reads a draft such as "/mode plan" or "/model Big". It returns null for any other text,
 *  { control, value } when the word matches a choice's value or label (case does not matter), and
 *  { control, error } with the choices when it does not. A command with no word after it is null: the
 *  menu handles that. */
export function parseSetting(draft, controls) {
  const m = typeof draft === "string" && draft.length <= VALUE_MAX + 16 ? /^\/(mode|model)\s+(\S.*?)\s*$/u.exec(draft) : null;
  if (!m) return null;
  const control = controlFor(controls, m[1]);
  if (!control) return null;
  const want = m[2].toLowerCase();
  const hit = control.choices.find((c) => c.value.toLowerCase() === want) || control.choices.find((c) => c.label.toLowerCase() === want);
  if (hit) return { control, value: hit.value };
  const names = control.choices.slice(0, 12).map((c) => c.label).join(", ");
  return { control, error: "No " + control.label.toLowerCase() + " called that. Choices: " + names + (control.choices.length > 12 ? ", …" : "") + "." };
}
