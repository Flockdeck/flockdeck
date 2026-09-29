// The isolation rules the harness keeps, in one place. The machine it runs on
// is usually running the owner's own Flockdeck, with their work in it, and the
// shell running this may well be one of its panes. So:
//
//   - The staged copy's state goes in a work directory under the system temp
//     folder. A Windows build finds its state directory with os.UserConfigDir,
//     which is %APPDATA%, and its home (Claude's settings, git's config) with
//     os.UserHomeDir, which is %USERPROFILE%; both are pointed into the work
//     directory. HOME and FLOCKDECK_HOME alone do not move it: an earlier
//     attempt set those and still wrote into the real %APPDATA%\flockdeck.
//   - Everything the harness starts gets an environment built from an allow
//     list, never the caller's with a few names taken out. A pane's shell
//     carries FLOCKDECK_API, FLOCKDECK_TOKEN, FLOCKDECK_PANE and the rest (and
//     their PERCH_* twins), which point straight at the live instance: a mock
//     agent that saw them would post its figures, or start panes, there.
//   - The live state directory is listed, with sizes, times and hashes, before
//     anything starts and after everything has stopped, and the two listings
//     compared.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

// realStateDirs names every directory the owner's own Flockdeck could be
// keeping its state in, from the environment the harness was started with:
// %APPDATA%\flockdeck, and the pre-rename %APPDATA%\perch.
export function realStateDirs(env = process.env) {
  const bases = new Set();
  if (env.APPDATA) bases.add(path.resolve(env.APPDATA));
  if (env.USERPROFILE) bases.add(path.resolve(env.USERPROFILE, "AppData", "Roaming"));
  const dirs = [];
  for (const b of bases) for (const n of ["flockdeck", "perch"]) dirs.push(path.join(b, n));
  return dirs;
}

const inside = (child, parent) => {
  const rel = path.relative(parent.toLowerCase(), child.toLowerCase());
  return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
};

// refuseLiveState throws if the staged state directory would be, or would sit
// inside, or would contain, one of the owner's.
export function refuseLiveState(stagedAppData, real = realStateDirs()) {
  const staged = path.join(path.resolve(stagedAppData), "flockdeck");
  for (const r of real) {
    if (inside(staged, r) || inside(r, staged)) {
      throw new Error(`refusing to run: the staged state directory ${staged} would be the live one at ${r}`);
    }
  }
}

// snapshot lists every file under the given directories with its size,
// modification time and SHA-256.
export function snapshot(dirs) {
  const files = {};
  const walk = (d) => {
    let entries;
    try { entries = fs.readdirSync(d, { withFileTypes: true }); } catch { return; }
    for (const e of entries) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) { walk(p); continue; }
      try {
        const st = fs.statSync(p);
        files[p] = { size: st.size, mtime: st.mtime.toISOString(), sha256: createHash("sha256").update(fs.readFileSync(p)).digest("hex") };
      } catch (err) {
        files[p] = { error: String(err.code || err) };
      }
    }
  };
  dirs.forEach(walk);
  return files;
}

// compare says what differs between two snapshots.
export function compare(before, after) {
  const added = Object.keys(after).filter((p) => !(p in before));
  const removed = Object.keys(before).filter((p) => !(p in after));
  const changed = Object.keys(after).filter((p) => p in before && before[p].sha256 !== after[p].sha256);
  return { added, removed, changed };
}

// The only variables passed through from the caller's environment: what
// Windows itself needs to start a process. Everything else is set below.
const PASS = ["SystemRoot", "windir", "SystemDrive", "ComSpec", "PATHEXT", "OS",
  "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "PROCESSOR_IDENTIFIER", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"];

// stagedEnv is the whole environment of everything the harness starts: the
// staged Flockdeck, which hands it on to its mock agents, and the browsers.
// PATH holds Windows and git and nothing else, so no agent the owner has
// installed is found, and the picker shows the staged ones.
export function stagedEnv(work, gitDir) {
  const env = {};
  for (const k of PASS) if (process.env[k] !== undefined) env[k] = process.env[k];
  const sys = env.SystemRoot || "C:\\Windows";
  env.PATH = [path.join(sys, "System32"), sys, path.join(sys, "System32", "Wbem"),
    path.join(sys, "System32", "WindowsPowerShell", "v1.0"), gitDir].join(";");
  env.APPDATA = path.join(work, "appdata");
  env.LOCALAPPDATA = path.join(work, "localappdata");
  env.USERPROFILE = path.join(work, "home");
  env.HOME = env.USERPROFILE;
  env.TEMP = env.TMP = path.join(work, "tmp");
  env.USERNAME = "sam";
  env.FLOCKDECK_UPDATE = "off";
  env.GIT_CONFIG_NOSYSTEM = "1";
  env.GIT_CONFIG_GLOBAL = path.join(env.USERPROFILE, ".gitconfig");
  for (const k of Object.keys(env)) {
    if (/^(FLOCKDECK|PERCH)_/i.test(k) && k !== "FLOCKDECK_UPDATE") throw new Error("staged environment carries " + k);
    if (/^CLAUDE/i.test(k) || /_API_KEY$/i.test(k)) throw new Error("staged environment carries " + k);
  }
  refuseLiveState(env.APPDATA);
  return env;
}

// browserEnv is the staged environment for the headless browsers, with the
// real USERPROFILE put back: Chrome will not start under a profile folder
// other than the account's own. A browser is no Flockdeck and keeps no state
// of Flockdeck's; its own profile is in the work directory.
export function browserEnv(staged) {
  return { ...staged, USERPROFILE: process.env.USERPROFILE };
}

// buildEnv is the caller's environment for `go build` and git, with every
// pane variable taken out: nothing built or run from it should find the live
// instance either.
export function buildEnv() {
  const env = { ...process.env };
  for (const k of Object.keys(env)) if (/^(FLOCKDECK|PERCH)_/i.test(k)) delete env[k];
  return env;
}

// livePID is the process id the live instance recorded, if there is one, so
// that the cleanup below can never touch it even by accident.
export function livePIDs(real = realStateDirs()) {
  const pids = new Set();
  for (const d of real) {
    try { pids.add(JSON.parse(fs.readFileSync(path.join(d, "instance.json"), "utf8").replace(/^\uFEFF/, "")).pid); } catch {}
  }
  return pids;
}

// running lists the processes whose command line holds the work directory:
// everything the harness started, and nothing else. Chrome hands itself off
// to other processes as it starts, so this goes by path, never by PID.
export function running(work) {
  const ps = `Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -and $_.CommandLine.ToLower().Contains('${work.toLowerCase().replace(/'/g, "''")}') -and $_.ProcessId -ne ${process.pid} -and $_.Name -notin @('bash.exe','powershell.exe','pwsh.exe','cmd.exe','conhost.exe','node.exe') } | ForEach-Object { "$($_.ProcessId) $($_.Name)" }`;
  const out = execFileSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", ps], { encoding: "utf8", env: buildEnv() });
  return out.split(/\r?\n/).filter(Boolean).map((l) => { const [pid, name] = l.split(" "); return { pid: Number(pid), name }; });
}

// stopAll stops everything running(work) finds, a few rounds over, and says
// what, if anything, is still left.
export async function stopAll(work) {
  const never = livePIDs();
  for (let round = 0; round < 6; round++) {
    const left = running(work).filter((p) => !never.has(p.pid));
    if (left.length === 0) return [];
    for (const p of left) { try { process.kill(p.pid, "SIGKILL"); } catch {} }
    await new Promise((r) => setTimeout(r, 700));
  }
  return running(work);
}
