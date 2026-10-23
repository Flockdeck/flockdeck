// What the Updates dialog draws and sends, without a DOM: release notes as blocks
// of words, the address of a release page, the label of an install button, and
// the commands. Release notes are text from a published release, so they are
// shaped here into a handful of block kinds and the view builds elements from
// them; nothing is ever parsed as markup.

/** RELAY_WORDS is what a window reached through the relay says in place of the
 *  controls that change what the machine runs. */
export const RELAY_WORDS = "Updating, detaching and quitting are done on the machine Flockdeck runs on, not from a window reached through the relay.";

/** QUIT_QUESTION is the key table's own question before quit, or this fallback
 *  for a table that has none. */
export const QUIT_QUESTION = "Stop every agent in every open project?";

/** updateOf is the update the state push names, or null: { version, notes, url }
 *  with every field text. A window that cannot act on it (remote) gets none. */
export function updateOf(state, remote) {
  const u = !remote && state && state.update;
  if (!u || typeof u.version !== "string" || !u.version) return null;
  return { version: u.version, notes: typeof u.notes === "string" ? u.notes : "", url: typeof u.url === "string" ? u.url : "" };
}

/** webUrl is the address when it is an http(s) one, else "". A link to a
 *  release is never anything else. */
export function webUrl(u) {
  try {
    const url = new URL(String(u || ""));
    return url.protocol === "https:" || url.protocol === "http:" ? url.href : "";
  } catch {
    return "";
  }
}

/** inlineOf splits a line into runs: plain text, **bold** and `code`. */
export function inlineOf(s) {
  const out = [];
  const re = /\*\*(.+?)\*\*|`(.+?)`/g;
  let last = 0;
  let m;
  while ((m = re.exec(s))) {
    if (m.index > last) out.push({ t: "text", text: s.slice(last, m.index) });
    out.push(m[1] !== undefined ? { t: "strong", text: m[1] } : { t: "code", text: m[2] });
    last = re.lastIndex;
  }
  if (last < s.length) out.push({ t: "text", text: s.slice(last) });
  return out;
}

/** notesBlocks turns a release note into the shapes docs/releases/*.md uses:
 *  headings, bullet lists and paragraphs, with bold and code inside a line. A
 *  heading's level is its markdown level plus two, at most 6, so nothing in a
 *  note outranks the dialog's own title. */
export function notesBlocks(text) {
  const lines = String(text || "").replace(/\r\n?/g, "\n").split("\n");
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      out.push({ kind: "heading", level: Math.min(6, heading[1].length + 2), runs: inlineOf(heading[2].trim()) });
      i++;
      continue;
    }
    if (/^[-*]\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
        items.push(inlineOf(lines[i].replace(/^[-*]\s+/, "")));
        i++;
      }
      out.push({ kind: "list", items });
      continue;
    }
    const para = [];
    while (i < lines.length && lines[i].trim() && !/^#{1,6}\s/.test(lines[i]) && !/^[-*]\s+/.test(lines[i])) {
      para.push(lines[i].trim());
      i++;
    }
    out.push({ kind: "para", runs: inlineOf(para.join(" ")) });
  }
  return out;
}

/** installLabel is the words on a version's button. One click downloads and
 *  stages the release at once, so it says download; restarting onto it is a
 *  separate step. */
export function installLabel(relation) {
  return relation === "newer" ? "Download update" : relation === "older" ? "Download older version" : "Download again";
}

/** versionsOf reads the desktop's listVersions answer into { items, error }, or
 *  null before one has come. An item without a version is dropped. */
export function versionsOf(msg) {
  if (!msg) return null;
  const items = (Array.isArray(msg.items) ? msg.items : [])
    .filter((v) => v && typeof v.version === "string" && v.version)
    .map((v) => ({
      version: v.version,
      notes: typeof v.notes === "string" ? v.notes : "",
      url: typeof v.url === "string" ? v.url : "",
      published: typeof v.published === "string" ? v.published : "",
      relation: typeof v.relation === "string" ? v.relation : "",
    }));
  return { items, error: typeof msg.error === "string" ? msg.error : "" };
}

/** dateOf is a published time as a date, or "" for one that is not a time. */
export function dateOf(s) {
  const d = new Date(s);
  return s && !Number.isNaN(d.getTime()) ? d.toISOString().slice(0, 10) : "";
}

/** quitQuestion is the key table's question for the quit action, or the fallback. */
export function quitQuestion(keys) {
  const k = (keys || []).find((x) => x && x.id === "quit");
  return (k && k.confirm) || QUIT_QUESTION;
}

export const checkCommand = () => ({ cmd: "checkForUpdate" });
export const versionsCommand = () => ({ cmd: "listVersions" });
export const installCommand = (version) => ({ cmd: "installVersion", text: version });
export const restartCommand = () => ({ cmd: "restart" });
export const updatesCommand = (off) => ({ cmd: "updates", kind: off ? "off" : "on" });
