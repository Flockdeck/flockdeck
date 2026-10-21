// A small fake DOM, enough for Preact to render a component into and for a test
// to click, type into and press keys on it. There is no browser or jsdom here;
// this exists so the shell's effects (listeners, focus, callbacks) can run.
//
// It is not a model of the web platform. It does what the shell and Preact ask
// of it: elements with attributes, children, event listeners, focus and
// document.activeElement.

const EVENT_PROPS = ["onclick", "oninput", "onkeydown", "onmousedown", "onchange", "onfocus", "onblur"];

class Node_ {
  constructor(doc) {
    this.ownerDocument = doc;
    this.parentNode = null;
    this.childNodes = [];
  }
  get firstChild() {
    return this.childNodes[0] || null;
  }
  get nextSibling() {
    if (!this.parentNode) return null;
    const sibs = this.parentNode.childNodes;
    return sibs[sibs.indexOf(this) + 1] || null;
  }
  appendChild(n) {
    return this.insertBefore(n, null);
  }
  insertBefore(n, ref) {
    if (n.parentNode) n.parentNode.removeChild(n);
    const at = ref ? this.childNodes.indexOf(ref) : this.childNodes.length;
    this.childNodes.splice(at < 0 ? this.childNodes.length : at, 0, n);
    n.parentNode = this;
    return n;
  }
  removeChild(n) {
    const at = this.childNodes.indexOf(n);
    if (at >= 0) this.childNodes.splice(at, 1);
    n.parentNode = null;
    // A removed element cannot hold the keyboard.
    if (this.ownerDocument && this.ownerDocument._active && n.contains && n.contains(this.ownerDocument._active)) this.ownerDocument._active = null;
    return n;
  }
  remove() {
    if (this.parentNode) this.parentNode.removeChild(this);
  }
  contains(n) {
    for (let x = n; x; x = x.parentNode) if (x === this) return true;
    return false;
  }
}

class Text_ extends Node_ {
  constructor(doc, data) {
    super(doc);
    this.nodeType = 3;
    this.data = data;
  }
  get textContent() {
    return this.data;
  }
  set textContent(v) {
    this.data = v;
  }
}

class Element_ extends Node_ {
  constructor(doc, name) {
    super(doc);
    this.nodeType = 1;
    this.localName = name.toLowerCase();
    this.nodeName = name.toUpperCase();
    this.attributes = {};
    this.listeners = {};
    this.style = { setProperty() {}, removeProperty() {} };
    this.focusCalls = 0;
    // Preact decides whether an on* prop is a property or an event name by
    // asking whether it is "in" the element.
    for (const p of EVENT_PROPS) this[p] = null;
  }
  setAttribute(k, v) {
    this.attributes[k] = String(v);
  }
  removeAttribute(k) {
    delete this.attributes[k];
  }
  getAttribute(k) {
    return k in this.attributes ? this.attributes[k] : null;
  }
  addEventListener(type, fn, capture) {
    (this.listeners[type] ||= []).push({ fn, capture: !!capture });
  }
  removeEventListener(type, fn, capture) {
    this.listeners[type] = (this.listeners[type] || []).filter((l) => l.fn !== fn || l.capture !== !!capture);
  }
  get textContent() {
    return this.childNodes.map((c) => c.textContent).join("");
  }
  set textContent(v) {
    this.childNodes = [];
    if (v !== "") this.appendChild(new Text_(this.ownerDocument, v));
  }
  // Only what a browser lets take the keyboard: a control, or an element with
  // a tabindex. Anything else ignores focus().
  get focusable() {
    return "tabindex" in this.attributes || ["button", "input", "a", "select", "textarea"].includes(this.localName);
  }
  focus() {
    this.focusCalls++;
    if (this.focusable) this.ownerDocument._active = this;
  }
  blur() {
    if (this.ownerDocument._active === this) this.ownerDocument._active = null;
  }
  // The element's own listeners, then the ones on its ancestors, the way an
  // event bubbles, and the target's window last. Capture listeners on the
  // window run first.
  dispatch(type, init = {}) {
    const ev = {
      type,
      target: this,
      currentTarget: null,
      defaultPrevented: false,
      stopped: false,
      preventDefault() {
        this.defaultPrevented = true;
      },
      stopPropagation() {
        this.stopped = true;
      },
      ...init,
    };
    const run = (node, capture) => {
      for (const l of (node.listeners && node.listeners[type]) || []) {
        if (l.capture !== capture) continue;
        ev.currentTarget = node;
        l.fn.call(node, ev);
        if (ev.stopped) return true;
      }
      return false;
    };
    const path = [];
    for (let n = this; n; n = n.parentNode) path.push(n);
    const win = this.ownerDocument.defaultView;
    if (win && run(win, true)) return ev;
    for (const n of [...path].reverse()) if (n !== this && run(n, true)) return ev;
    for (const n of path) {
      // Preact attaches a handler as a property proxy when the name is "in" the
      // element, and as a listener otherwise; both are covered.
      if (typeof n["on" + type] === "function") {
        ev.currentTarget = n;
        n["on" + type](ev);
        if (ev.stopped) return ev;
      }
      if (run(n, false)) return ev;
    }
    if (win) run(win, false);
    return ev;
  }
  // A press, as a mouse does it: mousedown, then the keyboard moves to the
  // nearest focusable ancestor (or to nothing) unless mousedown was prevented,
  // then click.
  click() {
    const down = this.dispatch("mousedown");
    if (!down.defaultPrevented) {
      let n = this;
      while (n && !(n.nodeType === 1 && n.focusable)) n = n.parentNode;
      if (n && n !== this.ownerDocument.body) n.focus();
      else this.ownerDocument._active = null;
    }
    return this.dispatch("click");
  }
  keydown(key, init = {}) {
    return this.dispatch("keydown", { key, ctrlKey: false, shiftKey: false, altKey: false, ...init });
  }
  enter(value) {
    this.value = value;
    return this.dispatch("input");
  }
  query(pred) {
    const out = [];
    const walk = (n) => {
      if (n.nodeType === 1 && pred(n)) out.push(n);
      (n.childNodes || []).forEach(walk);
    };
    walk(this);
    return out;
  }
  find(selector) {
    return this.query(matcher(selector))[0] || null;
  }
  findAll(selector) {
    return this.query(matcher(selector));
  }
  querySelectorAll(selector) {
    return this.findAll(selector);
  }
}

// Only what the tests ask for: tag, .class, #id, [attr] and [attr=value], one of them.
function matcher(sel) {
  if (sel.startsWith(".")) return (n) => (n.attributes.class || "").split(/\s+/).includes(sel.slice(1));
  if (sel.startsWith("#")) return (n) => n.attributes.id === sel.slice(1);
  const m = /^\[([\w-]+)="([^"]*)"\]$/.exec(sel);
  if (m) return (n) => n.attributes[m[1]] === m[2];
  const has = /^\[([\w-]+)\]$/.exec(sel);
  if (has) return (n) => has[1] in n.attributes;
  return (n) => n.localName === sel;
}

/** createDom makes a document with a body, and a window that has listeners of
 *  its own. Preact reads document, so install() puts them on globalThis. */
export function createDom() {
  const doc = {
    nodeType: 9,
    _active: null,
    createElement: (n) => new Element_(doc, n),
    createElementNS: (ns, n) => new Element_(doc, n),
    createTextNode: (t) => new Text_(doc, t),
  };
  Object.defineProperty(doc, "activeElement", { get: () => doc._active || doc.body });
  doc.body = new Element_(doc, "body");
  const win = new Element_(doc, "window");
  doc.defaultView = win;
  win.document = doc;
  win.matchMedia = (q) => ({ media: q, matches: false, addEventListener() {}, removeEventListener() {} });
  return { document: doc, window: win, body: doc.body };
}

/** install makes the fake document the global one for Preact, and returns what
 *  puts the globals back. */
export function install(dom) {
  const had = { document: globalThis.document, window: globalThis.window };
  globalThis.document = dom.document;
  globalThis.window = dom.window;
  return () => {
    globalThis.document = had.document;
    globalThis.window = had.window;
    if (had.document === undefined) delete globalThis.document;
    if (had.window === undefined) delete globalThis.window;
  };
}

/** settle waits for Preact to apply state changes and run effects, which it
 *  does after a frame (a short timer here, as there is no animation frame). */
export function settle(ms = 160) {
  return new Promise((r) => setTimeout(r, ms));
}
