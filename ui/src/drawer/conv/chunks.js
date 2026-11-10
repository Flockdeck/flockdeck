// Long text drawn in pieces. A row that shows "all" of a long reply, prompt or tool
// output draws it as several nodes of about CHUNK characters each, never as one
// giant node, and stops at a ceiling: past it the row says how much is left out and
// Copy still gives the whole text.

/** chunkText cuts text into pieces of about size characters. A piece ends at a line
 *  break when there is one in its last quarter, so lines stay whole. A Markdown
 *  piece (markdown true) ends only at a blank line outside a code fence, so a
 *  fence or a paragraph is not split; a piece with none is cut at the size. */
export function chunkText(text, size, markdown = false) {
  const s = typeof text === "string" ? text : "";
  const out = [];
  let from = 0;
  while (from < s.length) {
    if (s.length - from <= size) {
      out.push(s.slice(from));
      break;
    }
    let end = from + size;
    const cut = markdown ? markdownCut(s, from, end) : lineCut(s, from, end);
    if (cut > from) end = cut;
    // Do not split a surrogate pair.
    const c = s.charCodeAt(end - 1);
    if (c >= 0xd800 && c <= 0xdbff) end++;
    out.push(s.slice(from, end));
    from = end;
  }
  return out;
}

function lineCut(s, from, end) {
  const at = s.lastIndexOf("\n", end - 1);
  return at > from + (end - from) * 0.75 ? at + 1 : -1;
}

// The last blank line before end that is outside a code fence, counting fences from from.
function markdownCut(s, from, end) {
  let fenced = false;
  let best = -1;
  let i = from;
  while (i < end) {
    let nl = s.indexOf("\n", i);
    if (nl < 0 || nl >= end) break;
    const line = s.slice(i, nl);
    if (/^\s{0,3}(```|~~~)/.test(line)) fenced = !fenced;
    else if (!fenced && line.trim() === "" && nl + 1 > from + (end - from) / 2) best = nl + 1;
    i = nl + 1;
  }
  return best;
}
