// The real-page check (T98, M4): the five week-10 fixtures on the page users get.
//
// It builds cmd/graphfixture, serves each fixture with it (no
// workspace, no configuration directory), loads the page in headless Chromium
// over the Chrome DevTools protocol, and checks from the page alone:
//
//   1. the outline: a row for each agent, the decision blocks under the rows (permission,
//      question, down and conflict items of the fixture's .md, each with its head and the
//      agent's name, and no others), the chip on each row the .md names, a Down chip on the
//      agents that exited, and no chip of the item kinds anywhere else;
//   2. the drawer is closed on load; fixture 3's nested helper is the row after its parent,
//      one level in, and fixture 5's unread message is noted under the manager's row;
//   3. the second week-10 step: from a row, one click opens the drawer for that agent and the
//      text of what it waits on is on screen, within 2 s; the same from a key on the row and
//      from the block's Open button. The time runs from the pointer event to the text;
//   4. a window reached the way the relay reaches it (a remote hello): no graph command goes
//      out, the roster and the line about the desktop show.
//
// The page starts on the Team outline and the checks read it there. The canvas under
// "Graph" is not checked.
//
// Your own checks: --script FILE loads each fixture's page on the Team outline and calls the
// default export of FILE with { page, fixture, url, check, click, key, sleep }, in place of
// the checks above. See runScript below for what each is. --only N limits it to one fixture.
//
// It does not look at or change the configuration directory, but it lists it
// before and after. A change to the listing, or anything left in the sandbox,
// fails the run, because it means the page's server wrote where it should not.
// --allow-config-change turns that off, for a machine where another Flockdeck
// is running and changes the directory on its own.
//
// Whatever the run makes (the build directory, the sandbox, the Chrome profile,
// the server and the browser) is removed when it ends, however it ends: normally,
// on an error, on SIGINT or SIGTERM, or when --ceiling minutes (default 20) pass.
//
// With --shot DIR it also saves a dark screenshot (1440 by 900, or --shot-size WxH)
// of each fixture's page, as DIR/fixture-NN.png, for a pull request to show before
// and after. --shot-view NAME chooses a view of the tab strip's switch first, and
// --shot-feed pushes changed graphs so the activity feed has entries. --shot-open opens the first agent's drawer first, --shot-resize N widens the open drawer by N big steps of the keyboard, --shot-max opens the first outline row's drawer and maximises it, --shot-widen widens the rail, --shot-palette opens the command palette, --shot-team opens the Start a team dialog, and --shot-settings SECTION opens Settings there.
//
//   node scripts/realpage/check.mjs [--out result.json] [--slowdown 1,4] [--repeat 3] [--only N] [--script FILE] [--shot DIR]
//                                   [--allow-config-change] [--ceiling 20]
//
// CHROME names the browser; otherwise a Chromium of Playwright's is looked for.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn, execFileSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "..", "..");
const fixtureDir = path.join(repo, "internal", "server", "testdata", "graph-fixtures");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const args = process.argv.slice(2);
const opt = (name, dflt) => { const i = args.indexOf("--" + name); return i >= 0 ? args[i + 1] : dflt; };
const outFile = opt("out", "");
const slowdowns = opt("slowdown", "1,4").split(",").map(Number);
const repeat = Number(opt("repeat", "3"));
const only = opt("only", ""); // a fixture number, to run one
const shotDir = opt("shot", "");
const shotView = opt("shot-view", ""); // a view of the strip's switch to choose first: board, changes, replay
const shotSize = opt("shot-size", "1440x900").split("x").map(Number);
const shotResize = Number(args[args.indexOf("--shot-resize") + 1]) || 0; // with --shot-open: press Shift+Left on the drawer's border this many times
const shotLight = args.includes("--shot-light"); // draw the light theme instead of the dark one
const shotTab = opt("shot-tab", ""); // after opening the drawer, show this tab: conversation or changes
const shotClick = args.includes("--shot-click"); // open the first outline row's drawer with a real mouse click, not a script's click
const shotMax = args.includes("--shot-max"); // open the first outline row's drawer and maximise it
const shotOpen = args.includes("--shot-open"); // open the first agent's drawer first
const shotWiden = args.includes("--shot-widen"); // widen the rail first
const shotPalette = args.includes("--shot-palette"); // open the command palette first
const shotTeam = args.includes("--shot-team"); // open the Start a team dialog first
const shotFeed = args.includes("--shot-feed"); // give the activity feed entries first, by pushing changed graphs
const shotSettings = opt("shot-settings", ""); // open Settings on this section first
const scriptFile = opt("script", ""); // a module of your own to run on each fixture's page in place of the standard checks
const LIMIT_MS = 2000;
const allowConfigChange = args.includes("--allow-config-change");
const ceilingMin = Number(opt("ceiling", "20"));
const SEND_MS = 30000; // one DevTools call
const BUILD_MS = 180000;

// ------------------------------------------------------------------ cleanup

// Everything started or made is registered here, so that one function can undo
// it from the normal end, from a failure, from a signal and from the watchdog.
const live = { children: new Set(), dirs: [] };

function killTree(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  try {
    if (process.platform === "win32") execFileSync("taskkill", ["/pid", String(child.pid), "/T", "/F"], { stdio: "ignore", timeout: 10000 });
    else child.kill("SIGKILL");
  } catch { try { child.kill(); } catch { /* gone */ } }
}

function cleanup() {
  for (const c of live.children) killTree(c);
  live.children.clear();
  for (const d of live.dirs.splice(0)) {
    // Chrome's processes can hold the profile for a moment after they are killed.
    for (let i = 0; ; i++) {
      try { fs.rmSync(d, { recursive: true, force: true }); break; } catch (e) {
        if (i >= 25) { console.error("could not remove " + d + ": " + e.message); break; }
        Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 200);
      }
    }
  }
}

function die(code, why) {
  console.error(why);
  cleanup();
  process.exit(code);
}

process.on("SIGINT", () => die(130, "interrupted"));
process.on("SIGTERM", () => die(143, "terminated"));
setTimeout(() => die(1, `the check did not finish within ${ceilingMin} minutes`), ceilingMin * 60000).unref();

function tempDir(prefix) {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  live.dirs.push(d);
  return d;
}

// ------------------------------------------------------------------ expectations

// The reason words the page uses, from docs/team-graph.md section 5: a kind has a
// fixed phrase, and a down item says what its text says.
const WORDS = {
  permission: "asks to run a command",
  ask: "asks a question",
  returned: "task returned from review",
  conflict: "files overlap",
  "stale-unread": "has unread mail",
};

// kindOfMdLine reads the reason out of a line of a fixture's "Needs the person"
// list, in the words the .md uses.
function kindOfMdLine(text) {
  if (/^asks to run /.test(text)) return "permission";
  if (/^asks: /.test(text)) return "ask";
  if (/^task returned from review/.test(text)) return "returned";
  if (/ both change /.test(text)) return "conflict";
  if (/^exited with code \d+/.test(text)) return "down";
  if (/^\d+ unread/.test(text)) return "stale-unread";
  return "?";
}

function expectation(file) {
  const g = JSON.parse(fs.readFileSync(path.join(fixtureDir, file + ".json"), "utf8"));
  const md = fs.readFileSync(path.join(fixtureDir, file + ".md"), "utf8");
  const section = /## Needs the person[^\n]*\n([\s\S]*?)\n## /.exec(md)[1];
  const items = [];
  for (const line of section.split("\n")) {
    const m = /^- (.+?) \(([0-9a-f]{8})\)(?: and (.+?) \(([0-9a-f]{8})\))?: (.*)$/.exec(line);
    if (!m) continue;
    items.push({ pane: m[2], other: m[4] || "", kind: kindOfMdLine(m[5]), text: m[5] });
  }
  // The graph's needs in the order the graph has them: the teams' lists, then the solo one.
  const graphNeeds = [];
  for (const t of g.teams) for (const n of t.needs) graphNeeds.push(n);
  for (const n of g.soloNeeds || []) graphNeeds.push(n);
  const names = Object.fromEntries(g.nodes.map((n) => [n.pane, n.name]));
  return { file, graph: g, items, graphNeeds, names };
}

function wordOf(need) {
  if (need.kind === "down") {
    const m = /^exited with code (-?\d+)/.exec(need.text || "");
    return m ? "exited with code " + m[1] : "failed";
  }
  return WORDS[need.kind];
}

// ------------------------------------------------------------------ the fixture server

function configDir() {
  if (process.platform === "win32") return path.join(process.env.APPDATA || "", "flockdeck");
  if (process.platform === "darwin") return path.join(os.homedir(), "Library", "Application Support", "flockdeck");
  return path.join(process.env.XDG_CONFIG_HOME || path.join(os.homedir(), ".config"), "flockdeck");
}
function listDir(dir) {
  const out = [];
  const walk = (d) => {
    let ents = [];
    try { ents = fs.readdirSync(d, { withFileTypes: true }); } catch { return; }
    for (const e of ents) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p);
      else { const st = fs.statSync(p); out.push(`${path.relative(dir, p)} ${st.size} ${Math.round(st.mtimeMs)}`); }
    }
  };
  walk(dir);
  return out.sort();
}

function buildServer(tmp) {
  const exe = path.join(tmp, process.platform === "win32" ? "graphfixture.exe" : "graphfixture");
  execFileSync("go", ["build", "-o", exe, "./cmd/graphfixture"], { cwd: repo, stdio: "inherit", timeout: BUILD_MS });
  return exe;
}

// A sandbox environment: anything the server wrote would land here, not in the
// real configuration directory.
function sandboxEnv(dir) {
  return { PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, HOME: dir, USERPROFILE: dir, APPDATA: dir, LOCALAPPDATA: dir, XDG_CONFIG_HOME: dir, TEMP: dir, TMP: dir, TMPDIR: dir };
}

async function startServer(exe, fixture, sandbox) {
  const child = spawn(exe, ["-fixture", fixture, "-fixtures", fixtureDir], { env: sandboxEnv(sandbox), cwd: repo, stdio: ["ignore", "pipe", "pipe"] });
  live.children.add(child);
  let out = "";
  child.stdout.on("data", (d) => (out += d));
  child.stderr.on("data", (d) => (out += d));
  for (let i = 0; i < 200 && !/open: /.test(out); i++) await sleep(50);
  const m = /open: (http:\/\/127\.0\.0\.1:\d+\/\?t=[0-9a-f]+)/.exec(out);
  if (!m) { killTree(child); live.children.delete(child); throw new Error("the fixture server did not start: " + out); }
  return { url: m[1], stop: () => { killTree(child); live.children.delete(child); } };
}

// ------------------------------------------------------------------ the browser

function findChrome() {
  if (process.env.CHROME) return process.env.CHROME;
  const roots = [path.join(process.env.LOCALAPPDATA || "", "ms-playwright"), path.join(os.homedir(), ".cache", "ms-playwright"), path.join(os.homedir(), "Library", "Caches", "ms-playwright")];
  for (const r of roots) {
    let dirs = [];
    try { dirs = fs.readdirSync(r).filter((d) => d.startsWith("chromium-")).sort().reverse(); } catch { continue; }
    for (const d of dirs) for (const rel of ["chrome-win64/chrome.exe", "chrome-win/chrome.exe", "chrome-linux/chrome", "chrome-mac/Chromium.app/Contents/MacOS/Chromium"]) {
      const p = path.join(r, d, rel);
      if (fs.existsSync(p)) return p;
    }
  }
  throw new Error("no Chromium found; set CHROME");
}

class Browser {
  static async launch() {
    const dir = tempDir("realpage-profile-");
    const port = 9400 + Math.floor(Math.random() * 400);
    const child = spawn(findChrome(), ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${dir}`, "--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-background-networking", "--window-size=1280,900", "about:blank"], { stdio: "ignore" });
    live.children.add(child);
    let ver = null;
    for (let i = 0; i < 100 && !ver; i++) { try { ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); } catch { await sleep(100); } }
    if (!ver) throw new Error("the browser did not start");
    const b = new Browser(child, ver);
    await new Promise((res, rej) => {
      const t = setTimeout(() => rej(new Error("the DevTools socket did not open")), 10000);
      b.ws.onopen = () => { clearTimeout(t); res(); };
      b.ws.onerror = () => { clearTimeout(t); rej(new Error("the DevTools socket failed to open")); };
    });
    return b;
  }
  constructor(child, ver) {
    this.child = child;
    this.version = ver.Browser;
    this.userAgent = ver["User-Agent"];
    this.ws = new WebSocket(ver.webSocketDebuggerUrl);
    this.id = 0;
    this.pending = new Map();
    this.events = [];
    // A browser that dies mid-run must not leave a call waiting for ever.
    this.ws.onclose = () => {
      for (const { rej, timer } of this.pending.values()) { clearTimeout(timer); rej(new Error("the browser closed the DevTools socket")); }
      this.pending.clear();
    };
    this.ws.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id && this.pending.has(m.id)) {
        const { res, rej, timer } = this.pending.get(m.id);
        clearTimeout(timer);
        this.pending.delete(m.id);
        if (m.error) rej(new Error(m.error.message)); else res(m.result);
      } else this.events.push(m);
    };
  }
  send(method, params = {}, sessionId) {
    return new Promise((res, rej) => {
      const id = ++this.id;
      const timer = setTimeout(() => { this.pending.delete(id); rej(new Error(`${method} got no answer in ${SEND_MS / 1000} s`)); }, SEND_MS);
      this.pending.set(id, { res, rej, timer });
      try { this.ws.send(JSON.stringify({ id, method, params, sessionId })); } catch (e) { clearTimeout(timer); this.pending.delete(id); rej(e); }
    });
  }
  async page() {
    const br = this;
    const { targetId } = await this.send("Target.createTarget", { url: "about:blank" });
    const { sessionId } = await this.send("Target.attachToTarget", { targetId, flatten: true });
    const S = (m, p) => this.send(m, p, sessionId);
    await S("Runtime.enable");
    await S("Page.enable");
    const errors = [];
    const watch = (ev) => {
      if (ev.sessionId !== sessionId) return;
      if (ev.method === "Runtime.exceptionThrown") errors.push("exception: " + ((ev.params.exceptionDetails.exception && ev.params.exceptionDetails.exception.description) || ev.params.exceptionDetails.text));
      if (ev.method === "Runtime.consoleAPICalled" && ev.params.type === "error") errors.push("console: " + ev.params.args.map((a) => a.value || a.description).join(" "));
    };
    const timer = setInterval(() => { while (this.events.length) watch(this.events.shift()); }, 20);
    return {
      S, errors,
      async eval(expr) {
        const r = await S("Runtime.evaluate", { expression: expr, returnByValue: true, awaitPromise: true });
        if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails.exception || r.exceptionDetails));
        return r.result.value;
      },
      async until(expr, ms = 8000) {
        const t0 = Date.now();
        while (Date.now() - t0 < ms) { if (await this.eval(expr)) return true; await sleep(25); }
        return false;
      },
      async close() { clearInterval(timer); await br.send("Target.closeTarget", { targetId }).catch(() => {}); },
    };
  }
  close() { try { this.ws.close(); } catch { /* closed */ } killTree(this.child); live.children.delete(this.child); }
}

// What a page reports of itself: the outline's rows with their chips, the
// decision blocks under them, and the drawer.
const READ = `(() => {
  const text = (e) => (e ? e.textContent.trim() : "");
  const rows = [...document.querySelectorAll(".tree-row")].map((r) => ({
    pane: r.getAttribute("data-pane"), level: Number(r.getAttribute("aria-level")),
    chips: [...r.querySelectorAll(".tree-chip")].map((c) => ({ kind: c.className.split(" ").find((k) => k.startsWith("tree-chip--")).slice(11), text: text(c) })),
  }));
  const decisions = [...document.querySelectorAll("[data-decision]")].map((d) => ({
    kind: d.getAttribute("data-decision"), pane: d.getAttribute("data-pane"), head: text(d.querySelector(".dec-kind")), line: text(d.querySelector(".dec-line")),
    buttons: [...d.querySelectorAll("button")].map(text),
  }));
  const drawer = document.querySelector(".drawer");
  return {
    rows, decisions,
    notes: [...document.querySelectorAll(".tree-notes")].map(text),
    drawers: document.querySelectorAll("[data-drawer-pane]").length,
    drawerHidden: drawer ? drawer.hidden : true,
  };
})()`;

async function load(browser, url, boxesExpected, rate) {
  const page = await browser.page();
  await page.S("Emulation.setCPUThrottlingRate", { rate });
  if (shotFeed) {
    await page.S("Page.addScriptToEvaluateOnNewDocument", {
      source: `(() => {
        const Real = window.WebSocket;
        window.WebSocket = function (url, p) {
          const ws = new Real(url, p);
          window.__ws = ws;
          ws.addEventListener("message", (ev) => { try { const m = JSON.parse(ev.data); if (m.type === "graph") window.__graph = m; } catch { /* not JSON */ } });
          return ws;
        };
        window.WebSocket.prototype = Real.prototype;
        for (const k of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) window.WebSocket[k] = Real[k];
      })()`,
    });
  }
  await page.S("Page.navigate", { url });
  // The page starts on the Team outline. The canvas, kept one more release, is under "Graph".
  const ok = await page.until(`document.querySelectorAll(".tree-row").length >= ${boxesExpected}`);
  return { page, ok };
}

// screenshot saves the page as it is, at 1440 by 900 in the dark scheme, and puts
// the window back as it was.
async function screenshot(page, file) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  await page.S("Emulation.setDeviceMetricsOverride", { width: shotSize[0], height: shotSize[1], deviceScaleFactor: 1, mobile: false });
  await page.S("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: shotLight ? "light" : "dark" }] });
  if (shotLight) await page.eval(`document.documentElement.setAttribute("data-theme", "light")`);
  if (shotFeed) {
    // Three pushes after the first: a question, a helper that ended and a message, then the message read.
    const push = (edit) => page.eval(`(() => { const g = JSON.parse(JSON.stringify(window.__graph)); (${edit})(g); window.__graph = g; window.__ws.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(g) })); })()`);
    await push(`(g) => { const h = g.nodes.filter((n) => n.parent); const t = g.teams[0]; if (h[0]) t.needs = (t.needs || []).concat([{ id: "ask:" + h[0].pane, kind: "ask", pane: h[0].pane, time: new Date().toISOString(), text: "which flag store should T2 use?" }]); if (h[1]) h[1].status = "exited"; if (h[1] && h[2]) g.edges.push({ kind: "message", from: h[1].pane, to: h[2].pane, id: 9001, time: new Date().toISOString(), text: "T3 is ready for review", delivery: "queued" }); }`);
    await sleep(100);
    await push(`(g) => { for (const e of g.edges) if (e.id === 9001) e.delivery = "read"; const h = g.nodes.filter((n) => n.parent); if (h[3]) h[3].task = { id: "T9", title: "x", state: "in-review" }; }`);
    await sleep(200);
  }
  if (shotOpen) await page.eval(`(() => { const r = document.querySelector(".tree-row"); if (r) r.click(); })()`);
  if (shotWiden) await page.eval(`(() => { const b = document.querySelector(".rail-toggle"); if (b) b.click(); })()`);
  if (shotPalette) await page.eval(`document.dispatchEvent(new KeyboardEvent("keydown", { key: "K", code: "KeyK", ctrlKey: true, shiftKey: true, bubbles: true }))`);
  if (shotTeam) {
    await page.eval(`document.dispatchEvent(new KeyboardEvent("keydown", { key: "K", code: "KeyK", ctrlKey: true, shiftKey: true, bubbles: true }))`);
    await sleep(300);
    await page.eval(`(() => { const r = document.querySelector('.palette-row[data-action="startTeam"]'); if (r) r.click(); })()`);
  }
  if (shotSettings) {
    await page.eval(`(() => { const b = document.querySelector(".rail-settings"); if (b) b.click(); })()`);
    await sleep(300);
    await page.eval(`(() => { const t = document.querySelector('.settings-tab[data-section="${shotSettings}"]'); if (t) t.click(); })()`);
  }
  if (shotView) await page.eval(`(() => { const b = document.querySelector('.tabs-view[data-view="${shotView}"]'); if (b) b.click(); })()`);
  await sleep(300);
  if (shotClick) {
    const at = await page.eval(`(() => { const r = document.querySelector(".tree-row"); if (!r) return null; const b = r.getBoundingClientRect(); return { x: b.left + 160, y: b.top + b.height / 2, n: document.querySelectorAll('.tree-row').length }; })()`);
    console.log('click at ' + JSON.stringify(at));
    if (at) {
      await page.S("Input.dispatchMouseEvent", { type: "mouseMoved", x: at.x, y: at.y });
      await page.S("Input.dispatchMouseEvent", { type: "mousePressed", x: at.x, y: at.y, button: "left", clickCount: 1 });
      await page.S("Input.dispatchMouseEvent", { type: "mouseReleased", x: at.x, y: at.y, button: "left", clickCount: 1 });
      await sleep(600);
    }
  }
  if (shotTab) {
    await page.eval(`(() => { const t = document.querySelector("#drawer-tab-${shotTab}"); if (t) t.click(); })()`);
    await sleep(400);
  }
  if (shotResize) {
    await sleep(500);
    await page.eval(`(() => { const h = document.querySelector(".drawer-resize"); if (!h) return; for (let i = 0; i < ${shotResize}; i++) h.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", shiftKey: true, bubbles: true })); })()`);
  }
  if (shotMax) await sleep(400);
  if (shotMax) {
    await page.eval(`(() => { const r = document.querySelector(".tree-row"); if (r) r.click(); })()`);
    await sleep(500);
    await page.eval(`(() => { const b = document.querySelector(".drawer-max"); if (b) b.click(); })()`);
  }
  await sleep(800);
  // How far the page itself scrolls sideways, and the elements that reach past the window (inner scrollers do not count).
  const wide = await page.eval(`(() => { const w = document.documentElement.clientWidth; const over = []; for (const e of document.querySelectorAll("body *")) { const r = e.getBoundingClientRect(); if (r.width > 0 && r.right > w + 1) { let p = e.parentElement, clipped = false; while (p && p !== document.body) { if (getComputedStyle(p).overflowX !== "visible") { clipped = true; break; } p = p.parentElement; } if (!clipped) over.push(e.tagName.toLowerCase() + "." + String(e.className && e.className.baseVal !== undefined ? e.className.baseVal : e.className).split(" ")[0] + " right=" + Math.round(r.right)); } } return { scroll: document.documentElement.scrollWidth, width: w, over: over.slice(0, 6) }; })()`);
  console.log("page width " + (wide && wide.width) + ", scroll width " + (wide && wide.scroll) + (wide && wide.over.length ? ", reaching past: " + wide.over.join("; ") : ""));
  const { data } = await page.S("Page.captureScreenshot", { format: "png" });
  fs.writeFileSync(file, Buffer.from(data, "base64"));
  await page.S("Emulation.clearDeviceMetricsOverride");
}

// ------------------------------------------------------------------ checks

// The decision blocks draw four kinds of item (ui/src/graph/DecisionBlock.js), with these heads.
const HEADS = { permission: "Permission", ask: "Question", conflict: "Files overlap", down: "Down" };
// The chip a row carries for an item of each kind, in the words of ui/src/graph/chips.js.
const CHIP_WORDS = { permission: "Permission", ask: "Question", returned: "Returned", down: "Down" };

function fixtureChecks(ex, seen) {
  const results = [];
  const check = (name, pass, detail) => results.push({ name, pass: !!pass, detail: detail || "" });
  const rowOf = (pane) => seen.rows.find((r) => r.pane === pane);
  const nodes = ex.graph.nodes;

  check("every agent is a row", seen.rows.length === nodes.length && nodes.every((n) => rowOf(n.pane)), `${seen.rows.length} rows for ${nodes.length} agents`);
  check("the drawer is closed on load", seen.drawers === 0 && seen.drawerHidden === true, `drawers=${seen.drawers} hidden=${seen.drawerHidden}`);

  // The blocks: one for each permission, question, down and conflict the .md lists (a
  // conflict under the first pane of the item only), and no others. The .md names the two
  // panes of a conflict as it likes and the graph in sorted order, so both are compared as the sorted pair.
  const keyOf = (kind, pane, other) => kind + ":" + (kind === "conflict" ? [pane, other].sort().join("+") : pane);
  const drawn = (kind) => kind in HEADS;
  const blockSet = seen.decisions.map((d) => {
    const need = ex.graphNeeds.find((n) => n.pane === d.pane && n.kind === d.kind);
    return keyOf(d.kind, d.pane, need && need.other);
  }).sort();
  const mdSet = ex.items.filter((i) => drawn(i.kind)).map((i) => keyOf(i.kind, i.pane, i.other)).sort();
  check("the decision blocks are the .md's permissions, questions, downs and conflicts, and no others", JSON.stringify(blockSet) === JSON.stringify(mdSet), `blocks ${JSON.stringify(blockSet)} vs md ${JSON.stringify(mdSet)}`);
  // Heads and names.
  const wrong = [];
  for (const d of seen.decisions) {
    if (d.head !== HEADS[d.kind]) wrong.push(`${d.pane} ${d.kind}: head "${d.head}" not "${HEADS[d.kind]}"`);
    const who = ex.names[d.pane];
    if (!d.line.startsWith(who)) wrong.push(`${d.pane}: line "${d.line.slice(0, 40)}" does not start with "${who}"`);
  }
  check("each block has its kind's head and starts with the agent's name", wrong.length === 0, wrong.join("; "));
  // The order: blocks come in the outline's row order, and the needs of one pane in the graph's order.
  const rowIndex = (pane) => seen.rows.findIndex((r) => r.pane === pane);
  const inRowOrder = seen.decisions.every((d, i) => i === 0 || rowIndex(seen.decisions[i - 1].pane) <= rowIndex(d.pane));
  check("the blocks follow the outline's rows", inRowOrder, JSON.stringify(seen.decisions.map((d) => d.pane + ":" + d.kind)));

  // Chips: on the row of each pane the .md names, with the kind's word, and no chip of these kinds elsewhere.
  const wantChips = new Map();
  const want = (pane, text) => { if (!wantChips.has(pane)) wantChips.set(pane, new Set()); wantChips.get(pane).add(text); };
  for (const it of ex.items) {
    if (it.kind === "conflict") {
      const names = ex.names;
      want(it.pane, "overlaps " + names[it.other]);
      want(it.other, "overlaps " + names[it.pane]);
    } else if (CHIP_WORDS[it.kind]) want(it.pane, CHIP_WORDS[it.kind]);
  }
  const chipWrong = [];
  for (const [pane, texts] of wantChips) {
    const row = rowOf(pane);
    const have = new Set((row ? row.chips : []).map((c) => c.text));
    for (const t of texts) if (!have.has(t)) chipWrong.push(`${pane} lacks "${t}" (has ${JSON.stringify([...have])})`);
  }
  // A Down chip comes from the agent having exited, whether or not the .md lists an item for it.
  const kindWords = new Set(Object.values(CHIP_WORDS).filter((w) => w !== "Down"));
  for (const r of seen.rows) for (const c of r.chips) if (kindWords.has(c.text) && !(wantChips.get(r.pane) || new Set()).has(c.text)) chipWrong.push(`${r.pane} has an unlisted "${c.text}"`);
  check("each row the .md names has the chip of its item, and no other row has one", chipWrong.length === 0, chipWrong.join("; "));
  const downWrong = [];
  for (const n of nodes) {
    const has = (rowOf(n.pane) ? rowOf(n.pane).chips : []).some((c) => c.text === "Down");
    if (has !== (n.status === "exited")) downWrong.push(`${n.pane} (${n.status}) ${has ? "has" : "lacks"} a Down chip`);
  }
  check("a row has a Down chip when its agent has exited, and not otherwise", downWrong.length === 0, downWrong.join("; "));

  if (ex.file.startsWith("03-")) {
    // The nested helper: shards (21c76d4a) is a helper of indexer (025b0446), the row after it, one level in.
    const a = rowOf("025b0446"), b = rowOf("21c76d4a");
    const next = a && seen.rows[seen.rows.indexOf(a) + 1];
    check("fixture 3: the nested helper is the next row after its parent, one level in", a && b && next === b && b.level === a.level + 1, JSON.stringify({ indexer: a && a.level, shards: b && b.level }));
  }
  if (ex.file.startsWith("05-")) {
    check("fixture 5: the manager's unread message is noted under its row", seen.notes.length === 1 && /Unread mail/.test(seen.notes[0]) && /manager/.test(seen.notes[0]), JSON.stringify(seen.notes));
  }
  return results;
}

// The second week-10 step. target is the need to open: the first permission or
// question in the graph's order. The text looked for is the first 30 characters
// of the need's text, which is what the drawer's card shows of it.
async function secondCheck(browser, url, ex, rate) {
  const target = ex.graphNeeds.find((n) => n.kind === "permission" || n.kind === "ask");
  const probe = (target.text || "").replace(/^[\s\S]*?\n/, (m) => m).split("\n")[0].trim().slice(0, 30);
  const out = { pane: target.pane, kind: target.kind, name: ex.names[target.pane], text: probe, rate, row: {}, rowKey: {}, button: {} };
  const way = async (label, how) => {
    const { page, ok } = await load(browser, url, ex.graph.nodes.length, rate);
    if (!ok) { out[label] = { error: "the page did not draw" }; await page.close(); return; }
    // The rows are drawn before the page's handlers are all in on a slow machine; a click in that gap does nothing.
    await sleep(rate > 1 ? 1200 : 400);
    // Marks the time of the pointer or key event, and the first moment the text is in the drawer.
    await page.eval(`(() => {
      window.__t = { down: null, shown: null };
      const probe = ${JSON.stringify(probe)};
      const seen = () => { const d = document.querySelector(".drawer"); return d && !d.hidden && d.textContent.includes(probe); };
      addEventListener("pointerdown", () => { if (window.__t.down === null) window.__t.down = performance.now(); }, true);
      addEventListener("keydown", () => { if (window.__t.down === null) window.__t.down = performance.now(); }, true);
      const check = () => { if (window.__t.shown === null && seen()) { window.__t.shown = performance.now(); return; } if (window.__t.shown === null) requestAnimationFrame(check); };
      new MutationObserver(() => { if (window.__t.shown === null && seen()) window.__t.shown = performance.now(); }).observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true });
      requestAnimationFrame(check);
    })()`);
    const r = await how(page);
    await sleep(LIMIT_MS + 500);
    const t = await page.eval("window.__t");
    const drawer = await page.eval(`(() => { const d = document.querySelector("[data-drawer-pane]"); return d ? d.getAttribute("data-drawer-pane") : null; })()`);
    const card = await page.eval(`(() => {
      const t = (e) => (e ? e.textContent.trim() : "");
      const d = document.querySelector(".drawer");
      return {
        command: t(document.querySelector(".drawer .prompt-command")),
        yes: !!document.querySelector('.drawer button[data-option="yes"]'),
        no: !!document.querySelector('.drawer button[data-option="no"]'),
        question: t(document.querySelector(".drawer .prompt-question .prompt-text")),
        fallback: !!d && d.textContent.includes("The choices are in the terminal below."),
      };
    })()`);
    out[label] = { ...r, opened: drawer, card, ms: t.down !== null && t.shown !== null ? Math.round((t.shown - t.down) * 10) / 10 : null };
    out[label].pass = out[label].opened === target.pane && out[label].ms !== null && out[label].ms < LIMIT_MS;
    // The card is the one a real desk sends: the whole command and both choices of
    // a permission, the whole question of an ask, and not the graph's cut text.
    const c = out[label].card;
    out[label].cardOk = target.kind === "permission"
      ? c.command === target.text.trim() && c.yes && c.no
      : c.question === target.text.trim() && !c.fallback;
    await page.close();
  };
  const click = async (page, sel) => {
    const c = await page.eval(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
    if (!c) return { error: "no such element: " + sel };
    await page.S("Input.dispatchMouseEvent", { type: "mousePressed", x: c.x, y: c.y, button: "left", clickCount: 1 });
    await page.S("Input.dispatchMouseEvent", { type: "mouseReleased", x: c.x, y: c.y, button: "left", clickCount: 1 });
    return {};
  };
  // A click on the agent's row.
  await way("row", async (page) => click(page, `.tree-row[data-pane="${target.pane}"]`));
  // A key on the row: focus it, then Enter. Rows take the keyboard one at a time, so
  // the row is focused directly here; that Tab and the arrow keys reach it is covered
  // by the node tests.
  await way("rowKey", async (page) => {
    const focusable = await page.eval(`(() => { const e = document.querySelector('.tree-row[data-pane="${target.pane}"]'); if (!e) return false; e.focus(); return document.activeElement === e; })()`);
    await page.S("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", text: "\r", windowsVirtualKeyCode: 13 });
    await page.S("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
    return { rowFocusable: focusable };
  });
  // The button of the decision block, "Open <name>".
  await way("button", async (page) => {
    const tagged = await page.eval(`(() => { const b = [...document.querySelectorAll('[data-decision="${target.kind}"][data-pane="${target.pane}"] button')].find((x) => /^Open /.test(x.textContent.trim())); if (!b) return false; b.setAttribute("data-realpage", "open"); return true; })()`);
    if (!tagged) return { error: "the block has no Open button" };
    return click(page, '[data-realpage="open"]');
  });
  return out;
}

// A window reached as the relay reaches it: the hello says remote. The page is
// the same page; only the hello it is told differs.
async function remoteCheck(browser, url, ex) {
  const page = await browser.page();
  await page.S("Page.addScriptToEvaluateOnNewDocument", {
    source: `(() => {
      const Real = window.WebSocket;
      window.__sent = [];
      window.WebSocket = function (url, p) {
        const ws = new Real(url, p);
        const send = ws.send.bind(ws);
        ws.send = (d) => { try { window.__sent.push(typeof d === "string" ? JSON.parse(d) : "binary"); } catch { window.__sent.push("?"); } return send(d); };
        const desc = Object.getOwnPropertyDescriptor(Real.prototype, "onmessage");
        let handler = null;
        Object.defineProperty(ws, "onmessage", { get: () => handler, set: (fn) => { handler = fn; desc.set.call(ws, (ev) => {
          let data = ev.data;
          try { const m = JSON.parse(data); if (m.type === "hello") { m.remote = true; data = JSON.stringify(m); } } catch { /* not JSON */ }
          fn({ data });
        }); } });
        return ws;
      };
      window.WebSocket.prototype = Real.prototype;
      for (const k of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) window.WebSocket[k] = Real[k];
    })()`,
  });
  await page.S("Page.navigate", { url });
  await page.until(`!!document.querySelector(".home")`);
  await sleep(1500);
  const r = await page.eval(`(() => ({
    sent: window.__sent.map((c) => (c && c.cmd) || c),
    home: (document.querySelector(".home") || {}).textContent || "",
    roster: [...document.querySelectorAll(".home-agent")].map((e) => e.textContent.trim()),
    graph: document.querySelectorAll("g.gnode").length,
    mbar: !!document.querySelector(".mbar"),
    drawer: document.querySelectorAll("[data-drawer-pane]").length,
  }))()`);
  const results = [];
  const check = (name, pass, detail) => results.push({ name, pass: !!pass, detail: detail || "" });
  check("no graph, structured or paneSend command went out", !r.sent.some((c) => typeof c === "string" && /^(graph|structured|paneSend|permissionAnswer)/.test(c)), JSON.stringify(r.sent));
  check("the desktop line shows", /The team graph is shown on the desktop/.test(r.home), r.home.slice(0, 80));
  check("the roster shows every agent", r.roster.length === ex.graph.nodes.length, `${r.roster.length} of ${ex.graph.nodes.length}`);
  check("no graph, no manager bar, no drawer", r.graph === 0 && !r.mbar && r.drawer === 0, JSON.stringify({ graph: r.graph, mbar: r.mbar, drawer: r.drawer }));
  check("no page errors", page.errors.length === 0, page.errors.join(" | "));
  await page.close();
  return results;
}

// A script of your own. It is a module whose default export is called once per
// fixture, with the page loaded on the Team outline:
//
//   export default async function ({ page, fixture, url, check, click, key, sleep }) {
//     check("a tab for the goal", await page.eval(`!!document.querySelector(".tabs")`));
//   }
//
// page.eval(expr) runs an expression in the page and returns its value, page.until(expr, ms) waits
// for it to be true, page.S(method, params) is a raw DevTools call, page.errors holds the page's
// errors so far. fixture has file, num, graph (the push), names and the .md's items. click(selector)
// presses the element with a real mouse, key(name) sends a key. check(name, pass, detail) records a
// result; a script that throws fails the run. Run it with --script path/to/file.mjs [--only N].
async function runScript(page, url, ex, num) {
  const mod = await import(pathToFileURL(path.resolve(scriptFile)).href);
  if (typeof mod.default !== "function") throw new Error(scriptFile + " has no default export to call");
  const results = [];
  const check = (name, pass, detail) => results.push({ name, pass: !!pass, detail: detail || "" });
  const click = async (selector) => {
    const c = await page.eval(`(() => { const e = document.querySelector(${JSON.stringify(selector)}); if (!e) return null; e.scrollIntoView({ block: "center" }); const r = e.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
    if (!c) throw new Error("no such element: " + selector);
    await page.S("Input.dispatchMouseEvent", { type: "mousePressed", x: c.x, y: c.y, button: "left", clickCount: 1 });
    await page.S("Input.dispatchMouseEvent", { type: "mouseReleased", x: c.x, y: c.y, button: "left", clickCount: 1 });
  };
  const keys = { Enter: [13, "\r"], Escape: [27, ""], Tab: [9, ""], ArrowDown: [40, ""], ArrowUp: [38, ""], ArrowLeft: [37, ""], ArrowRight: [39, ""], " ": [32, " "] };
  const key = async (name) => {
    const [code, text] = keys[name] || [name.toUpperCase().charCodeAt(0), name];
    await page.S("Input.dispatchKeyEvent", { type: "keyDown", key: name, code: name, text, windowsVirtualKeyCode: code });
    await page.S("Input.dispatchKeyEvent", { type: "keyUp", key: name, code: name, windowsVirtualKeyCode: code });
  };
  try {
    await mod.default({ page, fixture: { file: ex.file, num, graph: ex.graph, names: ex.names, items: ex.items, needs: ex.graphNeeds }, url, check, click, key, sleep });
  } catch (e) {
    check("the script ran to the end", false, e && e.stack ? e.stack.split("\n").slice(0, 3).join(" | ") : String(e));
  }
  check("no page errors", page.errors.length === 0, page.errors.join(" | "));
  return results;
}

// ------------------------------------------------------------------ run

const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith(".json")).map((f) => f.slice(0, -5)).filter((f) => !only || f.startsWith(only.padStart(2, "0") + "-")).sort();
const tmp = tempDir("realpage-");
const sandbox = tempDir("realpage-sandbox-");
const cfg = configDir();
const before = listDir(cfg);
let browser = null;
let after = before;
const report = { platform: process.platform + " " + os.release(), node: process.version, slowdowns, repeat, limitMs: LIMIT_MS, fixtures: [] };

try {
  const exe = buildServer(tmp);
  browser = await Browser.launch();
  Object.assign(report, { browser: browser.version, userAgent: browser.userAgent });
  for (const file of files) {
    const ex = expectation(file);
    const num = String(Number(file.slice(0, 2)));
    const entry = { file, checks: [], second: [], remote: [] };
    const server = await startServer(exe, num, sandbox);
    try {
      const { page, ok } = await load(browser, server.url, ex.graph.nodes.length, 1);
      if (!ok) entry.checks.push({ name: "the page drew the outline", pass: false, detail: "no rows" });
      else if (scriptFile) {
        entry.checks.push(...(await runScript(page, server.url, ex, num)));
      } else {
        await sleep(500);
        if (shotDir) await screenshot(page, path.join(shotDir, "fixture-" + num.padStart(2, "0") + ".png"));
        const seen = await page.eval(READ);
        entry.checks.push(...fixtureChecks(ex, seen));
        entry.checks.push({ name: "no page errors on load", pass: page.errors.length === 0, detail: page.errors.join(" | ") });
        entry.mbar = seen.mbar;
      }
      await page.close();
      if (scriptFile) { report.fixtures.push(entry); continue; }
      for (const rate of slowdowns) {
        for (let i = 0; i < repeat; i++) entry.second.push(await secondCheck(browser, server.url, ex, rate));
      }
      entry.remote = await remoteCheck(browser, server.url, ex);
    } finally {
      server.stop();
    }
    report.fixtures.push(entry);
  }
  after = listDir(cfg);
  report.config = { dir: cfg, before: before.length, after: after.length, changed: JSON.stringify(before) !== JSON.stringify(after), sandboxLeft: fs.readdirSync(sandbox) };
} catch (e) {
  console.error(e && e.stack ? e.stack : String(e));
  cleanup();
  process.exit(1);
} finally {
  if (browser) browser.close();
  cleanup();
}

// ------------------------------------------------------------------ the report

const lines = [];
let failed = 0;
for (const f of report.fixtures) {
  lines.push(`\n${f.file}`);
  for (const c of [...f.checks, ...f.remote]) { if (!c.pass) failed++; lines.push(`  ${c.pass ? "ok  " : "FAIL"} ${c.name}${c.pass ? "" : ": " + c.detail}`); }
  const bySlow = {};
  for (const s of f.second) (bySlow[s.rate] ||= []).push(s);
  for (const [rate, list] of Object.entries(bySlow)) {
    for (const way of ["row", "rowKey", "button"]) {
      const ms = list.map((s) => s[way] && s[way].ms).filter((x) => x != null).sort((a, b) => a - b);
      const passes = list.filter((s) => s[way] && s[way].pass).length;
      const first = list[0][way] || {};
      const detail = way === "rowKey" ? ` rowFocusable=${first.rowFocusable}` : "";
      lines.push(`  ${passes === list.length ? "ok  " : "FAIL"} second step by ${way} at ${rate}x CPU slowdown: ${passes}/${list.length} opened ${list[0].name} within 2 s; ms ${ms.length ? ms.join(", ") : "none"}${detail}`);
      if (passes !== list.length) failed++;
    }
    const cards = list.map((s) => s.row && s.row.cardOk);
    const cardOk = cards.length > 0 && cards.every(Boolean);
    const c0 = (list[0].row && list[0].row.card) || {};
    lines.push(`  ${cardOk ? "ok  " : "FAIL"} the card at ${rate}x shows ${list[0].kind === "permission" ? "the whole command and the Yes and No buttons" : "the whole question"}: command=${JSON.stringify(c0.command)} yes=${c0.yes} no=${c0.no} question=${JSON.stringify(c0.question)} fallback=${c0.fallback}`);
    if (!cardOk) failed++;
  }
}
lines.push(`\nbrowser ${report.browser}`);
lines.push(`config dir ${cfg}: ${before.length} files before, ${after.length} after, ${report.config.changed ? "CHANGED" : "unchanged"}; sandbox left ${JSON.stringify(report.config.sandboxLeft)}`);
if ((report.config.changed || report.config.sandboxLeft.length) && !allowConfigChange) {
  failed++;
  lines.push("FAIL the configuration directory changed or the sandbox was written to; the page's server must not do either (pass --allow-config-change if another Flockdeck on this machine explains it)");
}
lines.push(failed ? `\n${failed} check(s) failed` : "\nall checks passed");
console.log(lines.join("\n"));
if (outFile) fs.writeFileSync(outFile, JSON.stringify(report, null, 2));
process.exit(failed ? 1 : 0);
