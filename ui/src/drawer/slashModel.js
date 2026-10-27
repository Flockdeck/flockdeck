// The slash menu of a structured pane's prompt box, without the drawing. The
// agent lists its commands (the push's `commands`); typing "/" at the start of
// the box offers them, and choosing one writes "/name " into the box, so the
// agent runs it when the text is sent: ACP has no call for running a command.
// Flockdeck's own commands are a separate group and run here, not in the agent.
//
// Names, descriptions and hints came from the agent. They are cut to a length,
// the count is capped, and a name with whitespace or an invisible character is
// dropped, so that choosing a command can only write one word into the box.

/** COMMANDS_MAX is how many of the agent's commands are kept, and SHOWN_MAX how many the menu lists. */
export const COMMANDS_MAX = 200;
export const SHOWN_MAX = 40;
export const NAME_MAX = 64;
export const DESC_MAX = 160;
export const HINT_MAX = 80;
/** SCAN_MAX is how many entries of the agent's list are looked at, whatever COMMANDS_MAX keeps. */
export const SCAN_MAX = 1000;

// Whitespace, controls, format characters (zero-width, bidi), "/" and the blank-looking
// letters (Hangul fillers, braille blank) cannot be in a name; neither can a run of three
// combining marks, which stacks into a smear.
const BAD_NAME = /[\s\p{C}/ㅤᅟᅠﾠ⠀]|\p{M}{3}/u;

// cut reads at most n+1 characters of s, so a huge text costs no more than a short one,
// and never leaves half of a surrogate pair.
const cut = (s, n) => {
  const t = typeof s === "string" ? s.slice(0, n + 1).replace(/[\p{C}\s]+/gu, " ").trim() : "";
  const cps = Array.from(t);
  return cps.length > n ? cps.slice(0, n - 1).join("") + "…" : t;
};

/** cleanCommands is the agent's list as the menu may use it: { name, description, hint }, deduplicated by name. */
export function cleanCommands(list) {
  const out = [];
  if (!Array.isArray(list)) return out;
  const seen = new Set();
  for (let i = 0; i < list.length && i < SCAN_MAX; i++) {
    const c = list[i];
    if (out.length >= COMMANDS_MAX) break;
    if (!c || typeof c.name !== "string") continue;
    const name = c.name.startsWith("/") ? c.name.slice(1) : c.name;
    if (!name || name.length > NAME_MAX || BAD_NAME.test(name) || seen.has(name)) continue;
    seen.add(name);
    out.push({ name, description: cut(c.description, DESC_MAX), hint: cut(c.hint, HINT_MAX) });
  }
  return out;
}

/** OWN_COMMANDS are Flockdeck's own; id says what the prompt box does for each. */
export const OWN_COMMANDS = [{ name: "stop", id: "stop", description: "Stop the agent's current turn" }];

/** queryOf is what has been typed after the "/" while the box holds only a command word, or null when the menu is not wanted. */
export function queryOf(draft) {
  if (typeof draft !== "string" || draft.length > NAME_MAX + 1) return null;
  const m = /^\/([^\s/]*)$/u.exec(draft);
  return m ? m[1] : null;
}

const rank = (name, q) => {
  const n = name.toLowerCase();
  if (n === q) return 0;
  if (n.startsWith(q)) return 1;
  return n.includes(q) ? 2 : -1;
};

const pick = (list, q) => {
  const lower = q.toLowerCase();
  const hits = [];
  list.forEach((c, i) => {
    const r = rank(c.name, lower);
    if (r >= 0) hits.push({ c, r, i });
  });
  hits.sort((a, b) => a.r - b.r || a.i - b.i);
  return hits.map((h) => h.c);
};

/** menuOf is the menu for a draft: { query, own, agent } with the commands that match, or null when there is none to show.
 *  An agent command with the name of one of Flockdeck's is not listed: the name would be ambiguous.
 *  Each list is capped, the agent's by what is left of SHOWN_MAX after Flockdeck's. */
export function menuOf(draft, agentCommands, own = OWN_COMMANDS) {
  const q = queryOf(draft);
  if (q === null) return null;
  const mine = pick(own, q);
  const taken = new Set(own.map((c) => c.name.toLowerCase()));
  const agent = pick(cleanCommands(agentCommands).filter((c) => !taken.has(c.name.toLowerCase())), q).slice(0, Math.max(0, SHOWN_MAX - mine.length));
  if (!mine.length && !agent.length) return null;
  return { query: q, own: mine, agent };
}

/** flatten is the menu's items in the order the keyboard moves through them, each marked with its group. */
export function flatten(menu) {
  if (!menu) return [];
  return [...menu.own.map((c) => ({ ...c, group: "own" })), ...menu.agent.map((c) => ({ ...c, group: "agent" }))];
}

/** insertText is what choosing an agent command writes into the box. */
export const insertText = (c) => "/" + c.name + " ";

/** keyOf names an item for the selection, so it follows the command when the list changes. */
export const keyOf = (it) => it.group + ":" + it.name;

/** selectedIndex is the index in items of the chosen row. A person's choice (key) is kept while its row is
 *  listed. Without one: a Flockdeck command whose name is exactly what was typed, else the first of the
 *  agent's. Flockdeck's commands are never the default for a partial match, so Enter on "/p" cannot run
 *  /stop. -1 when nothing is chosen. */
export function selectedIndex(items, key, query) {
  const kept = key ? items.findIndex((it) => keyOf(it) === key) : -1;
  if (kept >= 0) return kept;
  const q = (query || "").toLowerCase();
  const exact = q === "" ? -1 : items.findIndex((it) => it.group === "own" && it.name.toLowerCase() === q);
  if (exact >= 0) return exact;
  return items.findIndex((it) => it.group === "agent");
}
