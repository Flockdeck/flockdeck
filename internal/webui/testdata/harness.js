"use strict";
/* A DOM small enough to read and large enough to run app.js.
 *
 * The front end has no build step and no test runner, so this is the whole of
 * one: node, this file, and the real app.js and index.html read off disk.
 */
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const crypto = require("crypto").webcrypto;

const ASSETS = process.env.FLOCKDECK_ASSETS || ".";

// ------------------------------------------------------------------- nodes

const VOID = new Set(["meta", "link", "input", "br", "img", "hr", "source"]);

class TextNode {
  constructor(text) { this.nodeType = 3; this.data = String(text); this.parentElement = null; }
  get textContent() { return this.data; }
  set textContent(v) { this.data = String(v); }
  remove() { if (this.parentElement) this.parentElement.removeChild(this); }
  get isConnected() { return !!(this.parentElement && this.parentElement.isConnected); }
}

class ClassList {
  constructor(owner) { this.owner = owner; }
  get _set() {
    return new Set(String(this.owner._class || "").split(/\s+/).filter(Boolean));
  }
  _write(s) { this.owner._class = [...s].join(" "); }
  add(...c) { const s = this._set; c.forEach((x) => s.add(x)); this._write(s); }
  remove(...c) { const s = this._set; c.forEach((x) => s.delete(x)); this._write(s); }
  contains(c) { return this._set.has(c); }
  toggle(c, force) {
    const on = force === undefined ? !this.contains(c) : !!force;
    if (on) this.add(c); else this.remove(c);
    return on;
  }
  get length() { return this._set.size; }
  [Symbol.iterator]() { return this._set[Symbol.iterator](); }
}

let SERIAL = 1;

class Element {
  constructor(tag, doc) {
    this.nodeType = 1;
    this.tagName = String(tag).toUpperCase();
    this.ownerDocument = doc;
    this.childNodes = [];
    this.parentElement = null;
    this.attributes = new Map();
    this._class = "";
    this._listeners = new Map();
    this.style = {};
    this.hidden = false;
    this.serial = SERIAL++;
    const self = this;
    this.dataset = new Proxy({}, {
      get(_, k) { return self.attributes.get("data-" + dashed(k)); },
      set(_, k, v) { self.attributes.set("data-" + dashed(k), String(v)); return true; },
      deleteProperty(_, k) { self.attributes.delete("data-" + dashed(k)); return true; },
      has(_, k) { return self.attributes.has("data-" + dashed(k)); },
      ownKeys() { return [...self.attributes.keys()].filter((k) => k.startsWith("data-")).map((k) => camel(k.slice(5))); },
      getOwnPropertyDescriptor() { return { enumerable: true, configurable: true }; },
    });
  }

  get className() { return this._class; }
  set className(v) { this._class = String(v); }
  get classList() { return new ClassList(this); }

  get id() { return this.attributes.get("id") || ""; }
  set id(v) { this.attributes.set("id", String(v)); this.ownerDocument._index(this); }

  get title() { return this.attributes.get("title") || ""; }
  set title(v) { this.attributes.set("title", String(v)); }

  // Reflected as a browser reflects it, so input[type=password] finds a field
  // whose type was set as a property.
  get type() { return this.attributes.get("type") || ""; }
  set type(v) { this.attributes.set("type", String(v)); }

  get tabIndex() { return Number(this.attributes.get("tabindex") || 0); }
  set tabIndex(v) { this.attributes.set("tabindex", String(v)); }

  setAttribute(n, v) { this.attributes.set(n, String(v)); if (n === "id") this.ownerDocument._index(this); }
  getAttribute(n) { return this.attributes.has(n) ? this.attributes.get(n) : null; }
  hasAttribute(n) { return this.attributes.has(n); }
  removeAttribute(n) { this.attributes.delete(n); }

  get children() { return this.childNodes.filter((n) => n.nodeType === 1); }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    if (!this.parentElement) return null;
    const k = this.parentElement.childNodes;
    return k[k.indexOf(this) + 1] || null;
  }

  _adopt(n) {
    if (typeof n === "string") n = new TextNode(n);
    if (n.parentElement) n.parentElement.removeChild(n);
    n.parentElement = this;
    // Counted so a test can say whether an element was taken out of the
    // document and put back, which is what costs a terminal its selection.
    n.adoptions = (n.adoptions || 0) + 1;
    return n;
  }
  append(...kids) { kids.forEach((k) => this.childNodes.push(this._adopt(k))); }
  appendChild(k) { this.childNodes.push(this._adopt(k)); return k; }
  insertBefore(k, ref) {
    const at = ref ? this.childNodes.indexOf(ref) : -1;
    const n = this._adopt(k);
    if (at < 0) this.childNodes.push(n); else this.childNodes.splice(at, 0, n);
    return k;
  }
  removeChild(k) {
    const at = this.childNodes.indexOf(k);
    if (at >= 0) this.childNodes.splice(at, 1);
    k.parentElement = null;
    return k;
  }
  remove() { if (this.parentElement) this.parentElement.removeChild(this); }
  replaceChildren(...kids) {
    this.childNodes.slice().forEach((k) => this.removeChild(k));
    this.append(...kids);
  }

  get textContent() { return this.childNodes.map((n) => n.textContent).join(""); }
  set textContent(v) {
    this.childNodes.slice().forEach((k) => this.removeChild(k));
    // Emptied, an element has nothing to be scrolled through, and a browser
    // puts it back at the top.
    if (this.scrollTop) this.scrollTop = 0;
    if (v !== "" && v !== null && v !== undefined) this.append(new TextNode(v));
  }

  get innerHTML() { return this._html || ""; }
  set innerHTML(v) {
    this._html = String(v);
    this.childNodes.slice().forEach((k) => this.removeChild(k));
    this.append(new TextNode(stripTags(String(v))));
  }

  get isConnected() {
    for (let n = this; n; n = n.parentElement) if (n === this.ownerDocument.documentElement) return true;
    return false;
  }

  contains(n) {
    for (let p = n; p; p = p.parentElement) if (p === this) return true;
    return false;
  }
  closest(sel) {
    for (let p = this; p; p = p.parentElement) if (p.nodeType === 1 && matches(p, sel)) return p;
    return null;
  }
  matches(sel) { return matches(this, sel); }

  querySelectorAll(sel) {
    const out = [];
    walk(this, (n) => { if (n !== this && matches(n, sel)) out.push(n); });
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }

  addEventListener(type, fn, opts) {
    const capture = opts === true || (opts && opts.capture);
    const once = !!(opts && opts.once);
    if (!this._listeners.has(type)) this._listeners.set(type, []);
    this._listeners.get(type).push({ fn, capture, once });
  }
  removeEventListener(type, fn, opts) {
    const capture = opts === true || (opts && opts.capture);
    const l = this._listeners.get(type);
    if (!l) return;
    const at = l.findIndex((e) => e.fn === fn && !!e.capture === !!capture);
    if (at >= 0) l.splice(at, 1);
  }
  dispatchEvent(ev) { return dispatch(this, ev); }

  /** click is the element's own, as a browser has it: a disabled control
   *  ignores it. app.js calls it to follow a tab the arrow keys landed on. */
  click() { if (!this.disabled) dispatch(this, new Ev("click", { target: this })); }
  focus() { this.ownerDocument._focus(this); }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument._focus(this.ownerDocument.body); }

  getBoundingClientRect() {
    return { left: 0, top: 0, right: 100, bottom: 20, width: 100, height: 20, x: 0, y: 0 };
  }
  get offsetWidth() { return 100; }
  get offsetHeight() { return 20; }
  get offsetParent() { return this.isConnected ? this.parentElement : null; }
  scrollIntoView(opts) { this.scrolledTo = (this.scrolledTo || 0) + 1; this.scrolledWith = opts; }
  setSelectionRange(from, to) { this.selectionStart = from; this.selectionEnd = to; }
  select() { this.selectionStart = 0; this.selectionEnd = String(this.value || "").length; }
  setPointerCapture(id) { this.captured = id; }
  releasePointerCapture() { this.captured = undefined; }
}

function dashed(k) { return String(k).replace(/[A-Z]/g, (c) => "-" + c.toLowerCase()); }
function camel(k) { return String(k).replace(/-([a-z])/g, (_, c) => c.toUpperCase()); }
function stripTags(s) { return s.replace(/<[^>]*>/g, ""); }

/** Every element a selector search looks at, counted so that a test can say
 *  what searching the document costs. */
let SEARCHED = 0;

function walk(node, fn) {
  for (const k of node.childNodes.slice()) {
    if (k.nodeType !== 1) continue;
    SEARCHED++;
    fn(k);
    walk(k, fn);
  }
}

/* Selectors: a comma-separated list of compound simple selectors, which is
 * everything app.js asks for. No combinators, because it uses none. */
function matches(n, sel) {
  return String(sel).split(",").some((one) => matchOne(n, one.trim()));
}
function matchOne(n, sel) {
  if (!sel) return false;
  const parts = sel.match(/^([a-zA-Z][\w-]*)?((?:[#.][\w-]+|\[[^\]]+\])*)$/);
  if (!parts) throw new Error("harness: selector not supported: " + sel);
  if (parts[1] && n.tagName !== parts[1].toUpperCase()) return false;
  const rest = parts[2] || "";
  const re = /[#.][\w-]+|\[[^\]]+\]/g;
  let m;
  while ((m = re.exec(rest))) {
    const t = m[0];
    if (t[0] === "#") { if (n.id !== t.slice(1)) return false; }
    else if (t[0] === ".") { if (!n.classList.contains(t.slice(1))) return false; }
    else {
      const inner = t.slice(1, -1);
      const eq = inner.indexOf("=");
      if (eq < 0) { if (!n.hasAttribute(inner)) return false; }
      else {
        const name = inner.slice(0, eq);
        const want = inner.slice(eq + 1).replace(/^["']|["']$/g, "").replace(/\\(.)/g, "$1");
        if (n.getAttribute(name) !== want) return false;
      }
    }
  }
  return true;
}

// ------------------------------------------------------------------ events

class Ev {
  constructor(type, init) {
    this.type = type;
    Object.assign(this, init || {});
    this.defaultPrevented = false;
    this._stopped = false;
  }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this._stopped = true; }
  stopImmediatePropagation() { this._stopped = true; }
}

function dispatch(target, ev) {
  ev.target = ev.target || target;
  const chain = [];
  for (let n = target; n; n = n.parentElement) chain.push(n);
  const doc = target.ownerDocument || (target.documentElement ? target : null);
  if (doc) { chain.push(doc); if (doc.defaultView) chain.push(doc.defaultView); }
  for (let i = chain.length - 1; i >= 0 && !ev._stopped; i--) fire(chain[i], ev, true);
  for (let i = 0; i < chain.length && !ev._stopped; i++) fire(chain[i], ev, false);
  return !ev.defaultPrevented;
}
function fire(node, ev, capture) {
  ev.currentTarget = node;
  if (!capture) {
    const prop = node["on" + ev.type];
    if (typeof prop === "function") prop.call(node, ev);
  }
  const l = node._listeners && node._listeners.get(ev.type);
  if (!l) return;
  for (const e of l.slice()) {
    if (!!e.capture !== !!capture) continue;
    if (e.once) node.removeEventListener(ev.type, e.fn, e.capture);
    e.fn.call(node, ev);
    if (ev._stopped) return;
  }
}

// ---------------------------------------------------------------- document

class Doc {
  constructor() {
    this.nodeType = 9;
    this.byId = new Map();
    this._listeners = new Map();
    this.hidden = false;
    this._hasFocus = true;
    this.documentElement = new Element("html", this);
    this.head = new Element("head", this);
    this.body = new Element("body", this);
    this.documentElement.append(this.head, this.body);
    this.activeElement = this.body;
    this.title = "";
  }
  createElement(tag) { return new Element(tag, this); }
  createTextNode(t) { return new TextNode(t); }
  getElementById(id) { return this.byId.get(id) || null; }
  _index(n) { if (n.id) this.byId.set(n.id, n); }
  _focus(n) { this.activeElement = n; }
  querySelector(s) { return this.documentElement.querySelector(s); }
  querySelectorAll(s) { return this.documentElement.querySelectorAll(s); }
  addEventListener(t, f, o) { Element.prototype.addEventListener.call(this, t, f, o); }
  removeEventListener(t, f, o) { Element.prototype.removeEventListener.call(this, t, f, o); }
  dispatchEvent(ev) { return dispatch(this, ev); }
  hasFocus() { return this._hasFocus; }
}

// ------------------------------------------------------------- html parser

function parseInto(html, doc) {
  const stack = [doc.documentElement];
  let i = 0;
  const push = (n) => stack[stack.length - 1].append(n);
  while (i < html.length) {
    const lt = html.indexOf("<", i);
    if (lt < 0) break;
    if (lt > i) {
      const text = html.slice(i, lt);
      if (text.trim()) push(new TextNode(text.trim()));
    }
    if (html.startsWith("<!--", lt)) { i = html.indexOf("-->", lt) + 3; continue; }
    if (html.startsWith("<!", lt)) { i = html.indexOf(">", lt) + 1; continue; }
    const gt = html.indexOf(">", lt);
    if (gt < 0) break;
    const raw = html.slice(lt + 1, gt);
    i = gt + 1;
    if (raw[0] === "/") {
      const name = raw.slice(1).trim().toLowerCase();
      for (let k = stack.length - 1; k > 0; k--) {
        if (stack[k].tagName === name.toUpperCase()) { stack.length = k; break; }
      }
      continue;
    }
    const m = raw.match(/^([a-zA-Z][\w-]*)/);
    if (!m) continue;
    const name = m[1].toLowerCase();
    const selfClose = raw.trim().endsWith("/");
    const node = name === "html" ? doc.documentElement
      : name === "head" ? doc.head
      : name === "body" ? doc.body
      : new Element(name, doc);
    const attrRe = /([a-zA-Z_:][-\w:.]*)(?:\s*=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?/g;
    let a;
    const attrs = raw.slice(m[1].length);
    while ((a = attrRe.exec(attrs))) {
      const key = a[1];
      const val = a[2] === undefined ? "" : a[2].replace(/^["']|["']$/g, "");
      if (key === "class") node.className = val;
      else if (key === "hidden") node.hidden = true;
      else node.setAttribute(key, val);
    }
    if (node !== doc.documentElement && node !== doc.head && node !== doc.body) push(node);
    if (name === "script") { const end = html.indexOf("</script>", i); i = end < 0 ? html.length : end + 9; continue; }
    if (!selfClose && !VOID.has(name)) stack.push(node);
  }
}

// ---------------------------------------------------------------- fake bits

const sockets = [];
class FakeSocket {
  constructor(url) {
    this.url = url;
    this.readyState = 1;
    this.sent = [];
    this.opened = false;
    sockets.push(this);
  }
  send(d) { this.sent.push(d); }
  close() { this.readyState = 3; if (this.onclose) this.onclose(); }
}
FakeSocket.OPEN = 1;

const terms = [];
class FakeTerm {
  constructor(opts) {
    this.options = Object.assign({}, opts);
    this.cols = 80; this.rows = 24;
    // What has been drawn, and what has been written and not yet parsed.
    // xterm's write() only queues while its reset() acts at once, so the two
    // are kept apart: a reset reaches the screen ahead of bytes still queued.
    this.screen = []; this.queue = []; this.disposed = false; this.focused = false;
    this.buffer = { active: { viewportY: 0, baseY: 0 } };
    // What is selected with the mouse, and what has been pasted through
    // paste(), which is how xterm is given text that did not come from keys.
    this.selection = ""; this.pasted = [];
    terms.push(this);
  }
  attachCustomKeyEventHandler(fn) { this._keys = fn; }
  hasSelection() { return !!this.selection; }
  getSelection() { return this.selection; }
  clearSelection() { this.selection = ""; }
  paste(d) { this.pasted.push(d); }
  /** written is what the screen shows once everything written so far has
   *  been parsed: whatever follows the last full reset in the stream. */
  get written() {
    const all = this.screen.concat(this.queue.map((w) => w.data));
    const at = all.lastIndexOf("\x1bc");
    return at < 0 ? all : all.slice(at + 1);
  }
  /** parse is xterm getting round to what was written, calling back each
   *  write as it finishes with it, in order. */
  parse() {
    for (const w of this.queue.splice(0)) { this.screen.push(w.data); if (w.cb) w.cb(); }
  }
  loadAddon(a) { if (a instanceof FakeWebgl) a.term = this; }
  open(host) { this.host = host; }
  onScroll(fn) { this._scroll = fn; }
  onWriteParsed(fn) { this._parsed = fn; }
  scrollToBottom() { this.buffer.active.viewportY = this.buffer.active.baseY; if (this._scroll) this._scroll(this.buffer.active.viewportY); }
  onData(fn) { this._data = fn; }
  onBinary(fn) { this._bin = fn; }
  write(d, cb) { this.queue.push({ data: d, cb }); }
  reset() { this.screen.length = 0; }
  resize(cols, rows) { this.cols = cols; this.rows = rows; this.resizes = (this.resizes || 0) + 1; (this.sizeLog = this.sizeLog || []).push({ cols, rows, drawn: this.screen.map((d) => (typeof d === "string" ? d : new TextDecoder().decode(d))).join("") }); }
  dispose() { this.disposed = true; }
  focus() { terms.forEach((t) => { t.focused = false; }); this.focused = true; }
  blur() { this.focused = false; }
}

class FakeFit { fit() {} proposeDimensions() { return { cols: 80, rows: 24 }; } }
const searchers = [];
class FakeSearch {
  constructor() { this.forward = []; this.back = []; this.cleared = 0; this.hit = true; searchers.push(this); }
  findNext(q) { this.forward.push(q); return this.hit; }
  findPrevious(q) { this.back.push(q); return this.hit; }
  clearDecorations() { this.cleared++; }
  onDidChangeResults(fn) { this.results = fn; }
}
/** Every WebGL renderer made, with the terminal it was loaded into and
 *  whether it has been given up since. */
const webgls = [];
class FakeWebgl {
  constructor() { this.disposed = false; this.term = null; webgls.push(this); }
  onContextLoss(fn) { this._lost = fn; }
  dispose() { this.disposed = true; }
}

const notifications = [];
class FakeNotification {
  constructor(title, opts) { this.title = title; Object.assign(this, opts || {}); notifications.push(this); }
  close() { this.closed = true; }
  static requestPermission() {
    FakeNotification.asked++;
    return Promise.resolve(FakeNotification.permission);
  }
}
FakeNotification.permission = "granted";
FakeNotification.asked = 0;

const observers = [];
class FakeResizeObserver {
  constructor(fn) { this.fn = fn; this.targets = []; this.disconnected = false; observers.push(this); }
  observe(t) { this.targets.push(t); }
  unobserve(t) { this.targets = this.targets.filter((x) => x !== t); }
  disconnect() { this.disconnected = true; this.targets = []; }
}

// ---------------------------------------------------------------- fixtures

/** pane is one entry of the state message's pane map. */
function pane(id, over) {
  return Object.assign({
    id: id, kind: "agent", agent: "claude", model: "", name: "agent " + id, cwd: "C:/repo", branch: "main",
    status: "idle", detail: "", broadcast: false,
    cols: 80, rows: 24, dirty: 0, untracked: 0, ahead: 0, behind: 0,
  }, over || {});
}

/** catalog is the agent catalog every state push carries: one agent this
 *  machine has and one it has not, which is the shape the picker groups by. */
function catalog(over) {
  return Object.assign({
    items: [
      { id: "claude", name: "Claude Code", runner: "cli", available: true, defaultModel: "",
        models: [{ id: "", name: "Default", note: "whatever the CLI is set to" },
                 { id: "opus", name: "Opus", note: "most capable" },
                 { id: "sonnet", name: "Sonnet", note: "the everyday one" }] },
      { id: "codex", name: "Codex", runner: "cli", available: false, defaultModel: "gpt-5",
        install: "npm i -g @openai/codex", models: [{ id: "gpt-5", name: "GPT-5" }] },
    ],
    default: { agent: "claude", model: "" },
  }, over || {});
}

/** fixture is a plausible workspace: two tabs holding one pane each. Pass
 *  overrides for whatever the test is actually about. */
function fixture(over) {
  const s = {
    type: "state",
    root: "C:/repo",
    claudeAvailable: true,
    activeTab: "t1",
    broadcast: false,
    waiting: 0,
    working: 0,
    projects: [{ root: "C:/repo", name: "repo", active: true, tabs: 2, waiting: 0, working: 0 }],
    tabs: [
      { id: "t1", title: "one", focus: "p1", zoom: false, attention: false,
        root: { id: "n1", pane: "p1", weight: 1 } },
      { id: "t2", title: "two", focus: "p2", zoom: false, attention: false,
        root: { id: "n2", pane: "p2", weight: 1 } },
    ],
    panes: { p1: pane("p1"), p2: pane("p2") },
    agents: catalog(),
  };
  Object.assign(s, over || {});
  // What the server derives (workspace.Tally.Activity, TabActivity) and the
  // window draws from: a fixture that leaves it out gets what it would say.
  for (const p of s.projects || []) {
    if (p.activity === undefined) p.activity = p.waiting > 0 ? "waiting" : p.working > 0 ? "working" : undefined;
  }
  for (const t of s.tabs || []) {
    if (t.activity === undefined && t.attention) t.activity = "waiting";
  }
  return s;
}

/** split is a node holding several children, for tests about layout. */
function split(dir, children) {
  return { id: "s-" + dir + "-" + children.length, dir: dir, weight: 1, children: children };
}

/** leaf is a node holding one pane. */
function leaf(id, paneID) { return { id: id, pane: paneID, weight: 1 }; }

// --------------------------------------------------------------------- tab

/* Tab moves the focus unless the page says otherwise, and a dialog that means
 * to keep the keyboard has to say otherwise. Without this here there would be
 * nothing for a trap to prevent, and a broken one would look fine. */
const TAB_STOPS = "button, input, textarea, select, a, [tabindex]";

function tabbable(doc) {
  return doc.documentElement.querySelectorAll(TAB_STOPS).filter((n) => {
    if (n.disabled || n.getAttribute("tabindex") === "-1") return false;
    for (let p = n; p; p = p.parentElement) if (p.hidden) return false;
    return true;
  });
}

function tabTo(doc, back) {
  const all = tabbable(doc);
  if (!all.length) return;
  const at = all.indexOf(doc.activeElement);
  doc._focus(at < 0
    ? (back ? all[all.length - 1] : all[0])
    : all[(at + (back ? -1 : 1) + all.length) % all.length]);
}

// ---------------------------------------------------------------- indexedDB

/** A single-database stand-in for IndexedDB: just enough of it for e2e.js's
 *  getIdentity -- open, one object store, get/put by key -- to run in node.
 *  Real IndexedDB fires every callback asynchronously, which this keeps (via
 *  setImmediate); a case awaiting FlockdeckE2E's own promises already waits
 *  through that. Reset on every boot() (idbReset), the same as sockets and
 *  terms: a real browser's storage survives a page's own reconnects, but not
 *  a case that boots the front end again wanting a device with no identity
 *  yet. */
let idbDatabases = new Map();
function idbReset() { idbDatabases = new Map(); }

class FakeIDBRequest {
  constructor() { this.onsuccess = null; this.onerror = null; this.onupgradeneeded = null; this.result = undefined; this.error = null; }
}

class FakeIDBObjectStore {
  constructor(map) { this.map = map; }
  get(key) {
    const req = new FakeIDBRequest();
    setImmediate(() => { req.result = this.map.get(key); if (req.onsuccess) req.onsuccess({ target: req }); });
    return req;
  }
  put(value, key) {
    const req = new FakeIDBRequest();
    this.map.set(key, value);
    setImmediate(() => { req.result = key; if (req.onsuccess) req.onsuccess({ target: req }); });
    return req;
  }
}

class FakeIDBTransaction {
  constructor(store) {
    this.store = store;
    this.oncomplete = null;
    this.onerror = null;
    setImmediate(() => { if (this.oncomplete) this.oncomplete({ target: this }); });
  }
  objectStore() { return this.store; }
}

class FakeIDBDatabase {
  constructor(stores) { this.stores = stores; }
  createObjectStore(name) {
    const map = new Map();
    this.stores.set(name, map);
    return new FakeIDBObjectStore(map);
  }
  transaction(name) {
    let map = this.stores.get(name);
    if (!map) { map = new Map(); this.stores.set(name, map); }
    return new FakeIDBTransaction(new FakeIDBObjectStore(map));
  }
}

const fakeIndexedDB = {
  open(name) {
    const req = new FakeIDBRequest();
    setImmediate(() => {
      let entry = idbDatabases.get(name);
      const isNew = !entry;
      if (!entry) { entry = { stores: new Map() }; idbDatabases.set(name, entry); }
      req.result = new FakeIDBDatabase(entry.stores);
      if (isNew && req.onupgradeneeded) req.onupgradeneeded({ target: req, oldVersion: 0 });
      setImmediate(() => { if (req.onsuccess) req.onsuccess({ target: req }); });
    });
    return req;
  },
};

// -------------------------------------------------------------------- boot

function unref(t) { t.unref(); return t; }

/** boot starts the front end. opts.pathname is where the page was served
 *  from, which is "/" locally and a machine's own prefix through the relay. */
function boot(opts) {
  opts = opts || {};
  sockets.length = 0; terms.length = 0; observers.length = 0; searchers.length = 0;
  notifications.length = 0; webgls.length = 0;
  FakeNotification.permission = "granted";
  FakeNotification.asked = 0;
  idbReset();
  const doc = new Doc();
  parseInto(fs.readFileSync(path.join(ASSETS, "index.html"), "utf8"), doc);

  const store = new Map();
  const e2eKeyPosts = [];
  const win = {
    innerWidth: 1400, innerHeight: 900,
    document: doc,
    location: { protocol: "http:", host: "127.0.0.1:7777", pathname: opts.pathname || "/" },
    WebSocket: FakeSocket,
    Terminal: FakeTerm,
    FitAddon: { FitAddon: FakeFit },
    SearchAddon: { SearchAddon: FakeSearch },
    WebglAddon: { WebglAddon: FakeWebgl },
    ResizeObserver: FakeResizeObserver,
    // app.js asks whether something is an element before walking up from it,
    // and builds a bare click event in one place.
    Element: Element, Event: Ev,
    Notification: FakeNotification,
    CSS: { escape: (s) => String(s).replace(/([^\w-])/g, "\\$1") },
    TextEncoder, TextDecoder, console,
    // e2e.js's own identity and handshake: real WebCrypto (node's own, which
    // implements the same SubtleCrypto a browser does) against the fake
    // IndexedDB above, and the base64 globals a browser gives a page for
    // free that a vm context, its own fresh realm, does not.
    crypto, indexedDB: fakeIndexedDB, atob, btoa,
    // The page's own timers - a notice taking itself away twelve seconds on,
    // a reconnect - are not what a case waits for, and holding the process
    // open they made every case last as long as the longest of them after its
    // last assertion had run. A case that waits does so with h.sleep.
    setTimeout: (fn, ms, ...args) => unref(setTimeout(fn, ms, ...args)),
    setInterval: (fn, ms, ...args) => unref(setInterval(fn, ms, ...args)),
    clearTimeout, clearInterval, queueMicrotask,
    // Not a real clock: a callback is held until a case asks for the next
    // frame with h.raf(), so staggering WebGL contexts across several frames
    // is exact to test rather than a race against real timers.
    _raf: [],
    requestAnimationFrame: (fn) => { win._raf.push(fn); return win._raf.length; },
    cancelAnimationFrame: () => {},
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    },
    // Only /help.json is fetched by GET, on disk beside the assets;
    // FlockdeckE2E.ensureRegistered's POST to /.flockdeck-e2e-key is the one
    // other fetch app.js makes, answered here rather than off disk, and
    // recorded to e2eKeyPosts for a case to read (h.e2eKeyPosts()).
    fetch: (url, init) => {
      if (String(url) === "/.flockdeck-e2e-key" && init && init.method === "POST") {
        let body = null;
        try { body = JSON.parse(init.body); } catch { /* recorded as null */ }
        e2eKeyPosts.push(body);
        return Promise.resolve({ ok: true, status: 204 });
      }
      const name = String(url).replace(/^.*\//, "");
      try {
        const body = fs.readFileSync(path.join(ASSETS, name), "utf8");
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(JSON.parse(body)) });
      } catch (err) {
        return Promise.resolve({ ok: false, status: 404 });
      }
    },
    confirm: (q) => { win._confirmed = q; return win._confirm === undefined ? true : win._confirm; },
    prompt: () => win._prompt,
    close: () => { win._closed = true; },
    focus: () => {},
    // The clipboard a Copy button writes to; a case reads what it was given
    // back as _copied.
    navigator: { platform: opts.platform || "Win32",
      clipboard: { writeText: (t) => { win._copied = String(t); return Promise.resolve(); } } },
    _listeners: new Map(),
  };
  win.window = win;
  win.self = win;
  win.addEventListener = Element.prototype.addEventListener.bind(win);
  win.removeEventListener = Element.prototype.removeEventListener.bind(win);
  win.dispatchEvent = (ev) => {
    ev.target = ev.target || win;
    fire(win, ev, true); fire(win, ev, false);
    return !ev.defaultPrevented;
  };
  doc.defaultView = win;

  const ctx = vm.createContext(win);
  // e2e.js first, as index.html's own script order has it: app.js reads
  // window.FlockdeckE2E at the top of its own module body (applyHello,
  // connectPTY), the same as it already expects window.Terminal and the
  // rest of the vendor scripts to be there first.
  const e2eSrc = fs.readFileSync(path.join(ASSETS, "e2e.js"), "utf8");
  vm.runInContext(e2eSrc, ctx, { filename: "e2e.js" });
  const src = fs.readFileSync(path.join(ASSETS, "app.js"), "utf8");
  vm.runInContext(src, ctx, { filename: "app.js" });

  const h = {
    doc, win, sockets, terms, observers, searchers, notifications, webgls,
    control: sockets.find((s) => s.url.includes("/ws/control")),
    $: (id) => doc.getElementById(id),
    /** e2eKeyPosts is every body FlockdeckE2E.ensureRegistered has POSTed to
     *  /.flockdeck-e2e-key, in order -- what a case reads to check a window
     *  registered (or did not) once it knew it was reached through the
     *  relay. */
    e2eKeyPosts() { return e2eKeyPosts.slice(); },
    /** controls lists every control socket the page has opened, in order. */
    controls() { return sockets.filter((s) => s.url.includes("/ws/control")); },
    recv(msg) {
      const c = h.controls().pop();
      if (!c || !c.onmessage) throw new Error("harness: the control socket has no reader");
      c.onmessage({ data: JSON.stringify(msg) });
    },
    /** hello delivers the real action table, read from the Go side, so the
     *  palette and the keyboard are tested against the bindings that ship. */
    hello(prefs) {
      const keys = JSON.parse(fs.readFileSync(path.join(ASSETS, "keys.json"), "utf8"));
      h.recv({ type: "hello", keys: keys, prefs: Object.assign({ helpSeen: true, dismissedTips: [] }, prefs || {}) });
      return keys;
    },
    keyTable() { return JSON.parse(fs.readFileSync(path.join(ASSETS, "keys.json"), "utf8")); },
    sleep(ms) { return new Promise((done) => setTimeout(done, ms)); },
    /** waitFor polls cond every few ms until it is truthy, or rejects once
     *  timeoutMs has passed without it -- for a case that needs to wait on
     *  real async work (WebCrypto, the fake IndexedDB's own setImmediate
     *  hops) with no event of its own to await, where a fixed h.sleep is a
     *  race against however fast the machine running it happens to be.
     *
     *  The time allowed is only how long a failure takes to be told: a wait
     *  that is going to succeed returns the moment it does. It is generous
     *  because the work waited for is key generation and encryption, which a
     *  loaded Windows runner took several seconds over, and a wait of a second
     *  rejected a window that was working. */
    async waitFor(cond, timeoutMs) {
      const deadline = Date.now() + (timeoutMs || 20000);
      for (;;) {
        if (cond()) return;
        if (Date.now() >= deadline) throw new Error("harness: waitFor timed out");
        await h.sleep(5);
      }
    },
    /** raf runs the callbacks queued by requestAnimationFrame so far, as one
     *  frame: a callback that asks for another frame is queued for the next
     *  one, not run again by this call. See webglQueue in app.js. */
    raf() { win._raf.splice(0).forEach((fn) => fn()); },
    /** settleWebgl runs frames until nothing is waiting on one, which is as
     *  far as webglQueue ever goes on its own once given the time. */
    settleWebgl() { while (win._raf.length) h.raf(); },
    /** css is the style sheet, for the few things that are only expressible
     *  there and still have to hold. */
    css() { return fs.readFileSync(path.join(ASSETS, "app.css"), "utf8"); },
    /** made counts every element ever built, so a test can say how much of the
     *  screen an action puts together again. */
    made() { return SERIAL; },
    /** searched counts the elements every selector search has looked at. */
    searched() { return SEARCHED; },
    /** palette runs a command the way a person does: opens the palette, types
     *  the words and presses Enter. For the surfaces with no rail button. */
    palette(words) {
      h.press("palette");
      const input = h.$("palette-input");
      input.value = words;
      input.oninput();
      return h.key({ key: "Enter" });
    },
    /** bind presses the binding the action table gives for an action id. */
    press(actionID) {
      const k = h.keyTable().find((x) => x.id === actionID);
      if (!k || !k.keys) throw new Error("harness: no binding for " + actionID);
      const parts = k.keys.split("+").map((p) => p.trim());
      const name = parts.pop();
      const names = { "←": "ArrowLeft", "→": "ArrowRight", "↑": "ArrowUp", "↓": "ArrowDown" };
      return h.key({
        key: names[name] || name,
        ctrlKey: parts.some((p) => p.toLowerCase() === "ctrl"),
        shiftKey: parts.some((p) => p.toLowerCase() === "shift"),
        altKey: parts.some((p) => p.toLowerCase() === "alt"),
      });
    },
    open() { sockets.forEach((s) => { if (!s.opened && s.onopen) { s.opened = true; s.onopen(); } }); },
    /** commands is everything the page has sent up the control connection, in
     *  order, across a reconnect as well as within one. */
    commands() {
      return h.controls().flatMap((c) => c.sent).map((s) => JSON.parse(s)).filter((c) => c.cmd !== "presence");
    },
    /** presence is what the page has said of itself -- that it was just used
     *  -- up the control connection, which commands leaves out: it is said
     *  as the window sees real input, and is not what a case pressing keys
     *  is asking about. */
    presence() {
      return h.controls().flatMap((c) => c.sent).map((s) => JSON.parse(s)).filter((c) => c.cmd === "presence");
    },
    /** click presses something. A disabled control is not clicked at all — the
     *  browser does not dispatch the event — which is the whole point of
     *  disabling one. */
    click(node, init) {
      for (let n = node; n; n = n.parentElement) if (n.disabled) return false;
      return dispatch(node, new Ev("click", Object.assign({ target: node }, init)));
    },
    /** key presses a key at whatever has the keyboard. A keydown starts at the
     *  focused element and travels out to the window, so a handler on a row and
     *  the one on the window both get their turn, in that order. */
    key(init) {
      const ev = new Ev("keydown", Object.assign({ key: "", ctrlKey: false, shiftKey: false, altKey: false }, init));
      const at = doc.activeElement;
      if (at && at.nodeType === 1) dispatch(at, ev);
      else win.dispatchEvent(ev);
      if (ev.key === "Tab" && !ev.defaultPrevented) tabTo(doc, ev.shiftKey);
      // A button pressed with Enter or Space is clicked, which is the browser's
      // doing rather than the page's.
      if ((ev.key === "Enter" || ev.key === " ") && !ev.defaultPrevented &&
          at && at.tagName === "BUTTON" && !at.disabled) {
        dispatch(at, new Ev("click", { target: at }));
      }
      return ev;
    },
    Ev, dispatch,
  };
  h.open();
  return h;
}

module.exports = { boot, fixture, catalog, pane, split, leaf, Ev, Element, TextNode, dispatch };
