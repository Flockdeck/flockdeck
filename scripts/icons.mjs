// Builds the application icon from its geometry: the three SVG masters, every
// PNG size, both ICOs and the copies that must equal them. Nothing else in the
// repository makes these files; do not render them by hand.
//
//   node scripts/icons.mjs            write into this checkout
//   node scripts/icons.mjs <dir>      write the same tree under <dir> instead
//
// Needs Node 22+ and playwright-core with a Chrome, the same as
// cmd/sitegen/shots (PLAYWRIGHT_CORE_DIR and CHROME override where they are
// looked for; see cmd/sitegen/shots/README.md). It opens no Flockdeck instance
// and reads no state directory: the PNGs are the SVGs drawn in a blank page.
// Chrome's PNG encoder decides the bytes, so a different Chrome can change the
// files without changing a pixel; commit a rebuild only when the picture changed.
//
// The mark is five agents flying in formation, the one at the point being the
// status slot: a cyan circle working, an amber triangle waiting, a hollow grey
// circle idle. Sizes 16, 20, 24 and 32 are drawn on the pixel grid rather than
// scaled, and icon.svg carries the 16 and 24 drawings itself, picked by a media
// query on the size it is drawn at.
//
// The flockdeck-remote client keeps manual copies of these (web/app/icons);
// the script leaves them in a temp directory to copy across. Nothing enforces
// that they match the app's.
import { createRequire } from "node:module";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(process.argv[2] || path.join(HERE, ".."));
const A = path.join(ROOT, "internal/webui/assets");

const TILE = "#151B20";
const INK = {
  working: { base: "#C9D3D9", accent: "#4FD1DB" },
  waiting: { base: "#C9D3D9", accent: "#E2B341", state: "waiting" },
  idle: { base: "#738087", accent: "#738087", state: "idle" },
  nav: { base: "#93A1A9", accent: "#4FD1DB" }, // beside the word, on the page
};
const STATES = [
  ["", "working", "working: an agent is running"],
  ["-waiting", "waiting", "waiting: an agent is blocked on the user"],
  ["-idle", "idle", "idle: nothing is running"],
];

const n = (v) => +v.toFixed(3);
const PTS = [[9, 5.5], [16, 10.75], [23, 16], [16, 21.25], [9, 26.5]]; // 32 grid; [2] is the slot
const R = 3.3;

const slot = (cx, cy, r, { accent, state }) => {
  if (state === "waiting") return `<path fill="${accent}" d="M${cx} ${n(cy - r * 1.05)}L${n(cx + r * 1.15)} ${n(cy + r)}H${n(cx - r * 1.15)}Z"/>`;
  if (state === "idle") return `<circle fill="none" stroke="${accent}" stroke-width="${n(r * 0.45)}" cx="${cx}" cy="${cy}" r="${n(r * 0.775)}"/>`;
  return `<circle fill="${accent}" cx="${cx}" cy="${cy}" r="${r}"/>`;
};
const circles = (pts, r, base) => pts.map(([x, y]) => `\n  <circle fill="${base}" cx="${n(x)}" cy="${n(y)}" r="${r}"/>`).join("");
const draw = (ink) => `${circles(PTS.filter((_, k) => k !== 2), R, ink.base)}\n  ${slot(...PTS[2], R, ink)}`;
const squares = (pts, s, rx) => ({ base, accent }) => pts.map(([x, y], k) =>
  `\n  <rect x="${x}" y="${y}" width="${s}" height="${s}" rx="${rx}" fill="${k === 2 ? accent : base}"/>`).join("");
// Pixel-fitted drawings, viewBox = pixels: squares with softened corners on whole pixels.
const FITTED = {
  16: squares([[3, .5], [7, 3.5], [11, 6.5], [7, 9.5], [3, 12.5]], 3, 1),
  20: squares([[4, .5], [9, 4.5], [14, 8.5], [9, 12.5], [4, 16.5]], 3, 1),
  24: squares([[5, 2], [10, 6], [15, 10], [10, 14], [5, 18]], 4, 1.6),
  32(ink) {
    const P = [[9.5, 5], [16, 10.5], [22.5, 16], [16, 21.5], [9.5, 27]];
    return `${circles(P.filter((_, k) => k !== 2), 2.6, ink.base)}\n  ${slot(22.5, 16, 2.6, ink)}`;
  },
};

const open = (vb, size, extra = 'role="img" aria-label="flockdeck"') =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${vb} ${vb}" width="${size}" height="${size}" ${extra}>`;

// The master on its tile, at any size: a 30 x 30 tile at rx 7 in the 32 box, a
// 10% white hairline, the mark scaled to 74% inside.
const tileSvg = (ink, size = 32) => `${open(32, size)}
  <rect x="1" y="1" width="30" height="30" rx="7" fill="${TILE}"/>
  <rect x="1.5" y="1.5" width="29" height="29" rx="6.5" fill="none" stroke="#FFFFFF" stroke-opacity=".1"/>
  <g transform="translate(4.16 4.16) scale(.74)">${draw(ink)}
  </g>
</svg>
`;
// The tile runs to the edge at the fitted sizes: at 16 px a one-pixel margin is a sixth of the icon.
const fittedSvg = (px, ink) => `${open(px, px)}
  <rect width="${px}" height="${px}" rx="${px * 7 / 32}" fill="${TILE}"/>${FITTED[px](ink)}
</svg>
`;
// The three drawings in one file. The SVG's viewport is the size it is drawn
// at, so the rail mark (24), an SVG favicon (16) and the splash (56) each get theirs.
const iconSvg = (ink, note) => `${open(32, 32)}
  <!-- The mark on its tile, ${note}.
       Five agents flying in formation; the one at the point is the status slot.
       Keep the geometry identical across the three files. Only the slot changes. -->
  <style>.s16,.s24{display:none}@media (max-width:20px){.l{display:none}.s16{display:inline}}@media (min-width:21px) and (max-width:28px){.l{display:none}.s24{display:inline}}</style>
  <g class="l">
    <rect x="1" y="1" width="30" height="30" rx="7" fill="${TILE}"/>
    <rect x="1.5" y="1.5" width="29" height="29" rx="6.5" fill="none" stroke="#FFFFFF" stroke-opacity=".1"/>
    <g transform="translate(4.16 4.16) scale(.74)">${draw(ink)}
    </g>
  </g>
  <svg class="s24" viewBox="0 0 24 24" width="32" height="32">
    <rect width="24" height="24" rx="5.25" fill="${TILE}"/>${FITTED[24](ink)}
  </svg>
  <svg class="s16" viewBox="0 0 16 16" width="32" height="32">
    <rect width="16" height="16" rx="3.5" fill="${TILE}"/>${FITTED[16](ink)}
  </svg>
</svg>
`;
const bareSvg = (size) => `${open(32, size, 'aria-hidden="true"')}${draw(INK.nav)}\n</svg>\n`;

// ---- what to write ----------------------------------------------------------
const files = new Map(); // path -> string | Buffer
const jobs = []; // PNGs to draw: { svg, out, size }
const put = (p, data) => files.set(p, data);
const png = (svg, out, size) => jobs.push({ svg, out, size });

for (const [suffix, st, note] of STATES) {
  put(path.join(A, `icon${suffix}.svg`), iconSvg(INK[st], note));
  const sizes = suffix ? [16, 32, 48, 128, 192, 256] : [16, 20, 24, 32, 40, 48, 64, 96, 128, 180, 192, 256, 512];
  for (const s of sizes) png(FITTED[s] ? fittedSvg(s, INK[st]) : tileSvg(INK[st], s), path.join(A, `icon${suffix}-${s}.png`), s);
}

// ---- draw the PNGs ------------------------------------------------------------
const require = createRequire(import.meta.url);
const pwFrom = process.env.PLAYWRIGHT_CORE_DIR || path.join(os.tmpdir(), "flockdeck-shots-node");
const { chromium } = require(require.resolve("playwright-core", { paths: [pwFrom, HERE] }));
const chrome = [process.env.CHROME, "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe", "/usr/bin/google-chrome", "/usr/bin/chromium"]
  .find((p) => p && fs.existsSync(p));
if (!chrome) throw new Error("no Chrome found; set CHROME");
const browser = await chromium.launch({ executablePath: chrome });
const page = await browser.newPage({ viewport: { width: 600, height: 600 }, deviceScaleFactor: 1 });
for (const j of jobs) {
  const uri = "data:image/svg+xml;base64," + Buffer.from(j.svg).toString("base64");
  await page.setContent(`<body style="margin:0;background:transparent"><img id="i" src="${uri}" width="${j.size}" height="${j.size}" style="display:block"></body>`);
  put(j.out, await page.locator("#i").screenshot({ omitBackground: true }));
}
await browser.close();

// ---- ICOs and the copies that must equal the app's ------------------------------
// PNG-in-ICO: the exact PNG of each size, never rescaled.
const ico = (sizes) => {
  const blobs = sizes.map((s) => files.get(path.join(A, `icon-${s}.png`)));
  const head = Buffer.alloc(6 + 16 * sizes.length);
  head.writeUInt16LE(1, 2); head.writeUInt16LE(sizes.length, 4);
  let off = head.length;
  sizes.forEach((s, i) => {
    const e = 6 + 16 * i;
    head[e] = head[e + 1] = s % 256;
    head.writeUInt16LE(1, e + 4); head.writeUInt16LE(32, e + 6);
    head.writeUInt32LE(blobs[i].length, e + 8); head.writeUInt32LE(off, e + 12);
    off += blobs[i].length;
  });
  return Buffer.concat([head, ...blobs]);
};
put(path.join(A, "icon.ico"), ico([16, 20, 24, 32, 40, 48, 64, 96, 128, 256]));
put(path.join(ROOT, "build/windows/icon.ico"), ico([256, 128, 64, 48, 32, 16]));
put(path.join(ROOT, "build/appicon.png"), files.get(path.join(A, "icon-512.png")));
for (const gen of ["sitegen", "docgen"]) {
  const d = path.join(ROOT, "cmd", gen, "assets");
  put(path.join(d, "favicon.svg"), files.get(path.join(A, "icon.svg")));
  put(path.join(d, "favicon.ico"), files.get(path.join(A, "icon.ico")));
  put(path.join(d, "apple-touch-icon.png"), files.get(path.join(A, "icon-180.png")));
}

// flockdeck-remote's manual copies, in a directory of their own to copy from.
const REMOTE = path.join(os.tmpdir(), "flockdeck-remote-icons");
for (const [name, src] of [["favicon.svg", "icon.svg"], ["favicon.ico", "icon.ico"], ["apple-touch-icon.png", "icon-180.png"],
  ["icon-192.png", "icon-192.png"], ["icon-512.png", "icon-512.png"]]) put(path.join(REMOTE, name), files.get(path.join(A, src)));
put(path.join(REMOTE, "mark.svg"), bareSvg(20));

for (const [p, data] of files) {
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, data);
}
console.log("wrote", files.size, "files; flockdeck-remote's copies for web/app/icons are in", REMOTE);
