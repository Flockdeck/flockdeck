// First-load measurement of the new shell, as a phone behind the relay sees it.
//
// It serves the page through a throttling proxy and loads it in a headless
// Chromium, once per run, with the cache off (the server sends no-store, so a
// real load never has one). Per run it records the first contentful paint, the
// first time the app's own markup is on the page (first meaningful paint) and
// an interactive time. It does this for the module graph as shipped and for
// the same code concatenated into one file, and for a stand-in for the finished
// app that also imports every library module that is not yet used.
//
// Usage:
//   node scripts/firstload/measure.mjs --esbuild <path to esbuild> [options]
//
// Options (defaults in brackets):
//   --browser <exe>      Chromium or Edge [searched for]
//   --runs <n>           runs per case [5]
//   --kbit <n>           link speed in kbit/s, shared by all connections [1600]
//   --rtt <ms>           round trip time [150]
//   --connect-rtts <n>   round trips before a request can be sent: TCP, TLS [2]
//   --streams <n>        requests in flight at once [6]
//   --cpu <n>            CPU slowdown of the page [4]
//   --no-slow-start      send at the link speed from the first byte
//   --out <file>         write the raw results as JSON
//   --only <name,...>    run only the named cases
//   --bundled <folder>   add the case "joined": the files `go run ./cmd/webbundle -o <folder>`
//                        writes, which are what the server sends
//
// esbuild is used only to concatenate, for the measurement. Nothing here is a
// build step: the tool that would make a release build is a separate task.
import http from "node:http";
import net from "node:net";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import zlib from "node:zlib";
import { spawn, execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "..", "..");
const uiAssets = path.join(repo, "ui", "src");

// ------------------------------------------------------------------ options

const argv = process.argv.slice(2);
const opt = (name, def) => {
  const i = argv.indexOf("--" + name);
  if (i < 0) return def;
  const v = argv[i + 1];
  return v === undefined || v.startsWith("--") ? true : v;
};
const RUNS = Number(opt("runs", 5));
const KBIT = Number(opt("kbit", 1600));
const RTT = Number(opt("rtt", 150));
const CONNECT_RTTS = Number(opt("connect-rtts", 2));
const STREAMS = Number(opt("streams", 6));
const CPU = Number(opt("cpu", 4));
const SLOW_START = !argv.includes("--no-slow-start");
const ESBUILD = opt("esbuild", "");
const BUNDLED = opt("bundled", "");
const ONLY = opt("only", "") ? String(opt("only", "")).split(",") : null;

function findBrowser() {
  const given = opt("browser", "");
  if (given) return given;
  const home = os.homedir();
  const candidates = [];
  const pw = path.join(process.env.LOCALAPPDATA || path.join(home, "AppData", "Local"), "ms-playwright");
  if (fs.existsSync(pw)) {
    for (const d of fs.readdirSync(pw)) {
      if (d.startsWith("chromium-")) candidates.push(path.join(pw, d, "chrome-win64", "chrome.exe"));
    }
  }
  candidates.push(
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
    "/usr/bin/chromium", "/usr/bin/google-chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  );
  const found = candidates.find((c) => fs.existsSync(c));
  if (!found) throw new Error("no Chromium found; pass --browser");
  return found;
}

// ------------------------------------------------------------------ the files

/** A case is the files the page is served and which classic scripts it has. */
function readTree(rel) {
  // The shell's files, as the server embeds them.
  for (const root of [uiAssets]) {
    const p = path.join(root, rel);
    if (fs.existsSync(p) && fs.statSync(p).isFile()) return fs.readFileSync(p);
  }
  return null;
}

const TYPES = { ".js": "text/javascript", ".mjs": "text/javascript", ".css": "text/css", ".html": "text/html" };

function buildCases(scratch) {
  const index = readTree("index.html").toString();
  // The page as it was before the terminal's libraries were loaded on first
  // use: the three classic scripts in front of the module. The cases named
  // eager use it, to show what moving them out saved. A page that has them
  // already is used as it is.
  const eagerTags = ['assets/vendor/xterm.js', 'assets/vendor/addon-fit.js', 'assets/e2e.js']
    .map((p) => `<script src="${p}"></script>
`).join('');
  const moduleTag = '<script type="module" src="assets/main.js"></script>';
  if (!index.includes(moduleTag)) throw new Error('index.html no longer has the module script in the form this tool expects');
  const eager = index.includes('assets/vendor/xterm.js') ? index : index.replace(moduleTag, eagerTags + moduleTag);
  // The stand-in for the finished app: main.js plus every library module the
  // shell does not use yet, kept alive so a bundler cannot drop them.
  const full = `import "./main.js";
import * as store from "./lib/store.js";
import * as socket from "./lib/socket.js";
import * as pty from "./lib/pty.js";
import * as terminal from "./lib/terminal.js";
import * as useStore from "./lib/use-store.js";
import * as model from "./graph/model.js";
globalThis.__keep = [store, socket, pty, terminal, useStore, model];
`;
  // Served from memory for the module case; written to the scratch folder, with
  // absolute imports, for the bundler.
  const fullFile = path.join(scratch, "main-full.js");
  const abs = (p) => JSON.stringify(path.join(uiAssets, p).replaceAll("\\", "/"));
  fs.writeFileSync(fullFile, full.replace(/from "\.\/([^"]+)"/g, (_, p) => "from " + abs(p)).replace('import "./main.js"', "import " + abs("main.js")));
  const fullIndex = index.replace(`src="assets/main.js"`, `src="assets/main-full.js"`);
  const fullEager = eager.replace(`src="assets/main.js"`, `src="assets/main-full.js"`);
  const cases = [
    { name: "modules", index },
    { name: "modules-full", index: fullIndex, override: { "assets/main-full.js": Buffer.from(full) } },
    { name: "modules-eager", index: eager },
    { name: "modules-full-eager", index: fullEager, override: { "assets/main-full.js": Buffer.from(full) } },
  ];
  if (ESBUILD) {
    const bundle = (entry, outName) => {
      const out = path.join(scratch, outName);
      execFileSync(ESBUILD, [
        entry === "main.js" ? path.join(uiAssets, entry) : fullFile, "--bundle", "--format=esm", "--tree-shaking=false",
        "--outfile=" + out, "--log-level=warning",
      ], { stdio: "inherit" });
      return fs.readFileSync(out);
    };
    const one = bundle("main.js", "bundle-main.js");
    const two = bundle("main-full.js", "bundle-full.js");
    cases.push(
      { name: "concat", index, override: { "assets/main.js": one } },
      { name: "concat-full", index: fullIndex, override: { "assets/main-full.js": two } },
      { name: "concat-eager", index: eager, override: { "assets/main.js": one } },
      { name: "concat-full-eager", index: fullEager, override: { "assets/main-full.js": two } },
    );
  }
  if (BUNDLED && BUNDLED !== true) {
    // What the server sends: the module graph in main.js, the stylesheets in
    // app.css, and a page that names them.
    const made = (f) => fs.readFileSync(path.join(BUNDLED, f));
    cases.push({
      name: "joined", index: made("index.html").toString(),
      override: { "assets/main.js": made("main.js"), "assets/app.css": made("app.css") },
    });
  }
  return cases;
}

function makeOrigin(c, gzipOn) {
  const cache = new Map();
  const get = (rel) => {
    if (cache.has(rel)) return cache.get(rel);
    let body = null;
    if (rel === "index.html") body = Buffer.from(c.index);
    else if (c.override && c.override["assets/" + rel]) body = c.override["assets/" + rel];
    else body = readTree(rel);
    let entry = null;
    if (body) {
      const type = TYPES[path.extname(rel)] || "application/octet-stream";
      const gz = gzipOn && TYPES[path.extname(rel)];
      // The server compresses at the best level, once.
      entry = { type, body: gz ? zlib.gzipSync(body, { level: 9 }) : body, gz: !!gz, raw: body.length };
    }
    cache.set(rel, entry);
    return entry;
  };
  return (urlPath) => {
    let rel = decodeURIComponent(urlPath.split("?")[0]);
    if (rel === "/") rel = "/index.html";
    rel = rel.replace(/^\/(assets\/)?/, "");
    if (urlPath === "/" || urlPath === "/index.html") return get("index.html");
    return get(rel);
  };
}

// ------------------------------------------------------------------ the throttle

/** startProxy listens on a port and answers each connection as the relay's
 *  path would: a handshake of CONNECT_RTTS round trips, a request, a first byte
 *  one round trip on, then the body paced to the shared link speed, and the
 *  connection closed. Only STREAMS are served at once. With slow start on, a
 *  connection sends 10 segments in its first round trip and doubles each one. */
function startProxy(lookup) {
  const bytesPerSec = (KBIT * 1000) / 8;
  const TICK = 10;
  const perTick = (bytesPerSec * TICK) / 1000;
  const active = new Set();
  const waiting = [];
  const log = [];
  let inflight = 0;
  let t0 = Date.now();

  const server = net.createServer((sock) => {
    sock.on("error", () => {});
    const conn = { sock, buf: Buffer.alloc(0), out: null, pos: 0, sent: false, started: 0, cwnd: 10 * 1460, epochAt: 0, epochLeft: 0, url: "" };
    if (inflight >= STREAMS) waiting.push(conn);
    else begin(conn);
  });

  function begin(conn) {
    inflight++;
    conn.started = Date.now();
    let head = "";
    const chunks = [];
    conn.sock.on("data", (d) => {
      if (conn.sent) return;
      chunks.push(d);
      head = Buffer.concat(chunks).toString("latin1");
      if (!head.includes("\r\n\r\n")) return;
      conn.sent = true;
      const m = /^GET (\S+) HTTP\/1\.[01]/.exec(head);
      conn.url = m ? m[1] : "";
      const ae = /accept-encoding:\s*([^\r\n]*)/i.exec(head);
      const accepts = ae && /gzip/i.test(ae[1]);
      const file = m ? lookup(m[1]) : null;
      const first = (CONNECT_RTTS + 1) * RTT;
      const wait = Math.max(0, conn.started + first - Date.now());
      setTimeout(() => respond(conn, file, accepts), wait);
    });
    conn.sock.on("close", () => { if (!conn.done) finish(conn); });
  }

  function respond(conn, file, accepts) {
    let payload;
    if (!file) {
      payload = Buffer.from("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n");
    } else {
      const sendGz = file.gz && accepts;
      const body = sendGz ? file.body : (file.gz ? zlib.gunzipSync(file.body) : file.body);
      const h = [
        "HTTP/1.1 200 OK", `Content-Type: ${file.type}`, `Content-Length: ${body.length}`,
        "Cache-Control: no-store", "Connection: close", sendGz ? "Content-Encoding: gzip" : "",
      ].filter(Boolean).join("\r\n") + "\r\n\r\n";
      payload = Buffer.concat([Buffer.from(h), body]);
      log.push({ url: conn.url, wire: body.length, raw: file.raw, gz: sendGz });
    }
    conn.out = payload;
    conn.pos = 0;
    conn.epochAt = Date.now();
    conn.epochLeft = conn.cwnd;
    active.add(conn);
  }

  function finish(conn) {
    if (conn.done) return;
    conn.done = true;
    active.delete(conn);
    inflight--;
    try { conn.sock.end(); } catch { /* closed */ }
    const next = waiting.shift();
    if (next) begin(next);
  }

  const timer = setInterval(() => {
    if (!active.size) return;
    const now = Date.now();
    let budget = perTick;
    // Fair share: hand out in turns until the tick's bytes are used or every
    // connection is held by its window.
    let progressed = true;
    while (budget > 0 && progressed) {
      progressed = false;
      for (const conn of active) {
        if (budget <= 0) break;
        if (SLOW_START) {
          if (now - conn.epochAt >= RTT) { conn.epochAt = now; conn.cwnd *= 2; conn.epochLeft = conn.cwnd; }
          if (conn.epochLeft <= 0) continue;
        }
        const rest = conn.out.length - conn.pos;
        const n = Math.min(rest, 1460, budget, SLOW_START ? conn.epochLeft : Infinity);
        if (n <= 0) continue;
        conn.sock.write(conn.out.subarray(conn.pos, conn.pos + n));
        conn.pos += n;
        budget -= n;
        if (SLOW_START) conn.epochLeft -= n;
        progressed = true;
        if (conn.pos >= conn.out.length) { finish(conn); break; }
      }
    }
  }, TICK);

  return new Promise((resolve) => {
    server.listen(0, "127.0.0.1", () => resolve({
      port: server.address().port,
      log,
      reset() { log.length = 0; t0 = Date.now(); },
      close() { clearInterval(timer); server.close(); for (const c of active) c.sock.destroy(); },
    }));
  });
}

// ------------------------------------------------------------------ the browser

class Cdp {
  constructor(url) {
    this.ws = new WebSocket(url);
    this.id = 0;
    this.pending = new Map();
    this.handlers = [];
    this.open = new Promise((res, rej) => { this.ws.onopen = res; this.ws.onerror = rej; });
    this.ws.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.id && this.pending.has(m.id)) {
        const { res, rej } = this.pending.get(m.id);
        this.pending.delete(m.id);
        if (m.error) rej(new Error(m.error.message)); else res(m.result);
      } else if (m.method) for (const h of this.handlers) h(m);
    };
  }
  send(method, params = {}, sessionId) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params, sessionId }));
    return new Promise((res, rej) => this.pending.set(id, { res, rej }));
  }
  close() { try { this.ws.close(); } catch { /* closed */ } }
}

async function launch(exe) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "firstload-profile-"));
  const port = 9300 + Math.floor(Math.random() * 500);
  const child = spawn(exe, [
    "--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${dir}`,
    "--no-first-run", "--no-default-browser-check", "--disable-extensions", "--disable-background-networking",
    "--disable-http-cache", "--disable-features=Translate,OptimizationHints", "about:blank",
  ], { stdio: "ignore" });
  let ver = null;
  for (let i = 0; i < 100 && !ver; i++) {
    try { ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); } catch { await sleep(100); }
  }
  if (!ver) throw new Error("the browser did not start");
  return { child, dir, ws: ver.webSocketDebuggerUrl, version: ver.Browser };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Runs in the page before anything else: notes when the app first has markup,
// and the end of every long task.
const PROBE = `
(() => {
  const t = { fmp: null, longEnd: 0 };
  window.__t = t;
  new PerformanceObserver((l) => {
    for (const e of l.getEntries()) t.longEnd = Math.max(t.longEnd, e.startTime + e.duration);
  }).observe({ type: "longtask", buffered: true });
  const watch = () => {
    const app = document.getElementById("app");
    if (!app) return false;
    const seen = () => { if (t.fmp === null && app.children.length) t.fmp = performance.now(); };
    new MutationObserver(seen).observe(app, { childList: true, subtree: true });
    seen();
    return true;
  };
  if (!watch()) document.addEventListener("DOMContentLoaded", watch);
})();
`;

async function runOnce(cdp, port) {
  const { targetId } = await cdp.send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await cdp.send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => cdp.send(m, p, sessionId);
  await s("Page.enable");
  await s("Network.enable");
  await s("Network.setCacheDisabled", { cacheDisabled: true });
  await s("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await s("Emulation.setCPUThrottlingRate", { rate: CPU });
  await s("Page.addScriptToEvaluateOnNewDocument", { source: PROBE });
  const errors = [];
  cdp.handlers.push((m) => {
    if (m.sessionId === sessionId && m.method === "Runtime.exceptionThrown") errors.push(m.params.exceptionDetails.text);
  });
  await s("Runtime.enable");
  await s("Page.navigate", { url: `http://127.0.0.1:${port}/` });
  // Wait for the load event, then for a quiet second.
  let res = null;
  const deadline = Date.now() + 90000;
  while (Date.now() < deadline) {
    await sleep(500);
    const r = await s("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const nav = performance.getEntriesByType("navigation")[0];
        const fcp = performance.getEntriesByName("first-contentful-paint")[0];
        return { load: nav && nav.loadEventEnd, dcl: nav && nav.domContentLoadedEventEnd, fcp: fcp && fcp.startTime,
                 fmp: window.__t && window.__t.fmp, longEnd: window.__t && window.__t.longEnd, now: performance.now() };
      })()`,
    });
    const v = r.result.value;
    if (v && v.load > 0 && v.fmp !== null && v.fcp != null) {
      const settled = Math.max(v.dcl, v.fmp, v.longEnd);
      if (v.now - settled >= 1000) { res = { ...v, tti: settled }; break; }
    }
  }
  await cdp.send("Target.closeTarget", { targetId }).catch(() => {});
  cdp.handlers.length = 0;
  if (!res) throw new Error("the page did not settle");
  return { ...res, errors };
}

// ------------------------------------------------------------------ main

const median = (a) => { const b = [...a].sort((x, y) => x - y); const m = b.length >> 1; return b.length % 2 ? b[m] : (b[m - 1] + b[m]) / 2; };
const ms = (x) => Math.round(x);

async function main() {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "firstload-"));
  const exe = findBrowser();
  const cases = buildCases(scratch).filter((c) => !ONLY || ONLY.includes(c.name));
  const browser = await launch(exe);
  const cdp = new Cdp(browser.ws);
  await cdp.open;
  console.log(`browser ${browser.version}; link ${KBIT} kbit/s, rtt ${RTT} ms, ${CONNECT_RTTS} setup round trips, ${STREAMS} streams, cpu x${CPU}, slow start ${SLOW_START ? "on" : "off"}, ${RUNS} runs`);
  const results = [];
  for (const gzipOn of [true, false]) {
    for (const c of cases) {
      if (!gzipOn && !["modules", "concat", "joined"].includes(c.name)) continue;
      const proxy = await startProxy(makeOrigin(c, gzipOn));
      const runs = [];
      let reqs = 0, wire = 0, raw = 0, files = [];
      for (let i = 0; i < RUNS; i++) {
        proxy.reset();
        const r = await runOnce(cdp, proxy.port);
        if (r.errors.length) console.error(`  ${c.name}: page error: ${r.errors[0]}`);
        runs.push(r);
        reqs = proxy.log.length;
        wire = proxy.log.reduce((a, x) => a + x.wire, 0);
        raw = proxy.log.reduce((a, x) => a + x.raw, 0);
        files = proxy.log.map((x) => x.url);
      }
      proxy.close();
      const row = {
        name: c.name, gzip: gzipOn, requests: reqs, wireBytes: wire, rawBytes: raw,
        fcp: median(runs.map((r) => r.fcp)), fmp: median(runs.map((r) => r.fmp)), tti: median(runs.map((r) => r.tti)),
        load: median(runs.map((r) => r.load)),
        fmpAll: runs.map((r) => ms(r.fmp)), ttiAll: runs.map((r) => ms(r.tti)),
      };
      results.push(row);
      console.log(`${c.name.padEnd(26)} gzip ${gzipOn ? "on " : "off"} ${String(reqs).padStart(3)} req ${String(wire).padStart(7)} B  fcp ${ms(row.fcp)}  fmp ${ms(row.fmp)}  tti ${ms(row.tti)}  load ${ms(row.load)} ms  (fmp runs ${row.fmpAll.join(",")})`);
      if (opt("verbose", false)) console.log("   " + files.join("\n   "));
    }
  }
  const out = opt("out", "");
  if (out && out !== true) fs.writeFileSync(out, JSON.stringify({ browser: browser.version, esbuild: ESBUILD ? execFileSync(ESBUILD, ["--version"]).toString().trim() : null, params: { KBIT, RTT, CONNECT_RTTS, STREAMS, CPU, SLOW_START, RUNS }, results }, null, 2));
  cdp.close();
  browser.child.kill();
  try { fs.rmSync(scratch, { recursive: true, force: true }); } catch { /* in use */ }
  setTimeout(() => { try { fs.rmSync(browser.dir, { recursive: true, force: true }); } catch { /* in use */ } process.exit(0); }, 1500);
}

main().catch((err) => { console.error(err); process.exit(1); });
