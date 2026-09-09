package webui

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jmwri/perch/internal/help"
)

// readAsset returns one of the embedded front-end files.
func readAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(FS(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The palette, the keyboard and the help pages are all drawn from the key
// table, so an action the table names and the front end has not implemented
// would be listed and then do nothing when it was picked.
func TestFrontEndImplementsEveryAction(t *testing.T) {
	app := readAsset(t, "app.js")

	start := strings.Index(app, "const ACTIONS = {")
	if start < 0 {
		t.Fatal("app.js no longer has an ACTIONS table")
	}
	end := strings.Index(app[start:], "\n  };")
	if end < 0 {
		t.Fatal("the ACTIONS table in app.js is not terminated as expected")
	}
	block := app[start : start+end]

	for _, k := range help.Keys {
		if !strings.Contains(block, "\n    "+k.ID+":") {
			t.Errorf("action %q (%s) is in the key table but not implemented in app.js", k.ID, k.Label)
		}
	}
}

// A binding written into the interface by hand is the drift this whole
// arrangement exists to prevent: tooltips, hints and the palette are all
// filled in from the table once it arrives, so no binding the table owns
// should appear in the front end at all.
func TestNoBindingsAreWrittenIntoTheInterface(t *testing.T) {
	// A comment may talk about a key; nothing a user reads may.
	comment := regexp.MustCompile(`^\s*(//|\*|/\*|<!--)`)

	for _, name := range []string{"app.js", "index.html", "app.css"} {
		for i, line := range strings.Split(readAsset(t, name), "\n") {
			if comment.MatchString(line) {
				continue
			}
			for _, k := range help.Keys {
				if k.Keys == "" || !strings.Contains(line, k.Keys) {
					continue
				}
				t.Errorf("%s:%d writes %q out by hand; take it from the key table instead:\n\t%s",
					name, i+1, k.Keys, strings.TrimSpace(line))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Running the front end
// ---------------------------------------------------------------------------

/*
The front end has no build step, no package manager and no test runner, and it
should keep all three. What it does have is one large script that decides what
several agents look like on screen, and the only way to hold that to anything is
to run it.

So: node, the DOM in frontEndHarness below, and the real app.js and index.html
read out of the embedded file system. Nothing is installed and nothing is
checked in beyond this file. Where node is missing the behaviour tests skip and
the source-level ones above still run.
*/

// runFrontEnd boots app.js against the harness and runs body against it. The
// body is ordinary node: assert is in scope, and h is the booted front end.
func runFrontEnd(t *testing.T, body string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH, so the front-end behaviour tests cannot run")
	}

	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("app.js", readAsset(t, "app.js"))
	write("index.html", readAsset(t, "index.html"))
	write("harness.js", frontEndHarness)

	// The real action table, so a test presses the binding that ships rather
	// than one written out here to go stale.
	keys, err := json.Marshal(help.Keys)
	if err != nil {
		t.Fatalf("marshal the key table: %v", err)
	}
	write("keys.json", string(keys))

	write("case.js", "\"use strict\";\nconst assert = require(\"assert\");\n"+
		"const { boot, fixture, pane, split, leaf } = require(\"./harness.js\");\n"+
		"const h = boot();\n"+body+"\n")

	cmd := exec.Command(node, filepath.Join(dir, "case.js"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PERCH_ASSETS="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the front end failed this case: %v\n%s", err, out)
	}
	return string(out)
}

// A status push arrives every time any agent changes what it is doing, and with
// several running that is most of the time. Rebuilding the tab strip on each
// one throws away whatever the keyboard was holding, so a tab could not be
// reached from the keyboard at all while the agents were working.
func TestTabStripSurvivesAStatusPush(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());

const bar = h.$("tabs");
assert.strictEqual(bar.children.length, 2, "two tabs");
const first = bar.children[0], second = bar.children[1];
assert.strictEqual(first.querySelector(".label").textContent, "one");

// Someone has tabbed to the second tab button.
second.focus();
assert.ok(h.doc.activeElement === second, "the tab button has the keyboard");

// An agent starts working. Nothing about the tabs changed.
h.recv(fixture({ working: 1, panes: { p1: pane("p1", { status: "working" }), p2: pane("p2") } }));

assert.ok(h.$("tabs").children[0] === first, "the first tab button was replaced");
assert.ok(h.$("tabs").children[1] === second, "the second tab button was replaced");
assert.ok(h.doc.activeElement === second, "the push took the keyboard off the tab button");
`)
}

// The strip still has to follow the state it is given: titles, which tab is
// current, the marker for a blocked agent, tabs opening, closing and being
// reordered.
func TestTabStripFollowsTheState(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
const bar = h.$("tabs");
const one = bar.children[0], two = bar.children[1];

// A rename, the other tab selected, and an agent in it blocked.
h.recv(fixture({
  activeTab: "t2", waiting: 1,
  tabs: [
    { id: "t1", title: "renamed", focus: "p1", root: leaf("n1", "p1") },
    { id: "t2", title: "two", focus: "p2", attention: true, root: leaf("n2", "p2") },
  ],
  panes: { p1: pane("p1"), p2: pane("p2", { status: "waiting" }) },
}));
assert.strictEqual(bar.children[0].querySelector(".label").textContent, "renamed");
assert.ok(!one.classList.contains("active"), "the first tab is no longer current");
assert.ok(two.classList.contains("active"), "the second tab is current");
assert.strictEqual(two.getAttribute("aria-selected"), "true");
assert.ok(two.classList.contains("attention"), "the blocked tab is marked");
assert.ok(two.querySelector(".attn"), "the marker element is there");
assert.ok(two.querySelector(".attn").getAttribute("aria-label"), "the marker names itself");

// The two swap places, and a third arrives.
h.recv(fixture({
  activeTab: "t2",
  tabs: [
    { id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") },
    { id: "t3", title: "three", focus: "p3", root: leaf("n3", "p3") },
    { id: "t1", title: "renamed", focus: "p1", root: leaf("n1", "p1") },
  ],
  panes: { p1: pane("p1"), p2: pane("p2"), p3: pane("p3") },
}));
assert.deepStrictEqual(bar.children.map((b) => b.querySelector(".label").textContent),
  ["two", "three", "renamed"], "the strip is in the wrong order");
assert.ok(bar.children[0] === two, "reordering rebuilt a button it could have moved");
assert.ok(bar.children[2] === one, "reordering rebuilt a button it could have moved");
assert.ok(!two.querySelector(".attn"), "the marker was not taken away again");

// One closes.
h.recv(fixture({
  activeTab: "t2",
  tabs: [{ id: "t2", title: "two", focus: "p2", root: leaf("n2", "p2") }],
  panes: { p2: pane("p2") },
}));
assert.strictEqual(bar.children.length, 1, "the closed tabs are gone from the strip");
assert.ok(bar.children[0] === two, "the surviving tab kept its button");

// The close button still closes the tab it belongs to, not the one it was
// first drawn for.
h.click(bar.children[0].querySelector(".close"));
const last = h.commands().pop();
assert.deepStrictEqual(last, { cmd: "closeTab", id: "t2" });
`)
}

// frontEndHarness is the DOM app.js is run against: enough of one to build
// the interface, dispatch events through it and answer the questions it asks,
// and nothing beyond that. It is written to a temporary directory beside the
// real assets and required by each case.
const frontEndHarness = `"use strict";
/* A DOM small enough to read and large enough to run app.js.
 *
 * The front end has no build step and no test runner, so this is the whole of
 * one: node, this file, and the real app.js and index.html read off disk.
 */
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const ASSETS = process.env.PERCH_ASSETS || ".";

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

  focus() { this.ownerDocument._focus(this); }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument._focus(this.ownerDocument.body); }

  getBoundingClientRect() {
    return { left: 0, top: 0, right: 100, bottom: 20, width: 100, height: 20, x: 0, y: 0 };
  }
  get offsetWidth() { return 100; }
  get offsetHeight() { return 20; }
  get offsetParent() { return this.isConnected ? this.parentElement : null; }
  scrollIntoView() {}
}

function dashed(k) { return String(k).replace(/[A-Z]/g, (c) => "-" + c.toLowerCase()); }
function camel(k) { return String(k).replace(/-([a-z])/g, (_, c) => c.toUpperCase()); }
function stripTags(s) { return s.replace(/<[^>]*>/g, ""); }

function walk(node, fn) {
  for (const k of node.childNodes.slice()) {
    if (k.nodeType !== 1) continue;
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
    this.written = []; this.disposed = false; this.focused = false;
    terms.push(this);
  }
  loadAddon() {}
  open(host) { this.host = host; }
  onData(fn) { this._data = fn; }
  onBinary(fn) { this._bin = fn; }
  write(d) { this.written.push(d); }
  reset() { this.written.length = 0; }
  dispose() { this.disposed = true; }
  focus() { terms.forEach((t) => { t.focused = false; }); this.focused = true; }
  blur() { this.focused = false; }
}

class FakeFit { fit() {} proposeDimensions() { return { cols: 80, rows: 24 }; } }
class FakeSearch { findNext() { return true; } findPrevious() { return true; } clearDecorations() { this.cleared = (this.cleared || 0) + 1; } }
class FakeWebgl { onContextLoss() {} dispose() {} }

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
    id: id, kind: "claude", name: "agent " + id, cwd: "C:/repo", branch: "main",
    status: "idle", detail: "", broadcast: false,
    cols: 80, rows: 24, dirty: 0, untracked: 0, ahead: 0, behind: 0,
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
  };
  return Object.assign(s, over || {});
}

/** split is a node holding several children, for tests about layout. */
function split(dir, children) {
  return { id: "s-" + dir + "-" + children.length, dir: dir, weight: 1, children: children };
}

/** leaf is a node holding one pane. */
function leaf(id, paneID) { return { id: id, pane: paneID, weight: 1 }; }

// -------------------------------------------------------------------- boot

function boot() {
  sockets.length = 0; terms.length = 0; observers.length = 0;
  const doc = new Doc();
  parseInto(fs.readFileSync(path.join(ASSETS, "index.html"), "utf8"), doc);

  const store = new Map();
  const win = {
    innerWidth: 1400, innerHeight: 900,
    document: doc,
    location: { protocol: "http:", host: "127.0.0.1:7777" },
    WebSocket: FakeSocket,
    Terminal: FakeTerm,
    FitAddon: { FitAddon: FakeFit },
    SearchAddon: { SearchAddon: FakeSearch },
    WebglAddon: { WebglAddon: FakeWebgl },
    ResizeObserver: FakeResizeObserver,
    CSS: { escape: (s) => String(s).replace(/([^\w-])/g, "\\$1") },
    TextEncoder, TextDecoder, console,
    setTimeout, clearTimeout, setInterval, clearInterval, queueMicrotask,
    localStorage: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    },
    fetch: (...a) => (win._fetch ? win._fetch(...a) : Promise.reject(new Error("no fetch"))),
    confirm: () => (win._confirm === undefined ? true : win._confirm),
    prompt: () => win._prompt,
    close: () => { win._closed = true; },
    focus: () => {},
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
  const src = fs.readFileSync(path.join(ASSETS, "app.js"), "utf8");
  vm.runInContext(src, ctx, { filename: "app.js" });

  const h = {
    doc, win, sockets, terms, observers,
    control: sockets.find((s) => s.url.includes("/ws/control")),
    $: (id) => doc.getElementById(id),
    recv(msg) {
      if (!h.control || !h.control.onmessage) throw new Error("harness: the control socket has no reader");
      h.control.onmessage({ data: JSON.stringify(msg) });
    },
    /** hello delivers the real action table, read from the Go side, so the
     *  palette and the keyboard are tested against the bindings that ship. */
    hello(prefs) {
      const keys = JSON.parse(fs.readFileSync(path.join(ASSETS, "keys.json"), "utf8"));
      h.recv({ type: "hello", keys: keys, prefs: Object.assign({ helpSeen: true, dismissedTips: [] }, prefs || {}) });
      return keys;
    },
    keyTable() { return JSON.parse(fs.readFileSync(path.join(ASSETS, "keys.json"), "utf8")); },
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
    commands() { return h.control.sent.map((s) => JSON.parse(s)); },
    click(node, init) { return dispatch(node, new Ev("click", Object.assign({ target: node }, init))); },
    key(init) {
      const ev = new Ev("keydown", Object.assign({ key: "", ctrlKey: false, shiftKey: false, altKey: false }, init));
      ev.target = doc.activeElement;
      win.dispatchEvent(ev);
      return ev;
    },
    Ev, dispatch,
  };
  h.open();
  return h;
}

module.exports = { boot, fixture, pane, split, leaf, Ev, Element, TextNode, dispatch };
`
