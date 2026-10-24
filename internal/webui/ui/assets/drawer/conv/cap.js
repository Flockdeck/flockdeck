// Cutting text to a size in bytes, and saying so.
const encoder = new TextEncoder();
const decoder = new TextDecoder("utf-8");

/** capBytes returns the first max bytes of text (UTF-8) and how many bytes are left out.
 *  A character cut in half at the end is dropped, not shown as a broken one. */
export function capBytes(text, max) {
  const s = typeof text === "string" ? text : "";
  // A string of n UTF-16 units is never more than 3n bytes, so a short one needs no encoding.
  if (s.length * 3 <= max) return { text: s, more: 0 };
  const bytes = encoder.encode(s);
  if (bytes.length <= max) return { text: s, more: 0 };
  let end = max;
  // Back up to the start of a character: a continuation byte is 10xxxxxx.
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  return { text: decoder.decode(bytes.subarray(0, end)), more: bytes.length - end };
}

/** cutWords says plainly that text was cut and by how much. */
export function cutWords(what, more) {
  return what + " truncated: " + more.toLocaleString("en-US") + (more === 1 ? " byte" : " bytes") + " more " + (more === 1 ? "is" : "are") + " not shown.";
}
