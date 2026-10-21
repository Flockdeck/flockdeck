// The real-page check (T98, M4): the five week-10 fixtures on the page users get.
//
// It builds cmd/graphfixture with -tags next, serves each fixture with it (no
// workspace, no configuration directory), loads the page in headless Chromium
// over the Chrome DevTools protocol, and checks from the page alone:
//
//   1. the strip: the rows the fixture's .md says need the person, with the right
//      reason word, in the order of the graph's needs, and no others;
//   2. the badges: a badge on each pane of those rows and on no other pane;
//   3. the drawer is closed on load; fixture 3's nested helper and fixture 5's
//      undelivered message edge are drawn;
//   4. the second week-10 step: from a strip row, one click opens the drawer for
//      that agent and the text of what it waits on is on screen, within 2 s;
//      the same from a key. The time runs from the pointer event to the text;
//   5. a window reached the way the relay reaches it (a remote hello): no graph
//      command goes out, the roster and the line about the desktop show.
//
// It does not look at or change the configuration directory, but it lists it
// before and after and says whether anything changed (a Flockdeck running on the
// same machine can change it on its own, so a change is reported, not assumed).
//
//   node scripts/realpage/check.mjs [--out result.json] [--slowdown 1,4] [--repeat 3] [--only N]
//
// CHROME names the browser; otherwise a Chromium of Playwright's is looked for.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawn, execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

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
const LIMIT_MS = 2000;

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
  execFileSync("go", ["build", "-tags", "next", "-o", exe, "./cmd/graphfixture"], { cwd: repo, stdio: "inherit" });
  return exe;
}

// A sandbox environment: anything the server wrote would land here, not in the
// real configuration directory.
function sandboxEnv(dir) {
  return { PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, HOME: dir, USERPROFILE: dir, APPDATA: dir, LOCALAPPDATA: dir, XDG_CONFIG_HOME: dir, TEMP: dir, TMP: dir, TMPDIR: dir };
}

async function startServer(exe, fixture, sandbox) {
  const child = spawn(exe, ["-fixture", fixture, "-fixtures", fixtureDir], { env: sandboxEnv(sandbox), cwd: repo, stdio: ["ignore", "pipe", "pipe"] });
  let out = "";
  child.stdout.on("data", (d) => (out += d));
  child.stderr.on("data", (d) => (out += d));
  for (let i = 0; i < 200 && !/open: /.test(out); i++) await sleep(50);
  const m = /open: (http:\/\/127\.0\.0\.1:\d+\/\?t=[0-9a-f]+)/.exec(out);
  if (!m) { child.kill(); throw new Error("the fixture server did not start: " + out); }
  return { url: m[1], stop: () => child.kill() };
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
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "realpage-profile-"));
    const port = 9400 + Math.floor(Math.random() * 400);
    const child = spawn(findChrome(), ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${dir}`, "--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-background-networking", "--window-size=1280,900", "about:blank"], { stdio: "ignore" });
    let ver = null;
    for (let i = 0; i < 100 && !ver; i++) { try { ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); } catch { await sleep(100); } }
    if (!ver) throw new Error("the browser did not start");
    const b = new Browser(child, ver);
    await new Promise((r) => (b.ws.onopen = r));
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
    this.ws.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id && this.pending.has(m.id)) {
        const { res, rej } = this.pending.get(m.id);
        this.pending.delete(m.id);
        if (m.error) rej(new Error(m.error.message)); else res(m.result);
      } else this.events.push(m);
    };
  }
  send(method, params = {}, sessionId) {
    return new Promise((res, rej) => { const id = ++this.id; this.pending.set(id, { res, rej }); this.ws.send(JSON.stringify({ id, method, params, sessionId })); });
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
  close() { try { this.ws.close(); } catch { /* closed */ } this.child.kill(); }
}

// What a page reports of itself.
const READ = `(() => {
  const text = (e) => (e ? e.textContent.trim() : "");
  const rows = [...document.querySelectorAll(".needs-row")].map((r) => ({
    pane: r.getAttribute("data-pane"), kind: r.getAttribute("data-kind"),
    who: text(r.querySelector(".needs-who")), word: text(r.querySelector(".needs-word")),
    control: !!r.querySelector("button"),
  }));
  const badges = [...document.querySelectorAll(".gneed")].map((g) => {
    const b = g.querySelector(".gbadge");
    return { pane: g.getAttribute("data-pane"), kind: b ? b.getAttribute("data-kind") : "", word: text(g.querySelector(".gbadge-word")), more: text(g.querySelector(".gbadge-more")) };
  });
  const boxes = {};
  for (const g of document.querySelectorAll("g.gnode")) { const r = g.getBoundingClientRect(); boxes[g.getAttribute("data-pane")] = { x: r.x, y: r.y, w: r.width, h: r.height }; }
  const drawer = document.querySelector(".drawer");
  return {
    rows, badges, boxes,
    drawers: document.querySelectorAll("[data-drawer-pane]").length,
    drawerHidden: drawer ? drawer.hidden : null,
    undelivered: [...document.querySelectorAll(".gedge--undelivered")].map((e) => ({ from: e.getAttribute("data-from"), to: e.getAttribute("data-to"), text: text(e) })),
    mbar: !!document.querySelector(".top .mbar"),
  };
})()`;

async function load(browser, url, boxesExpected, rate) {
  const page = await browser.page();
  await page.S("Emulation.setCPUThrottlingRate", { rate });
  await page.S("Page.navigate", { url });
  const ok = await page.until(`document.querySelectorAll("g.gnode").length >= ${boxesExpected}`);
  return { page, ok };
}

// ------------------------------------------------------------------ checks

function fixtureChecks(ex, seen) {
  const results = [];
  const check = (name, pass, detail) => results.push({ name, pass: !!pass, detail: detail || "" });
  const mdPanes = new Set();
  for (const it of ex.items) { mdPanes.add(it.pane); if (it.other) mdPanes.add(it.other); }

  check("every node is a box", Object.keys(seen.boxes).length === ex.graph.nodes.length, `${Object.keys(seen.boxes).length} of ${ex.graph.nodes.length}`);
  check("the drawer is closed on load", seen.drawers === 0 && seen.drawerHidden === true, `drawers=${seen.drawers} hidden=${seen.drawerHidden}`);

  // The strip: one row per item the .md lists, no others. A conflict names two
  // panes: the .md names them as it likes and the graph in sorted order, so both are
  // compared as the sorted pair.
  const keyOf = (kind, pane, other) => kind + ":" + (kind === "conflict" ? [pane, other].sort().join("+") : pane);
  const stripSet = seen.rows.map((r) => {
    const need = ex.graphNeeds.find((n) => n.pane === r.pane && n.kind === r.kind);
    return keyOf(r.kind, r.pane, need && need.other);
  }).sort();
  const mdSet = ex.items.map((i) => keyOf(i.kind, i.pane, i.other)).sort();
  check("the strip has the .md's items and no others", JSON.stringify(stripSet) === JSON.stringify(mdSet), `strip ${JSON.stringify(stripSet)} vs md ${JSON.stringify(mdSet)}`);
  // The reason words.
  const wrong = [];
  for (const r of seen.rows) {
    const need = ex.graphNeeds.find((n) => n.pane === r.pane && n.kind === r.kind);
    const want = need ? wordOf(need) : "?";
    if (r.word !== want) wrong.push(`${r.pane} ${r.kind}: "${r.word}" not "${want}"`);
    const who = ex.names[r.pane];
    if (!r.who.startsWith(who)) wrong.push(`${r.pane}: who "${r.who}" does not start with "${who}"`);
  }
  check("each strip row has the right reason word and name", wrong.length === 0, wrong.join("; "));
  // The order.
  const stripOrder = seen.rows.map((r) => r.pane + ":" + r.kind);
  const graphOrder = ex.graphNeeds.map((n) => n.pane + ":" + n.kind);
  check("the strip order is the graph's needs order", JSON.stringify(stripOrder) === JSON.stringify(graphOrder), `strip ${JSON.stringify(stripOrder)} vs graph ${JSON.stringify(graphOrder)}`);

  // Badges: on the panes of those items, on no other.
  const badged = new Set(seen.badges.map((b) => b.pane));
  check("a badge on each pane the .md names, and on no other", badged.size === mdPanes.size && [...mdPanes].every((p) => badged.has(p)), `badges ${JSON.stringify([...badged].sort())} vs md ${JSON.stringify([...mdPanes].sort())}`);
  const badWords = [];
  for (const b of seen.badges) {
    const first = ex.graphNeeds.find((n) => n.pane === b.pane || n.other === b.pane);
    const want = first ? wordOf(first) : "?";
    if (b.word !== want) badWords.push(`${b.pane}: "${b.word}" not "${want}"`);
  }
  check("each badge has the reason word of its pane's first item", badWords.length === 0, badWords.join("; "));

  if (ex.file.startsWith("03-")) {
    // The nested helper: shards (21c76d4a) is a helper of indexer (025b0446), drawn below it.
    const a = seen.boxes["025b0446"], b = seen.boxes["21c76d4a"];
    check("fixture 3: the nested helper is drawn below its parent", a && b && b.y > a.y, JSON.stringify({ indexer: a, shards: b }));
  }
  if (ex.file.startsWith("05-")) {
    const u = seen.undelivered[0];
    check("fixture 5: the undelivered message edge is drawn from tests to the manager, in amber, with its words",
      seen.undelivered.length === 1 && u.from === "2023df34" && u.to === "c2beaec2" && /not told yet/.test(u.text), JSON.stringify(seen.undelivered));
  }
  return results;
}

// The second week-10 step. target is the need to open: the first permission or
// question in the graph's order. The text looked for is the first 30 characters
// of the need's text, which is what the drawer's card shows of it.
async function secondCheck(browser, url, ex, rate) {
  const target = ex.graphNeeds.find((n) => n.kind === "permission" || n.kind === "ask");
  const probe = (target.text || "").replace(/^[\s\S]*?\n/, (m) => m).split("\n")[0].trim().slice(0, 30);
  const out = { pane: target.pane, kind: target.kind, name: ex.names[target.pane], text: probe, rate, strip: {}, box: {} };
  const way = async (label, how) => {
    const { page, ok } = await load(browser, url, ex.graph.nodes.length, rate);
    if (!ok) { out[label] = { error: "the page did not draw" }; await page.close(); return; }
    await sleep(300);
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
    out[label] = { ...r, opened: drawer, ms: t.down !== null && t.shown !== null ? Math.round((t.shown - t.down) * 10) / 10 : null };
    out[label].pass = out[label].opened === target.pane && out[label].ms !== null && out[label].ms < LIMIT_MS;
    await page.close();
  };
  const click = async (page, sel) => {
    const c = await page.eval(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; })()`);
    if (!c) return { error: "no such element: " + sel };
    await page.S("Input.dispatchMouseEvent", { type: "mousePressed", x: c.x, y: c.y, button: "left", clickCount: 1 });
    await page.S("Input.dispatchMouseEvent", { type: "mouseReleased", x: c.x, y: c.y, button: "left", clickCount: 1 });
    return {};
  };
  // A click on the strip row.
  await way("strip", async (page) => {
    const sel = `.needs-row[data-pane="${target.pane}"][data-kind="${target.kind}"]`;
    const isControl = await page.eval(`!!document.querySelector(${JSON.stringify(sel + " button")})`);
    const r = await click(page, sel);
    return { ...r, rowIsControl: isControl };
  });
  // A key on the strip row: Tab to it, then Enter. The row is reached by keyboard only if it is a control.
  await way("stripKey", async (page) => {
    const sel = `.needs-row[data-pane="${target.pane}"][data-kind="${target.kind}"] .needs-pick`;
    const focusable = await page.eval(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return false; e.focus(); return document.activeElement === e; })()`);
    await page.S("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
    await page.S("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 });
    return { rowFocusable: focusable };
  });
  // For comparison, and not the strip path: a click on the agent's box in the graph.
  await way("box", async (page) => click(page, `g.gnode[data-pane="${target.pane}"]`));
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

// ------------------------------------------------------------------ run

const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith(".json")).map((f) => f.slice(0, -5)).filter((f) => !only || f.startsWith(only.padStart(2, "0") + "-")).sort();
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "realpage-"));
const sandbox = fs.mkdtempSync(path.join(os.tmpdir(), "realpage-sandbox-"));
const cfg = configDir();
const before = listDir(cfg);
const exe = buildServer(tmp);
const browser = await Browser.launch();
const report = { browser: browser.version, userAgent: browser.userAgent, platform: process.platform + " " + os.release(), node: process.version, slowdowns, repeat, limitMs: LIMIT_MS, fixtures: [] };

try {
  for (const file of files) {
    const ex = expectation(file);
    const num = String(Number(file.slice(0, 2)));
    const entry = { file, checks: [], second: [], remote: [] };
    const server = await startServer(exe, num, sandbox);
    try {
      const { page, ok } = await load(browser, server.url, ex.graph.nodes.length, 1);
      if (!ok) entry.checks.push({ name: "the page drew the graph", pass: false, detail: "no boxes" });
      else {
        await sleep(500);
        const seen = await page.eval(READ);
        entry.checks.push(...fixtureChecks(ex, seen));
        entry.checks.push({ name: "no page errors on load", pass: page.errors.length === 0, detail: page.errors.join(" | ") });
        entry.mbar = seen.mbar;
      }
      await page.close();
      for (const rate of slowdowns) {
        for (let i = 0; i < repeat; i++) entry.second.push(await secondCheck(browser, server.url, ex, rate));
      }
      entry.remote = await remoteCheck(browser, server.url, ex);
    } finally {
      server.stop();
    }
    report.fixtures.push(entry);
  }
} finally {
  browser.close();
}

const after = listDir(cfg);
report.config = { dir: cfg, before: before.length, after: after.length, changed: JSON.stringify(before) !== JSON.stringify(after), sandboxLeft: fs.readdirSync(sandbox) };

// ------------------------------------------------------------------ the report

const lines = [];
let failed = 0;
for (const f of report.fixtures) {
  lines.push(`\n${f.file}`);
  for (const c of [...f.checks, ...f.remote]) { if (!c.pass) failed++; lines.push(`  ${c.pass ? "ok  " : "FAIL"} ${c.name}${c.pass ? "" : ": " + c.detail}`); }
  const bySlow = {};
  for (const s of f.second) (bySlow[s.rate] ||= []).push(s);
  for (const [rate, list] of Object.entries(bySlow)) {
    for (const way of ["strip", "stripKey", "box"]) {
      const ms = list.map((s) => s[way] && s[way].ms).filter((x) => x != null).sort((a, b) => a - b);
      const passes = list.filter((s) => s[way] && s[way].pass).length;
      const first = list[0][way] || {};
      const detail = way === "strip" ? ` rowIsControl=${first.rowIsControl}` : way === "stripKey" ? ` rowFocusable=${first.rowFocusable}` : "";
      lines.push(`  ${passes === list.length ? "ok  " : "FAIL"} second step by ${way} at ${rate}x CPU slowdown: ${passes}/${list.length} opened ${list[0].name} within 2 s; ms ${ms.length ? ms.join(", ") : "none"}${detail}`);
      if (way !== "box" && passes !== list.length) failed++;
    }
  }
}
lines.push(`\nbrowser ${report.browser}`);
lines.push(`config dir ${cfg}: ${before.length} files before, ${after.length} after, ${report.config.changed ? "CHANGED" : "unchanged"}; sandbox left ${JSON.stringify(report.config.sandboxLeft)}`);
lines.push(failed ? `\n${failed} check(s) failed` : "\nall checks passed");
console.log(lines.join("\n"));
if (outFile) fs.writeFileSync(outFile, JSON.stringify(report, null, 2));
process.exit(failed ? 1 : 0);
