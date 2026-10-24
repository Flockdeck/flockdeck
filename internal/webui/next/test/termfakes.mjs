// Fakes for the terminal tests: a socket that records binary frames, a
// terminal, a fit addon, a store with a hand-set snapshot, and the real e2e.js
// (the old window's file) run in node against a stand-in for IndexedDB.
import fs from "node:fs";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

export class PtySocket {
  static instances = [];
  static reset() { PtySocket.instances = []; }
  static get last() { return PtySocket.instances[PtySocket.instances.length - 1]; }
  constructor(url) {
    this.url = url;
    this.readyState = 0;
    this.sent = [];
    this.closed = false;
    PtySocket.instances.push(this);
  }
  send(data) { this.sent.push(data); }
  close(code, reason) {
    if (this.closed) return;
    this.closed = true;
    this.readyState = 3;
    if (this.onclose) this.onclose({ code, reason });
  }
  open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
  text(s) { this.onmessage({ data: s }); }
  bin(bytes) {
    const u = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    this.onmessage({ data: u.buffer.slice(u.byteOffset, u.byteOffset + u.byteLength) });
  }
  // The server closes the socket with a code.
  serverClose(code = 1006, reason = "") {
    this.readyState = 3;
    if (this.onclose) this.onclose({ code, reason });
  }
  get plainJson() { return this.sent.filter((s) => typeof s === "string").map((s) => JSON.parse(s)); }
}

export class FakeTerm {
  static instances = [];
  static reset() { FakeTerm.instances = []; }
  static get last() { return FakeTerm.instances[FakeTerm.instances.length - 1]; }
  constructor(options) {
    this.options = options;
    this.cols = 80;
    this.rows = 24;
    this.written = [];
    this.resizes = [];
    this.pasted = [];
    this.selection = "";
    this.disposed = false;
    this.dataHandlers = [];
    this.binaryHandlers = [];
    this.textarea = null;
    FakeTerm.instances.push(this);
  }
  loadAddon() {}
  open(host) { this.host = host; }
  attachCustomKeyEventHandler(fn) { this.keyHandler = fn; }
  onData(fn) { this.dataHandlers.push(fn); }
  onBinary(fn) { this.binaryHandlers.push(fn); }
  write(data, cb) { this.written.push(data); if (cb) cb(); }
  resize(c, r) { this.cols = c; this.rows = r; this.resizes.push([c, r]); }
  paste(t) { this.pasted.push(t); }
  hasSelection() { return !!this.selection; }
  getSelection() { return this.selection; }
  clearSelection() { this.selection = ""; }
  focus() { this.focused = true; }
  dispose() { this.disposed = true; }
  // The person types.
  type(s) { for (const fn of this.dataHandlers) fn(s); }
  text() {
    return this.written.map((w) => (typeof w === "string" ? w : new TextDecoder().decode(w))).join("");
  }
}

export class FakeFit {
  static box = { cols: 100, rows: 30 };
  proposeDimensions() { return FakeFit.box; }
}

export class FakeObserver {
  static instances = [];
  constructor(fn) { this.fn = fn; this.disconnected = false; FakeObserver.instances.push(this); }
  observe() {}
  disconnect() { this.disconnected = true; }
  fire() { this.fn(); }
}

export class FakeHost {
  constructor() { this.listeners = {}; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn); }
  get listenerCount() { return Object.values(this.listeners).reduce((n, l) => n + l.length, 0); }
  contains() { return false; }
  emit(type, ev) { for (const fn of this.listeners[type] || []) fn(ev); }
}

// store is the part of store.js the terminal uses, with a snapshot set by hand.
export function fakeStore(snap) {
  let cur = snap;
  const subs = new Set();
  const sent = [];
  const watched = [];
  return {
    getState: () => cur,
    // What the drawer asks of the store besides reading it.
    send(cmd) { sent.push(cmd); return true; },
    watchStructured(id) { watched.push(id); return true; },
    unwatchStructured() { watched.push(null); },
    // The conversation tab's watch: the drawer asks, and these tests do not answer.
    watchConversation() { return true; },
    olderConversation() { return false; },
    unwatchConversation() {},
    get sent() { return sent; },
    get watched() { return watched; },
    subscribe(fn) { subs.add(fn); return () => subs.delete(fn); },
    set(patch) { cur = { ...cur, ...patch }; for (const fn of [...subs]) fn(cur); },
    get subscribers() { return subs.size; },
  };
}

export const paneState = (panes) => ({ type: "state", panes });

// --------------------------------------------------------------- e2e.js

// A single-database stand-in for IndexedDB: open, one object store, get and
// put by key, which is all getIdentity uses.
class Req { constructor() { this.onsuccess = null; this.onerror = null; this.onupgradeneeded = null; this.result = undefined; this.error = null; } }
class Store {
  constructor(map) { this.map = map; }
  get(key) { const r = new Req(); setImmediate(() => { r.result = this.map.get(key); if (r.onsuccess) r.onsuccess({ target: r }); }); return r; }
  put(value, key) { const r = new Req(); this.map.set(key, value); setImmediate(() => { r.result = key; if (r.onsuccess) r.onsuccess({ target: r }); }); return r; }
}
class Tx {
  constructor(store) { this.store = store; this.oncomplete = null; setImmediate(() => { if (this.oncomplete) this.oncomplete({ target: this }); }); }
  objectStore() { return this.store; }
}
class Db {
  constructor(stores) { this.stores = stores; }
  createObjectStore(name) { const m = new Map(); this.stores.set(name, m); return new Store(m); }
  transaction(name) { let m = this.stores.get(name); if (!m) { m = new Map(); this.stores.set(name, m); } return new Tx(new Store(m)); }
}
const databases = new Map();
const fakeIndexedDB = {
  open(name) {
    const r = new Req();
    setImmediate(() => {
      let entry = databases.get(name);
      const isNew = !entry;
      if (!entry) { entry = { stores: new Map() }; databases.set(name, entry); }
      r.result = new Db(entry.stores);
      if (isNew && r.onupgradeneeded) r.onupgradeneeded({ target: r, oldVersion: 0 });
      setImmediate(() => { if (r.onsuccess) r.onsuccess({ target: r }); });
    });
    return r;
  },
};

/** loadE2E runs the old window's e2e.js, unmodified, as the page's classic
 *  script does, and returns FlockdeckE2E. */
export function loadE2E() {
  if (globalThis.FlockdeckE2E) return globalThis.FlockdeckE2E;
  const file = fileURLToPath(new URL("../assets/e2e.js", import.meta.url));
  globalThis.window = globalThis;
  globalThis.indexedDB = fakeIndexedDB;
  vm.runInThisContext(fs.readFileSync(file, "utf8"), { filename: file });
  return globalThis.FlockdeckE2E;
}

/** hostSide makes a desktop's identity and returns what a test needs to play
 *  the desktop: its public key to put in the hello, and answer(hello) to run
 *  the host side of the handshake. */
export async function hostSide(E2E) {
  const pair = await crypto.subtle.generateKey({ name: "ECDH", namedCurve: "P-256" }, true, ["deriveBits"]);
  const pub = E2E.encodePublicKey(new Uint8Array(await crypto.subtle.exportKey("raw", pair.publicKey)));
  return {
    pub,
    async answer(hello) {
      const identity = await E2E.getIdentity();
      return E2E.respondHostHandshake(pair.privateKey, pair.publicKey, identity.publicKey, hello);
    },
  };
}

export const sleep = (ms = 0) => new Promise((r) => setTimeout(r, ms));
export async function waitFor(cond, what = "a condition") {
  for (let i = 0; i < 400; i++) {
    if (cond()) return;
    await sleep(5);
  }
  throw new Error("timed out waiting for " + what);
}

/** tag seals a plaintext the way the desktop does: one byte of kind first. */
export async function sealFor(session, tag, plain) {
  const bytes = typeof plain === "string" ? new TextEncoder().encode(plain) : plain;
  const tagged = new Uint8Array(bytes.length + 1);
  tagged[0] = tag;
  tagged.set(bytes, 1);
  return session.seal(tagged);
}
