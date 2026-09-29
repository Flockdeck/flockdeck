// Retakes the site's screenshots from a staged Flockdeck built from this
// checkout. See README.md for what it stages and the rules it keeps.
//
//   node cmd/sitegen/shots/shots.mjs [-relay-src DIR] [-only deck,fanout,...] [-keep]
//
// Windows only: the staged projects live in C:\code, which is what the
// pictures show.
import { execFileSync, spawn } from "node:child_process";
import { createRequire } from "node:module";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { browserEnv, buildEnv, compare, livePIDs, realStateDirs, snapshot, stagedEnv, stopAll } from "./guard.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, "..", "..", "..");
const SHOTS = path.join(REPO, "cmd", "sitegen", "assets", "shots");
const CODE = "C:\\code";
const MARK = path.join(CODE, ".flockdeck-shots");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const args = process.argv.slice(2);
const opt = (name) => { const i = args.indexOf(name); return i < 0 ? null : args[i + 1]; };
const relaySrc = opt("-relay-src");
const only = (opt("-only") || "deck,fanout,worktrees,agent-picker,settings,remote-choices,og").split(",");
const keep = args.includes("-keep");

if (process.platform !== "win32") throw new Error("the harness stages C:\\code and runs on Windows only");

// playwright-core, from wherever it was installed (README.md says how).
const require = createRequire(import.meta.url);
const pwFrom = process.env.PLAYWRIGHT_CORE_DIR || path.join(os.tmpdir(), "flockdeck-shots-node");
const { chromium } = require(require.resolve("playwright-core", { paths: [pwFrom, HERE] }));
const CHROME = [process.env.CHROME, "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe"].find((p) => p && fs.existsSync(p));
if (!CHROME) throw new Error("no Chrome found; set CHROME to chrome.exe");

// ---- Isolation, before anything is started ----------------------------------

const LIVE = realStateDirs();
const work = fs.mkdtempSync(path.join(os.tmpdir(), "flockdeck-shots-"));
const bin = path.join(work, "bin"), out = path.join(work, "out");
for (const d of ["bin", "out", "appdata/flockdeck", "localappdata", "home", "tmp", "claude/subscription", "claude/apikey"]) {
  fs.mkdirSync(path.join(work, d), { recursive: true });
}
const git = execFileSync("where.exe", ["git"], { encoding: "utf8" }).split(/\r?\n/)[0].trim();
const env = stagedEnv(work, path.dirname(git)); // throws if the state would be the live one
const before = snapshot(LIVE);
fs.writeFileSync(path.join(work, "live-before.json"), JSON.stringify(before, null, 1));
console.log("work directory", work);
console.log("live state", LIVE.filter((d) => fs.existsSync(d)).join(", ") || "(none)", "-", Object.keys(before).length, "files");

const procs = [];
let browser = null;
let failed = false;
try {
  // ---- Build ------------------------------------------------------------------
  const version = execFileSync("git", ["-C", REPO, "describe", "--tags", "--always"], { encoding: "utf8", env: buildEnv() }).trim();
  console.log("building flockdeck", version);
  execFileSync("go", ["build", "-ldflags", `-s -w -X main.version=${version} -H=windowsgui`, "-o", path.join(bin, "flockdeck.exe"), "."], { cwd: REPO, env: buildEnv(), stdio: "inherit" });
  execFileSync("go", ["build", "-o", path.join(bin, "mockagent.exe"), "./cmd/sitegen/shots/mockagent"], { cwd: REPO, env: buildEnv(), stdio: "inherit" });
  // The relay embeds the web client, a module in a private repository.
  if (relaySrc) execFileSync("go", ["build", "-o", path.join(bin, "flockdeck-relay.exe"), "./cmd/flockdeck-relay"], { cwd: relaySrc, env: { ...buildEnv(), GOPRIVATE: "github.com/Flockdeck/flockdeck-remote" }, stdio: "inherit" });

  // ---- Stage --------------------------------------------------------------------
  stageProjects();
  stageState();

  // ---- Start the staged instance --------------------------------------------------
  const fd = start(path.join(bin, "flockdeck.exe"), ["-solo", "-no-window"], path.join(CODE, "shopfront"), "flockdeck");
  const url = await waitFor(() => (fd.log.match(/(http:\/\/127\.0\.0\.1:\d+\/\?t=[0-9a-f]+)/) || [])[1], "the staged instance's URL", 30000);
  checkStagedInstance(url);

  // The window is 2x from the start, forced on Chrome's command line rather
  // than emulated: under an emulated scale the terminals draw their text at
  // twice the size of everything else. Headless Chrome keeps 16x95 of the
  // window for itself, hence a 1600x1080 window for a 1584x985 page.
  browser = await chromium.launchPersistentContext(path.join(work, "chrome-desk"), {
    executablePath: CHROME, headless: true, env: browserEnv(env), viewport: null,
    args: ["--hide-scrollbars", "--mute-audio", "--no-first-run", "--no-default-browser-check",
      "--window-size=1600,1080", "--force-device-scale-factor=2"],
  });
  const page = browser.pages()[0] || await browser.newPage();
  await page.goto(url);
  const vp = await page.evaluate(() => [innerWidth, innerHeight, devicePixelRatio].join("x"));
  if (vp !== "1584x985x2") throw new Error("the viewport is " + vp + ", not 1584x985 at 2x");
  await sleep(2500);
  await page.keyboard.press("Escape");
  const panes = await page.evaluate(stageWorkspace);
  console.log("staged panes", JSON.stringify(panes));
  await waitForFigures(page, panes);

  const shoot = async (name, open) => {
    if (!only.includes(name)) return;
    await page.evaluate((id) => window.__stage.cmd({ cmd: "focusPane", id }), open === "fanout" ? panes.p0 : panes.p3);
    await sleep(600);
    await page.evaluate(() => document.activeElement && document.activeElement.blur && document.activeElement.blur());
    if (open === "fanout") await page.keyboard.press("Control+Shift+X");
    if (open === "worktrees") await page.keyboard.press("Control+Shift+G");
    if (open === "picker") await page.click("#new-tab-pick");
    if (open === "settings") {
      await page.click("#btn-settings");
      await sleep(1200);
      await page.click("#settings-tab-plan");
    }
    await sleep(open ? 2500 : 1500);
    await page.screenshot({ path: path.join(out, name + ".png") });
    console.log("captured", name);
    if (open) { await page.keyboard.press("Escape"); await sleep(800); }
  };
  await shoot("deck");
  await shoot("fanout", "fanout");
  await shoot("worktrees", "worktrees");
  await shoot("agent-picker", "picker");

  // ---- Remote: a relay on loopback, the staged desktop enrolled with it --------
  let relayURL = null;
  if (relaySrc) {
    relayURL = await startRelay();
    const r = execFileSync(path.join(bin, "flockdeck.exe"), ["remote", "enable", "-relay", relayURL, "-name", "workstation"], { env, encoding: "utf8" });
    console.log(r.trim());
    await sleep(3000);
  } else {
    console.log("no -relay-src: settings is taken without a relay, and remote-choices is not taken");
  }
  await shoot("settings", "settings");

  if (relayURL && only.includes("remote-choices")) await shootPhone(relayURL, panes.p3);

  // ---- Install ---------------------------------------------------------------------
  const taken = fs.readdirSync(out).filter((f) => f.endsWith(".png")).map((f) => path.join(out, f) + "=" + f.slice(0, -4));
  if (taken.length) execFileSync("python", [path.join(HERE, "halve.py"), SHOTS, ...taken], { stdio: "inherit", env: buildEnv() });
  if (only.includes("og")) await renderOG();
} catch (err) {
  failed = true;
  console.error(err);
} finally {
  if (browser) await browser.close().catch(() => {});
  const left = await stopAll(work);
  if (left.length) { failed = true; console.error("STILL RUNNING:", left); }
  else console.log("nothing of the harness's is running");
  if (fs.existsSync(MARK)) fs.rmSync(CODE, { recursive: true, force: true });
  const after = snapshot(LIVE);
  fs.writeFileSync(path.join(work, "live-after.json"), JSON.stringify(after, null, 1));
  const d = compare(before, after);
  // The live instance saves its own layouts and settings as its owner works,
  // so a changed file there is reported for a person to look at; a file added
  // or removed, or a changed instance record, is taken as this run's doing.
  console.log("live state: added", d.added.length, "removed", d.removed.length, "changed", d.changed.length);
  for (const p of d.changed) console.log("  changed", p, before[p].mtime, "->", after[p].mtime);
  for (const p of [...d.added, ...d.removed]) console.log("  ADDED/REMOVED", p);
  if (d.added.length || d.removed.length || d.changed.some((p) => /instance\.json$/.test(p))) {
    failed = true;
    console.error("THE LIVE STATE DIRECTORY CHANGED IN A WAY THIS RUN MAY HAVE CAUSED: stop and look");
  }
  if (!keep) fs.rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 500 });
  else console.log("kept", work);
  process.exitCode = failed ? 1 : 0;
}

// ---- Steps ----------------------------------------------------------------------------

function start(exe, argv, cwd, name) {
  const p = spawn(exe, argv, { cwd, env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  p.log = "";
  p.stdout.on("data", (b) => { p.log += b; });
  p.stderr.on("data", (b) => { p.log += b; });
  p.on("exit", (code) => console.log(name, "exited", code));
  procs.push(p);
  return p;
}

async function waitFor(fn, what, ms) {
  for (let t = 0; t < ms; t += 250) { const v = await fn(); if (v) return v; await sleep(250); }
  throw new Error("timed out waiting for " + what);
}

// checkStagedInstance proves the instance that answered is the staged one:
// its record is in the work directory, its port is not the live one's, and
// the live record has not moved.
function checkStagedInstance(url) {
  const rec = JSON.parse(fs.readFileSync(path.join(work, "appdata", "flockdeck", "instance.json"), "utf8"));
  if (!url.startsWith(rec.url)) throw new Error(`the staged record says ${rec.url}, but the instance printed ${url}`);
  for (const d of LIVE) {
    const f = path.join(d, "instance.json");
    if (!fs.existsSync(f)) continue;
    const live = JSON.parse(fs.readFileSync(f, "utf8"));
    if (live.url === rec.url) throw new Error("the staged instance has the live one's address");
    if (before[f] && snapshot([d])[f].sha256 !== before[f].sha256) throw new Error("the live instance record changed");
  }
  if (livePIDs().has(rec.pid)) throw new Error("the staged pid is the live one's");
  console.log("staged instance", rec.url, "pid", rec.pid, "state in", path.join(work, "appdata", "flockdeck"));
}

// stageProjects makes the fictional projects under C:\code: shopfront with
// three worktrees on feature branches (one with a new file, one with a
// change), a fourth whose folder has gone, and three other projects for the
// rail. C:\code is refused if it is there and not the harness's own.
function stageProjects() {
  if (fs.existsSync(CODE)) {
    if (!fs.existsSync(MARK)) throw new Error("C:\\code exists and is not the harness's: refusing to touch it");
    fs.rmSync(CODE, { recursive: true, force: true });
  }
  fs.mkdirSync(CODE);
  fs.writeFileSync(MARK, "made by cmd/sitegen/shots; removed when it finishes\n");
  fs.writeFileSync(env.GIT_CONFIG_GLOBAL, "[user]\n\tname = Sam\n\temail = sam@example.com\n[init]\n\tdefaultBranch = main\n[core]\n\tautocrlf = false\n");
  const g = (cwd, ...a) => execFileSync("git", a, { cwd, env, stdio: "pipe" });
  const write = (root, files) => {
    for (const [f, body] of Object.entries(files)) {
      fs.mkdirSync(path.dirname(path.join(root, f)), { recursive: true });
      fs.writeFileSync(path.join(root, f), body);
    }
  };
  const sf = path.join(CODE, "shopfront");
  write(sf, {
    "README.md": "# shopfront\n\nThe storefront API: carts, checkout, payments and search.\n\n## Running\n\n    go run ./cmd/server\n",
    "go.mod": "module github.com/shopfront/shopfront\n\ngo 1.27.0\n",
    "cmd/server/main.go": "package main\n\nfunc main() {}\n",
    "internal/checkout/cart.go": "package checkout\n\n// Cart holds the lines a customer is buying.\ntype Cart struct{ Lines []Line }\n\ntype Line struct {\n\tSKU   string\n\tQty   int\n\tPrice int64\n}\n\n// Checkout turns a cart into an order.\nfunc Checkout(c *Cart) error { return nil }\n",
    "internal/http/routes.go": "package http\n",
    "internal/invoice/invoice.go": "package invoice\n",
    "internal/payments/webhook.go": "package payments\n\n// Webhook handles events from Stripe.\ntype Webhook struct{}\n",
    "internal/search/index.go": "package search\n\n// Index is the product name index.\ntype Index struct{}\n",
  });
  g(sf, "init", "-q"); g(sf, "add", "-A"); g(sf, "commit", "-qm", "shopfront: cart, checkout, payments and search");
  for (const n of ["billing-service", "design-system", "infra"]) {
    const d = path.join(CODE, n);
    write(d, { "README.md": `# ${n}\n` });
    g(d, "init", "-q"); g(d, "add", "-A"); g(d, "commit", "-qm", "start " + n);
  }
  g(sf, "worktree", "add", "-q", "-b", "feat/pricing-package", path.join(CODE, "shopfront-checkout"));
  g(sf, "worktree", "add", "-q", "-b", "feat/stripe-refunds", path.join(CODE, "shopfront-payments"));
  g(sf, "worktree", "add", "-q", "-b", "feat/search-api", path.join(CODE, "shopfront-search"));
  fs.writeFileSync(path.join(CODE, "shopfront-checkout", "totals.go"), "package checkout\n\n// Totals moves to internal/pricing.\n");
  fs.appendFileSync(path.join(CODE, "shopfront-payments", "internal", "payments", "webhook.go"), "\n// charge.refunded marks the order refunded.\n");
  g(sf, "worktree", "add", "-q", "-b", "fix/flaky-cart-test", path.join(CODE, "shopfront-flaky"));
  fs.rmSync(path.join(CODE, "shopfront-flaky"), { recursive: true, force: true });
}

// stageState writes the staged instance's agents.json and prefs.json: the
// mock standing in for Claude Code, Codex and Gemini CLI, and shopfront
// defaulting to Claude Code's Sonnet with routing on, so a fan-out from it
// routes one task down to Haiku and, by the payments rule, one up to Opus.
function stageState() {
  const mock = path.join(bin, "mockagent.exe").replaceAll("\\", "/");
  const model = [{ if: "model", args: [{ value: "--model" }, { value: "{{model}}" }] }];
  const agents = {
    version: 1,
    defaults: { agent: "gemini", model: "gemini-2.5-pro" },
    projects: {
      [path.join(CODE, "shopfront")]: {
        agent: "claude", model: "sonnet",
        routing: {
          mode: "suggest",
          rules: [
            { name: "hard work", tier: "top", when: { task: "\\b(refactor|redesign|architect|concurren|race|deadlock|migration|security)" } },
            { name: "schema changes", tier: "top", when: { files: ["**/migrations/**", "**/*.sql"] } },
            { name: "payments code", tier: "top", when: { task: "\\b(stripe|refunds?|payments?)\\b" } },
            { name: "run the tests", tier: "small", when: { task: "^(re-?)?run (the |all )?(unit |integration )?tests?\\b" } },
            { name: "rename or move", tier: "small", when: { task: "\\b(rename|move)\\b", maxWords: 25 } },
            { name: "changelog and docs wording", tier: "small", when: { task: "\\b(changelog|typo|spelling|wording|readme)\\b", maxWords: 40 } },
          ],
        },
      },
    },
    agents: [
      { id: "claude", exe: mock, args: model, resumeArgs: model, caps: { hooks: false, resume: false, transcript: false, trust: false, context: "none" }, patterns: { waiting: ["proceed?"], idle: ["shortcuts"] } },
      { id: "codex", exe: mock, args: model, caps: { context: "none" }, patterns: { waiting: ["allow command?"], idle: ["send a message"] } },
      { id: "gemini", exe: mock, defaultModel: "gemini-2.5-pro", args: model, caps: { context: "none" }, patterns: { waiting: ["allow execution"], idle: ["type your message"] } },
    ],
  };
  const dir = path.join(env.APPDATA, "flockdeck");
  fs.writeFileSync(path.join(dir, "agents.json"), JSON.stringify(agents, null, 2) + "\n");
  // Tips and first-run help would sit over the pictures. Every hint's id is
  // read from the front end, so one added later is dismissed too.
  const app = fs.readFileSync(path.join(REPO, "internal", "webui", "assets", "app.js"), "utf8");
  const tips = [...new Set([...app.matchAll(/^\s*id: "([a-z-]+)",$/gm)].map((m) => m[1]))];
  fs.writeFileSync(path.join(dir, "prefs.json"), JSON.stringify({ helpSeen: true, dismissedTips: tips, updatesOff: true }) + "\n");
}

// stageWorkspace runs in the page. Over the window's own control socket it
// opens the three other projects (one with an agent waiting on a question),
// then builds shopfront's deck of four agents on four checkouts and a second
// tab, and leaves the waiting pane focused.
async function stageWorkspace() {
  const ctl = new WebSocket(location.origin.replace(/^http/, "ws") + "/ws/control");
  window.__stage = { ctl, last: null };
  ctl.onmessage = (ev) => { try { const m = JSON.parse(ev.data); if (m.type === "state") window.__stage.last = m; } catch {} };
  await new Promise((r) => (ctl.onopen = r));
  const cmd = (c) => ctl.send(JSON.stringify(c));
  window.__stage.cmd = cmd;
  const wait = async (pred, what) => {
    for (let i = 0; i < 300; i++) { const s = window.__stage.last; if (s && pred(s)) return s; await new Promise((r) => setTimeout(r, 100)); }
    throw new Error("timed out waiting for " + what);
  };
  const ids = (s) => Object.keys(s.panes);
  const active = (s) => (s.projects || []).find((p) => p.active);
  const added = async (c) => {
    const before = ids(window.__stage.last);
    cmd(c);
    const s2 = await wait((s) => ids(s).some((x) => !before.includes(x)), "a new pane");
    return ids(s2).find((x) => !before.includes(x));
  };
  // A pane opened without naming its agent records none, so its header has
  // no badge: every pane is opened naming one, and the first is replaced.
  const reseat = async (first, agent, model, path) => {
    const p = await added({ cmd: "splitPane", id: first, dir: "h", agent, model, path });
    cmd({ cmd: "closePane", id: first });
    await wait((s) => !ids(s).includes(first), "the first pane to close");
    return p;
  };
  let s = await wait((s) => ids(s).length >= 1 && active(s), "the workspace");
  const home = active(s).root;
  for (const [root, agent, model] of [["C:\\code\\billing-service", "claude", "sonnet"], ["C:\\code\\design-system", "gemini", "gemini-2.5-pro"], ["C:\\code\\infra", "codex", "gpt-5.6-terra"]]) {
    cmd({ cmd: "openProject", path: root });
    s = await wait((s) => active(s) && active(s).root.toLowerCase() === root.toLowerCase() && ids(s).length >= 1, "project " + root);
    await reseat(ids(s)[0], agent, model, root);
  }
  cmd({ cmd: "selectProject", root: home });
  s = await wait((s) => active(s) && active(s).root === home, "shopfront again");
  const tab = s.tabs[0].id;
  const p0 = await reseat(ids(s)[0], "gemini", "gemini-2.5-pro", "C:\\code\\shopfront");
  const p1 = await added({ cmd: "splitPane", id: p0, dir: "h", agent: "claude", model: "opus", path: "C:\\code\\shopfront-checkout" });
  const p2 = await added({ cmd: "splitPane", id: p0, dir: "v", agent: "codex", model: "gpt-5.6-sol", path: "C:\\code\\shopfront-search" });
  const p3 = await added({ cmd: "splitPane", id: p1, dir: "v", agent: "claude", model: "sonnet", path: "C:\\code\\shopfront-payments" });
  cmd({ cmd: "renameTab", id: tab, text: "spring-release" });
  await added({ cmd: "newTab", agent: "claude", model: "haiku", path: "C:\\code\\shopfront", text: "api-docs" });
  cmd({ cmd: "selectTab", id: tab });
  cmd({ cmd: "focusPane", id: p3 });
  window.__stage.panes = { p0, p1, p2, p3 };
  return window.__stage.panes;
}

// waitForFigures waits until the panes show what the pictures are for: the
// payments pane waiting on its question, and the figures the mocks handed the
// real status line bridge in the Claude panes' headers.
async function waitForFigures(page, panes) {
  await waitFor(() => page.evaluate((p) => {
    const s = window.__stage.last;
    const st = (id) => s && s.panes[id] && s.panes[id].status;
    const heads = [...document.querySelectorAll(".pane-spend, .pane-limit")].filter((e) => e.offsetParent && e.textContent.trim());
    return st(p.p3) === "waiting" && st(p.p1) === "working" && st(p.p2) === "working" && heads.length >= 2;
  }, panes), "the waiting pane and the spend figures", 60000);
  const shown = await page.evaluate(() => [...document.querySelectorAll(".pane-spend, .pane-limit")].filter((e) => e.offsetParent).map((e) => e.textContent.trim()));
  console.log("header figures", JSON.stringify(shown));
}

async function startRelay() {
  const data = path.join(work, "relaydata");
  fs.mkdirSync(data, { recursive: true });
  for (let i = 0; i < 8; i++) {
    const port = 41000 + Math.floor(Math.random() * 8000);
    const u = `http://127.0.0.1:${port}`;
    // Plans enforced, as on the shared relay: the account is on its trial.
    start(path.join(bin, "flockdeck-relay.exe"), ["serve", "-public-url", u, "-addr", `127.0.0.1:${port}`, "-data", data, "-admin-addr=", "-enforce-plans"], work, "relay");
    await sleep(2000);
    try { if ((await fetch(u + "/readyz")).ok) { console.log("relay on", u); return u; } } catch {}
    await stopAll(work);
  }
  throw new Error("the relay did not start");
}

// shootPhone pairs a phone-sized browser through the relay and opens the
// waiting pane in the web client, where its question is offered as buttons.
async function shootPhone(relayURL, pane) {
  const pair = execFileSync(path.join(bin, "flockdeck.exe"), ["remote", "pair"], { env, encoding: "utf8" });
  const link = (pair.match(/(http:\/\/127\.0\.0\.1:\d+\/pair#\S+)/) || [])[1];
  if (!link) throw new Error("no pairing link in: " + pair);
  const phone = await chromium.launchPersistentContext(path.join(work, "chrome-phone"), {
    executablePath: CHROME, headless: true, env: browserEnv(env),
    viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true,
    userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
    args: ["--hide-scrollbars", "--no-first-run", "--no-default-browser-check"],
  });
  try {
    const p = phone.pages()[0] || await phone.newPage();
    await p.goto(link);
    await sleep(2500);
    await p.locator("input").first().fill("iPhone");
    await p.getByRole("button", { name: /pair this device/i }).click();
    await sleep(4000);
    const host = await p.evaluate(() => [...document.querySelectorAll('a[href^="/d/"]')].map((a) => a.getAttribute("href").split("/")[2])[0]);
    if (!host) throw new Error("the paired phone lists no desktop");
    await p.goto(`${relayURL}/d/${host}/${pane}`);
    await sleep(6000);
    const sheet = await p.evaluate(() => { const s = document.querySelector(".choices"); return s ? (s.hidden ? "hidden" : s.innerText.replace(/\s+/g, " ")) : "none"; });
    console.log("phone choices:", sheet);
    await p.screenshot({ path: path.join(out, "remote-choices.png") });
    console.log("captured remote-choices");
  } finally {
    await phone.close().catch(() => {});
  }
}

// renderOG draws og.html, over the deck.png just installed, at 1200x630.
async function renderOG() {
  const b = await chromium.launchPersistentContext(path.join(work, "chrome-og"), {
    executablePath: CHROME, headless: true, env: browserEnv(env), viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1, args: ["--hide-scrollbars"],
  });
  try {
    const p = b.pages()[0] || await b.newPage();
    await p.goto("file:///" + path.join(REPO, "cmd", "sitegen", "assets", "og.html").replaceAll("\\", "/"), { waitUntil: "networkidle" });
    await p.evaluate(() => document.fonts.ready);
    await sleep(500);
    await p.screenshot({ path: path.join(SHOTS, "og.png") });
    console.log("rendered og.png");
  } finally {
    await b.close();
  }
}
