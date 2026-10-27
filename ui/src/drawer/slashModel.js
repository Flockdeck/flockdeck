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

// Whitespace, controls, format characters (zero-width, bidi) and "/" cannot be in a name.
const BAD_NAME = /[\s\p{C}\/]/u;

const cut = (s, n) => {
  const t = typeof s === "string" ? s.replace(/[\p{C}\s]+/gu, " ").trim() : "";
  return t.length > n ? t.slice(0, n - 1) + "…" : t;
};

/** cleanCommands is the agent's list as the menu may use it: { name, description, hint }, deduplicated by name. */
export function cleanCommands(list) {
  const out = [];
  if (!Array.isArray(list)) return out;
  const seen = new Set();
  for (const c of list) {
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
  const m = /^\/([^\s\/]*)$/u.exec(draft);
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
 *  Each list is capped, the agent's by what is left of SHOWN_MAX after Flockdeck's. */
export function menuOf(draft, agentCommands, own = OWN_COMMANDS) {
  const q = queryOf(draft);
  if (q === null) return null;
  const mine = pick(own, q);
  const agent = pick(cleanCommands(agentCommands), q).slice(0, Math.max(0, SHOWN_MAX - mine.length));
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
