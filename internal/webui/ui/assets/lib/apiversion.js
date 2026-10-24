// The version of the control socket's API this page was written for, and what it
// does with the answers to it. The server tells its own version in the hello
// (api: { major, minor, caps }). The page answers with a clientHello, once, and
// the server replies only when it has something to say: apiMismatch for another
// major version, and unsupported for a command it does not know. Both are shown
// as a notice and nothing more; the page keeps working as far as it can.

/** UI_API is the version this page speaks. A change that only adds raises the minor. */
export const UI_API = { major: 1, minor: 0 };

/** CLIENT_LABEL is what the page calls itself in a clientHello. */
export const CLIENT_LABEL = "web";

/** clientHello is the frame the page sends once after each hello. */
export function clientHello() {
  return { type: "clientHello", api: { major: UI_API.major, minor: UI_API.minor }, client: CLIENT_LABEL };
}

/** capsOf is the features the server said it has, as a list of names, or an empty list when it said none (a server that does not send the block). */
export function capsOf(hello) {
  const api = hello && typeof hello === "object" ? hello.api : null;
  if (!api || !Array.isArray(api.caps)) return [];
  return api.caps.filter((c) => typeof c === "string" && c !== "" && c.length <= 64).slice(0, 64);
}

/** hasCap says whether the server offered a feature by name. */
export function hasCap(hello, name) {
  return capsOf(hello).includes(name);
}

const clip = (s, n) => (typeof s === "string" ? s.slice(0, n) : "");

/** mismatchNotice is the notice for an apiMismatch message, or null for one with no sentence. */
export function mismatchNotice(msg) {
  const text = clip(msg && msg.text, 300);
  return text ? { text, error: true } : null;
}

/** unsupportedNotice is the notice for an unsupported message. */
export function unsupportedNotice(msg) {
  const text = clip(msg && msg.text, 200);
  return text ? { text, error: true } : null;
}
