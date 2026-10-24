// The key table, from the files the page carries. keys.json under /assets/ is
// the table as built (ui/help writes it), and the hello adds only what this
// person and this window change: the actions they remapped (keyOverrides) and
// the ones a window reached through the relay is not offered (keysHidden). A
// server without the keyOverrides cap sends the whole table in the hello, and
// the page reads that as before.
import { hasCap } from "./apiversion.js";

export const KEYS_CAP = "keyOverrides";

/** KEYS_PATH is keys.json, next to the page's other assets. */
export const KEYS_PATH = "assets/keys.json";

/** keysUrl is the address of keys.json for the page at `loc`. Through the relay
 *  the page lives under a prefix, so the path is the page's own directory. */
export function keysUrl(loc) {
  return new URL(KEYS_PATH, new URL(loc.pathname.replace(/[^/]*$/, ""), loc.href)).href;
}

/** defaultsFrom keeps the entries of a keys.json that name an action and a label.
 *  It returns the list, or null when the answer is not one. */
export function defaultsFrom(data) {
  if (!Array.isArray(data)) return null;
  const out = data.filter((k) => k && typeof k === "object" && typeof k.id === "string" && k.id !== "" && typeof k.label === "string");
  return out.length ? out : null;
}

/** loadKeyDefaults fetches keys.json. fetchFn and loc are the page's own, passed
 *  in so a test can give fakes. It resolves to the list or to null; it does not
 *  reject. */
export async function loadKeyDefaults(fetchFn, loc) {
  try {
    const r = await fetchFn(keysUrl(loc), { credentials: "same-origin", cache: "no-store" });
    if (!r.ok) return null;
    return defaultsFrom(await r.json());
  } catch {
    return null;
  }
}

/** readsKeyFiles says the server offered the table as files plus changes. */
export function readsKeyFiles(hello) {
  return hasCap(hello, KEYS_CAP);
}

/** mergeKeys is the table a window works from: the defaults with the hidden
 *  actions left out and each remapped action's binding replaced and marked
 *  overridden. An override naming an action the table lacks is ignored. */
export function mergeKeys(defaults, overrides, hidden) {
  const over = overrides && typeof overrides === "object" ? overrides : {};
  const skip = new Set(Array.isArray(hidden) ? hidden.filter((h) => typeof h === "string") : []);
  const out = [];
  for (const k of defaults) {
    if (skip.has(k.id)) continue;
    if (Object.prototype.hasOwnProperty.call(over, k.id) && typeof over[k.id] === "string") out.push({ ...k, keys: over[k.id], overridden: true });
    else out.push(k);
  }
  return out;
}
